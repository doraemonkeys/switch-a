package proxy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"

	"github.com/coder/websocket"
	"go.uber.org/zap/zaptest"
)

// --- Integration tests ---

func TestWebSocketForwarder_Forward_EchoRoundtrip(t *testing.T) {
	t.Parallel()

	upstream := newEchoWSServer(t)
	defer upstream.Close()

	// Create a proxy server that forwards WebSocket to the echo server.
	fwd := NewWebSocketForwarder(WebSocketForwarderConfig{
		Logger: zaptest.NewLogger(t),
	})

	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		result, err := fwd.Forward(r.Context(), w, r, wsURL(upstream), nil)
		if err != nil {
			t.Errorf("Forward error: %v", err)
			return
		}
		if !result.HandshakeAccepted {
			t.Errorf("expected HandshakeAccepted=true, got false")
		}
	}))
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Connect as a client to the proxy.
	clientConn := connectWSClient(t, ctx, wsURL(proxyServer))
	defer clientConn.Close(websocket.StatusNormalClosure, "")

	// Send a message and verify the echo.
	msg := "hello websocket"
	if err := clientConn.Write(ctx, websocket.MessageText, []byte(msg)); err != nil {
		t.Fatalf("write: %v", err)
	}

	msgType, data, err := clientConn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if msgType != websocket.MessageText {
		t.Errorf("expected MessageText, got %v", msgType)
	}
	if string(data) != msg {
		t.Errorf("expected %q, got %q", msg, string(data))
	}
}

func TestWebSocketForwarder_Forward_BinaryMessages(t *testing.T) {
	t.Parallel()

	upstream := newEchoWSServer(t)
	defer upstream.Close()

	fwd := NewWebSocketForwarder(WebSocketForwarderConfig{
		Logger: zaptest.NewLogger(t),
	})

	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fwd.Forward(r.Context(), w, r, wsURL(upstream), nil)
	}))
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	clientConn := connectWSClient(t, ctx, wsURL(proxyServer))
	defer clientConn.Close(websocket.StatusNormalClosure, "")

	// Send binary data.
	payload := []byte{0x00, 0xFF, 0xDE, 0xAD, 0xBE, 0xEF}
	if err := clientConn.Write(ctx, websocket.MessageBinary, payload); err != nil {
		t.Fatalf("write binary: %v", err)
	}

	msgType, data, err := clientConn.Read(ctx)
	if err != nil {
		t.Fatalf("read binary: %v", err)
	}
	if msgType != websocket.MessageBinary {
		t.Errorf("expected MessageBinary, got %v", msgType)
	}
	if !bytes.Equal(data, payload) {
		t.Errorf("binary payload mismatch: got %x, want %x", data, payload)
	}
}

