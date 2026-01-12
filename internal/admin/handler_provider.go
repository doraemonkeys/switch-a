package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"switch-a/internal/model"
	"switch-a/internal/store"

	"go.uber.org/zap"
)

// Provider API handlers

// ListProviders handles GET /admin/api/providers.
func (h *Handler) ListProviders(w http.ResponseWriter, r *http.Request) {
	providers, err := h.store.ListProviders(r.Context())
	if err != nil {
		h.logger.Error("failed to list providers", zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to list providers")
		return
	}

	// Collect provider IDs for batch health state fetch
	providerIDs := make([]string, len(providers))
	for i := range providers {
		providerIDs[i] = providers[i].ID
	}

	// Batch fetch health states to avoid N+1 queries.
	// Using partial failure design: if batch retrieval fails,
	// we log and continue without health states rather than failing the entire request.
	healthStates, err := h.store.GetHealthStatesByProviderIDs(r.Context(), providerIDs)
	if err != nil {
		h.logger.Warn("failed to batch fetch health states", zap.Error(err))
	} else {
		for i := range providers {
			if state, ok := healthStates[providers[i].ID]; ok {
				providers[i].Health = state
			}
		}
	}

	writeJSON(w, http.StatusOK, providers)
}

// GetProvider handles GET /admin/api/providers/{id}.
func (h *Handler) GetProvider(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Provider ID is required")
		return
	}

	provider, err := h.store.GetProvider(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, ErrCodeNotFound, "Provider not found: "+id)
			return
		}
		h.logger.Error("failed to get provider", zap.String("id", id), zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to get provider")
		return
	}

	// Populate health state.
	// Using partial failure design: if health retrieval fails,
	// we log and continue rather than failing the entire request.
	state, err := h.store.GetHealthState(r.Context(), id)
	if err != nil {
		h.logger.Warn("failed to get health state", zap.String("id", id), zap.Error(err))
	} else {
		provider.Health = state
	}

	writeJSON(w, http.StatusOK, provider)
}

// CreateProviderRequest represents the request to create a provider.
type CreateProviderRequest struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	BaseURL     string   `json:"base_url"`
	APIKey      string   `json:"api_key"`
	APITypes    []string `json:"api_types"`
	AuthMode    string   `json:"auth_mode"`
	GroupID     *string  `json:"group_id"`
	Weight      int      `json:"weight"`
	Priority    int      `json:"priority"`
	Concurrency int      `json:"concurrency"`
	MaxRetries  *int     `json:"max_retries"` // Pointer to distinguish unset (nil) from explicit 0
	Enabled     *bool    `json:"enabled"`
}

