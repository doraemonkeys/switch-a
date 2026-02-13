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

// ProviderResponse wraps a Provider with optional warnings for API responses.
type ProviderResponse struct {
	*model.Provider
	Warnings []string `json:"warnings,omitempty"`
}

// CreateProviderRequest represents the request to create a provider.
type CreateProviderRequest struct {
	ID             string               `json:"id"`
	Name           string               `json:"name"`
	BaseURL        string               `json:"base_url"`
	APIKey         string               `json:"api_key"`
	APITypes       []string             `json:"api_types"`
	AuthMode       string               `json:"auth_mode"`
	GroupID        *string              `json:"group_id"`
	Weight         int                  `json:"weight"`
	Priority       int                  `json:"priority"`
	Concurrency    int                  `json:"concurrency"`
	MaxRetries     *int                 `json:"max_retries"`     // Pointer to distinguish unset (nil) from explicit 0
	Backoff        *model.BackoffPolicy `json:"backoff"`         // Exponential backoff for same-provider retries
	Vendor         string               `json:"vendor"`          // Empty = no isolation, "*" = wildcard (see model.Provider.Vendor)
	FailoverScope  *model.Scope         `json:"failover_scope"`  // Pointer to distinguish unset (nil) from explicit empty
	AcceptFailover *model.Scope         `json:"accept_failover"` // Pointer to distinguish unset (nil) from explicit empty
	Enabled        *bool                `json:"enabled"`
}

// validate checks that all required fields are present and all provided fields have valid values.
// Returns an error message if validation fails, empty string otherwise.
func (req *CreateProviderRequest) validate() string {
	if req.ID == "" {
		return "Provider ID is required"
	}
	if req.Name == "" {
		return "Provider name is required"
	}
	if req.BaseURL == "" {
		return "Provider base_url is required"
	}
	if req.APIKey == "" {
		return "Provider api_key is required"
	}
	if len(req.APITypes) == 0 {
		return "At least one api_type is required"
	}
	for _, apiType := range req.APITypes {
		if !IsValidAPIType(apiType) {
			return "Invalid api_type: " + apiType
		}
	}
	if req.MaxRetries != nil && *req.MaxRetries < 0 {
		return "MaxRetries must be non-negative"
	}
	if req.Backoff != nil {
		if err := req.Backoff.Validate(); err != nil {
			return "Invalid backoff: " + err.Error()
		}
	}
	if req.FailoverScope != nil && !model.IsValidScope(*req.FailoverScope) {
		return "Invalid failover_scope: must be 'none', 'vendor', or 'any'"
	}
	if req.AcceptFailover != nil && !model.IsValidScope(*req.AcceptFailover) {
		return "Invalid accept_failover: must be 'none', 'vendor', or 'any'"
	}
	if req.AuthMode != "" && !IsValidAuthMode(req.AuthMode) {
		return "Invalid auth_mode: must be 'auto', 'bearer', or 'x-api-key'"
	}
	return ""
}

// toProvider converts the request to a Provider model with appropriate defaults applied.
func (req *CreateProviderRequest) toProvider() *model.Provider {
	apiTypes := make([]model.ProviderAPIType, len(req.APITypes))
	for i, at := range req.APITypes {
		apiTypes[i] = model.ProviderAPIType{
			ProviderID: req.ID,
			APIType:    at,
		}
	}

	provider := &model.Provider{
		ID:             req.ID,
		Name:           req.Name,
		BaseURL:        req.BaseURL,
		APIKey:         req.APIKey,
		APITypes:       apiTypes,
		AuthMode:       req.AuthMode,
		GroupID:        req.GroupID,
		Weight:         req.Weight,
		Priority:       req.Priority,
		Concurrency:    req.Concurrency,
		MaxRetries:     DefaultProviderMaxRetries,
		Backoff:        model.BackoffPolicy{},
		Vendor:         req.Vendor,
		FailoverScope:  model.ScopeAny,
		AcceptFailover: model.ScopeAny,
		Enabled:        true,
	}

	// Apply explicit values where provided
	if req.Enabled != nil {
		provider.Enabled = *req.Enabled
	}
	if req.MaxRetries != nil {
		provider.MaxRetries = *req.MaxRetries
	}
	if req.FailoverScope != nil {
		provider.FailoverScope = *req.FailoverScope
	}
	if req.AcceptFailover != nil {
		provider.AcceptFailover = *req.AcceptFailover
	}
	if req.Backoff != nil {
		provider.Backoff = *req.Backoff
	}
	if provider.Weight <= 0 {
		provider.Weight = DefaultWeight
	}
	if provider.AuthMode == "" {
		provider.AuthMode = DefaultAuthMode
	}

	return provider
}