func TestWebSocketForwarder_Forward_TracksClientLifecycleBoundaries(t *testing.T) {
	t.Parallel()

	upstream := newEchoWSServer(t)
	defer upstream.Close()

	fwd := NewWebSocketForwarder(WebSocketForwarderConfig{
		Logger: zaptest.NewLogger(t),
	})

	resultCh := make(chan *WebSocketResult, 1)
	errCh := make(chan error, 1)
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		result, err := fwd.Forward(r.Context(), w, r, wsURL(upstream), nil)
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- result
	}))
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	clientConn := connectWSClient(t, ctx, wsURL(proxyServer))
	if err := clientConn.Write(ctx, websocket.MessageText, []byte("hello")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, _, err := clientConn.Read(ctx); err != nil {
		t.Fatalf("read: %v", err)
	}
	_ = clientConn.Close(websocket.StatusNormalClosure, "")

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

func TestWebSocketForwarder_Relay_OnClientVisibleRunsOnce(t *testing.T) {
	t.Parallel()

	upstreamMessages := []webSocketReplayMessage{
		{MessageType: websocket.MessageText, Data: []byte("first")},
		{MessageType: websocket.MessageText, Data: []byte("second")},
	}
	upstream := newPushMessagesWSServer(t, upstreamMessages)
	defer upstream.Close()

	fwd := NewWebSocketForwarder(WebSocketForwarderConfig{
		Logger: zaptest.NewLogger(t),
	})

	visibleCh := make(chan webSocketVisibleWriteContext, len(upstreamMessages))
	resultCh := make(chan *webSocketRelaySessionResult, 1)
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamConn, dialResult := fwd.dialUpstream(r.Context(), wsURL(upstream), nil)
		if dialResult != nil {
			t.Errorf("unexpected dial result: %+v", dialResult)
			return
		}

		clientConn, err := fwd.acceptClient(w, r)
		if err != nil {
			t.Errorf("unexpected client accept error: %v", err)
			_ = upstreamConn.Close(websocket.StatusGoingAway, "client accept failed")
			return
		}

		lifecycle := newWebSocketLifecycleState()
		lifecycle.MarkClientAccepted()
		resultCh <- fwd.relay(r.Context(), clientConn, upstreamConn, webSocketRelayOptions{
			Lifecycle: lifecycle,
			OnClientVisible: func(ctx webSocketVisibleWriteContext) {
				visibleCh <- webSocketVisibleWriteContext{
					MessageType: ctx.MessageType,
					Data:        append([]byte(nil), ctx.Data...),
					Observation: ctx.Observation,
				}
			},
		})
	}))
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	clientConn := connectWSClient(t, ctx, wsURL(proxyServer))
	defer clientConn.Close(websocket.StatusNormalClosure, "")

	for i, want := range upstreamMessages {
		messageType, data, err := clientConn.Read(ctx)
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		if messageType != want.MessageType {
			t.Fatalf("message type %d = %v, want %v", i, messageType, want.MessageType)
		}
		if !bytes.Equal(data, want.Data) {
			t.Fatalf("payload %d = %q, want %q", i, data, want.Data)
		}
	}
	_ = clientConn.Close(websocket.StatusNormalClosure, "done")

	select {
	case relayResult := <-resultCh:
		if relayResult == nil {
			t.Fatal("expected non-nil relay result")
		}
		if !relayResult.ClientVisible {
			t.Fatal("expected ClientVisible=true")
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for relay result")
	}

	visibleEvents := make([]webSocketVisibleWriteContext, 0, len(upstreamMessages))
drainVisible:
	for {
		select {
		case event := <-visibleCh:
			visibleEvents = append(visibleEvents, event)
		default:
			break drainVisible
		}
	}
	if len(visibleEvents) != 1 {
		t.Fatalf("visible hook count = %d, want 1", len(visibleEvents))
	}
	if visibleEvents[0].MessageType != upstreamMessages[0].MessageType {
		t.Fatalf("visible hook message type = %v, want %v", visibleEvents[0].MessageType, upstreamMessages[0].MessageType)
	}
	if !bytes.Equal(visibleEvents[0].Data, upstreamMessages[0].Data) {
		t.Fatalf("visible hook payload = %q, want %q", visibleEvents[0].Data, upstreamMessages[0].Data)
	}
}