// CreateProvider handles POST /admin/api/providers.
func (h *Handler) CreateProvider(w http.ResponseWriter, r *http.Request) {
	limitRequestBody(w, r)
	var req CreateProviderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Invalid request body")
		return
	}

	if req.ID == "" {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Provider ID is required")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Provider name is required")
		return
	}
	if req.BaseURL == "" {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Provider base_url is required")
		return
	}
	if req.APIKey == "" {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Provider api_key is required")
		return
	}
	if len(req.APITypes) == 0 {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "At least one api_type is required")
		return
	}
	for _, apiType := range req.APITypes {
		if !IsValidAPIType(apiType) {
			writeError(w, http.StatusBadRequest, ErrCodeValidation, "Invalid api_type: "+apiType)
			return
		}
	}
	if req.MaxRetries != nil && *req.MaxRetries < -1 {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "MaxRetries must be -1 (default) or non-negative")
		return
	}

	// Check if provider already exists
	_, err := h.store.GetProvider(r.Context(), req.ID)
	if err == nil {
		writeError(w, http.StatusConflict, ErrCodeConflict, "Provider already exists: "+req.ID)
		return
	}
	if !errors.Is(err, store.ErrNotFound) {
		h.logger.Error("failed to check provider existence", zap.String("id", req.ID), zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to create provider")
		return
	}

	// Build provider model
	apiTypes := make([]model.ProviderAPIType, len(req.APITypes))
	for i, at := range req.APITypes {
		apiTypes[i] = model.ProviderAPIType{
			ProviderID: req.ID,
			APIType:    at,
		}
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	// Determine MaxRetries value: use explicit value if provided, otherwise default
	maxRetries := DefaultMaxRetries
	if req.MaxRetries != nil {
		maxRetries = *req.MaxRetries
	}

	provider := &model.Provider{
		ID:          req.ID,
		Name:        req.Name,
		BaseURL:     req.BaseURL,
		APIKey:      req.APIKey,
		APITypes:    apiTypes,
		AuthMode:    req.AuthMode,
		GroupID:     req.GroupID,
		Weight:      req.Weight,
		Priority:    req.Priority,
		Concurrency: req.Concurrency,
		MaxRetries:  maxRetries,
		Enabled:     enabled,
	}

	// Set defaults and validate
	if provider.Weight <= 0 {
		provider.Weight = DefaultWeight
	}
	if provider.AuthMode == "" {
		provider.AuthMode = DefaultAuthMode
	} else if !IsValidAuthMode(provider.AuthMode) {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Invalid auth_mode: must be 'auto', 'bearer', or 'x-api-key'")
		return
	}

	// Validate GroupID exists if provided
	if provider.GroupID != nil {
		groupID, ok := h.validateAndResolveGroupID(w, r, *provider.GroupID)
		if !ok {
			return
		}
		provider.GroupID = groupID
	}

	if err := h.store.CreateProvider(r.Context(), provider); err != nil {
		h.logger.Error("failed to create provider", zap.String("id", req.ID), zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to create provider")
		return
	}

	writeJSON(w, http.StatusCreated, provider)
}

// UpdateProviderRequest represents the request to update a provider.
type UpdateProviderRequest struct {
	Name        *string  `json:"name"`
	BaseURL     *string  `json:"base_url"`
	APIKey      *string  `json:"api_key"`
	APITypes    []string `json:"api_types"`
	AuthMode    *string  `json:"auth_mode"`
	GroupID     *string  `json:"group_id"`
	Weight      *int     `json:"weight"`
	Priority    *int     `json:"priority"`
	Concurrency *int     `json:"concurrency"`
	MaxRetries  *int     `json:"max_retries"`
	Enabled     *bool    `json:"enabled"`
}

// validate checks that all provided fields have valid values.
// Returns an error message if validation fails, empty string otherwise.
func (req *UpdateProviderRequest) validate() string {
	if req.Name != nil && *req.Name == "" {
		return "Name cannot be empty"
	}
	if req.BaseURL != nil && *req.BaseURL == "" {
		return "BaseURL cannot be empty"
	}
	if req.APIKey != nil && *req.APIKey == "" {
		return "APIKey cannot be empty"
	}
	if req.Weight != nil && *req.Weight <= 0 {
		return "Weight must be positive"
	}
	if req.Concurrency != nil && *req.Concurrency < 0 {
		return "Concurrency cannot be negative"
	}
	if req.MaxRetries != nil && *req.MaxRetries < -1 {
		return "MaxRetries must be -1 (default) or non-negative"
	}
	if req.APITypes != nil && len(req.APITypes) == 0 {
		return "At least one api_type is required"
	}
	for _, apiType := range req.APITypes {
		if !IsValidAPIType(apiType) {
			return "Invalid api_type: " + apiType
		}
	}
	if req.AuthMode != nil && !IsValidAuthMode(*req.AuthMode) {
		return "Invalid auth_mode: must be 'auto', 'bearer', or 'x-api-key'"
	}
	return ""
}

// applyTo updates the provider fields from the request.
func (req *UpdateProviderRequest) applyTo(provider *model.Provider) {
	if req.Name != nil {
		provider.Name = *req.Name
	}
	if req.BaseURL != nil {
		provider.BaseURL = *req.BaseURL
	}
	if req.APIKey != nil {
		provider.APIKey = *req.APIKey
	}
	if req.APITypes != nil {
		apiTypes := make([]model.ProviderAPIType, len(req.APITypes))
		for i, at := range req.APITypes {
			apiTypes[i] = model.ProviderAPIType{
				ProviderID: provider.ID,
				APIType:    at,
			}
		}
		provider.APITypes = apiTypes
	}
	if req.AuthMode != nil {
		provider.AuthMode = *req.AuthMode
	}
	if req.Weight != nil {
		provider.Weight = *req.Weight
	}
	if req.Priority != nil {
		provider.Priority = *req.Priority
	}
	if req.Concurrency != nil {
		provider.Concurrency = *req.Concurrency
	}
	if req.MaxRetries != nil {
		provider.MaxRetries = *req.MaxRetries
	}
	if req.Enabled != nil {
		provider.Enabled = *req.Enabled
	}
}

// UpdateProvider handles PUT /admin/api/providers/{id}.
func (h *Handler) UpdateProvider(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Provider ID is required")
		return
	}

	limitRequestBody(w, r)
	var req UpdateProviderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Invalid request body")
		return
	}

	if errMsg := req.validate(); errMsg != "" {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, errMsg)
		return
	}

	provider, err := h.store.GetProvider(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, ErrCodeNotFound, "Provider not found: "+id)
			return
		}
		h.logger.Error("failed to get provider", zap.String("id", id), zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to update provider")
		return
	}

	originalEnabled := provider.Enabled

	req.applyTo(provider)

	// Validate and resolve GroupID separately (requires store access)
	if req.GroupID != nil {
		groupID, ok := h.validateAndResolveGroupID(w, r, *req.GroupID)
		if !ok {
			return
		}
		provider.GroupID = groupID
	}

	if err := h.store.UpdateProvider(r.Context(), provider); err != nil {
		h.logger.Error("failed to update provider", zap.String("id", id), zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to update provider")
		return
	}

	if provider.Enabled != originalEnabled {
		h.syncHealthManagerState(r.Context(), id, provider.Enabled)
	}

	writeJSON(w, http.StatusOK, provider)
}

