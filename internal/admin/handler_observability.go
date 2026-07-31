package admin

import (
	"net/http"

	"github.com/doraemonkeys/switch-a/internal/model"

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

// ActiveRequestsResponse represents the response for active requests API.
type ActiveRequestsResponse struct {
	Requests []ActiveRequest `json:"requests"`
	Count    int             `json:"count"`
}

// GetHealth handles GET /admin/api/health.
func (h *Handler) GetHealth(w http.ResponseWriter, r *http.Request) {
	states, err := h.store.ListHealthStates(r.Context())
	if err != nil {
		h.logger.Error("failed to list health states", zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to get health states")
		return
	}
	writeJSON(w, http.StatusOK, states)
}

// GetActiveRequests handles GET /admin/api/requests/active.
func (h *Handler) GetActiveRequests(w http.ResponseWriter, _ *http.Request) {
	if h.activeReqList == nil {
		writeJSON(w, http.StatusOK, ActiveRequestsResponse{Requests: []ActiveRequest{}})
		return
	}
	requests := h.activeReqList.List()
	// The admin contract uses an empty collection, not null, even when an
	// implementation returns a nil slice for an idle system.
	result := append([]ActiveRequest{}, requests...)
	writeJSON(w, http.StatusOK, ActiveRequestsResponse{Requests: result, Count: len(result)})
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
