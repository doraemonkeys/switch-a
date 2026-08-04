package admin

import (
	"context"
	"errors"
	"net/http"

	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/store"

	"go.uber.org/zap"
)

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

	wasEnabled := provider.Enabled
	provider.Enabled = enabled
	update := func() error { return h.store.UpdateProvider(r.Context(), provider) }
	if wasEnabled != enabled {
		err = h.mutateProviderGeneration(id, update)
	} else {
		err = update()
	}
	if err != nil {
		h.logger.Error("failed to update provider", zap.String("id", id), zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to "+action+" provider")
		return
	}
	// Keep health state in sync with the persisted enabled flag so manual API toggles
	// do not leave the circuit breaker believing a provider is still unavailable.
	h.syncHealthManagerState(r.Context(), id, enabled)

	writeJSON(w, http.StatusOK, h.providerPayload(provider))
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