func TestWebSocketForwarder_Relay_SuppressesAllowlistedProviderScopedErrorBeforeClientVisible(t *testing.T) {
	t.Parallel()

	semanticPayload := []byte(`{"type":"error","status":403,"error":{"type":"auth_error","code":"model_not_allowed","message":"model access denied"}}`)
	upstream := newSemanticErrorWSServer(t, semanticPayload)
	defer upstream.Close()

	fwd := NewWebSocketForwarder(WebSocketForwarderConfig{
		Logger: zaptest.NewLogger(t),
	})

	type relayAttempt struct {
		result *webSocketRelaySessionResult
		buffer *preVisibleClientMessageBuffer
	}

	attemptCh := make(chan relayAttempt, 1)
	releaseClient := make(chan struct{})
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamConn, dialResult := fwd.dialUpstream(r.Context(), wsURL(upstream), nil)
		if dialResult != nil {
			t.Errorf("unexpected dial result: %+v", dialResult)
			return
		}

		clientConn, err := fwd.acceptClient(w, r)
		if err != nil {
			t.Errorf("unexpected client accept error: %v", err)
			_ = upstreamConn.Close(websocket.StatusGoingAway, "client accept failed")
			return
		}

		lifecycle := newWebSocketLifecycleState()
		lifecycle.MarkClientAccepted()
		buffer := newPreVisibleClientMessageBuffer(preVisibleClientReplayBufferLimitBytes)
		observer := newCodexWebSocketMessageObserver(ModelUnknown, nil, nil, nil)

		relayResult := fwd.relay(r.Context(), clientConn, upstreamConn, webSocketRelayOptions{
			Observer:                 observer,
			PreWriteToClient:         newAllowlistedProviderScopedSuppressDecision(buffer),
			PreVisibleReplayBuffer:   buffer,
			Lifecycle:                lifecycle,
			PreserveClientOnSuppress: true,
		})
		attemptCh <- relayAttempt{
			result: relayResult,
			buffer: buffer,
		}
		<-releaseClient
		_ = clientConn.Close(websocket.StatusGoingAway, "test cleanup")
	}))
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	clientConn := connectWSClient(t, ctx, wsURL(proxyServer))
	defer clientConn.Close(websocket.StatusNormalClosure, "")

	const prompt = "buffer this request"
	if err := clientConn.Write(ctx, websocket.MessageText, []byte(prompt)); err != nil {
		t.Fatalf("write client message: %v", err)
	}

	var attempt relayAttempt
	select {
	case attempt = <-attemptCh:
	case <-ctx.Done():
		t.Fatal("timed out waiting for suppressed relay result")
	}

	if attempt.result.Disposition != webSocketRelayDispositionSuppressedUpstreamError {
		t.Fatalf("Disposition = %v, want suppressed upstream error", attempt.result.Disposition)
	}
	if !attempt.result.ClientAccepted {
		t.Fatal("expected ClientAccepted=true")
	}
	if attempt.result.ClientVisible {
		t.Fatal("client must remain invisible when the upstream semantic error is suppressed")
	}
	if attempt.result.BytesUpstreamToClient != 0 {
		t.Fatalf("BytesUpstreamToClient = %d, want 0 for suppressed pre-visible payload", attempt.result.BytesUpstreamToClient)
	}
	if attempt.result.SuppressedUpstreamError == nil {
		t.Fatal("expected suppressed upstream error to be preserved")
	}
	if attempt.result.SuppressedUpstreamError.Raw != string(semanticPayload) {
		t.Fatalf("suppressed Raw = %q, want original payload", attempt.result.SuppressedUpstreamError.Raw)
	}

	readCtx, readCancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer readCancel()
	if _, _, err := clientConn.Read(readCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected suppressed payload to stay invisible, got %v", err)
	}

	replayReceived := make(chan webSocketReplayMessage, 1)
	replayServer := newRecordingWSServer(t, replayReceived)
	defer replayServer.Close()

	replayConn := connectWSClient(t, ctx, wsURL(replayServer))
	if err := attempt.buffer.Replay(ctx, replayConn); err != nil {
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

	close(releaseClient)
}

