package admin

import (
	"encoding/json"
	"net/http"
	"strconv"

	"go.uber.org/zap"
)

// Config API handlers

// GetConfig handles GET /admin/api/config.
func (h *Handler) GetConfig(w http.ResponseWriter, r *http.Request) {
	config, err := h.store.GetAllConfig(r.Context())
	if err != nil {
		h.logger.Error("failed to get config", zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to get config")
		return
	}

	writeJSON(w, http.StatusOK, config)
}

// UpdateConfig handles PUT /admin/api/config.
func (h *Handler) UpdateConfig(w http.ResponseWriter, r *http.Request) {
	limitRequestBody(w, r)
	var updates map[string]string
	if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Invalid request body")
		return
	}

	if len(updates) > MaxConfigUpdates {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Too many config updates: maximum "+strconv.Itoa(MaxConfigUpdates)+" allowed")
		return
	}

	// Validate all keys and values before updating
	for key, value := range updates {
		if !IsValidConfigKey(key) {
			writeError(w, http.StatusBadRequest, ErrCodeValidation, "Invalid config key: "+key)
			return
		}
		if err := ValidateConfigValue(key, value); err != nil {
			writeError(w, http.StatusBadRequest, ErrCodeValidation, "Invalid value for "+key+": "+err.Error())
			return
		}
	}

	// Update all configs atomically in a single transaction
	if err := h.store.SetConfigs(r.Context(), updates); err != nil {
		h.logger.Error("failed to update configs", zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to update config")
		return
	}

	// Return updated config
	config, err := h.store.GetAllConfig(r.Context())
	if err != nil {
		h.logger.Error("failed to get config after update", zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to get config")
		return
	}

	writeJSON(w, http.StatusOK, config)
}
