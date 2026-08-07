package websocketproxy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/coder/websocket"
	"github.com/doraemonkeys/switch-a/internal/attemptevidence"
)

const (
	webSocketConnectionLimitErrorType = "websocket_connection_limit_reached"
	webSocketEvidenceJSONLimitBytes   = 4096

	// webSocketEvidenceSchemaVersion pins the schema version consumed by the
	// frontend v2 renderer. The JSON tag is a constant wire contract; any
	// rename here is a schema break.
	webSocketEvidenceSchemaVersion = 2

	transportSourceUpstream            = "upstream"
	transportSourceClient              = "client"
	transportStagePreConnectionVisible = "pre_connection_visible"
	transportStagePrePayloadVisible    = "pre_payload_visible"
	transportStagePostPayloadVisible   = "post_payload_visible"
	transportKindTimeout               = "timeout"
	transportKindDisconnect            = "disconnect"
	transportKindLocalError            = "local_error"
	transportSignalEOF                 = "eof"
	transportSignalUnexpectedEOF       = "unexpected_eof"
	transportSignalCloseWithoutStatus  = "close_without_status"
	transportSignalCloseError          = "close_error"
	transportSignalTimeout             = "timeout"
	transportSignalCanceled            = "canceled"
	transportSignalUnknownTransport    = "unknown_transport"
	transportRawErrorSnippetLimitRunes = 256
)

// sanitizeEvidenceSnippet keeps WebSocket evidence transparent unless the
// current attempt explicitly supplied a switch-a credential. This avoids losing
// provider-owned tokens or close/error diagnostics merely because they resemble
// credentials.
func sanitizeEvidenceSnippet(value, injectedCredential string) string {
	return attemptevidence.SanitizeSnippet(value, injectedCredential)
}

type transportDiagnostic struct {
	Source             string `json:"source"`
	Stage              string `json:"stage"`
	Kind               string `json:"kind"`
	Signal             string `json:"signal"`
	RawErrorSnippet    string `json:"raw_error_snippet,omitempty"`
	CloseCode          *int   `json:"close_code,omitempty"`
	CloseReasonSnippet string `json:"close_reason_snippet,omitempty"`
}

type webSocketGatewayEvidenceInput struct {
	StatusCode int
	ErrorCode  string
	Message    string
}

// webSocketEvidence is the v2 session / attempt evidence envelope. The
// SchemaVersion field is encoded as `"v"` and is a hard contract: the v2
// renderer keys off this to route to the new parser. A zero SchemaVersion
// (i.e., a bug in the builder that forgets to set it) will fall through to
// the v1 renderer and misrender — the builder always sets it explicitly.
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

