package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/doraemonkeys/switch-a/internal/codex/startup"
	"github.com/doraemonkeys/switch-a/internal/store"

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

	if err := h.applyValidatedConfigUpdate(r.Context(), updates); err != nil {
		if isCodexFeatureConfigError(err) {
			h.writeCodexFeatureConfigError(w, err)
			return
		}
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

func (h *Handler) applyValidatedConfigUpdate(ctx context.Context, updates map[string]string) error {
	// Serializing validation with persistence prevents two individually valid
	// feature updates from interleaving into an invalid durable combination.
	h.configMutationMu.Lock()
	defer h.configMutationMu.Unlock()
	current, err := h.store.GetAllConfig(ctx)
	if err != nil {
		return err
	}
	snapshot, err := h.codexFeatureCandidate(ctx, current, updates)
	if err != nil {
		return err
	}
	if err := h.store.SetConfigs(ctx, updates); err != nil {
		return err
	}
	return h.publishCodexFeatures(snapshot)
}

func (h *Handler) validateCodexFeatureCandidate(
	ctx context.Context,
	current map[string]string,
	updates map[string]string,
) error {
	_, err := h.codexFeatureCandidate(ctx, current, updates)
	return err
}

func (h *Handler) codexFeatureCandidate(
	ctx context.Context,
	current map[string]string,
	updates map[string]string,
) (codexstartup.Snapshot, error) {
	values := codexstartup.Defaults()
	for key, value := range current {
		if codexstartup.IsKey(key) {
			values[key] = value
		}
	}
	for key, value := range updates {
		if codexstartup.IsKey(key) {
			values[key] = value
		}
	}
	snapshot, err := codexstartup.Parse(values)
	if err != nil {
		return codexstartup.Snapshot{}, err
	}
	if h.codexFeatureValidator != nil {
		if err := h.codexFeatureValidator.ValidateCodexFeatures(ctx, snapshot); err != nil {
			return codexstartup.Snapshot{}, err
		}
		return snapshot, nil
	}
	if err := snapshot.ValidateDependencies(); err != nil {
		return codexstartup.Snapshot{}, err
	}
	return snapshot, nil
}

func (h *Handler) publishCodexFeatures(snapshot codexstartup.Snapshot) error {
	publisher, ok := h.codexFeatureValidator.(CodexFeaturePublisher)
	if !ok {
		return nil
	}
	return publisher.PublishCodexFeatures(snapshot)
}

func (h *Handler) writeCodexFeatureConfigError(w http.ResponseWriter, err error) {
	if h.logger != nil {
		h.logger.Warn("rejected Codex feature configuration", zap.Error(err))
	}
	if isCodexFeatureConfigError(err) {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Invalid Codex feature configuration: "+err.Error())
		return
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		writeError(w, http.StatusRequestTimeout, ErrCodeInternal, "Codex feature validation did not complete")
		return
	}
	writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to validate Codex feature configuration")
}

func isCodexFeatureConfigError(err error) bool {
	return codexstartup.IsError(err, codexstartup.ErrorInvalidConfig) ||
		codexstartup.IsError(err, codexstartup.ErrorDependency) ||
		codexstartup.IsError(err, codexstartup.ErrorCapabilityMissing)
}
