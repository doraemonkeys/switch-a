package admin

import (
	"encoding/json"
	"net/http"
	"strconv"

	"switch-a/internal/store"

	"go.uber.org/zap"
)

// ConfigResponse represents the config API response with separated defaults and user values.
// This design allows the frontend to:
// - Know which settings are user-modified vs default
// - Implement "reset to default" functionality
// - Show visual indicators for modified settings
type ConfigResponse struct {
	// Defaults contains all default configuration values.
	Defaults map[string]string `json:"defaults"`
	// Values contains only the user-modified configuration values.
	// If a key exists in Values, it means the user has customized it.
	Values map[string]string `json:"values"`
}

// Config API handlers

// GetConfig handles GET /admin/api/config.
// Returns both defaults and user-modified values separately.
func (h *Handler) GetConfig(w http.ResponseWriter, r *http.Request) {
	values, err := h.store.GetAllConfig(r.Context())
	if err != nil {
		h.logger.Error("failed to get config", zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to get config")
		return
	}

	// Filter out any stale config keys that are no longer valid.
	// This handles database entries from previous versions.
	filteredValues := make(map[string]string)
	for key, value := range values {
		if IsValidConfigKey(key) {
			filteredValues[key] = value
		}
	}

	resp := ConfigResponse{
		Defaults: store.GetDefaultConfigs(),
		Values:   filteredValues,
	}

	writeJSON(w, http.StatusOK, resp)
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

	// Return updated config with defaults and user values separated
	values, err := h.store.GetAllConfig(r.Context())
	if err != nil {
		h.logger.Error("failed to get config after update", zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to get config")
		return
	}

	// Filter out any stale config keys that are no longer valid.
	// This handles database entries from previous versions.
	filteredValues := make(map[string]string)
	for key, value := range values {
		if IsValidConfigKey(key) {
			filteredValues[key] = value
		}
	}

	resp := ConfigResponse{
		Defaults: store.GetDefaultConfigs(),
		Values:   filteredValues,
	}

	writeJSON(w, http.StatusOK, resp)
}
