package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/coder/websocket"
	"github.com/doraemonkeys/switch-a/internal/websocketproxy"
)

const (
	wsReadLimit                    = 16 * 1024 * 1024
	webSocketGatewayErrorEventType = "error"
	webSocketGatewayErrorType      = "gateway_error"
)

type WebSocketForwarderConfig = websocketproxy.WebSocketForwarderConfig

func NewWebSocketForwarder(cfg WebSocketForwarderConfig) *websocketproxy.WebSocketForwarder {
	return websocketproxy.NewWebSocketForwarder(cfg)
}

func setWebSocketForwarderForTest(handler *Handler, forwarder *websocketproxy.WebSocketForwarder) {
	handler.webSocketGateway = websocketproxy.NewGateway(websocketproxy.Config{
		Store:                      handler.store,
		Selector:                   newWebSocketSelectorAdapter(handler.selector, handler.httpSelector),
		Health:                     handler.health,
		ActiveSessions:             newWebSocketActiveSessions(handler.activeRegistry),
		VisibleContinuitySeedStore: handler.visibleContinuitySeedStore,
		Auth:                       handler.auth,
		Capture:                    handler.capture,
		Forwarder:                  forwarder,
		Logger:                     handler.logger,
	})
}

func newEchoWSServer(t *testing.T) *httptest.Server {
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

func wsURL(server *httptest.Server) string { return "ws" + strings.TrimPrefix(server.URL, "http") }

func isNormalClose(err error) bool {
	var closeErr websocket.CloseError
	if !errors.As(err, &closeErr) {
		return false
	}
	return closeErr.Code == websocket.StatusNormalClosure || closeErr.Code == websocket.StatusGoingAway
}

type webSocketGatewayErrorEnvelope struct {
	Type   string `json:"type"`
	Status int    `json:"status"`
	Error  struct {
		Type    string `json:"type"`
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type webSocketReplayMessage struct {
	MessageType websocket.MessageType
	Data        []byte
}

func marshalWebSocketGatewayError(statusCode int, code, message string) []byte {
	envelope := webSocketGatewayErrorEnvelope{Type: webSocketGatewayErrorEventType, Status: statusCode}
	envelope.Error.Type = webSocketGatewayErrorType
	envelope.Error.Code = code
	envelope.Error.Message = message
	payload, _ := json.Marshal(envelope)
	return payload
}

func readTerminalGatewayErrorEvent(t *testing.T, ctx context.Context, conn *websocket.Conn, wantStatus int, wantCode string) webSocketGatewayErrorEnvelope {
	t.Helper()
	messageType, payload, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read terminal gateway event: %v", err)
	}
	if messageType != websocket.MessageText {
		t.Fatalf("message type = %v, want text", messageType)
	}
	var envelope webSocketGatewayErrorEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		t.Fatalf("decode terminal gateway event %q: %v", string(payload), err)
	}
	if envelope.Type != "error" || envelope.Error.Type != "gateway_error" || envelope.Status != wantStatus || envelope.Error.Code != wantCode {
		t.Fatalf("terminal gateway event = %#v, want status=%d code=%q", envelope, wantStatus, wantCode)
	}
	if _, _, err := conn.Read(ctx); err == nil || (!errors.Is(err, io.EOF) && !isNormalClose(err)) {
		t.Fatalf("expected websocket close after terminal gateway error, got %v", err)
	}
	return envelope
}

type mockDialer struct {
	dialFunc func(context.Context, string, *websocket.DialOptions) (*websocket.Conn, *http.Response, error)
}

func (dialer *mockDialer) Dial(ctx context.Context, url string, options *websocket.DialOptions) (*websocket.Conn, *http.Response, error) {
	if dialer.dialFunc == nil {
		return nil, nil, nil
	}
	return dialer.dialFunc(ctx, url, options)
}