func TestWebSocketForwarder_Relay_SuppressesAllowlistedProviderScopedErrorWithoutBufferedClientMessage(t *testing.T) {
	t.Parallel()

	semanticPayload := []byte(`{"type":"error","status":403,"error":{"type":"auth_error","code":"model_not_allowed","message":"model access denied"}}`)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		conn.SetReadLimit(wsReadLimit)
		_ = conn.Write(r.Context(), websocket.MessageText, semanticPayload)
		<-r.Context().Done()
	}))
	defer upstream.Close()

	fwd := NewWebSocketForwarder(WebSocketForwarderConfig{
		Logger: zaptest.NewLogger(t),
	})

	type relayAttempt struct {
		result *webSocketRelaySessionResult
		buffer *preVisibleClientMessageBuffer
	}

	attemptCh := make(chan relayAttempt, 1)
	releaseClient := make(chan struct{})
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamConn, dialResult := fwd.dialUpstream(r.Context(), wsURL(upstream), nil)
		if dialResult != nil {
			t.Errorf("unexpected dial result: %+v", dialResult)
			return
		}

		clientConn, err := fwd.acceptClient(w, r)
		if err != nil {
			t.Errorf("unexpected client accept error: %v", err)
			_ = upstreamConn.Close(websocket.StatusGoingAway, "client accept failed")
			return
		}

		lifecycle := newWebSocketLifecycleState()
		lifecycle.MarkClientAccepted()
		buffer := newPreVisibleClientMessageBuffer(preVisibleClientReplayBufferLimitBytes)
		observer := newCodexWebSocketMessageObserver(ModelUnknown, nil, nil, nil)

		relayResult := fwd.relay(r.Context(), clientConn, upstreamConn, webSocketRelayOptions{
			Observer:                 observer,
			PreWriteToClient:         newAllowlistedProviderScopedSuppressDecision(buffer),
			PreVisibleReplayBuffer:   buffer,
			Lifecycle:                lifecycle,
			PreserveClientOnSuppress: true,
		})
		attemptCh <- relayAttempt{
			result: relayResult,
			buffer: buffer,
		}
		<-releaseClient
		_ = clientConn.Close(websocket.StatusGoingAway, "test cleanup")
	}))
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	clientConn := connectWSClient(t, ctx, wsURL(proxyServer))
	defer clientConn.Close(websocket.StatusNormalClosure, "")

	var attempt relayAttempt
	select {
	case attempt = <-attemptCh:
	case <-ctx.Done():
		t.Fatal("timed out waiting for suppressed relay result")
	}

	if attempt.result.Disposition != webSocketRelayDispositionSuppressedUpstreamError {
		t.Fatalf("Disposition = %v, want suppressed upstream error", attempt.result.Disposition)
	}
	if !attempt.result.ClientAccepted {
		t.Fatal("expected ClientAccepted=true")
	}
	if attempt.result.ClientVisible {
		t.Fatal("client must remain invisible when the upstream semantic error is suppressed")
	}
	if attempt.result.BytesClientToUpstream != 0 {
		t.Fatalf("BytesClientToUpstream = %d, want 0 when no client payload was buffered", attempt.result.BytesClientToUpstream)
	}
	if attempt.result.BytesUpstreamToClient != 0 {
		t.Fatalf("BytesUpstreamToClient = %d, want 0 for suppressed pre-visible payload", attempt.result.BytesUpstreamToClient)
	}
	if attempt.result.SuppressedUpstreamError == nil {
		t.Fatal("expected suppressed upstream error to be preserved")
	}

	snapshot := attempt.buffer.Snapshot()
	if len(snapshot.Messages) != 0 {
		t.Fatalf("buffered messages = %d, want 0 when provider fails before the first client frame", len(snapshot.Messages))
	}

	readCtx, readCancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer readCancel()
	if _, _, err := clientConn.Read(readCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected suppressed payload to stay invisible, got %v", err)
	}

	close(releaseClient)
}

func TestPreVisibleClientMessageBuffer_DisablesSemanticReplacementOnOverflow(t *testing.T) {
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

func TestAllowlistedProviderScopedSuppressDecision_EmptyReplayBufferStillSuppresses(t *testing.T) {
	t.Parallel()

	semanticPayload := []byte(`{"type":"error","status":403,"error":{"type":"auth_error","code":"model_not_allowed","message":"model access denied"}}`)
	buffer := newPreVisibleClientMessageBuffer(preVisibleClientReplayBufferLimitBytes)
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
		t.Fatalf("Action = %v, want suppress when no client payload needs replay", decision.Action)
	}
	if decision.SuppressedUpstreamError == nil {
		t.Fatal("expected suppressed upstream error to be preserved for semantic replacement")
	}
}

func TestAllowlistedProviderScopedSuppressDecision_ParseDegradedFallsThrough(t *testing.T) {
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

func TestWebSocketForwarder_Forward_UpstreamDialFailure(t *testing.T) {
	t.Parallel()

	fwd := NewWebSocketForwarder(WebSocketForwarderConfig{
		Logger: zaptest.NewLogger(t),
	})

	// Use an invalid upstream URL that will fail to dial.
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		result, _ := fwd.Forward(r.Context(), w, r, "ws://127.0.0.1:1", nil)
		if result.HandshakeAccepted {
			t.Error("expected HandshakeAccepted=false for unreachable upstream")
		}
		if result.TerminalCause != model.TerminalUpstreamTransportError {
			t.Errorf("TerminalCause = %q, want %q", result.TerminalCause, model.TerminalUpstreamTransportError)
		}
		statusCode := http.StatusBadGateway
		if result.HandshakeStatusCode > 0 {
			statusCode = result.HandshakeStatusCode
		}
		w.WriteHeader(statusCode)
	}))
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Client should receive the upstream handshake failure as an HTTP response because
	// the proxy has not upgraded the socket yet.
	_, resp, err := websocket.Dial(ctx, wsURL(proxyServer), nil)
	if err == nil {
		t.Fatal("expected dial to fail before the proxy upgraded the client")
	}
	if resp == nil {
		t.Fatal("expected HTTP response on handshake failure")
	}
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadGateway)
	}
}

