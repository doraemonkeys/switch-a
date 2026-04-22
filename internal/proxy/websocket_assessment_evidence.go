package proxy

import (
	"encoding/json"
	"net/http"

	"github.com/coder/websocket"
)

const (
	webSocketConnectionLimitErrorType = "websocket_connection_limit_reached"
	webSocketEvidenceJSONLimitBytes   = 4096

	// webSocketEvidenceSchemaVersion pins the schema version consumed by the
	// frontend v2 renderer. The JSON tag is a constant wire contract; any
	// rename here is a schema break.
	webSocketEvidenceSchemaVersion = 2
)

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
) *string {
	evidence := webSocketEvidence{SchemaVersion: webSocketEvidenceSchemaVersion}
	if gateway.StatusCode > 0 || gateway.ErrorCode != "" || gateway.Message != "" {
		evidence.Gateway = &webSocketGatewayEvidence{
			TerminalStatusCode:     gateway.StatusCode,
			TerminalErrorCode:      sanitizeEvidenceSnippet(gateway.ErrorCode),
			TerminalMessageSnippet: sanitizeEvidenceSnippet(gateway.Message),
		}
	}
	if result != nil && (result.HandshakeStatusCode > 0 || result.HandshakeBodySnippet != "") {
		evidence.UpstreamHandshake = &webSocketUpstreamHandshakeEvidence{
			StatusCode:  result.HandshakeStatusCode,
			BodySnippet: sanitizeEvidenceSnippet(result.HandshakeBodySnippet),
		}
	}
	if transport := buildWebSocketTransportDiagnostic(result, fallback, isSyntheticFinal); transport != nil {
		transport.RawErrorSnippet = sanitizeEvidenceSnippet(transport.RawErrorSnippet)
		transport.CloseReasonSnippet = sanitizeEvidenceSnippet(transport.CloseReasonSnippet)
		evidence.Transport = transport
	}
	if result != nil && result.UpstreamError != nil {
		evidence.UpstreamEvent = &webSocketUpstreamEventEvidence{
			EnvelopeType:      sanitizeEvidenceSnippet(result.UpstreamError.EnvelopeType),
			ProviderErrorType: sanitizeEvidenceSnippet(result.UpstreamError.ProviderErrorType),
			ProviderErrorCode: sanitizeEvidenceSnippet(result.UpstreamError.Code),
			StatusCode:        result.UpstreamError.StatusCode,
			MessageSnippet:    sanitizeEvidenceSnippet(result.UpstreamError.Message),
			RawPayloadSnippet: sanitizeEvidenceSnippet(result.UpstreamError.Raw),
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
		return buildWebSocketEvidence(gateway, attempt.Result, attempt.terminalErr(), false)
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
	)
}

// buildWebSocketTransportDiagnostic assembles a transportObservation from the
// WebSocketResult and delegates to the pure derivation function. Keeping the
// observation construction out of the shared derivation layer is deliberate:
// protocol-specific mapping of "runtime fact → observation" lives in the
// protocol package; "observation → diagnostic" lives in the shared layer.
func buildWebSocketTransportDiagnostic(result *WebSocketResult, fallback error, isSyntheticFinal bool) *transportDiagnostic {
	err := fallback
	if result != nil && result.Err != nil {
		err = result.Err
	}
	ws := wsObservation{}
	if result != nil {
		ws.closeError = result.TransportObservation.CloseError
		// `isUnexpectedPeerDisconnect` collapses EOF / ErrUnexpectedEOF / no-
		// status-received into the synthetic StatusNoStatusRcvd close code, so
		// the relay layer signals close_without_status precisely when the
		// outcome used that code AND no concrete CloseError was captured.
		if result.CloseCode == websocket.StatusNoStatusRcvd && ws.closeError == nil {
			ws.closedWithoutStatus = true
		}
		ws.upgradeCompleted = result.HandshakeAccepted
		// Any transported bytes imply a frame was delivered to the client; if
		// byte counters are unavailable, fall back to the ClientVisible flag.
		ws.anyFrameDelivered = result.BytesUpstreamToClient > 0 || result.ClientVisible
		// Propagate the peer that reduction attributed the failure to so the
		// derivation layer can flip `source` for client-originated closes.
		// Without this wire the `source` axis silently stays upstream even
		// when the client tore the connection down (bug flagged by the
		// observability review).
		ws.failurePeer = result.TransportObservation.FailurePeer
	}
	obs := transportObservation{
		protocol:                   transportProtocolWS,
		err:                        err,
		isSuppressedSyntheticFinal: isSyntheticFinal,
		ws:                         ws,
	}
	return deriveTransportDiagnostic(obs)
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
