package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"switch-a/internal/model"
)

const (
	webSocketConnectionLimitErrorType    = "websocket_connection_limit_reached"
	webSocketEvidenceSnippetLimitBytes   = 512
	webSocketEvidenceJSONLimitBytes      = 4096
	webSocketEvidenceRedactedPlaceholder = "[REDACTED]"
)

var (
	webSocketEvidenceHeaderSecretPattern = regexp.MustCompile(`(?i)\b(authorization|proxy-authorization|x-api-key|api[_-]?key|cookie|set-cookie)\b\s*[:=]\s*[^\s,;]+`)
	webSocketEvidenceBearerTokenPattern  = regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]+\b`)
	webSocketEvidenceBasicTokenPattern   = regexp.MustCompile(`(?i)\bbasic\s+[A-Za-z0-9._~+/=-]+\b`)
)

type webSocketGatewayEvidenceInput struct {
	StatusCode int
	ErrorCode  string
	Message    string
}

type webSocketEvidence struct {
	Gateway           *webSocketGatewayEvidence           `json:"gateway,omitempty"`
	UpstreamHandshake *webSocketUpstreamHandshakeEvidence `json:"upstream_handshake,omitempty"`
	Transport         *webSocketTransportEvidence         `json:"transport,omitempty"`
	UpstreamEvent     *webSocketUpstreamEventEvidence     `json:"upstream_event,omitempty"`
}

type webSocketGatewayEvidence struct {
	TerminalStatusCode     int    `json:"terminal_status_code,omitempty"`
	TerminalErrorCode      string `json:"terminal_error_code,omitempty"`
	TerminalMessageSnippet string `json:"terminal_message_snippet,omitempty"`
}

type webSocketUpstreamHandshakeEvidence struct {
	StatusCode  int    `json:"status_code,omitempty"`
	BodySnippet string `json:"body_snippet,omitempty"`
}

type webSocketTransportEvidence struct {
	Source          string `json:"source,omitempty"`
	MessageSnippet  string `json:"message_snippet,omitempty"`
	IsTimeout       bool   `json:"is_timeout,omitempty"`
	IsClientCancel  bool   `json:"is_client_cancel,omitempty"`
	RawErrorSnippet string `json:"raw_error_snippet,omitempty"`
}

type webSocketUpstreamEventEvidence struct {
	EnvelopeType      string `json:"envelope_type,omitempty"`
	ProviderErrorType string `json:"provider_error_type,omitempty"`
	ProviderErrorCode string `json:"provider_error_code,omitempty"`
	StatusCode        int    `json:"status_code,omitempty"`
	MessageSnippet    string `json:"message_snippet,omitempty"`
	RawPayloadSnippet string `json:"raw_payload_snippet,omitempty"`
}

// Keep evidence capture isolated so the core assessment path stays focused on
// end-state classification rather than serialization and redaction mechanics.
func buildWebSocketEvidence(
	gateway webSocketGatewayEvidenceInput,
	result *WebSocketResult,
	fallback error,
) *string {
	evidence := webSocketEvidence{}
	if gateway.StatusCode > 0 || gateway.ErrorCode != "" || gateway.Message != "" {
		evidence.Gateway = &webSocketGatewayEvidence{
			TerminalStatusCode:     gateway.StatusCode,
			TerminalErrorCode:      sanitizeWebSocketEvidenceSnippet(gateway.ErrorCode),
			TerminalMessageSnippet: sanitizeWebSocketEvidenceSnippet(gateway.Message),
		}
	}
	if result != nil && (result.HandshakeStatusCode > 0 || result.HandshakeBodySnippet != "") {
		evidence.UpstreamHandshake = &webSocketUpstreamHandshakeEvidence{
			StatusCode:  result.HandshakeStatusCode,
			BodySnippet: sanitizeWebSocketEvidenceSnippet(result.HandshakeBodySnippet),
		}
	}
	if transport := buildWebSocketTransportEvidence(result, fallback); transport != nil {
		evidence.Transport = transport
	}
	if result != nil && result.UpstreamError != nil {
		evidence.UpstreamEvent = &webSocketUpstreamEventEvidence{
			EnvelopeType:      sanitizeWebSocketEvidenceSnippet(result.UpstreamError.EnvelopeType),
			ProviderErrorType: sanitizeWebSocketEvidenceSnippet(result.UpstreamError.ProviderErrorType),
			ProviderErrorCode: sanitizeWebSocketEvidenceSnippet(result.UpstreamError.Code),
			StatusCode:        result.UpstreamError.StatusCode,
			MessageSnippet:    sanitizeWebSocketEvidenceSnippet(result.UpstreamError.Message),
			RawPayloadSnippet: sanitizeWebSocketEvidenceSnippet(result.UpstreamError.Raw),
		}
	}
	if evidence == (webSocketEvidence{}) {
		return nil
	}
	return marshalWebSocketEvidence(evidence)
}

func buildWebSocketAttemptEvidence(attempt WebSocketAttemptResult) *string {
	return buildWebSocketEvidence(
		webSocketGatewayEvidenceInput{
			StatusCode: attempt.GatewayStatusCode,
			ErrorCode:  attempt.GatewayErrorCode,
			Message:    attempt.GatewayMessage,
		},
		attempt.Result,
		attempt.terminalErr(),
	)
}

func buildWebSocketTransportEvidence(result *WebSocketResult, fallback error) *webSocketTransportEvidence {
	err := fallback
	if result != nil && result.Err != nil {
		err = result.Err
	}
	if err == nil {
		return nil
	}

	transport := &webSocketTransportEvidence{
		Source:          webSocketTransportSource(result),
		MessageSnippet:  sanitizeWebSocketEvidenceSnippet(err.Error()),
		IsTimeout:       errors.Is(err, context.DeadlineExceeded),
		IsClientCancel:  errors.Is(err, context.Canceled) || (result != nil && result.TerminalCause == model.TerminalClientDisconnect),
		RawErrorSnippet: sanitizeWebSocketEvidenceSnippet(err.Error()),
	}
	if *transport == (webSocketTransportEvidence{}) {
		return nil
	}
	return transport
}

func marshalWebSocketEvidence(evidence webSocketEvidence) *string {
	serialized, err := json.Marshal(evidence)
	if err != nil {
		return nil
	}
	if len(serialized) <= webSocketEvidenceJSONLimitBytes {
		return ptr(string(serialized))
	}

	trimmed := evidence
	if trimmed.UpstreamEvent != nil {
		trimmed.UpstreamEvent.RawPayloadSnippet = ""
	}
	if trimmed.Transport != nil {
		trimmed.Transport.RawErrorSnippet = ""
	}
	if trimmed.UpstreamHandshake != nil {
		trimmed.UpstreamHandshake.BodySnippet = ""
	}
	if trimmed.Gateway != nil {
		trimmed.Gateway.TerminalMessageSnippet = ""
	}
	if trimmed.UpstreamEvent != nil {
		trimmed.UpstreamEvent.MessageSnippet = ""
	}
	if trimmed.Transport != nil {
		trimmed.Transport.MessageSnippet = ""
	}
	serialized, err = json.Marshal(trimmed)
	if err != nil || len(serialized) > webSocketEvidenceJSONLimitBytes {
		return nil
	}
	return ptr(string(serialized))
}

func sanitizeWebSocketEvidenceSnippet(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	sanitized := webSocketEvidenceHeaderSecretPattern.ReplaceAllString(trimmed, `$1: `+webSocketEvidenceRedactedPlaceholder)
	sanitized = webSocketEvidenceBearerTokenPattern.ReplaceAllString(sanitized, "Bearer "+webSocketEvidenceRedactedPlaceholder)
	sanitized = webSocketEvidenceBasicTokenPattern.ReplaceAllString(sanitized, "Basic "+webSocketEvidenceRedactedPlaceholder)
	return truncateUTF8(sanitized, webSocketEvidenceSnippetLimitBytes)
}

func webSocketTransportSource(result *WebSocketResult) string {
	if result == nil {
		return string(model.TerminationActorUnknown)
	}
	switch result.TerminalCause {
	case model.TerminalClientDisconnect:
		return string(model.TerminationActorClient)
	case model.TerminalClientUpgradeRejected, model.TerminalProviderUnavailable, model.TerminalProviderConfigurationError:
		return string(model.TerminationActorGateway)
	case model.TerminalInternalError:
		return string(model.TerminationActorInternal)
	default:
		return string(model.TerminationActorUpstream)
	}
}

func webSocketClientTransportStatusCode(gatewayStatusCode int, result *WebSocketResult) int {
	if result != nil && result.ClientAccepted {
		return http.StatusSwitchingProtocols
	}
	if gatewayStatusCode > 0 {
		return gatewayStatusCode
	}
	if result == nil {
		return StatusCodeNoResponse
	}
	if !result.HandshakeAccepted {
		if result.HandshakeStatusCode > 0 {
			return result.HandshakeStatusCode
		}
		return StatusCodeNoResponse
	}
	return StatusCodeNoResponse
}

func webSocketAttemptTransportStatusCode(result *WebSocketResult) int {
	if result == nil {
		return StatusCodeNoResponse
	}
	if !result.HandshakeAccepted {
		if result.HandshakeStatusCode > 0 {
			return result.HandshakeStatusCode
		}
		return StatusCodeNoResponse
	}
	return http.StatusSwitchingProtocols
}
