package admin

import (
	"net/http"

	"switch-a/internal/model"

	"go.uber.org/zap"
)

// Status API handlers

// ProviderStatus represents the status of a single provider.
type ProviderStatus struct {
	ID              string             `json:"id"`
	Name            string             `json:"name"`
	Enabled         bool               `json:"enabled"`
	CurrentRequests int64              `json:"current_requests"`
	Health          *model.HealthState `json:"health"`
}

// SystemStatus represents the overall system status.
type SystemStatus struct {
	Providers []ProviderStatus `json:"providers"`
}

// GetStatus handles GET /admin/api/status.
func (h *Handler) GetStatus(w http.ResponseWriter, r *http.Request) {
	providers, err := h.store.ListProviders(r.Context())
	if err != nil {
		h.logger.Error("failed to list providers", zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to get status")
		return
	}

	status := SystemStatus{
		Providers: make([]ProviderStatus, len(providers)),
	}

	for i, p := range providers {
		ps := ProviderStatus{
			ID:      p.ID,
			Name:    p.Name,
			Enabled: p.Enabled,
		}

		// Get current concurrency
		if h.concurrency != nil {
			ps.CurrentRequests = h.concurrency.Current(p.ID)
		}

		// Get health state.
		// Partial failure design: if health state retrieval fails for a provider,
		// we log and continue rather than failing the entire request. This allows
		// clients to receive status for all available providers even when some
		// health data is temporarily unavailable.
		state, err := h.store.GetHealthState(r.Context(), p.ID)
		if err != nil {
			h.logger.Warn("failed to get health state", zap.String("id", p.ID), zap.Error(err))
		} else {
			ps.Health = state
		}

		status.Providers[i] = ps
	}

	writeJSON(w, http.StatusOK, status)
}
