package websocketproxy

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
)

func classifyWebSocketUpstreamMessage(messageType websocket.MessageType, data []byte, parseDegraded bool) webSocketSemanticClassification {
	if parseDegraded || shouldSkipCodexObservedPayload(messageType, data) {
		return webSocketSemanticClassificationUnknown
	}

	var event codexWebSocketEventEnvelope
	if err := json.Unmarshal(data, &event); err != nil {
		return webSocketSemanticClassificationUnknown
	}
	if !codexEventRepresentsError(&event) {
		return webSocketSemanticClassificationUnknown
	}
	return classifyWebSocketUpstreamError(buildWebSocketUpstreamError(&event, data, time.Now().UTC()))
}

func classifyWebSocketUpstreamError(upstreamErr *WebSocketUpstreamError) webSocketSemanticClassification {
	if upstreamErr == nil {
		return webSocketSemanticClassificationUnknown
	}
	identifierClassification, identifierMatched := classifyWebSocketUpstreamErrorIdentifiers(upstreamErr)
	statusClassification, statusMatched := classifyWebSocketUpstreamErrorStatus(upstreamErr.StatusCode)
	if identifierMatched && statusMatched &&
		webSocketSemanticClassificationScope(identifierClassification) !=
			webSocketSemanticClassificationScope(statusClassification) {
		return webSocketSemanticClassificationUnknown
	}
	if identifierMatched {
		return identifierClassification
	}
	if statusMatched {
		return statusClassification
	}
	return webSocketSemanticClassificationUnknown
}

func classifyWebSocketUpstreamErrorStatus(statusCode int) (webSocketSemanticClassification, bool) {
	switch statusCode {
	case http.StatusPaymentRequired,
		http.StatusTooManyRequests,
		http.StatusUnauthorized,
		http.StatusForbidden:
		return webSocketSemanticClassificationProviderScopedAllowlisted, true
	}
	scope, matched := classifyProviderFailureScopeFromStatus(statusCode)
	switch scope {
	case providerFailureScopeProvider:
		return webSocketSemanticClassificationProviderScoped, matched
	case providerFailureScopeClient:
		return webSocketSemanticClassificationClientScoped, matched
	default:
		return webSocketSemanticClassificationUnknown, matched
	}
}

func webSocketSemanticClassificationScope(classification webSocketSemanticClassification) providerFailureScope {
	switch classification {
	case webSocketSemanticClassificationClientScoped:
		return providerFailureScopeClient
	case webSocketSemanticClassificationProviderScoped,
		webSocketSemanticClassificationProviderScopedAllowlisted:
		return providerFailureScopeProvider
	default:
		return providerFailureScopeUnknown
	}
}

func classifyWebSocketUpstreamErrorIdentifiers(upstreamErr *WebSocketUpstreamError) (webSocketSemanticClassification, bool) {
	var errorKeys []string
	if upstreamErr != nil {
		errorKeys = []string{
			normalizeWebSocketSemanticErrorKey(upstreamErr.SemanticErrorKey()),
			normalizeWebSocketSemanticErrorKey(upstreamErr.Code),
		}
	}
	scope, matched := classifyProviderFailureScopeFromIdentifiers(errorKeys)
	switch scope {
	case providerFailureScopeProvider:
		return webSocketSemanticClassificationProviderScopedAllowlisted, matched
	case providerFailureScopeClient:
		return webSocketSemanticClassificationClientScoped, matched
	default:
		return webSocketSemanticClassificationUnknown, matched
	}
}

func classifyWebSocketSemanticErrorKey(key string) (webSocketSemanticClassification, bool) {
	scope, matched := classifyProviderFailureScopeFromIdentifier(normalizeWebSocketSemanticErrorKey(key))
	switch scope {
	case providerFailureScopeProvider:
		return webSocketSemanticClassificationProviderScopedAllowlisted, matched
	case providerFailureScopeClient:
		return webSocketSemanticClassificationClientScoped, matched
	default:
		return webSocketSemanticClassificationUnknown, false
	}
}

// decideWebSocketUpstreamMessage preserves clientVisible as an explicit runtime fact.
// Semantic parsing decides eligibility, but only the relay can decide whether the
// client has already observed upstream data and therefore whether suppression is legal.
func decideWebSocketUpstreamMessage(messageType websocket.MessageType, data []byte, parseDegraded, clientVisible bool) webSocketSemanticDecision {
	classification := classifyWebSocketUpstreamMessage(messageType, data, parseDegraded)
	return decideWebSocketSemanticClassification(classification, clientVisible)
}

func decideWebSocketUpstreamError(upstreamErr *WebSocketUpstreamError, parseDegraded, clientVisible bool) webSocketSemanticDecision {
	classification := webSocketSemanticClassificationUnknown
	if !parseDegraded {
		classification = classifyWebSocketUpstreamError(upstreamErr)
	}
	return decideWebSocketSemanticClassification(classification, clientVisible)
}

func decideWebSocketSemanticClassification(classification webSocketSemanticClassification, clientVisible bool) webSocketSemanticDecision {
	decision := webSocketSemanticDecision{
		Classification: classification,
		FrameDecision:  webSocketSemanticFrameDecisionForward,
	}
	if classification == webSocketSemanticClassificationProviderScopedAllowlisted && !clientVisible {
		decision.FrameDecision = webSocketSemanticFrameDecisionSuppress
	}
	return decision
}

func buildWebSocketUpstreamError(
	event *codexWebSocketEventEnvelope,
	data []byte,
	observedAt time.Time,
) *WebSocketUpstreamError {
	if event == nil {
		return nil
	}

	upstreamErr := &WebSocketUpstreamError{
		EnvelopeType: event.Type,
		EventType:    event.Type,
		StatusCode:   codexEventStatusCode(event),
		ObservedAt:   observedAt,
		Raw:          string(data),
	}
	if event.Error != nil {
		upstreamErr.ProviderErrorType = event.Error.Type
		if event.Error.Type != "" {
			upstreamErr.EventType = event.Error.Type
		}
		if upstreamErr.StatusCode == 0 && event.Error.StatusCode > 0 {
			upstreamErr.StatusCode = event.Error.StatusCode
		}
		upstreamErr.Code = strings.TrimSpace(event.Error.Code)
		upstreamErr.Message = strings.TrimSpace(event.Error.Message)
		if resetAt := unixSecondsToFutureTime(event.Error.ResetsAt, observedAt); resetAt != nil {
			upstreamErr.ResetAt = resetAt
		}
	}
	if upstreamErr.Message == "" {
		if upstreamErr.Code != "" {
			upstreamErr.Message = upstreamErr.Code
		} else {
			upstreamErr.Message = upstreamErr.SemanticErrorKey()
		}
	}
	return upstreamErr
}

func codexEventStatusCode(event *codexWebSocketEventEnvelope) int {
	if event == nil {
		return 0
	}
	if event.StatusCode > 0 {
		return event.StatusCode
	}
	return event.Status
}

func timesEqual(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.Equal(*right)
}

func normalizeWebSocketSemanticErrorKey(key string) string {
	normalized := strings.ToLower(strings.TrimSpace(key))
	normalized = strings.ReplaceAll(normalized, " ", "_")
	return normalized
}
