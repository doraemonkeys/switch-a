package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/coder/websocket"
)

// newEchoWSServer keeps the common echo fixture in one place so transport tests
// can stay focused on forwarder behavior instead of repeating server plumbing.
func newEchoWSServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Logf("echo server accept error: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		conn.SetReadLimit(wsReadLimit)
		for {
			msgType, data, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			if err := conn.Write(r.Context(), msgType, data); err != nil {
				return
			}
		}
	}))
}

// newCloseAfterNWSServer isolates close-sequence fixtures because multiple test
// themes depend on the same protocol shape.
func newCloseAfterNWSServer(t *testing.T, n int, code websocket.StatusCode, reason string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		conn.SetReadLimit(wsReadLimit)
		for range n {
			msgType, data, err := conn.Read(r.Context())
			if err != nil {
				conn.Close(websocket.StatusInternalError, "read failed")
				return
			}
			if err := conn.Write(r.Context(), msgType, data); err != nil {
				return
			}
		}
		conn.Close(code, reason)
	}))
}

// newHeaderCapturingWSServer centralizes handshake capture because several
// header-propagation tests share the same contract.
func newHeaderCapturingWSServer(t *testing.T, captured *http.Header, mu *sync.Mutex) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		*captured = r.Header.Clone()
		mu.Unlock()

		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		conn.Close(websocket.StatusNormalClosure, "")
	}))
}

func newSemanticErrorWSServer(t *testing.T, payload []byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		conn.SetReadLimit(wsReadLimit)
		if _, _, err := conn.Read(r.Context()); err != nil {
			return
		}
		_ = conn.Write(r.Context(), websocket.MessageText, payload)
		<-r.Context().Done()
	}))
}

func newRecordingWSServer(t *testing.T, received chan<- webSocketReplayMessage) *httptest.Server {
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

func newPushMessagesWSServer(t *testing.T, messages []webSocketReplayMessage) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		for _, message := range messages {
			if err := conn.Write(r.Context(), message.MessageType, message.Data); err != nil {
				return
			}
		}
	}))
}

func wsURL(s *httptest.Server) string {
	return "ws" + strings.TrimPrefix(s.URL, "http")
}

func connectWSClient(t *testing.T, ctx context.Context, url string) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	return conn
}

func readTerminalGatewayErrorEvent(
	t *testing.T,
	ctx context.Context,
	conn *websocket.Conn,
	wantStatus int,
	wantCode string,
) webSocketGatewayErrorEnvelope {
	t.Helper()

	msgType, payload, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read terminal gateway event: %v", err)
	}
	if msgType != websocket.MessageText {
		t.Fatalf("message type = %v, want %v", msgType, websocket.MessageText)
	}

	var envelope webSocketGatewayErrorEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		t.Fatalf("decode terminal gateway event %q: %v", string(payload), err)
	}
	if envelope.Type != webSocketGatewayErrorEventType {
		t.Fatalf("event type = %q, want %q", envelope.Type, webSocketGatewayErrorEventType)
	}
	if envelope.Error.Type != webSocketGatewayErrorType {
		t.Fatalf("error.type = %q, want %q", envelope.Error.Type, webSocketGatewayErrorType)
	}
	if envelope.Status != wantStatus {
		t.Fatalf("status = %d, want %d", envelope.Status, wantStatus)
	}
	if envelope.Error.Code != wantCode {
		t.Fatalf("error.code = %q, want %q", envelope.Error.Code, wantCode)
	}

	if _, _, err := conn.Read(ctx); err == nil || (!errors.Is(err, io.EOF) && !isNormalClose(err)) {
		t.Fatalf("expected websocket close after terminal gateway error, got %v", err)
	}

	return envelope
}

type mockDialer struct {
	dialFunc func(ctx context.Context, url string, opts *websocket.DialOptions) (*websocket.Conn, *http.Response, error)
}

func (m *mockDialer) Dial(ctx context.Context, url string, opts *websocket.DialOptions) (*websocket.Conn, *http.Response, error) {
	if m.dialFunc != nil {
		return m.dialFunc(ctx, url, opts)
	}
	return nil, nil, nil
}
