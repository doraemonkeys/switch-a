package proxy

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"switch-a/internal/model"

	"github.com/coder/websocket"
	"go.uber.org/zap/zaptest"
)

func newRuntimeEchoWSServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		conn.SetReadLimit(wsReadLimit)
		for {
			messageType, data, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			if err := conn.Write(r.Context(), messageType, data); err != nil {
				return
			}
		}
	}))
}

func newRuntimeRecordingWSServer(t *testing.T, received chan<- webSocketReplayMessage) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		conn.SetReadLimit(wsReadLimit)
		messageType, data, err := conn.Read(r.Context())
		if err != nil {
			return
		}
		received <- webSocketReplayMessage{
			MessageType: messageType,
			Data:        append([]byte(nil), data...),
		}
	}))
}

func runtimeWSURL(server *httptest.Server) string {
	return "ws" + server.URL[len("http"):]
}

func connectRuntimeWSClient(t *testing.T, ctx context.Context, url string) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	return conn
}

func TestWebSocketRuntime_TracksClientLifecycleBoundaries(t *testing.T) {
	t.Parallel()

	upstream := newRuntimeEchoWSServer(t)
	defer upstream.Close()

	fwd := NewWebSocketForwarder(WebSocketForwarderConfig{
		Logger: zaptest.NewLogger(t),
	})

	resultCh := make(chan *WebSocketResult, 1)
	errCh := make(chan error, 1)
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		result, err := fwd.Forward(r.Context(), w, r, runtimeWSURL(upstream), nil)
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- result
	}))
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	clientConn := connectRuntimeWSClient(t, ctx, runtimeWSURL(proxyServer))
	if err := clientConn.Write(ctx, websocket.MessageText, []byte("hello")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, _, err := clientConn.Read(ctx); err != nil {
		t.Fatalf("read: %v", err)
	}
	if err := clientConn.Close(websocket.StatusNormalClosure, ""); err != nil {
		t.Fatalf("close client websocket: %v", err)
	}

	select {
	case err := <-errCh:
		t.Fatalf("forward returned error: %v", err)
	case result := <-resultCh:
		if !result.HandshakeAccepted {
			t.Fatal("expected HandshakeAccepted=true")
		}
		if !result.ClientAccepted {
			t.Fatal("expected ClientAccepted=true")
		}
		if !result.ClientVisible {
			t.Fatal("expected ClientVisible=true after echoed upstream payload")
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for forward result")
	}
}

func TestWebSocketRuntime_SuppressDecisionPreservesProviderScopedPayloadBeforeClientVisible(t *testing.T) {
	t.Parallel()

	semanticPayload := []byte(`{"type":"error","status":403,"error":{"type":"auth_error","code":"model_not_allowed","message":"model access denied"}}`)
	buffer := newPreVisibleClientMessageBuffer(preVisibleClientReplayBufferLimitBytes)
	buffer.Record(websocket.MessageText, []byte(`{"type":"response.create"}`), false)
	decision := newAllowlistedProviderScopedSuppressDecision(buffer)(webSocketPreWriteContext{
		MessageType: websocket.MessageText,
		Data:        semanticPayload,
		Observation: WebSocketObservation{
			UpstreamError: &WebSocketUpstreamError{
				EventType: "auth_error",
				Code:      "model_not_allowed",
				Message:   "model access denied",
				Raw:       string(semanticPayload),
			},
		},
		ClientAccepted: true,
		ClientVisible:  false,
	})
	if decision.Action != webSocketPreWriteActionSuppress {
		t.Fatalf("Action = %v, want suppress", decision.Action)
	}
	if decision.SuppressedUpstreamError == nil {
		t.Fatal("expected suppressed upstream error to be preserved")
	}
	if decision.SuppressedUpstreamError.Raw != string(semanticPayload) {
		t.Fatalf("suppressed Raw = %q, want original payload", decision.SuppressedUpstreamError.Raw)
	}
}

func TestWebSocketRuntime_SuppressDecisionUsesCanonicalRecoveryAfterClientVisible(t *testing.T) {
	t.Parallel()

	semanticPayload := []byte(`{"type":"error","status":403,"error":{"type":"auth_error","code":"model_not_allowed","message":"model access denied"}}`)
	decision := newAllowlistedProviderScopedSuppressDecision(nil)(webSocketPreWriteContext{
		MessageType: websocket.MessageText,
		Data:        semanticPayload,
		Observation: WebSocketObservation{
			UpstreamError: &WebSocketUpstreamError{
				EventType:  "auth_error",
				Code:       "model_not_allowed",
				StatusCode: http.StatusForbidden,
				Message:    "model access denied",
				Raw:        string(semanticPayload),
			},
		},
		ClientAccepted: true,
		ClientVisible:  true,
	})
	if decision.Action != webSocketPreWriteActionSuppress {
		t.Fatalf("Action = %v, want suppress after client-visible provider-scoped failure", decision.Action)
	}
	if decision.SuppressedUpstreamError == nil {
		t.Fatal("expected suppressed upstream error to be preserved")
	}
	if decision.SuppressedUpstreamError.Raw != string(semanticPayload) {
		t.Fatalf("suppressed Raw = %q, want original payload", decision.SuppressedUpstreamError.Raw)
	}
}

func TestWebSocketRuntime_ReplaysBufferedMessages(t *testing.T) {
	t.Parallel()

	buffer := newPreVisibleClientMessageBuffer(preVisibleClientReplayBufferLimitBytes)
	const prompt = "buffer this request"
	buffer.Record(websocket.MessageText, []byte(prompt), false)

	replayReceived := make(chan webSocketReplayMessage, 1)
	replayServer := newRuntimeRecordingWSServer(t, replayReceived)
	defer replayServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	replayConn := connectRuntimeWSClient(t, ctx, runtimeWSURL(replayServer))
	if err := buffer.Replay(ctx, replayConn); err != nil {
		t.Fatalf("replay buffered client messages: %v", err)
	}
	if err := replayConn.Close(websocket.StatusNormalClosure, ""); err != nil {
		t.Fatalf("close replay connection: %v", err)
	}

	select {
	case replayed := <-replayReceived:
		if replayed.MessageType != websocket.MessageText {
			t.Fatalf("replayed MessageType = %v, want text", replayed.MessageType)
		}
		if string(replayed.Data) != prompt {
			t.Fatalf("replayed payload = %q, want %q", replayed.Data, prompt)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for replay payload")
	}
}

func TestWebSocketRuntime_DisablesSemanticReplacementOnReplayBufferOverflow(t *testing.T) {
	t.Parallel()

	buffer := newPreVisibleClientMessageBuffer(4)
	buffer.Record(websocket.MessageText, []byte("hello"), false)

	if buffer.Enabled() {
		t.Fatal("buffer must disable semantic replacement after exceeding its limit")
	}

	decision := newAllowlistedProviderScopedSuppressDecision(buffer)(webSocketPreWriteContext{
		MessageType: websocket.MessageText,
		Data:        []byte(`{"type":"error"}`),
		Observation: WebSocketObservation{
			UpstreamError: &WebSocketUpstreamError{
				EventType: "auth_error",
				Code:      "model_not_allowed",
				Message:   "denied",
			},
		},
	})
	if decision.Action != webSocketPreWriteActionForward {
		t.Fatalf("Action = %v, want forward when replay buffer is disabled", decision.Action)
	}
}

func TestWebSocketRuntime_ParseDegradedDisablesSemanticReplacement(t *testing.T) {
	t.Parallel()

	buffer := newPreVisibleClientMessageBuffer(preVisibleClientReplayBufferLimitBytes)
	decision := newAllowlistedProviderScopedSuppressDecision(buffer)(webSocketPreWriteContext{
		MessageType: websocket.MessageText,
		Data:        []byte(`{"type":"error"}`),
		Observation: WebSocketObservation{
			ParseDegraded: true,
			UpstreamError: &WebSocketUpstreamError{
				EventType: "auth_error",
				Code:      "model_not_allowed",
				Message:   "denied",
			},
		},
	})
	if decision.Action != webSocketPreWriteActionForward {
		t.Fatalf("Action = %v, want forward when semantic parsing degraded", decision.Action)
	}
}

func TestWebSocketRuntime_RelaySessionResultProjectsFallbackCommitAndLifecycle(t *testing.T) {
	t.Parallel()

	fallbackCommit := newWebSocketCommitState()
	if committed := fallbackCommit.Commit(model.CommitUpstreamMessage); !committed {
		t.Fatal("Commit() = false, want true on first commit")
	}

	lifecycle := newWebSocketLifecycleState()
	lifecycle.MarkClientAccepted()
	if markedVisible := lifecycle.MarkClientVisible(); !markedVisible {
		t.Fatal("MarkClientVisible() = false, want true on first visible message")
	}

	session := newWebSocketRelaySessionResultFromOutcome(
		webSocketRelayOutcome{
			closeCode:     websocket.StatusNoStatusRcvd,
			err:           io.EOF,
			terminalCause: model.TerminalUpstreamTransportError,
		},
		fallbackCommit,
		lifecycle,
		12,
		34,
	)
	if !session.SessionCommitted {
		t.Fatal("SessionCommitted = false, want true")
	}
	if session.CommitSource != model.CommitUpstreamMessage {
		t.Fatalf("CommitSource = %q, want %q", session.CommitSource, model.CommitUpstreamMessage)
	}
	if !session.ClientAccepted || !session.ClientVisible {
		t.Fatalf("lifecycle = %+v, want client accepted and visible", session)
	}

	result := session.toWebSocketResult()
	if result.CommitSource != model.CommitUpstreamMessage {
		t.Fatalf("result.CommitSource = %q, want %q", result.CommitSource, model.CommitUpstreamMessage)
	}
	if result.BytesClientToUpstream != 12 || result.BytesUpstreamToClient != 34 {
		t.Fatalf("byte counters = (%d, %d), want (12, 34)", result.BytesClientToUpstream, result.BytesUpstreamToClient)
	}
	if result.CloseCode != websocket.StatusNoStatusRcvd {
		t.Fatalf("CloseCode = %v, want %v", result.CloseCode, websocket.StatusNoStatusRcvd)
	}
}

func TestWebSocketRuntime_NewWebSocketRelayResult_CapturesSuppressedErrorClone(t *testing.T) {
	t.Parallel()

	var errorOrder atomic.Uint32
	suppressed := &WebSocketUpstreamError{
		EventType: "auth_error",
		Code:      "model_not_allowed",
		Message:   "denied",
	}

	result := newWebSocketRelayResult(
		7,
		&webSocketSuppressedUpstreamError{upstreamError: suppressed},
		webSocketPeerUpstream,
		&errorOrder,
	)
	if result.bytes != 7 {
		t.Fatalf("bytes = %d, want 7", result.bytes)
	}
	if result.errorOrder != 1 {
		t.Fatalf("errorOrder = %d, want 1", result.errorOrder)
	}
	if result.suppressedUpstreamError == nil {
		t.Fatal("suppressedUpstreamError = nil, want clone")
	}
	if result.suppressedUpstreamError == suppressed {
		t.Fatal("suppressedUpstreamError shares original pointer, want clone")
	}

	first := firstSuppressedUpstreamError(webSocketRelayResult{}, result)
	suppressed.Message = "changed after capture"
	if first == nil || first.Message != "denied" {
		t.Fatalf("firstSuppressedUpstreamError() = %#v, want cloned original error", first)
	}
}

func TestWebSocketRuntime_ClassifyAndSanitizeTerminalOutcomes(t *testing.T) {
	t.Parallel()

	if got := classifyDialFailure(&http.Response{StatusCode: http.StatusBadGateway}); got != model.TerminalUpstreamHandshakeRejected {
		t.Fatalf("classifyDialFailure(response) = %q, want %q", got, model.TerminalUpstreamHandshakeRejected)
	}
	if got := classifyDialFailure(nil); got != model.TerminalUpstreamTransportError {
		t.Fatalf("classifyDialFailure(nil) = %q, want %q", got, model.TerminalUpstreamTransportError)
	}

	tests := []struct {
		name string
		err  error
		peer webSocketPeer
		want model.TerminalCause
	}{
		{
			name: "caller cancellation stays internal",
			err:  context.Canceled,
			peer: webSocketPeerUpstream,
			want: model.TerminalInternalError,
		},
		{
			name: "client-side failure attributes disconnect to client",
			err:  io.EOF,
			peer: webSocketPeerClient,
			want: model.TerminalClientDisconnect,
		},
		{
			name: "clean upstream close stays clean",
			err:  websocket.CloseError{Code: websocket.StatusGoingAway},
			peer: webSocketPeerUpstream,
			want: model.TerminalCleanClose,
		},
		{
			name: "unexpected upstream disconnect is transport failure",
			err:  io.ErrUnexpectedEOF,
			peer: webSocketPeerUpstream,
			want: model.TerminalUpstreamTransportError,
		},
		{
			name: "unknown peer falls back to internal error",
			err:  errors.New("boom"),
			peer: webSocketPeerUnknown,
			want: model.TerminalInternalError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := classifyRelayTerminalCause(tt.err, tt.peer); got != tt.want {
				t.Fatalf("classifyRelayTerminalCause() = %q, want %q", got, tt.want)
			}
		})
	}

	if got := sanitizeWebSocketCloseCode(websocket.StatusNoStatusRcvd, nil); got != websocket.StatusNormalClosure {
		t.Fatalf("sanitizeWebSocketCloseCode(no status, nil) = %v, want %v", got, websocket.StatusNormalClosure)
	}
	if got := sanitizeWebSocketCloseCode(websocket.StatusAbnormalClosure, errors.New("relay failed")); got != websocket.StatusInternalError {
		t.Fatalf("sanitizeWebSocketCloseCode(abnormal, err) = %v, want %v", got, websocket.StatusInternalError)
	}
	if got := sanitizeWebSocketCloseCode(websocket.StatusPolicyViolation, nil); got != websocket.StatusPolicyViolation {
		t.Fatalf("sanitizeWebSocketCloseCode(policy violation) = %v, want %v", got, websocket.StatusPolicyViolation)
	}
}

func TestWebSocketRuntime_HelperNilAndOrderingPaths(t *testing.T) {
	t.Parallel()

	var nilResult *webSocketRelaySessionResult
	if got := nilResult.toWebSocketResult(); got.CommitSource != model.CommitUnknown {
		t.Fatalf("nil toWebSocketResult() commit source = %q, want %q", got.CommitSource, model.CommitUnknown)
	}

	var nilLifecycle *webSocketLifecycleState
	nilLifecycle.MarkClientAccepted()
	if got := nilLifecycle.MarkClientVisible(); got {
		t.Fatal("nil lifecycle MarkClientVisible() = true, want false")
	}
	if snapshot := nilLifecycle.Snapshot(); snapshot != (webSocketLifecycleSnapshot{}) {
		t.Fatalf("nil lifecycle snapshot = %+v, want zero value", snapshot)
	}

	closeWebSocketForSemanticReplacement(nil)

	if got := (&webSocketSuppressedUpstreamError{}).Error(); got != webSocketSemanticReplacementCloseReason {
		t.Fatalf("suppressed error string = %q, want %q", got, webSocketSemanticReplacementCloseReason)
	}
	var nilSuppressed *webSocketSuppressedUpstreamError
	if got := nilSuppressed.UpstreamError(); got != nil {
		t.Fatalf("nil suppressed error UpstreamError() = %#v, want nil", got)
	}

	if got := isReplayableWebSocketMessageType(websocket.MessageText); !got {
		t.Fatal("text message should be replayable")
	}
	if got := isReplayableWebSocketMessageType(websocket.MessageType(99)); got {
		t.Fatal("unexpected message type should not be replayable")
	}

	primary, secondary := orderWebSocketRelayResults(
		webSocketRelayResult{errorOrder: 0},
		webSocketRelayResult{errorOrder: 2},
	)
	if primary.errorOrder != 2 || secondary.errorOrder != 0 {
		t.Fatalf("orderWebSocketRelayResults(first zero) = (%d, %d), want (2, 0)", primary.errorOrder, secondary.errorOrder)
	}

	primary, secondary = orderWebSocketRelayResults(
		webSocketRelayResult{errorOrder: 3},
		webSocketRelayResult{errorOrder: 0},
	)
	if primary.errorOrder != 3 || secondary.errorOrder != 0 {
		t.Fatalf("orderWebSocketRelayResults(second zero) = (%d, %d), want (3, 0)", primary.errorOrder, secondary.errorOrder)
	}

	primary, secondary = orderWebSocketRelayResults(
		webSocketRelayResult{errorOrder: 4},
		webSocketRelayResult{errorOrder: 1},
	)
	if primary.errorOrder != 1 || secondary.errorOrder != 4 {
		t.Fatalf("orderWebSocketRelayResults(reordered) = (%d, %d), want (1, 4)", primary.errorOrder, secondary.errorOrder)
	}

	outcome := reduceWebSocketRelayErrors(webSocketRelayResult{}, webSocketRelayResult{})
	if outcome.closeCode != websocket.StatusNormalClosure {
		t.Fatalf("reduceWebSocketRelayErrors(no errors) close code = %v, want %v", outcome.closeCode, websocket.StatusNormalClosure)
	}
	if outcome.terminalCause != model.TerminalCleanClose {
		t.Fatalf("reduceWebSocketRelayErrors(no errors) terminal cause = %q, want %q", outcome.terminalCause, model.TerminalCleanClose)
	}
}