// DeleteProvider handles DELETE /admin/api/providers/{id}.
// After successful deletion, clears in-memory state to prevent memory leaks:
// - Concurrency counter (in ConcurrencyLimiter)
// - Circuit breaker failure history (in HealthManager)
// Note: StickyCache entries are not explicitly cleared as they self-heal
// (entries are deleted on next access when the provider is not found).
func (h *Handler) DeleteProvider(w http.ResponseWriter, r *http.Request) {
	h.handleDelete(w, r, deleteConfig{
		resourceType: "Provider",
		getFunc: func(ctx context.Context, id string) error {
			_, err := h.store.GetProvider(ctx, id)
			return err
		},
		deleteFunc: func(ctx context.Context, id string) error {
			if err := h.store.DeleteProvider(ctx, id); err != nil {
				return err
			}
			// Clear concurrency counter to prevent memory leak.
			// The ConcurrencyLimiter holds counters in a sync.Map that persist
			// until explicitly cleared. Without this, deleted providers would
			// leave orphaned counter entries.
			if h.cleaner != nil {
				h.cleaner.ClearConcurrency(id)
			}
			// Clear circuit breaker failure history to prevent memory leak.
			// The CircuitBreaker holds failure timestamps in a map that persist
			// until explicitly cleared or cleaned up by the periodic cleanup loop.
			if h.health != nil {
				h.health.ResetCircuitBreaker(id)
			}
			return nil
		},
	})
}

// EnableProvider handles POST /admin/api/providers/{id}/enable.
func (h *Handler) EnableProvider(w http.ResponseWriter, r *http.Request) {
	h.setProviderEnabled(w, r, true)
}

// DisableProvider handles POST /admin/api/providers/{id}/disable.
func (h *Handler) DisableProvider(w http.ResponseWriter, r *http.Request) {
	h.setProviderEnabled(w, r, false)
}

