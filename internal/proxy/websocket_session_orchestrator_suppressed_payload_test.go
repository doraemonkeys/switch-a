package proxy

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"

	"github.com/coder/websocket"
)

func TestWebSocketSessionOrchestrator_FinalSessionUsesSuppressedPayload(t *testing.T) {
	visible := make(chan webSocketVisibleWriteContext, 1)
	sessionCh := make(chan *WebSocketSessionResult, 1)
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		orchestrator := &WebSocketSessionOrchestrator{
			handler: &Handler{
				wsForwarder: NewWebSocketForwarder(WebSocketForwarderConfig{}),
			},
			apiType:         APITypeCodex,
			requestID:       "req-suppressed",
			isSticky:        true,
			lifecycle:       newWebSocketLifecycleState(),
			replayBuffer:    newPreVisibleClientMessageBuffer(preVisibleClientReplayBufferLimitBytes),
			onClientVisible: func(ctx webSocketVisibleWriteContext) { visible <- ctx },
			suppressedAttempt: &webSocketSuppressedAttempt{
				provider:    &model.Provider{ID: "provider-origin"},
				messageType: websocket.MessageText,
				payload:     []byte(`{"type":"error"}`),
				upstreamError: &WebSocketUpstreamError{
					EventType:  "auth_error",
					Code:       "invalid_api_key",
					StatusCode: http.StatusUnauthorized,
					Raw:        `{"type":"error"}`,
				},
			},
		}
		orchestrator.replayBuffer.Record(websocket.MessageText, []byte("buffered request"), false)
		if err := orchestrator.ensureClientAccepted(w, r); err != nil {
			t.Errorf("ensureClientAccepted() error = %v", err)
			return
		}
		sessionCh <- orchestrator.finalSessionFromLastAttempt(r.Context())
	}))
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	clientConn := connectWSClient(t, ctx, wsURL(proxyServer))
	defer clientConn.Close(websocket.StatusNormalClosure, "")

	msgType, payload, err := clientConn.Read(ctx)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	wantGatewayPayload := string(marshalWebSocketGatewayError(
		http.StatusServiceUnavailable,
		ErrCodeProviderUnavailable,
		"No available provider for api_type: codex",
	))
	if msgType != websocket.MessageText || string(payload) != wantGatewayPayload {
		t.Fatalf("Read() = (%v, %q), want text/%q", msgType, string(payload), wantGatewayPayload)
	}

	select {
	case session := <-sessionCh:
		if session.FinalProvider == nil || session.FinalProvider.ID != "provider-origin" {
			t.Fatalf("FinalProvider = %#v, want provider-origin", session.FinalProvider)
		}
		if session.FinalResult == nil || session.FinalResult.ClientVisible {
			t.Fatalf("FinalResult = %#v, want canonical invisible gateway result", session.FinalResult)
		}
		if session.FinalResult.TerminalCause != model.TerminalProviderUnavailable {
			t.Fatalf("TerminalCause = %q, want %q", session.FinalResult.TerminalCause, model.TerminalProviderUnavailable)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for suppressed session result")
	}

	select {
	case event := <-visible:
		t.Fatalf("visible event = %#v, want no client-visible callback for canonical gateway termination", event)
	default:
	}

	if _, _, err := clientConn.Read(ctx); err == nil || (!errors.Is(err, io.EOF) && !isNormalClose(err)) {
		t.Fatalf("expected websocket close after suppressed payload, got %v", err)
	}
}

func TestWebSocketSessionOrchestrator_FallsBackToSuppressedPayloadAfterRelaySuppression(t *testing.T) {
	semanticPayload := []byte(`{"type":"error","status":403,"error":{"type":"auth_error","code":"model_not_allowed","message":"model access denied"}}`)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept upstream websocket: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		if _, _, err := conn.Read(r.Context()); err != nil {
			return
		}
		if err := conn.Write(r.Context(), websocket.MessageText, semanticPayload); err != nil {
			t.Errorf("write semantic payload: %v", err)
		}
	}))
	defer upstream.Close()

	resultCh := make(chan *WebSocketSessionResult, 1)
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fwd := NewWebSocketForwarder(WebSocketForwarderConfig{})
		dialExchange := fwd.dialUpstream(r.Context(), WebSocketDialRequest{URL: wsURL(upstream)})
		if !dialExchange.Accepted() {
			t.Errorf("unexpected dial exchange: %+v", dialExchange)
			return
		}
		upstreamConn := dialExchange.Conn

		clientConn, err := fwd.acceptClient(w, r)
		if err != nil {
			t.Errorf("unexpected client accept error: %v", err)
			_ = upstreamConn.Close(websocket.StatusGoingAway, "client accept failed")
			return
		}

		lifecycle := newWebSocketLifecycleState()
		lifecycle.MarkClientAccepted()
		buffer := newPreVisibleClientMessageBuffer(preVisibleClientReplayBufferLimitBytes)
		relayResult := fwd.relay(r.Context(), clientConn, upstreamConn, webSocketRelayOptions{
			Observer:                 newCodexWebSocketMessageObserver(ModelUnknown, nil, nil, nil),
			PreWriteToClient:         newAllowlistedProviderScopedSuppressDecision(buffer),
			PreVisibleReplayBuffer:   buffer,
			Lifecycle:                lifecycle,
			PreserveClientOnSuppress: true,
		})
		if relayResult.Disposition != webSocketRelayDispositionSuppressedUpstreamError {
			t.Errorf("Disposition = %v, want suppressed upstream error", relayResult.Disposition)
		}

		orchestrator := &WebSocketSessionOrchestrator{
			apiType:      APITypeCodex,
			requestID:    "req-suppressed-after-relay",
			lifecycle:    lifecycle,
			clientConn:   clientConn,
			replayBuffer: buffer,
		}
		orchestrator.captureSuppressedAttempt(&model.Provider{ID: "provider-origin"}, relayResult)
		resultCh <- orchestrator.sessionFromSuppressedPayload(r.Context())
	}))
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	clientConn := connectWSClient(t, ctx, wsURL(proxyServer))
	defer clientConn.Close(websocket.StatusNormalClosure, "")
	if err := clientConn.Write(ctx, websocket.MessageText, []byte(`{"type":"response.create"}`)); err != nil {
		t.Fatalf("write client prompt: %v", err)
	}

	msgType, payload, err := clientConn.Read(ctx)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	wantGatewayPayload := string(marshalWebSocketGatewayError(
		http.StatusServiceUnavailable,
		ErrCodeProviderUnavailable,
		"No available provider for api_type: codex",
	))
	if msgType != websocket.MessageText || string(payload) != wantGatewayPayload {
		t.Fatalf("Read() = (%v, %q), want text/%q", msgType, string(payload), wantGatewayPayload)
	}

	select {
	case session := <-resultCh:
		if session.FinalProvider == nil || session.FinalProvider.ID != "provider-origin" {
			t.Fatalf("FinalProvider = %#v, want provider-origin", session.FinalProvider)
		}
		if session.FinalResult == nil || session.FinalResult.ClientVisible {
			t.Fatalf("FinalResult = %#v, want canonical invisible gateway result", session.FinalResult)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for session result")
	}

	if _, _, err := clientConn.Read(ctx); err == nil || (!errors.Is(err, io.EOF) && !isNormalClose(err)) {
		t.Fatalf("expected websocket close after suppressed payload fallback, got %v", err)
	}
}

func internalErrNoProvider() error {
	return errors.New("no provider")
}
