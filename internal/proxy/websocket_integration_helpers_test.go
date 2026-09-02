package proxy

import (
	"context"
	"encoding/json"
	"errors"
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

func proxyCodexDialOptions() *websocket.DialOptions {
	return &websocket.DialOptions{HTTPHeader: http.Header{
		"Authorization": {proxyCodexTestAuthorization},
	}}
}

type WebSocketForwarderConfig = websocketproxy.WebSocketForwarderConfig

func NewWebSocketForwarder(cfg WebSocketForwarderConfig) *websocketproxy.WebSocketForwarder {
	return websocketproxy.NewWebSocketForwarder(cfg)
}

func setWebSocketForwarderForTest(t *testing.T, handler *Handler, forwarder *websocketproxy.WebSocketForwarder) {
	t.Helper()
	handler.webSocketGateway = websocketproxy.NewGateway(websocketproxy.Config{
		Store:                      handler.store,
		Selector:                   newWebSocketSelectorAdapter(handler.selector, handler.httpSelector),
		Health:                     handler.health,
		ActiveSessions:             newWebSocketActiveSessions(handler.activeRegistry),
		VisibleContinuitySeedStore: handler.visibleContinuitySeedStore,
		Auth:                       handler.auth,
		Capture:                    handler.capture,
		Forwarder:                  forwarder,
		Codex:                      newProxyCodexFixture(t).webSocketRuntime,
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

type mockDialer struct {
	dialFunc func(context.Context, string, *websocket.DialOptions) (*websocket.Conn, *http.Response, error)
}

func (dialer *mockDialer) Dial(ctx context.Context, url string, options *websocket.DialOptions) (*websocket.Conn, *http.Response, error) {
	if dialer.dialFunc == nil {
		return nil, nil, nil
	}
	return dialer.dialFunc(ctx, url, options)
}