// setProviderEnabled is a helper to enable or disable a provider.
func (h *Handler) setProviderEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	action := "enable"
	if !enabled {
		action = "disable"
	}

	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Provider ID is required")
		return
	}

	provider, err := h.store.GetProvider(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, ErrCodeNotFound, "Provider not found: "+id)
			return
		}
		h.logger.Error("failed to get provider", zap.String("id", id), zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to "+action+" provider")
		return
	}

	provider.Enabled = enabled
	if err := h.store.UpdateProvider(r.Context(), provider); err != nil {
		h.logger.Error("failed to update provider", zap.String("id", id), zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to "+action+" provider")
		return
	}

	// Update health manager state
	h.syncHealthManagerState(r.Context(), id, enabled)

	writeJSON(w, http.StatusOK, provider)
}

// syncHealthManagerState updates the health manager to reflect the provider's enabled state.
func (h *Handler) syncHealthManagerState(ctx context.Context, id string, enabled bool) {
	if h.health == nil {
		return
	}

	var err error
	if enabled {
		err = h.health.ManualEnable(ctx, id)
	} else {
		err = h.health.ManualDisable(ctx, id, "disabled via API")
	}

	if err != nil {
		action := "enable"
		if !enabled {
			action = "disable"
		}
		h.logger.Warn("failed to "+action+" provider in health manager", zap.String("id", id), zap.Error(err))
	}
}

// ResetProvider handles POST /admin/api/providers/{id}/reset.
// This clears the auto-disabled state from the circuit breaker.
func (h *Handler) ResetProvider(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Provider ID is required")
		return
	}

	// Check if provider exists
	_, err := h.store.GetProvider(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, ErrCodeNotFound, "Provider not found: "+id)
			return
		}
		h.logger.Error("failed to get provider", zap.String("id", id), zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to reset provider")
		return
	}

	// Reset via health manager
	if h.health != nil {
		if err := h.health.ManualEnable(r.Context(), id); err != nil {
			h.logger.Error("failed to reset provider in health manager", zap.String("id", id), zap.Error(err))
			writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to reset provider")
			return
		}
	}

	// Get updated health state
	state, err := h.store.GetHealthState(r.Context(), id)
	if err != nil {
		h.logger.Warn("failed to get health state after reset", zap.String("id", id), zap.Error(err))
	}

	writeJSON(w, http.StatusOK, state)
}

// getProviderOrError retrieves a provider by ID, returning a standardized error for batch operations.
// Used by batch operations to avoid duplicating the "check provider exists -> handle not found" pattern.
func (h *Handler) getProviderOrError(ctx context.Context, id string) (*model.Provider, error) {
	provider, err := h.store.GetProvider(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, errors.New("provider not found: " + id)
		}
		return nil, errors.New("failed to get provider: " + id)
	}
	return provider, nil
}

// validateAndResolveGroupID validates the groupID and returns the resolved pointer.
// Returns nil pointer if groupID is empty (to clear the association).
// Returns false for ok if validation failed and error response was written.
func (h *Handler) validateAndResolveGroupID(w http.ResponseWriter, r *http.Request, groupID string) (*string, bool) {
	if groupID == "" {
		return nil, true
	}
	if _, err := h.store.GetGroup(r.Context(), groupID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusBadRequest, ErrCodeValidation, "Group not found: "+groupID)
			return nil, false
		}
		h.logger.Error("failed to check group existence", zap.String("group_id", groupID), zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to validate group")
		return nil, false
	}
	return &groupID, true
}

// Batch operation types

// BatchProviderRequest represents a batch operation request for providers.
type BatchProviderRequest struct {
	Action string   `json:"action"` // "reset" | "enable" | "disable" | "delete"
	IDs    []string `json:"ids"`
}

