package websocketproxy

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/model"

	"github.com/coder/websocket"
)

// These tests exercise Step #3 wiring: per-peer CloseError propagation, the
// synthetic-final structural guard on TransportObservation, Clone() copy
// semantics, attempt.Result=nil fallback evidence, trim-order after the
// schema switch, and the suppressed-pre-visible → nil diagnostic invariant.

func TestWebSocketRelayResult_CloseErrorPropagatedThroughReduction(t *testing.T) {
	t.Parallel()

	closeErr := websocket.CloseError{Code: websocket.StatusAbnormalClosure, Reason: "broken pipe"}
	var order atomic.Uint32
	primary := newWebSocketRelayResultForOperation(
		0,
		closeErr,
		webSocketPeerUpstream,
		webSocketRelayFailureOperationRead,
		&order,
	)
	if primary.closeError == nil {
		t.Fatal("closeError = nil on primary, want populated")
	}
	outcome := reduceOrderedWebSocketRelayResults(primary, webSocketRelayResult{})
	if outcome.observedCloseError == nil {
		t.Fatal("outcome.observedCloseError = nil, want primary closeError forwarded")
	}
	if outcome.observedCloseError.Code != websocket.StatusAbnormalClosure {
		t.Fatalf("outcome.observedCloseError.Code = %d, want %d",
			outcome.observedCloseError.Code, websocket.StatusAbnormalClosure)
	}
	if outcome.failurePeer != webSocketPeerUpstream {
		t.Fatalf("outcome.failurePeer = %v, want upstream", outcome.failurePeer)
	}

	session := newWebSocketRelaySessionResultFromOutcome(outcome, nil, nil, 0, 0)
	ws := session.toWebSocketResult()
	if ws.TransportObservation.CloseError == nil {
		t.Fatal("WebSocketResult.TransportObservation.CloseError = nil, want propagated")
	}
	if ws.TransportObservation.CloseError.Code != websocket.StatusAbnormalClosure {
		t.Fatalf("propagated close code = %d, want %d",
			ws.TransportObservation.CloseError.Code, websocket.StatusAbnormalClosure)
	}
	if ws.TransportObservation.FailurePeer != webSocketPeerUpstream {
		t.Fatalf("propagated FailurePeer = %v, want upstream", ws.TransportObservation.FailurePeer)
	}
}

func TestWebSocketRelayResult_WriteCloseIsNotPeerObservation(t *testing.T) {
	t.Parallel()

	var order atomic.Uint32
	result := newWebSocketRelayResultForOperation(
		0,
		websocket.CloseError{Code: websocket.StatusNormalClosure, Reason: "propagated locally"},
		webSocketPeerUpstream,
		webSocketRelayFailureOperationWrite,
		&order,
	)
	if result.closeError != nil {
		t.Fatalf("write failure close observation = %#v, want nil", result.closeError)
	}
	outcome := reduceOrderedWebSocketRelayResults(result, webSocketRelayResult{})
	if outcome.observedCloseError != nil {
		t.Fatalf("reduced write failure close observation = %#v, want nil", outcome.observedCloseError)
	}
}

func TestWebSocketRelayResult_SinglePeerResultInheritsObservation(t *testing.T) {
	t.Parallel()

	// A single peer read runs through the same reduction path, so the observed
	// frame must flow through without any special-case plumbing.
	closeErr := websocket.CloseError{Code: websocket.StatusPolicyViolation}
	session := newSinglePeerRelaySessionResultForOperation(
		closeErr,
		webSocketPeerUpstream,
		webSocketRelayFailureOperationRead,
		nil,
		nil,
		0,
		0,
	)
	ws := session.toWebSocketResult()
	if ws.TransportObservation.CloseError == nil || ws.TransportObservation.CloseError.Code != websocket.StatusPolicyViolation {
		t.Fatalf("TransportObservation.CloseError = %+v, want policy-violation forwarded",
			ws.TransportObservation.CloseError)
	}
	if ws.TransportObservation.FailurePeer != webSocketPeerUpstream {
		t.Fatalf("TransportObservation.FailurePeer = %v, want upstream", ws.TransportObservation.FailurePeer)
	}
}

