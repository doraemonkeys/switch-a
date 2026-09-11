package tokenusageapi

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"strings"

	"github.com/doraemonkeys/switch-a/internal/tokenanalytics"
	"go.uber.org/zap"
)

const clientAPIKeyFilterName = "client_api_key_fingerprint"

type ClientAPIKeyDTO struct {
	Fingerprint string `json:"fingerprint"`
	Name        string `json:"name"`
	MaskedKey   string `json:"masked_key"`
}

// Omission means all traffic; an explicit empty value means no attributable key.
// Keeping that distinction avoids reserving a magic credential or fingerprint.
func clientAPIKeyFilter(values url.Values) (*string, error) {
	raw, present := values[clientAPIKeyFilterName]
	if !present {
		return nil, nil
	}
	if len(raw) != 1 {
		return nil, &validationError{field: clientAPIKeyFilterName, reason: "duplicate"}
	}
	value := raw[0]
	if value != "" {
		if len(value) != sha256.Size*2 {
			return nil, &validationError{field: clientAPIKeyFilterName, reason: "invalid_fingerprint"}
		}
		decoded, err := hex.DecodeString(value)
		if err != nil || len(decoded) != sha256.Size || value != strings.ToLower(value) {
			return nil, &validationError{field: clientAPIKeyFilterName, reason: "invalid_fingerprint"}
		}
	}
	return &value, nil
}

func (h *Handler) GetClientAPIKeys(w http.ResponseWriter, r *http.Request) {
	startedAt := h.clock.Now()
	log := h.logger.With(zap.String("operation", "token_usage_client_api_keys"), zap.String("operation_id", h.operationIDs.NewOperationID()))
	log.Debug("token usage client API keys started")
	keys, err := h.analyzer.ClientAPIKeys(r.Context())
	if err != nil {
		log.Error("token usage client API keys failed", zap.String("failure_stage", string(failureStage(err))), zap.String("failure_code", string(tokenanalytics.FailureCodeOf(err))), zap.Duration("duration", h.clock.Now().Sub(startedAt)))
		writeError(w, http.StatusInternalServerError, internalErrorCode, "Failed to get token usage API keys", nil)
		return
	}
	log.Debug("token usage client API keys completed", zap.Int("key_count", len(keys)), zap.Duration("duration", h.clock.Now().Sub(startedAt)))
	response := make([]ClientAPIKeyDTO, len(keys))
	for index, key := range keys {
		response[index] = ClientAPIKeyDTO{Fingerprint: key.Fingerprint, Name: key.Name, MaskedKey: key.MaskedKey}
	}
	writeJSON(w, http.StatusOK, response)
}