// BatchProviderResult represents the result of a single provider operation.
type BatchProviderResult struct {
	ID      string `json:"id"`
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// BatchProviderResponse represents the response for batch provider operations.
type BatchProviderResponse struct {
	Success  bool                  `json:"success"`
	Affected int                   `json:"affected"`
	Results  []BatchProviderResult `json:"results"`
}

// validBatchActions contains the allowed batch action values.
// This is unexported to prevent external mutation of global state.
var validBatchActions = map[string]bool{
	"reset":   true,
	"enable":  true,
	"disable": true,
	"delete":  true,
}

// BatchProviderAction handles POST /admin/api/providers/batch.
// Performs batch operations on multiple providers.
func (h *Handler) BatchProviderAction(w http.ResponseWriter, r *http.Request) {
	limitRequestBody(w, r)
	var req BatchProviderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Invalid request body")
		return
	}

	// Validate action
	if !validBatchActions[req.Action] {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Invalid action: must be 'reset', 'enable', 'disable', or 'delete'")
		return
	}

	// Validate IDs
	if len(req.IDs) == 0 {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "At least one provider ID is required")
		return
	}

	// Execute batch operation
	results := make([]BatchProviderResult, len(req.IDs))
	successCount := 0
	ctx := r.Context()

	for i, id := range req.IDs {
		result := BatchProviderResult{ID: id}
		var err error

		switch req.Action {
		case "reset":
			err = h.batchReset(ctx, id)
		case "enable":
			err = h.batchEnable(ctx, id)
		case "disable":
			err = h.batchDisable(ctx, id)
		case "delete":
			err = h.batchDelete(ctx, id)
		}

		if err != nil {
			result.Success = false
			result.Error = err.Error()
		} else {
			result.Success = true
			successCount++
		}
		results[i] = result
	}

	// Determine response
	resp := BatchProviderResponse{
		Success:  successCount == len(req.IDs),
		Affected: successCount,
		Results:  results,
	}

	// Use 207 Multi-Status for partial success
	status := http.StatusOK
	if successCount > 0 && successCount < len(req.IDs) {
		status = http.StatusMultiStatus
	} else if successCount == 0 {
		status = http.StatusBadRequest
	}

	writeJSON(w, status, resp)
}

// batchReset resets a provider's circuit breaker state.
func (h *Handler) batchReset(ctx context.Context, id string) error {
	if _, err := h.getProviderOrError(ctx, id); err != nil {
		return err
	}

	// Reset via health manager
	if h.health != nil {
		if err := h.health.ManualEnable(ctx, id); err != nil {
			return errors.New("failed to reset provider: " + id)
		}
	}
	return nil
}

// batchEnable enables a provider.
func (h *Handler) batchEnable(ctx context.Context, id string) error {
	provider, err := h.getProviderOrError(ctx, id)
	if err != nil {
		return err
	}

	provider.Enabled = true
	if err := h.store.UpdateProvider(ctx, provider); err != nil {
		return errors.New("failed to enable provider: " + id)
	}

	// Update health manager state
	if h.health != nil {
		if err := h.health.ManualEnable(ctx, id); err != nil {
			h.logger.Warn("failed to enable provider in health manager", zap.String("id", id), zap.Error(err))
		}
	}
	return nil
}

// batchDisable disables a provider.
func (h *Handler) batchDisable(ctx context.Context, id string) error {
	provider, err := h.getProviderOrError(ctx, id)
	if err != nil {
		return err
	}

	provider.Enabled = false
	if err := h.store.UpdateProvider(ctx, provider); err != nil {
		return errors.New("failed to disable provider: " + id)
	}

	// Update health manager state
	if h.health != nil {
		if err := h.health.ManualDisable(ctx, id, "disabled via batch API"); err != nil {
			h.logger.Warn("failed to disable provider in health manager", zap.String("id", id), zap.Error(err))
		}
	}
	return nil
}

// batchDelete deletes a provider.
func (h *Handler) batchDelete(ctx context.Context, id string) error {
	if _, err := h.getProviderOrError(ctx, id); err != nil {
		return err
	}

	if err := h.store.DeleteProvider(ctx, id); err != nil {
		return errors.New("failed to delete provider: " + id)
	}

	// Clear concurrency counter
	if h.cleaner != nil {
		h.cleaner.ClearConcurrency(id)
	}

	// Clear circuit breaker failure history
	if h.health != nil {
		h.health.ResetCircuitBreaker(id)
	}

	return nil
}