func TestWebSocketForwarder_Forward_HandshakeFailureCapturesUpstreamResponse(t *testing.T) {
	t.Parallel()

	const handshakeBody = `{"error":{"message":"Account quota exhausted","type":"billing_error"}}`
	dialErr := errors.New("failed to WebSocket dial: expected handshake response status code 101 but got 402")
	const usagePercent = "100"
	doneCh := make(chan *WebSocketResult, 1)

	fwd := NewWebSocketForwarder(WebSocketForwarderConfig{
		Dialer: &mockDialer{
			dialFunc: func(context.Context, string, *websocket.DialOptions) (*websocket.Conn, *http.Response, error) {
				return nil, &http.Response{
					StatusCode: http.StatusPaymentRequired,
					Header: http.Header{
						headerCodexPrimaryUsedPercent: []string{usagePercent},
					},
					Body: io.NopCloser(strings.NewReader(handshakeBody)),
				}, dialErr
			},
		},
		Logger: zaptest.NewLogger(t),
	})

	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		result, _ := fwd.Forward(r.Context(), w, r, "ws://provider.invalid/realtime", nil)
		doneCh <- result
		statusCode := http.StatusBadGateway
		if result != nil && result.HandshakeStatusCode > 0 {
			statusCode = result.HandshakeStatusCode
		}
		w.WriteHeader(statusCode)
	}))
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, resp, err := websocket.Dial(ctx, wsURL(proxyServer), nil)
	if err == nil {
		t.Fatal("expected upstream handshake rejection to fail the proxy dial")
	}
	if resp == nil {
		t.Fatal("expected HTTP response on upstream handshake rejection")
	}
	if resp.StatusCode != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusPaymentRequired)
	}

	select {
	case result := <-doneCh:
		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if result.HandshakeAccepted {
			t.Fatal("expected HandshakeAccepted=false when upstream rejects handshake")
		}
		if result.HandshakeStatusCode != http.StatusPaymentRequired {
			t.Fatalf("HandshakeStatusCode = %d, want %d", result.HandshakeStatusCode, http.StatusPaymentRequired)
		}
		if result.TerminalCause != model.TerminalUpstreamHandshakeRejected {
			t.Fatalf("TerminalCause = %q, want %q", result.TerminalCause, model.TerminalUpstreamHandshakeRejected)
		}
		if result.HandshakeBodySnippet != handshakeBody {
			t.Fatalf("HandshakeBodySnippet = %q, want %q", result.HandshakeBodySnippet, handshakeBody)
		}
		if result.HandshakeHeaders.Get(headerCodexPrimaryUsedPercent) != usagePercent {
			t.Fatalf("HandshakeHeaders[%q] = %q, want %q", headerCodexPrimaryUsedPercent, result.HandshakeHeaders.Get(headerCodexPrimaryUsedPercent), usagePercent)
		}
		if result.HandshakeObservedAt.IsZero() {
			t.Fatal("HandshakeObservedAt should be recorded for failed handshakes")
		}
		if !errors.Is(result.Err, dialErr) {
			t.Fatalf("Err = %v, want dialErr", result.Err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Forward did not return a result")
	}
}

func TestWebSocketForwarder_Forward_UpstreamCloses(t *testing.T) {
	t.Parallel()

	// Upstream echoes 1 message then closes.
	upstream := newCloseAfterNWSServer(t, 1, websocket.StatusNormalClosure, "done")
	defer upstream.Close()

	fwd := NewWebSocketForwarder(WebSocketForwarderConfig{
		Logger: zaptest.NewLogger(t),
	})

	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fwd.Forward(r.Context(), w, r, wsURL(upstream), nil)
	}))
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	clientConn := connectWSClient(t, ctx, wsURL(proxyServer))
	defer clientConn.Close(websocket.StatusNormalClosure, "")

	// Send one message — will be echoed.
	if err := clientConn.Write(ctx, websocket.MessageText, []byte("ping")); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, data, err := clientConn.Read(ctx)
	if err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if string(data) != "ping" {
		t.Errorf("expected 'ping', got %q", string(data))
	}

	// Next read should fail — upstream closed.
	_, _, err = clientConn.Read(ctx)
	if err == nil {
		t.Error("expected error after upstream close")
	}
}