func TestWebSocketResult_ClonePreservesTransportObservation(t *testing.T) {
	t.Parallel()

	closeErr := &websocket.CloseError{Code: websocket.StatusInternalError, Reason: "boom"}
	original := &WebSocketResult{
		TransportObservation: WebSocketTransportObservation{
			CloseError:  closeErr,
			FailurePeer: webSocketPeerUpstream,
		},
	}
	clone := original.Clone()
	if clone.TransportObservation.CloseError != closeErr {
		t.Fatalf("Clone().TransportObservation.CloseError = %p, want pointer-identical %p",
			clone.TransportObservation.CloseError, closeErr)
	}
	if clone.TransportObservation.FailurePeer != webSocketPeerUpstream {
		t.Fatalf("Clone().TransportObservation.FailurePeer = %v, want upstream",
			clone.TransportObservation.FailurePeer)
	}
}

func TestWebSocketResult_CloneNilSafe(t *testing.T) {
	t.Parallel()

	var nilResult *WebSocketResult
	if got := nilResult.Clone(); got != nil {
		t.Fatalf("nil Clone() = %+v, want nil", got)
	}
}

// synthetic-final structural guard: the replaced attempt's CloseError must
// NOT reach the final session, and the session evidence builder must emit
// no transport diagnostic.
func TestApplyLastAttemptToSuppressedPayload_ZerosTransportObservation(t *testing.T) {
	t.Parallel()

	lastAttempt := WebSocketAttemptResult{
		Provider: &model.Provider{ID: "provider-origin"},
		Result: &WebSocketResult{
			HandshakeAccepted: true,
			TerminalCause:     model.TerminalUpstreamTransportError,
			TransportObservation: WebSocketTransportObservation{
				CloseError:  &websocket.CloseError{Code: websocket.StatusAbnormalClosure},
				FailurePeer: webSocketPeerUpstream,
			},
		},
		ForwardErr: errors.New("original transport failure"),
	}
	finalResult := newWebSocketGatewayFailureResult(
		http.StatusServiceUnavailable,
		model.TerminalProviderUnavailable,
		errors.New("no provider"),
	)

	_, _, _, _, _ = applyLastAttemptToSuppressedPayload(
		lastAttempt,
		nil,
		errors.New("no provider"),
		finalResult,
		http.StatusServiceUnavailable,
		ErrCodeProviderUnavailable,
		"No available provider",
	)

	// TerminalCause inheritance policy is unchanged: the synthetic final still
	// adopts the replaced attempt's terminal cause (the plan explicitly
	// preserves this).
	if finalResult.TerminalCause != model.TerminalUpstreamTransportError {
		t.Fatalf("TerminalCause = %q, want inherited %q",
			finalResult.TerminalCause, model.TerminalUpstreamTransportError)
	}
	// The TransportObservation must be zeroed — not just CloseError, the
	// entire nested struct, so no partial-inheritance bug can slip through.
	if finalResult.TransportObservation != (WebSocketTransportObservation{}) {
		t.Fatalf("TransportObservation = %+v, want zero value", finalResult.TransportObservation)
	}
}

// End-to-end: synthetic final session evidence must not emit transport
// diagnostic even if something reintroduced a non-zero observation (the
// isSyntheticFinal flag is the second barrier).
func TestBuildWebSocketEvidence_SyntheticFinal_SkipsTransportDiagnostic(t *testing.T) {
	t.Parallel()

	result := &WebSocketResult{
		HandshakeAccepted: true,
		TerminalCause:     model.TerminalUpstreamTransportError,
		Err:               errors.New("simulated transport error"),
		// Intentionally non-zero to exercise the second barrier.
		TransportObservation: WebSocketTransportObservation{
			CloseError:  &websocket.CloseError{Code: websocket.StatusAbnormalClosure},
			FailurePeer: webSocketPeerUpstream,
		},
	}

	evidenceJSON := buildWebSocketEvidence(
		webSocketGatewayEvidenceInput{
			StatusCode: http.StatusServiceUnavailable,
			ErrorCode:  ErrCodeProviderUnavailable,
			Message:    "No available provider",
		},
		result,
		errors.New("no provider"),
		true, // isSyntheticFinal
		"",
	)
	if evidenceJSON == nil {
		t.Fatal("evidenceJSON = nil, want gateway evidence present")
	}
	var evidence webSocketEvidence
	if err := json.Unmarshal([]byte(*evidenceJSON), &evidence); err != nil {
		t.Fatalf("json.Unmarshal = %v", err)
	}
	if evidence.Transport != nil {
		t.Fatalf("Transport = %+v, want nil for synthetic final", evidence.Transport)
	}
	if evidence.SchemaVersion != webSocketEvidenceSchemaVersion {
		t.Fatalf("SchemaVersion = %d, want %d", evidence.SchemaVersion, webSocketEvidenceSchemaVersion)
	}
}