// CreateProvider handles POST /admin/api/providers.
func (h *Handler) CreateProvider(w http.ResponseWriter, r *http.Request) {
	limitRequestBody(w, r)
	var req CreateProviderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Invalid request body")
		return
	}

	if errMsg := req.validate(); errMsg != "" {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, errMsg)
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

	provider := req.toProvider()

	// Validate GroupID exists if provided
	if provider.GroupID != nil {
		groupID, ok := h.validateAndResolveGroupID(w, r, *provider.GroupID)
		if !ok {
			return
		}
		provider.GroupID = groupID
	}

	// Check for contradictory failover configuration
	warnings := model.HasContradictoryConfig(provider)
	for _, warning := range warnings {
		h.logger.Warn("provider has contradictory failover config",
			zap.String("id", req.ID),
			zap.String("warning", warning))
	}

	if err := h.store.CreateProvider(r.Context(), provider); err != nil {
		h.logger.Error("failed to create provider", zap.String("id", req.ID), zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to create provider")
		return
	}

	writeJSON(w, http.StatusCreated, ProviderResponse{Provider: provider, Warnings: warnings})
}

// UpdateProviderRequest represents the request to update a provider.
type UpdateProviderRequest struct {
	Name           *string              `json:"name"`
	BaseURL        *string              `json:"base_url"`
	APIKey         *string              `json:"api_key"`
	APITypes       []string             `json:"api_types"`
	AuthMode       *string              `json:"auth_mode"`
	GroupID        *string              `json:"group_id"`
	Weight         *int                 `json:"weight"`
	Priority       *int                 `json:"priority"`
	Concurrency    *int                 `json:"concurrency"`
	MaxRetries     *int                 `json:"max_retries"`
	Backoff        *model.BackoffPolicy `json:"backoff"` // Exponential backoff for same-provider retries
	Vendor         *string              `json:"vendor"`
	FailoverScope  *model.Scope         `json:"failover_scope"`
	AcceptFailover *model.Scope         `json:"accept_failover"`
	Enabled        *bool                `json:"enabled"`
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
	if req.MaxRetries != nil && *req.MaxRetries < 0 {
		return "MaxRetries must be non-negative"
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
	if req.Backoff != nil {
		if err := req.Backoff.Validate(); err != nil {
			return "Invalid backoff: " + err.Error()
		}
	}
	if req.FailoverScope != nil && !model.IsValidScope(*req.FailoverScope) {
		return "Invalid failover_scope: must be 'none', 'vendor', or 'any'"
	}
	if req.AcceptFailover != nil && !model.IsValidScope(*req.AcceptFailover) {
		return "Invalid accept_failover: must be 'none', 'vendor', or 'any'"
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
	if req.Backoff != nil {
		provider.Backoff = *req.Backoff
	}
	if req.Vendor != nil {
		provider.Vendor = *req.Vendor
	}
	if req.FailoverScope != nil {
		provider.FailoverScope = *req.FailoverScope
	}
	if req.AcceptFailover != nil {
		provider.AcceptFailover = *req.AcceptFailover
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

	// Check for contradictory failover configuration
	warnings := model.HasContradictoryConfig(provider)
	for _, warning := range warnings {
		h.logger.Warn("provider has contradictory failover config",
			zap.String("id", id),
			zap.String("warning", warning))
	}

	if err := h.store.UpdateProvider(r.Context(), provider); err != nil {
		h.logger.Error("failed to update provider", zap.String("id", id), zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to update provider")
		return
	}

	if provider.Enabled != originalEnabled {
		h.syncHealthManagerState(r.Context(), id, provider.Enabled)
	}

	writeJSON(w, http.StatusOK, ProviderResponse{Provider: provider, Warnings: warnings})
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
