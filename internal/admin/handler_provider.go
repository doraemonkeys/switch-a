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

	// Update fields if provided
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
		for _, apiType := range req.APITypes {
			if !IsValidAPIType(apiType) {
				writeError(w, http.StatusBadRequest, ErrCodeValidation, "Invalid api_type: "+apiType)
				return
			}
		}
		apiTypes := make([]model.ProviderAPIType, len(req.APITypes))
		for i, at := range req.APITypes {
			apiTypes[i] = model.ProviderAPIType{
				ProviderID: id,
				APIType:    at,
			}
		}
		provider.APITypes = apiTypes
	}
	if req.AuthMode != nil {
		if !IsValidAuthMode(*req.AuthMode) {
			writeError(w, http.StatusBadRequest, ErrCodeValidation, "Invalid auth_mode: must be 'auto', 'bearer', or 'x-api-key'")
			return
		}
		provider.AuthMode = *req.AuthMode
	}
	if req.GroupID != nil {
		groupID, ok := h.validateAndResolveGroupID(w, r, *req.GroupID)
		if !ok {
			return
		}
		provider.GroupID = groupID
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

	if err := h.store.UpdateProvider(r.Context(), provider); err != nil {
		h.logger.Error("failed to update provider", zap.String("id", id), zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to update provider")
		return
	}

	writeJSON(w, http.StatusOK, provider)
}

// DeleteProvider handles DELETE /admin/api/providers/{id}.
// After successful deletion, clears the concurrency counter to prevent memory leaks.
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
