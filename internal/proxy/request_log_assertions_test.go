package proxy

import (
	"encoding/json"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/model"
)

type webSocketEvidence struct {
	SchemaVersion     int                                 `json:"v"`
	Gateway           *webSocketGatewayEvidence           `json:"gateway,omitempty"`
	UpstreamHandshake *webSocketUpstreamHandshakeEvidence `json:"upstream_handshake,omitempty"`
	Transport         *transportDiagnostic                `json:"transport,omitempty"`
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

type webSocketUpstreamEventEvidence struct {
	EnvelopeType      string `json:"envelope_type,omitempty"`
	ProviderErrorType string `json:"provider_error_type,omitempty"`
	ProviderErrorCode string `json:"provider_error_code,omitempty"`
	StatusCode        int    `json:"status_code,omitempty"`
	MessageSnippet    string `json:"message_snippet,omitempty"`
	RawPayloadSnippet string `json:"raw_payload_snippet,omitempty"`
}

func requestLogClientTransportStatusCode(log *model.RequestLog) int {
	if log == nil || log.ClientTransportStatusCode == nil {
		return StatusCodeNoResponse
	}
	return *log.ClientTransportStatusCode
}

func requestLogCompletionState(log *model.RequestLog) model.CompletionState {
	if log == nil || log.CompletionState == nil {
		return ""
	}
	return *log.CompletionState
}

func requestLogServiceOutcome(log *model.RequestLog) model.ServiceOutcome {
	if log == nil || log.ServiceOutcome == nil {
		return ""
	}
	return *log.ServiceOutcome
}

func requestLogTerminationReason(log *model.RequestLog) model.TerminationReason {
	if log == nil || log.TerminationReason == nil {
		return ""
	}
	return *log.TerminationReason
}

func requestLogClientAction(log *model.RequestLog) model.ClientAction {
	if log == nil || log.ClientAction == nil {
		return ""
	}
	return *log.ClientAction
}

func requestLogEvidence(t *testing.T, log *model.RequestLog) webSocketEvidence {
	t.Helper()
	if log == nil || log.SessionEvidenceJSON == nil || *log.SessionEvidenceJSON == "" {
		return webSocketEvidence{}
	}

	var evidence webSocketEvidence
	if err := json.Unmarshal([]byte(*log.SessionEvidenceJSON), &evidence); err != nil {
		t.Fatalf("json.Unmarshal(SessionEvidenceJSON) = %v", err)
	}
	return evidence
}

func requestLogEvidenceMessage(t *testing.T, log *model.RequestLog) string {
	t.Helper()
	evidence := requestLogEvidence(t, log)
	switch {
	case evidence.Gateway != nil && evidence.Gateway.TerminalMessageSnippet != "":
		return evidence.Gateway.TerminalMessageSnippet
	case evidence.UpstreamHandshake != nil && evidence.UpstreamHandshake.BodySnippet != "":
		return evidence.UpstreamHandshake.BodySnippet
	case evidence.UpstreamEvent != nil && evidence.UpstreamEvent.RawPayloadSnippet != "":
		return evidence.UpstreamEvent.RawPayloadSnippet
	case evidence.UpstreamEvent != nil && evidence.UpstreamEvent.MessageSnippet != "":
		return evidence.UpstreamEvent.MessageSnippet
	case evidence.Transport != nil && evidence.Transport.RawErrorSnippet != "":
		return evidence.Transport.RawErrorSnippet
	default:
		return ""
	}
}