// Keep evidence capture isolated so the core assessment path stays focused on
// end-state classification rather than serialization and redaction mechanics.
//
// `isSyntheticFinal` is a belt-and-suspenders second barrier: the primary
// guard is the structural zero-out of WebSocketResult.TransportObservation in
// `applyLastAttemptToSuppressedPayload`. If that guard were ever bypassed by
// a future code path writing to the result between creation and zeroing, this
// flag still shuts down transport-diagnostic emission on the session side.
// Attempt-level callers pass false: attempts always represent their own
// observation, never a replaced inheritance chain.
func buildWebSocketEvidence(
	gateway webSocketGatewayEvidenceInput,
	result *WebSocketResult,
	fallback error,
	isSyntheticFinal bool,
	injectedCredential string,
) *string {
	evidence := webSocketEvidence{SchemaVersion: webSocketEvidenceSchemaVersion}
	if gateway.StatusCode > 0 || gateway.ErrorCode != "" || gateway.Message != "" {
		evidence.Gateway = &webSocketGatewayEvidence{
			TerminalStatusCode:     gateway.StatusCode,
			TerminalErrorCode:      sanitizeEvidenceSnippet(gateway.ErrorCode, injectedCredential),
			TerminalMessageSnippet: sanitizeEvidenceSnippet(gateway.Message, injectedCredential),
		}
	}
	if result != nil && (result.HandshakeStatusCode > 0 || result.HandshakeBodySnippet != "") {
		evidence.UpstreamHandshake = &webSocketUpstreamHandshakeEvidence{
			StatusCode:  result.HandshakeStatusCode,
			BodySnippet: sanitizeEvidenceSnippet(result.HandshakeBodySnippet, injectedCredential),
		}
	}
	if transport := buildWebSocketTransportDiagnostic(result, fallback, isSyntheticFinal); transport != nil {
		transport.RawErrorSnippet = sanitizeEvidenceSnippet(transport.RawErrorSnippet, injectedCredential)
		transport.CloseReasonSnippet = sanitizeEvidenceSnippet(transport.CloseReasonSnippet, injectedCredential)
		evidence.Transport = transport
	}
	if result != nil && result.UpstreamError != nil {
		evidence.UpstreamEvent = &webSocketUpstreamEventEvidence{
			EnvelopeType:      sanitizeEvidenceSnippet(result.UpstreamError.EnvelopeType, injectedCredential),
			ProviderErrorType: sanitizeEvidenceSnippet(result.UpstreamError.ProviderErrorType, injectedCredential),
			ProviderErrorCode: sanitizeEvidenceSnippet(result.UpstreamError.Code, injectedCredential),
			StatusCode:        result.UpstreamError.StatusCode,
			MessageSnippet:    sanitizeEvidenceSnippet(result.UpstreamError.Message, injectedCredential),
			RawPayloadSnippet: sanitizeEvidenceSnippet(result.UpstreamError.Raw, injectedCredential),
		}
	}
	// When only the schema-version marker is populated, suppress the envelope
	// entirely: emitting `{"v":2}` alone would pollute logs without carrying
	// any diagnostic value.
	if evidence.isEmptyPayload() {
		return nil
	}
	return marshalWebSocketEvidence(evidence)
}

// buildWebSocketAttemptEvidence produces attempt-level evidence, including a
// handshake-phase fallback when Result is nil. Without the fallback branch, a
// dial failure that never produced a WebSocketResult would leave the attempt
// row with no transport evidence at all, hiding the only signal that exists
// (the terminal error text).
func buildWebSocketAttemptEvidence(attempt WebSocketAttemptResult) *string {
	gateway := webSocketGatewayEvidenceInput{
		StatusCode: attempt.GatewayStatusCode,
		ErrorCode:  attempt.GatewayErrorCode,
		Message:    attempt.GatewayMessage,
	}
	if attempt.Result != nil {
		return buildWebSocketEvidence(gateway, attempt.Result, attempt.terminalErr(), false, injectedCredentialForCapture(attempt.Provider, attempt.APIType))
	}
	// Synthesize a minimal WebSocketResult so the evidence builder has a
	// canonical observation carrier. FailurePeer=upstream reflects that a nil
	// Result on an attempt means the upstream dial leg produced the error
	// (client accept is handled later, only after an upstream result exists).
	return buildWebSocketEvidence(
		gateway,
		&WebSocketResult{
			Err: attempt.terminalErr(),
			TransportObservation: WebSocketTransportObservation{
				FailurePeer: webSocketPeerUpstream,
			},
		},
		attempt.terminalErr(),
		false,
		injectedCredentialForCapture(attempt.Provider, attempt.APIType),
	)
}

