package debugcapture

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/doraemonkeys/switch-a/internal/requestcapture"

	"go.uber.org/zap"
)

func (h *Handler) StartSession(w http.ResponseWriter, r *http.Request) {
	operation := operationContext{name: "start_session"}
	if h.sessions == nil || h.providers == nil {
		h.writeUnavailable(w, operation)
		return
	}

	var input StartSessionRequest
	if err := decodeJSONBody(w, r, &input); err != nil {
		h.writeBodyDecodeError(w, err, operation)
		return
	}
	if !input.AcknowledgeRawPayloadRisk {
		h.writeError(w, http.StatusBadRequest, errorCodeValidation, "acknowledge_raw_payload_risk must be true", map[string]string{"field": "acknowledge_raw_payload_risk"}, operation)
		return
	}
	providers, validation, err := h.resolveProviders(r, input.ProviderIDs)
	if err != nil {
		if requestContextEnded(r.Context(), err) {
			return
		}
		h.logger.Error("failed to resolve debug capture providers",
			zap.String("operation", operation.name),
			zap.Int("requested_provider_count", len(input.ProviderIDs)),
			// Provider-store errors can embed connection details or provider data;
			// retain only the stable boundary reason in observability output.
			zap.String("failure_reason", failureReasonProviderCatalogFailure),
		)
		h.writeError(w, http.StatusInternalServerError, errorCodeInternal, "Unable to resolve debug capture providers", nil, operation)
		return
	}
	if validation != nil {
		h.writeError(w, http.StatusBadRequest, errorCodeValidation, validation.message, validation.details, operation)
		return
	}

	session, err := h.sessions.Start(requestcapture.StartRequest{
		Providers:                   providers,
		CompletedRecordsPerProvider: input.CompletedRecordsPerProvider,
		RetainedBytesLimit:          input.RetainedBytesLimit,
		AcknowledgeRawPayloadRisk:   input.AcknowledgeRawPayloadRisk,
	})
	if err != nil {
		h.writeServiceError(w, err, operation)
		return
	}
	operation.sessionID = session.SessionID
	w.Header().Set("Location", "/admin/api/debug-capture/sessions/"+url.PathEscape(session.SessionID))
	h.logger.Info("debug capture session started through admin API",
		zap.String("session_id", session.SessionID),
		zap.Int("provider_count", len(session.ProviderIDs)),
	)
	h.writeJSON(w, http.StatusCreated, session, operation)
}

type inputValidation struct {
	message string
	details map[string]string
}

func (h *Handler) resolveProviders(r *http.Request, providerIDs []string) ([]requestcapture.ProviderIdentity, *inputValidation, error) {
	if len(providerIDs) == 0 {
		return nil, &inputValidation{message: "provider_ids must contain at least one provider", details: map[string]string{"field": "provider_ids"}}, nil
	}

	normalized := make([]string, 0, len(providerIDs))
	seen := make(map[string]struct{}, len(providerIDs))
	for _, rawID := range providerIDs {
		id := strings.TrimSpace(rawID)
		if id == "" {
			return nil, &inputValidation{message: "provider_ids must not contain empty values", details: map[string]string{"field": "provider_ids"}}, nil
		}
		if id != rawID {
			return nil, &inputValidation{message: "provider_ids must not contain surrounding whitespace", details: map[string]string{"field": "provider_ids"}}, nil
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, &inputValidation{message: "provider_ids must contain unique values", details: map[string]string{"field": "provider_ids", "provider_id": id}}, nil
		}
		seen[id] = struct{}{}
		normalized = append(normalized, id)
	}

	providers, err := h.providers.ListProviders(r.Context())
	if err != nil {
		return nil, nil, err
	}
	identities := make(map[string]requestcapture.ProviderIdentity, len(providers))
	for i := range providers {
		identities[providers[i].ID] = requestcapture.ProviderIdentity{ID: providers[i].ID, Name: providers[i].Name}
	}

	resolved := make([]requestcapture.ProviderIdentity, 0, len(normalized))
	for _, id := range normalized {
		provider, exists := identities[id]
		if !exists {
			return nil, &inputValidation{message: "provider_ids contains an unknown provider", details: map[string]string{"field": "provider_ids", "provider_id": id}}, nil
		}
		resolved = append(resolved, provider)
	}
	return resolved, nil, nil
}

func (h *Handler) Status(w http.ResponseWriter, r *http.Request) {
	operation := operationContext{name: "status"}
	if h.sessions == nil {
		h.writeUnavailable(w, operation)
		return
	}
	lease, err := h.sessions.OpenStatus(r.Context())
	if err != nil {
		h.writeServiceError(w, err, operation)
		return
	}
	defer lease.Close()
	h.writeStatusJSON(w, r.Context(), lease, operation)
}

func (h *Handler) StopSession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session_id")
	operation := operationContext{name: "stop_session"}
	if h.sessions == nil {
		h.writeUnavailable(w, operation)
		return
	}
	if !requestcapture.IsCanonicalSessionID(sessionID) {
		h.writeError(w, http.StatusBadRequest, errorCodeValidation, "session_id must be a non-empty canonical identifier", map[string]string{"field": "session_id"}, operation)
		return
	}
	operation.sessionID = sessionID
	if err := h.sessions.Stop(sessionID); err != nil {
		h.writeServiceError(w, err, operation)
		return
	}
	h.logger.Info("debug capture session stopped through admin API", zap.String("session_id", sessionID))
	w.WriteHeader(http.StatusNoContent)
}