func TestWebSocketForwarder_Forward_AuthHeadersPassed(t *testing.T) {
	t.Parallel()

	var captured http.Header
	var mu sync.Mutex
	upstream := newHeaderCapturingWSServer(t, &captured, &mu)
	defer upstream.Close()

	fwd := NewWebSocketForwarder(WebSocketForwarderConfig{
		Logger: zaptest.NewLogger(t),
	})

	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers := http.Header{}
		headers.Set("Authorization", "Bearer sk-test-key")
		headers.Set("OpenAI-Beta", "realtime=v1")
		fwd.Forward(r.Context(), w, r, wsURL(upstream), headers)
	}))
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(proxyServer), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	conn.Close(websocket.StatusNormalClosure, "")

	// Give the proxy time to complete the upstream handshake.
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	if captured.Get("Authorization") != "Bearer sk-test-key" {
		t.Errorf("expected Authorization header, got %q", captured.Get("Authorization"))
	}
	if captured.Get("OpenAI-Beta") != "realtime=v1" {
		t.Errorf("expected OpenAI-Beta header, got %q", captured.Get("OpenAI-Beta"))
	}
	if got := captured.Get(headerUserAgent); got != "" {
		t.Errorf("expected empty User-Agent, got %q", got)
	}
}

func TestWebSocketForwarder_Forward_PreservesExplicitUserAgent(t *testing.T) {
	t.Parallel()

	var captured http.Header
	var mu sync.Mutex
	upstream := newHeaderCapturingWSServer(t, &captured, &mu)
	defer upstream.Close()

	fwd := NewWebSocketForwarder(WebSocketForwarderConfig{
		Logger: zaptest.NewLogger(t),
	})

	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers := http.Header{}
		headers.Set(headerUserAgent, "switch-a-proxy/1.0")
		fwd.Forward(r.Context(), w, r, wsURL(upstream), headers)
	}))
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(proxyServer), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	conn.Close(websocket.StatusNormalClosure, "")

	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	if got := captured.Get(headerUserAgent); got != "switch-a-proxy/1.0" {
		t.Fatalf("User-Agent = %q, want %q", got, "switch-a-proxy/1.0")
	}
}

func TestWebSocketForwarder_Forward_ContextCancel(t *testing.T) {
	t.Parallel()

	upstream := newEchoWSServer(t)
	defer upstream.Close()

	fwd := NewWebSocketForwarder(WebSocketForwarderConfig{
		Logger: zaptest.NewLogger(t),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Create a proxy with a cancellable request context.
	reqCtx, reqCancel := context.WithCancel(ctx)

	doneCh := make(chan *WebSocketResult, 1)
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Override request context with our cancellable one.
		r = r.WithContext(reqCtx)
		result, _ := fwd.Forward(r.Context(), w, r, wsURL(upstream), nil)
		doneCh <- result
	}))
	defer proxyServer.Close()

	clientConn := connectWSClient(t, ctx, wsURL(proxyServer))
	defer clientConn.Close(websocket.StatusNormalClosure, "")

	// Verify connection works.
	if err := clientConn.Write(ctx, websocket.MessageText, []byte("hi")); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, _, err := clientConn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	// Cancel the request context — both relay goroutines should exit.
	reqCancel()

	select {
	case result := <-doneCh:
		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if !result.HandshakeAccepted {
			t.Error("expected HandshakeAccepted=true (connection was established before cancel)")
		}
		if !errors.Is(result.Err, context.Canceled) {
			t.Errorf("expected context cancellation to remain observable, got: %v", result.Err)
		}
		if result.TerminalCause != model.TerminalInternalError {
			t.Errorf("TerminalCause = %q, want %q", result.TerminalCause, model.TerminalInternalError)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Forward did not return after context cancellation")
	}
}

