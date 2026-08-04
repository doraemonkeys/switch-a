package websocketproxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"

	"github.com/coder/websocket"
	"go.uber.org/zap/zaptest"
)

func TestWebSocketForwarder_ForwardObserved_CommitsFromSemanticObserver(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept upstream websocket: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		if err := conn.Write(r.Context(), websocket.MessageText, []byte(`{"type":"response.created","response":{"id":"resp_1","model":"gpt-5.4"}}`)); err != nil {
			t.Errorf("write response.created: %v", err)
		}
	}))
	defer upstream.Close()

	fwd := NewWebSocketForwarder(WebSocketForwarderConfig{
		Logger: zaptest.NewLogger(t),
	})

	doneCh := make(chan *WebSocketResult, 1)
	commitCh := make(chan WebSocketObservation, 1)
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observer := newCodexWebSocketMessageObserver(ModelUnknown, nil, nil, func(observation WebSocketObservation) {
			commitCh <- observation
		})
		result, _ := fwd.ForwardObserved(r.Context(), w, r, wsURL(upstream), nil, observer, nil, nil)
		doneCh <- result
	}))
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	clientConn := connectWSClient(t, ctx, wsURL(proxyServer))
	defer clientConn.Close(websocket.StatusNormalClosure, "")

	if _, _, err := clientConn.Read(ctx); err != nil {
		t.Fatalf("read response.created: %v", err)
	}
	clientConn.Read(ctx) //nolint:errcheck

	select {
	case observation := <-commitCh:
		if !observation.SessionCommitted {
			t.Fatal("fallback callback must observe committed state")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("expected commit callback")
	}

	select {
	case result := <-doneCh:
		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if !result.HandshakeAccepted {
			t.Fatal("expected HandshakeAccepted=true")
		}
		if !result.SessionCommitted {
			t.Fatal("expected SessionCommitted=true")
		}
		if result.CommitSource != model.CommitSemantic {
			t.Fatalf("CommitSource = %q, want %q", result.CommitSource, model.CommitSemantic)
		}
		if result.TerminalCause != model.TerminalCleanClose {
			t.Fatalf("TerminalCause = %q, want %q", result.TerminalCause, model.TerminalCleanClose)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ForwardObserved did not return")
	}
}

func TestWebSocketForwarder_ForwardObserved_FallsBackWhenObserverParseDegrades(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept upstream websocket: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		if err := conn.Write(r.Context(), websocket.MessageText, []byte(`{"type":"response.created"`)); err != nil {
			t.Errorf("write malformed upstream frame: %v", err)
		}
	}))
	defer upstream.Close()

	fwd := NewWebSocketForwarder(WebSocketForwarderConfig{
		Logger: zaptest.NewLogger(t),
	})

	doneCh := make(chan *WebSocketResult, 1)
	commitCh := make(chan WebSocketObservation, 1)
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observer := newCodexWebSocketMessageObserver(ModelUnknown, nil, nil, nil)
		result, _ := fwd.ForwardObserved(r.Context(), w, r, wsURL(upstream), nil, observer, func(observation WebSocketObservation) {
			commitCh <- observation
		}, nil)
		doneCh <- result
	}))
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	clientConn := connectWSClient(t, ctx, wsURL(proxyServer))
	defer clientConn.Close(websocket.StatusNormalClosure, "")

	if _, _, err := clientConn.Read(ctx); err != nil {
		t.Fatalf("read malformed upstream frame: %v", err)
	}
	clientConn.Read(ctx) //nolint:errcheck

	select {
	case observation := <-commitCh:
		if !observation.SessionCommitted {
			t.Fatal("fallback callback must report committed session")
		}
		if !observation.ParseDegraded {
			t.Fatal("fallback callback must preserve parse-degraded state")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("expected fallback commit callback")
	}

	select {
	case result := <-doneCh:
		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if !result.SessionCommitted {
			t.Fatal("expected SessionCommitted=true via fallback path")
		}
		if result.CommitSource != model.CommitUpstreamMessage {
			t.Fatalf("CommitSource = %q, want %q", result.CommitSource, model.CommitUpstreamMessage)
		}
		if result.TerminalCause != model.TerminalCleanClose {
			t.Fatalf("TerminalCause = %q, want %q", result.TerminalCause, model.TerminalCleanClose)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ForwardObserved did not return")
	}
}

func TestWebSocketForwarder_ForwardObserved_FallsBackWhenTrackingWrapperHasNoSemanticObserver(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept upstream websocket: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		if err := conn.Write(r.Context(), websocket.MessageText, []byte(`{"hello":"world"}`)); err != nil {
			t.Errorf("write upstream frame: %v", err)
		}
	}))
	defer upstream.Close()

	fwd := NewWebSocketForwarder(WebSocketForwarderConfig{
		Logger: zaptest.NewLogger(t),
	})

	doneCh := make(chan *WebSocketResult, 1)
	commitCh := make(chan WebSocketObservation, 1)
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observer := newBytesTrackingObserver(nil, &LiveBytesTracker{})
		result, _ := fwd.ForwardObserved(r.Context(), w, r, wsURL(upstream), nil, observer, func(observation WebSocketObservation) {
			commitCh <- observation
		}, nil)
		doneCh <- result
	}))
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	clientConn := connectWSClient(t, ctx, wsURL(proxyServer))
	defer clientConn.Close(websocket.StatusNormalClosure, "")

	msgType, payload, err := clientConn.Read(ctx)
	if err != nil {
		t.Fatalf("read upstream frame: %v", err)
	}
	if msgType != websocket.MessageText {
		t.Fatalf("message type = %v, want MessageText", msgType)
	}
	if string(payload) != `{"hello":"world"}` {
		t.Fatalf("payload = %q, want upstream frame", string(payload))
	}
	clientConn.Read(ctx) //nolint:errcheck

	select {
	case observation := <-commitCh:
		if !observation.SessionCommitted {
			t.Fatal("fallback callback must report committed session")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("expected fallback commit callback for tracking-only observer")
	}

	select {
	case result := <-doneCh:
		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if !result.SessionCommitted {
			t.Fatal("expected SessionCommitted=true for tracking-only observer")
		}
		if result.CommitSource != model.CommitUpstreamMessage {
			t.Fatalf("CommitSource = %q, want %q", result.CommitSource, model.CommitUpstreamMessage)
		}
		if result.TerminalCause != model.TerminalCleanClose {
			t.Fatalf("TerminalCause = %q, want %q", result.TerminalCause, model.TerminalCleanClose)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ForwardObserved did not return")
	}
}