// Non-synthetic session path with a real observation must emit a transport
// diagnostic carrying the CloseError-derived fields.
func TestBuildWebSocketEvidence_LivePathEmitsTransportDiagnostic(t *testing.T) {
	t.Parallel()

	result := &WebSocketResult{
		HandshakeAccepted: true,
		ClientVisible:     true,
		TerminalCause:     model.TerminalUpstreamTransportError,
		Err:               io.ErrUnexpectedEOF,
		TransportObservation: WebSocketTransportObservation{
			CloseError:  &websocket.CloseError{Code: websocket.StatusInternalError, Reason: "oops"},
			FailurePeer: webSocketPeerUpstream,
		},
	}
	evidenceJSON := buildWebSocketEvidence(webSocketGatewayEvidenceInput{}, result, nil, false, "")
	if evidenceJSON == nil {
		t.Fatal("evidenceJSON = nil, want transport diagnostic")
	}
	var evidence webSocketEvidence
	if err := json.Unmarshal([]byte(*evidenceJSON), &evidence); err != nil {
		t.Fatalf("json.Unmarshal = %v", err)
	}
	if evidence.Transport == nil {
		t.Fatal("Transport = nil, want diagnostic")
	}
	if evidence.Transport.Signal != transportSignalCloseError {
		t.Fatalf("Signal = %q, want %q", evidence.Transport.Signal, transportSignalCloseError)
	}
	if evidence.Transport.CloseCode == nil || *evidence.Transport.CloseCode != int(websocket.StatusInternalError) {
		t.Fatalf("CloseCode = %+v, want %d", evidence.Transport.CloseCode, websocket.StatusInternalError)
	}
	if evidence.Transport.Stage != transportStagePostPayloadVisible {
		t.Fatalf("Stage = %q, want %q", evidence.Transport.Stage, transportStagePostPayloadVisible)
	}
	if evidence.Transport.CloseReasonSnippet != "oops" {
		t.Fatalf("CloseReasonSnippet = %q, want %q", evidence.Transport.CloseReasonSnippet, "oops")
	}
}

// attempt.Result == nil must still produce evidence via attempt.terminalErr()
// so handshake-phase failures don't vanish.
func TestBuildWebSocketAttemptEvidence_NilResult_UsesTerminalErrFallback(t *testing.T) {
	t.Parallel()

	attempt := WebSocketAttemptResult{
		Provider:   &model.Provider{ID: "p1"},
		Result:     nil, // pre-handshake failure path
		ForwardErr: errors.New("dial tcp: i/o timeout"),
	}
	evidenceJSON := buildWebSocketAttemptEvidence(attempt)
	if evidenceJSON == nil {
		t.Fatal("evidenceJSON = nil, want fallback evidence")
	}
	var evidence webSocketEvidence
	if err := json.Unmarshal([]byte(*evidenceJSON), &evidence); err != nil {
		t.Fatalf("json.Unmarshal = %v", err)
	}
	if evidence.Transport == nil {
		t.Fatal("Transport = nil, want synthesized diagnostic")
	}
	if evidence.Transport.Stage != transportStagePreConnectionVisible {
		t.Fatalf("Stage = %q, want %q", evidence.Transport.Stage, transportStagePreConnectionVisible)
	}
	if evidence.SchemaVersion != webSocketEvidenceSchemaVersion {
		t.Fatalf("SchemaVersion = %d, want %d", evidence.SchemaVersion, webSocketEvidenceSchemaVersion)
	}
}