func TestReduceWebSocketRelayErrors_PeerEOFClearsInternalCancellation(t *testing.T) {
	t.Parallel()

	outcome := reduceWebSocketRelayErrors(
		webSocketRelayResult{err: io.EOF, errorOrder: 1},
		webSocketRelayResult{err: context.Canceled, errorOrder: 2},
	)
	if outcome.err != nil {
		t.Fatalf("expected err=nil for peer EOF plus relay cancellation, got %v", outcome.err)
	}
	if outcome.closeCode != websocket.StatusNoStatusRcvd {
		t.Fatalf("CloseCode = %d, want %d", outcome.closeCode, websocket.StatusNoStatusRcvd)
	}
}

func TestReduceWebSocketRelayErrors_PreservesCallerCancellationWithoutPeerDisconnect(t *testing.T) {
	t.Parallel()

	outcome := reduceWebSocketRelayErrors(
		webSocketRelayResult{err: context.Canceled, errorOrder: 1},
		webSocketRelayResult{err: context.Canceled, errorOrder: 2},
	)
	if !errors.Is(outcome.err, context.Canceled) {
		t.Fatalf("expected context cancellation to remain a failure, got %v", outcome.err)
	}
	if outcome.closeCode != websocket.StatusNormalClosure {
		t.Fatalf("CloseCode = %d, want %d", outcome.closeCode, websocket.StatusNormalClosure)
	}
}

func TestReduceWebSocketRelayErrors_PrefersActualFirstFailureOverSiblingCancellation(t *testing.T) {
	t.Parallel()

	upstreamClose := websocket.CloseError{
		Code:   websocket.StatusPolicyViolation,
		Reason: "blocked",
	}
	outcome := reduceWebSocketRelayErrors(
		webSocketRelayResult{err: context.Canceled, errorOrder: 2},
		webSocketRelayResult{err: upstreamClose, errorOrder: 1},
	)
	if websocket.CloseStatus(outcome.err) != websocket.StatusPolicyViolation {
		t.Fatalf("CloseStatus(outcome.err) = %d, want %d", websocket.CloseStatus(outcome.err), websocket.StatusPolicyViolation)
	}
	if outcome.closeCode != websocket.StatusPolicyViolation {
		t.Fatalf("CloseCode = %d, want %d", outcome.closeCode, websocket.StatusPolicyViolation)
	}
}

func TestWebSocketForwarder_Forward_ClientCloseNowNotAnError(t *testing.T) {
	t.Parallel()

	upstream := newEchoWSServer(t)
	defer upstream.Close()

	fwd := NewWebSocketForwarder(WebSocketForwarderConfig{
		Logger: zaptest.NewLogger(t),
	})

	doneCh := make(chan *WebSocketResult, 1)
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		result, _ := fwd.Forward(r.Context(), w, r, wsURL(upstream), nil)
		doneCh <- result
	}))
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	clientConn := connectWSClient(t, ctx, wsURL(proxyServer))
	if err := clientConn.Write(ctx, websocket.MessageText, []byte("ping")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, _, err := clientConn.Read(ctx); err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if err := clientConn.CloseNow(); err != nil {
		t.Fatalf("CloseNow: %v", err)
	}

	select {
	case result := <-doneCh:
		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if !result.HandshakeAccepted {
			t.Fatal("expected HandshakeAccepted=true")
		}
		if result.Err != nil {
			t.Fatalf("expected Err=nil for CloseNow teardown after successful traffic, got %v", result.Err)
		}
		if result.CloseCode != websocket.StatusNoStatusRcvd {
			t.Fatalf("CloseCode = %d, want %d", result.CloseCode, websocket.StatusNoStatusRcvd)
		}
		if result.TerminalCause != model.TerminalClientDisconnect {
			t.Fatalf("TerminalCause = %q, want %q", result.TerminalCause, model.TerminalClientDisconnect)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Forward did not return after CloseNow")
	}
}

func TestWebSocketForwarder_Forward_ByteCountsAccurate(t *testing.T) {
	t.Parallel()

	upstream := newEchoWSServer(t)
	defer upstream.Close()

	fwd := NewWebSocketForwarder(WebSocketForwarderConfig{
		Logger: zaptest.NewLogger(t),
	})

	doneCh := make(chan *WebSocketResult, 1)
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		result, _ := fwd.Forward(r.Context(), w, r, wsURL(upstream), nil)
		doneCh <- result
	}))
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	clientConn := connectWSClient(t, ctx, wsURL(proxyServer))

	// Send two messages.
	msg1 := "hello"
	msg2 := "world!"
	clientConn.Write(ctx, websocket.MessageText, []byte(msg1))
	clientConn.Read(ctx) // echo 1
	clientConn.Write(ctx, websocket.MessageText, []byte(msg2))
	clientConn.Read(ctx) // echo 2

	// Close client — triggers relay shutdown.
	clientConn.Close(websocket.StatusNormalClosure, "")

	select {
	case result := <-doneCh:
		expectedBytes := int64(len(msg1) + len(msg2))
		if result.BytesClientToUpstream != expectedBytes {
			t.Errorf("BytesClientToUpstream = %d, want %d", result.BytesClientToUpstream, expectedBytes)
		}
		if result.BytesUpstreamToClient != expectedBytes {
			t.Errorf("BytesUpstreamToClient = %d, want %d", result.BytesUpstreamToClient, expectedBytes)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Forward did not return")
	}
}