// buildWebSocketTransportDiagnostic assembles a transportObservation from the
// WebSocketResult and delegates to the pure derivation function. Keeping the
// observation construction out of the shared derivation layer is deliberate:
// protocol-specific mapping of "runtime fact → observation" lives in the
// protocol package; "observation → diagnostic" lives in the shared layer.
func buildWebSocketTransportDiagnostic(result *WebSocketResult, fallback error, isSyntheticFinal bool) *transportDiagnostic {
	if isSyntheticFinal {
		return nil
	}
	err := fallback
	if result != nil && result.Err != nil {
		err = result.Err
	}
	var closeError *websocket.CloseError
	closedWithoutStatus := false
	stage := transportStagePreConnectionVisible
	source := transportSourceUpstream
	if result != nil {
		closeError = result.TransportObservation.CloseError
		// `isUnexpectedPeerDisconnect` collapses EOF / ErrUnexpectedEOF / no-
		// status-received into the synthetic StatusNoStatusRcvd close code, so
		// the relay layer signals close_without_status precisely when the
		// outcome used that code AND no concrete CloseError was captured.
		if result.CloseCode == websocket.StatusNoStatusRcvd && closeError == nil {
			closedWithoutStatus = true
		}
		switch {
		case result.BytesUpstreamToClient > 0 || result.ClientVisible:
			stage = transportStagePostPayloadVisible
		case result.HandshakeAccepted:
			stage = transportStagePrePayloadVisible
		}
		if result.TransportObservation.FailurePeer == webSocketPeerClient {
			source = transportSourceClient
		}
	}
	if err == nil && closeError == nil && !closedWithoutStatus {
		return nil
	}

	signal, kind := transportSignalUnknownTransport, transportKindLocalError
	switch {
	case closeError != nil:
		signal, kind = transportSignalCloseError, transportKindDisconnect
	case errors.Is(err, context.DeadlineExceeded):
		signal, kind, source = transportSignalTimeout, transportKindTimeout, transportSourceUpstream
	case errors.Is(err, context.Canceled):
		signal, kind, source = transportSignalCanceled, transportKindLocalError, transportSourceClient
	case errors.Is(err, io.ErrUnexpectedEOF):
		signal, kind = transportSignalUnexpectedEOF, transportKindDisconnect
	case errors.Is(err, io.EOF):
		signal, kind = transportSignalEOF, transportKindDisconnect
	case closedWithoutStatus:
		signal, kind = transportSignalCloseWithoutStatus, transportKindDisconnect
	}
	diagnostic := &transportDiagnostic{
		Source: source, Stage: stage, Kind: kind, Signal: signal,
		RawErrorSnippet: truncateTransportSnippet(errorText(err)),
	}
	if closeError != nil {
		code := int(closeError.Code)
		diagnostic.CloseCode = &code
		diagnostic.CloseReasonSnippet = truncateTransportSnippet(closeError.Reason)
	}
	return diagnostic
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func truncateTransportSnippet(value string) string {
	runes := 0
	for index := range value {
		if runes == transportRawErrorSnippetLimitRunes {
			return value[:index]
		}
		runes++
	}
	return value
}

// isEmptyPayload detects whether the evidence envelope carries any content
// beyond the schema-version field. Without this check, every request would
// emit `{"v":2}` even when no diagnostic data exists, wasting log bytes and
// distorting evidence-presence dashboards.
func (e webSocketEvidence) isEmptyPayload() bool {
	return e.Gateway == nil && e.UpstreamHandshake == nil && e.Transport == nil && e.UpstreamEvent == nil
}

// marshalWebSocketEvidence enforces the 4 KiB evidence budget by trimming
// snippet fields in a deterministic order when the payload overflows. The
// order reflects "easiest to recover from other sources" first:
//
//  1. UpstreamEvent.RawPayloadSnippet — the full original event body, usually
//     recoverable from upstream logs if needed.
//  2. Transport.CloseReasonSnippet — a human-readable close reason; the
//     structured close_code + signal fields still carry the diagnostic value
//     after this is dropped.
//  3. Transport.RawErrorSnippet — the raw error text is diagnostic-critical
//     but redundant with Transport.Signal once the renderer maps it.
//  4. UpstreamHandshake.BodySnippet, Gateway.TerminalMessageSnippet,
//     UpstreamEvent.MessageSnippet — long-form human text; structured codes
//     remain intact so the summary still renders.
//
// After this pass the structural classification fields (Signal / Kind /
// Stage / Source / CloseCode / provider error codes) are preserved so the
// frontend summary helper can still produce a useful line.
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
		trimmed.Transport.CloseReasonSnippet = ""
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
	serialized, err = json.Marshal(trimmed)
	if err != nil || len(serialized) > webSocketEvidenceJSONLimitBytes {
		return nil
	}
	return ptr(string(serialized))
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