// A suppressed pre-visible relay result carries no transport signal (the
// relay layer reports it as a semantic error), so the evidence builder must
// return nil diagnostic — the semantic error surfaces via UpstreamEvent.
func TestBuildWebSocketEvidence_SuppressedPreVisibleYieldsNilTransport(t *testing.T) {
	t.Parallel()

	decision := webSocketPreWriteDecision{
		SuppressedUpstreamError: &WebSocketUpstreamError{
			EventType:  "auth_error",
			Code:       "invalid_api_key",
			StatusCode: http.StatusUnauthorized,
			Raw:        `{"type":"error"}`,
		},
		SuppressedMessageType: websocket.MessageText,
		SuppressedMessageData: []byte(`{"type":"error"}`),
	}
	relay := newSuppressedPreVisibleRelayResult(newWebSocketCommitState(), webSocketLifecycleSnapshot{ClientAccepted: true}, 0, decision)
	result := relay.toWebSocketResult()
	// TransportObservation must be zero on the suppressed path — no CloseError
	// was observed; the provider produced a semantic envelope.
	if result.TransportObservation != (WebSocketTransportObservation{}) {
		t.Fatalf("TransportObservation = %+v, want zero for suppressed-pre-visible",
			result.TransportObservation)
	}
	evidenceJSON := buildWebSocketEvidence(webSocketGatewayEvidenceInput{}, result, nil, false, "")
	if evidenceJSON == nil {
		t.Fatal("evidenceJSON = nil, want upstream_event evidence")
	}
	var evidence webSocketEvidence
	if err := json.Unmarshal([]byte(*evidenceJSON), &evidence); err != nil {
		t.Fatalf("json.Unmarshal = %v", err)
	}
	if evidence.Transport != nil {
		t.Fatalf("Transport = %+v, want nil — semantic error path must not emit transport diagnostic",
			evidence.Transport)
	}
	if evidence.UpstreamEvent == nil {
		t.Fatal("UpstreamEvent = nil, want semantic error evidence")
	}
}

// Session-level assessWebSocketSession for a synthetic-final session must
// skip transport diagnostic end-to-end, including when the session has a
// non-zero TransportObservation that was smuggled in via some other path.
func TestAssessWebSocketSession_SyntheticFinalSkipsTransportDiagnostic(t *testing.T) {
	t.Parallel()

	session := &WebSocketSessionResult{
		RequestID:                           "req-synthetic",
		FinalProvider:                       &model.Provider{ID: "provider-origin"},
		FinalErr:                            errors.New("no provider"),
		GatewayStatusCode:                   http.StatusServiceUnavailable,
		GatewayErrorCode:                    ErrCodeProviderUnavailable,
		GatewayMessage:                      "No available provider",
		syntheticFinalFromSuppressedPayload: true,
		FinalResult: &WebSocketResult{
			HandshakeAccepted: true,
			TerminalCause:     model.TerminalUpstreamTransportError,
			Err:               errors.New("broken pipe"),
			TransportObservation: WebSocketTransportObservation{
				CloseError: &websocket.CloseError{Code: websocket.StatusAbnormalClosure},
			},
		},
	}
	assessment := assessWebSocketSession(session)
	if assessment.SessionEvidenceJSON == nil {
		t.Fatal("SessionEvidenceJSON = nil, want gateway evidence")
	}
	var evidence webSocketEvidence
	if err := json.Unmarshal([]byte(*assessment.SessionEvidenceJSON), &evidence); err != nil {
		t.Fatalf("json.Unmarshal = %v", err)
	}
	if evidence.Transport != nil {
		t.Fatalf("Transport = %+v, want nil for synthetic final session", evidence.Transport)
	}
	// Gateway evidence must still be present — the session has a clear
	// gateway-layer classification.
	if evidence.Gateway == nil {
		t.Fatal("Gateway = nil, want gateway evidence retained")
	}
}

// Evidence JSON always carries the schema version even on bare upstream-
// handshake or gateway-only evidence, so the v2 renderer can match reliably.
func TestBuildWebSocketEvidence_AlwaysStampsSchemaVersion(t *testing.T) {
	t.Parallel()

	evidenceJSON := buildWebSocketEvidence(
		webSocketGatewayEvidenceInput{StatusCode: http.StatusBadGateway, ErrorCode: "bad"},
		nil,
		nil,
		false,
		"",
	)
	if evidenceJSON == nil {
		t.Fatal("evidenceJSON = nil")
	}
	if !strings.Contains(*evidenceJSON, `"v":2`) {
		t.Fatalf("evidenceJSON = %q, must include schema version stamp", *evidenceJSON)
	}
}

