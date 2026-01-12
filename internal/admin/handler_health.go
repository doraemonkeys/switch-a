package admin

import (
	"net/http"

	"go.uber.org/zap"
)

// Health API handlers

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