func TestWebSocketForwarder_Forward_NormalCloseNoError(t *testing.T) {
	t.Parallel()

	// Upstream echoes 1 message then closes normally.
	upstream := newCloseAfterNWSServer(t, 1, websocket.StatusNormalClosure, "done")
	defer upstream.Close()

	fwd := NewWebSocketForwarder(WebSocketForwarderConfig{
		Logger: zaptest.NewLogger(t),
	})

	doneCh := make(chan *WebSocketResult, 1)
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		result, _ := fwd.Forward(r.Context(), w, r, wsURL(upstream), nil)
		doneCh <- result
	}))
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	clientConn := connectWSClient(t, ctx, wsURL(proxyServer))
	clientConn.Write(ctx, websocket.MessageText, []byte("ping"))
	clientConn.Read(ctx)

	// Trigger close by reading again (upstream already closed).
	clientConn.Read(ctx) //nolint:errcheck

	select {
	case result := <-doneCh:
		if result == nil {
			t.Fatal("expected non-nil result")
		}
		// Normal close should produce Err=nil (contract: clean close is not an error).
		if result.Err != nil {
			t.Errorf("expected Err=nil for normal close, got: %v", result.Err)
		}
		if result.CloseCode != websocket.StatusNormalClosure {
			t.Errorf("CloseCode = %d, want %d", result.CloseCode, websocket.StatusNormalClosure)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Forward did not return")
	}
}

func TestWebSocketForwarder_Forward_NonCleanClosePreservesFirstError(t *testing.T) {
	t.Parallel()

	upstream := newCloseAfterNWSServer(t, 1, websocket.StatusPolicyViolation, "blocked")
	defer upstream.Close()

	fwd := NewWebSocketForwarder(WebSocketForwarderConfig{
		Logger: zaptest.NewLogger(t),
	})

	doneCh := make(chan *WebSocketResult, 1)
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		result, _ := fwd.Forward(r.Context(), w, r, wsURL(upstream), nil)
		doneCh <- result
	}))
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	clientConn := connectWSClient(t, ctx, wsURL(proxyServer))
	if err := clientConn.Write(ctx, websocket.MessageText, []byte("ping")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, _, err := clientConn.Read(ctx); err != nil {
		t.Fatalf("read echo: %v", err)
	}

	clientConn.Read(ctx) //nolint:errcheck

	select {
	case result := <-doneCh:
		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if websocket.CloseStatus(result.Err) != websocket.StatusPolicyViolation {
			t.Fatalf("CloseStatus(result.Err) = %d, want %d (err=%v)", websocket.CloseStatus(result.Err), websocket.StatusPolicyViolation, result.Err)
		}
		if result.CloseCode != websocket.StatusPolicyViolation {
			t.Fatalf("CloseCode = %d, want %d", result.CloseCode, websocket.StatusPolicyViolation)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Forward did not return")
	}
}