// Empty envelope (no gateway, no handshake, no upstream event, no transport
// signal) suppresses evidence entirely — emitting `{"v":2}` alone would
// distort evidence-presence dashboards.
func TestBuildWebSocketEvidence_EmptyEnvelopeReturnsNil(t *testing.T) {
	t.Parallel()

	if got := buildWebSocketEvidence(webSocketGatewayEvidenceInput{}, nil, nil, false, ""); got != nil {
		t.Fatalf("evidenceJSON = %q, want nil for empty envelope", *got)
	}
}

// The trim pass must preserve structural classification fields and drop raw
// snippets in the documented order: RawPayloadSnippet, CloseReasonSnippet,
// RawErrorSnippet, then long-form human text.
func TestMarshalWebSocketEvidence_TrimOrderHonorsCloseReasonBeforeRawError(t *testing.T) {
	t.Parallel()

	large := strings.Repeat("x", 1200)
	evidence := webSocketEvidence{
		SchemaVersion: webSocketEvidenceSchemaVersion,
		Gateway: &webSocketGatewayEvidence{
			TerminalStatusCode:     http.StatusBadGateway,
			TerminalErrorCode:      "code",
			TerminalMessageSnippet: large,
		},
		UpstreamHandshake: &webSocketUpstreamHandshakeEvidence{
			StatusCode:  http.StatusBadGateway,
			BodySnippet: large,
		},
		Transport: &transportDiagnostic{
			Source:             transportSourceUpstream,
			Stage:              transportStagePostPayloadVisible,
			Kind:               transportKindDisconnect,
			Signal:             transportSignalCloseError,
			CloseReasonSnippet: large,
			RawErrorSnippet:    large,
		},
		UpstreamEvent: &webSocketUpstreamEventEvidence{
			EnvelopeType:      "error",
			ProviderErrorType: "rate_limit",
			ProviderErrorCode: "code",
			StatusCode:        http.StatusTooManyRequests,
			MessageSnippet:    large,
			RawPayloadSnippet: large,
		},
	}
	out := marshalWebSocketEvidence(evidence)
	if out == nil {
		t.Fatal("marshalWebSocketEvidence = nil")
	}
	if len(*out) > webSocketEvidenceJSONLimitBytes {
		t.Fatalf("length = %d, want <= %d", len(*out), webSocketEvidenceJSONLimitBytes)
	}
	var parsed webSocketEvidence
	if err := json.Unmarshal([]byte(*out), &parsed); err != nil {
		t.Fatalf("json.Unmarshal = %v", err)
	}
	// Signal / Kind / Stage / Source / CloseCode presence are the classification
	// fields the frontend summary needs — they must survive trimming.
	if parsed.Transport == nil || parsed.Transport.Signal != transportSignalCloseError {
		t.Fatalf("Transport.Signal = %+v, want preserved signal", parsed.Transport)
	}
	if parsed.Transport.Kind != transportKindDisconnect {
		t.Fatalf("Transport.Kind = %q, want preserved", parsed.Transport.Kind)
	}
	if parsed.Transport.Stage != transportStagePostPayloadVisible {
		t.Fatalf("Transport.Stage = %q, want preserved", parsed.Transport.Stage)
	}
	if parsed.Transport.CloseReasonSnippet != "" {
		t.Fatalf("Transport.CloseReasonSnippet = %q, want dropped by trim", parsed.Transport.CloseReasonSnippet)
	}
	if parsed.Transport.RawErrorSnippet != "" {
		t.Fatalf("Transport.RawErrorSnippet = %q, want dropped by trim", parsed.Transport.RawErrorSnippet)
	}
	if parsed.UpstreamEvent == nil || parsed.UpstreamEvent.ProviderErrorType == "" {
		t.Fatalf("UpstreamEvent classification = %+v, want preserved", parsed.UpstreamEvent)
	}
}
