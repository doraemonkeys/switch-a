package proxy

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"switch-a/internal/model"

	"github.com/coder/websocket"
	"go.uber.org/zap"
)

func TestIsWebSocketUpgrade(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		headers  http.Header
		expected bool
	}{
		{
			name:     "valid upgrade",
			headers:  http.Header{"Upgrade": {"websocket"}, "Connection": {"Upgrade"}},
			expected: true,
		},
		{
			name:     "case insensitive",
			headers:  http.Header{"Upgrade": {"WebSocket"}, "Connection": {"upgrade"}},
			expected: true,
		},
		{
			name:     "connection with multiple values",
			headers:  http.Header{"Upgrade": {"websocket"}, "Connection": {"keep-alive, Upgrade"}},
			expected: true,
		},
		{
			name:     "missing upgrade header",
			headers:  http.Header{"Connection": {"Upgrade"}},
			expected: false,
		},
		{
			name:     "missing connection header",
			headers:  http.Header{"Upgrade": {"websocket"}},
			expected: false,
		},
		{
			name:     "wrong upgrade value",
			headers:  http.Header{"Upgrade": {"h2c"}, "Connection": {"Upgrade"}},
			expected: false,
		},
		{
			name:     "empty headers",
			headers:  http.Header{},
			expected: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := &http.Request{Header: tt.headers}
			got := isWebSocketUpgrade(r)
			if got != tt.expected {
				t.Errorf("isWebSocketUpgrade() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestExtractWebSocketModel(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		url      string
		expected string
	}{
		{name: "model in query", url: "/responses?model=gpt-4o-realtime", expected: "gpt-4o-realtime"},
		{name: "no model param", url: "/responses", expected: ModelUnknown},
		{name: "empty model param", url: "/responses?model=", expected: ModelUnknown},
		{name: "model with other params", url: "/responses?foo=bar&model=claude-4", expected: "claude-4"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := httptest.NewRequest(http.MethodGet, tt.url, nil)
			got := extractWebSocketModel(r)
			if got != tt.expected {
				t.Errorf("extractWebSocketModel() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestBuildWebSocketDialHeaders(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequest(http.MethodGet, "/responses", nil)
	r.Header.Set("OpenAI-Beta", "realtime=v1")
	r.Header.Set("X-Custom", "value")
	r.Header.Set("Authorization", "Bearer client-key") // auth — should be replaced
	r.Header.Set("Connection", "Upgrade")              // hop-by-hop — should be stripped
	r.Header.Set("Upgrade", "websocket")               // hop-by-hop — should be stripped

	provider := &model.Provider{
		APIKey:   "sk-provider-key",
		AuthMode: "bearer",
	}

	headers := buildWebSocketDialHeaders(r, provider, "codex", "auto")

	// Provider auth should be injected.
	if got := headers.Get("Authorization"); got != "Bearer sk-provider-key" {
		t.Errorf("Authorization = %q, want 'Bearer sk-provider-key'", got)
	}

	// Non-hop-by-hop, non-auth headers should pass through.
	if got := headers.Get("OpenAI-Beta"); got != "realtime=v1" {
		t.Errorf("OpenAI-Beta = %q, want 'realtime=v1'", got)
	}
	if got := headers.Get("X-Custom"); got != "value" {
		t.Errorf("X-Custom = %q, want 'value'", got)
	}

	// Hop-by-hop headers should NOT be in dial headers.
	if got := headers.Get("Connection"); got != "" {
		t.Errorf("Connection should be empty, got %q", got)
	}
	if got := headers.Get("Upgrade"); got != "" {
		t.Errorf("Upgrade should be empty, got %q", got)
	}
}

func TestBuildWebSocketDialHeaders_UsesAPITypeKeyOverride(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequest(http.MethodGet, "/responses", nil)
	provider := &model.Provider{
		APIKey:   "default-key",
		AuthMode: "bearer",
		APITypes: []model.ProviderAPIType{{
			ProviderID: "p1",
			APIType:    "codex",
			BaseURL:    "https://example.com",
			APIKey:     "codex-key",
		}},
	}

	headers := buildWebSocketDialHeaders(r, provider, "codex", "auto")

	if got := headers.Get("Authorization"); got != "Bearer codex-key" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer codex-key")
	}
}

// TestHandler_ServeHTTP_WebSocket_FullProxy tests the complete WebSocket proxy flow
// through the handler's ServeHTTP method.
func TestHandler_ServeHTTP_WebSocket_FullProxy(t *testing.T) {
	// Create upstream WebSocket echo server.
	upstream := newEchoWSServer(t)
	defer upstream.Close()

	store := newMockStore()
	store.providers = []model.Provider{
		{
			ID:       "ws-p1",
			Name:     "WS Provider",
			APIKey:   "ws-key",
			AuthMode: "bearer",
			Enabled:  true,
			APITypes: []model.ProviderAPIType{{ProviderID: "ws-p1", APIType: "codex", BaseURL: upstream.URL}},
		},
	}

	registry := NewActiveRequestRegistry()
	handler := NewHandler(Config{
		Store:          store,
		Logger:         zap.NewNop(),
		ActiveRegistry: registry,
	})

	// Wrap handler in a real HTTP server (httptest.NewRecorder doesn't support WebSocket).
	proxyServer := httptest.NewServer(handler)
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Connect to the proxy as a WebSocket client via the /responses route.
	conn, _, err := websocket.Dial(ctx, wsURL(proxyServer)+"/responses?model=gpt-4o-realtime", nil)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// Send a message and verify echo.
	msg := "hello from handler test"
	if err := conn.Write(ctx, websocket.MessageText, []byte(msg)); err != nil {
		t.Fatalf("write: %v", err)
	}
	msgType, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if msgType != websocket.MessageText || string(data) != msg {
		t.Errorf("echo = %q, want %q", string(data), msg)
	}

	// Close client to trigger cleanup.
	conn.Close(websocket.StatusNormalClosure, "done")

	// Wait for async log.
	waitFor(t, func() bool {
		return store.LogsLen() > 0
	}, testPollTimeout)

	log := store.LastLog()
	if log == nil {
		t.Fatal("expected log entry")
	}
	if !log.IsWebSocket {
		t.Error("expected IsWebSocket=true in log")
	}
	if log.APIType != "codex" {
		t.Errorf("log.APIType = %q, want 'codex'", log.APIType)
	}
	if log.Model != "gpt-4o-realtime" {
		t.Errorf("log.Model = %q, want 'gpt-4o-realtime'", log.Model)
	}
	if log.ProviderID != "ws-p1" {
		t.Errorf("log.ProviderID = %q, want 'ws-p1'", log.ProviderID)
	}
	if !log.Success {
		t.Errorf("expected log.Success=true, got false (err: %s)", log.ErrorMsg)
	}
	if log.StatusCode != http.StatusSwitchingProtocols {
		t.Errorf("log.StatusCode = %d, want %d", log.StatusCode, http.StatusSwitchingProtocols)
	}
	if log.ErrorMsg != "" {
		t.Errorf("expected empty ErrorMsg for successful WS, got %q", log.ErrorMsg)
	}

	// Active registry should be cleaned up.
	if len(registry.List()) != 0 {
		t.Errorf("expected 0 active requests after cleanup, got %d", len(registry.List()))
	}
}

func TestHandler_logWebSocketRequest_UsesHandshakeDiagnostics(t *testing.T) {
	t.Parallel()

	store := newMockStore()
	handler := NewHandler(Config{
		Store:  store,
		Logger: zap.NewNop(),
	})

	const handshakeBody = `{"error":{"message":"Account quota exhausted","type":"billing_error"}}`
	info := RequestInfo{
		APIType:   "codex",
		Model:     "gpt-4o-realtime",
		ClientIP:  "127.0.0.1",
		UserID:    "user-1",
		Path:      "/responses",
		Method:    http.MethodGet,
		UserAgent: "codex-test",
		RequestID: "upstream-request-id",
	}
	result := &WebSocketResult{
		HandshakeStatusCode:  http.StatusPaymentRequired,
		HandshakeBodySnippet: handshakeBody,
		Err:                  errors.New("failed to WebSocket dial: expected handshake response status code 101 but got 402"),
	}

	handler.logWebSocketRequest("req-ws-handshake", info, &model.Provider{ID: "ws-p1"}, false, result, result.Err, 250*time.Millisecond)

	log := store.LastLog()
	if log == nil {
		t.Fatal("expected log entry")
	}
	if log.StatusCode != http.StatusPaymentRequired {
		t.Fatalf("StatusCode = %d, want %d", log.StatusCode, http.StatusPaymentRequired)
	}
	if log.ErrorMsg != handshakeBody {
		t.Fatalf("ErrorMsg = %q, want %q", log.ErrorMsg, handshakeBody)
	}
	if log.Success {
		t.Fatal("expected Success=false for failed handshake")
	}
	if !log.IsWebSocket {
		t.Fatal("expected IsWebSocket=true")
	}
}

// TestHandler_ServeHTTP_WebSocket_NoProvider tests that a 503 is returned
// when no provider is available for WebSocket.
func TestHandler_ServeHTTP_WebSocket_NoProvider(t *testing.T) {
	store := newMockStore()
	// No providers configured.

	handler := NewHandler(Config{
		Store:  store,
		Logger: zap.NewNop(),
	})

	proxyServer := httptest.NewServer(handler)
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Attempt WebSocket connection — should fail before upgrade.
	_, resp, err := websocket.Dial(ctx, wsURL(proxyServer)+"/responses", nil)
	if err == nil {
		t.Fatal("expected dial to fail with no providers")
	}
	if resp == nil {
		t.Fatal("expected HTTP response from server even on dial failure")
	}
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusServiceUnavailable)
	}

	// Wait for async log.
	waitFor(t, func() bool {
		return store.LogsLen() > 0
	}, testPollTimeout)

	log := store.LastLog()
	if log == nil {
		t.Fatal("expected log entry")
	}
	if log.Success {
		t.Error("expected log.Success=false")
	}
	if !log.IsWebSocket {
		t.Error("expected IsWebSocket=true in log")
	}
}

// TestHandler_ServeHTTP_WebSocket_ActiveRegistryTracking verifies that WebSocket
// connections are registered and unregistered in the active request registry.
func TestHandler_ServeHTTP_WebSocket_ActiveRegistryTracking(t *testing.T) {
	upstream := newEchoWSServer(t)
	defer upstream.Close()

	store := newMockStore()
	store.providers = []model.Provider{
		{
			ID:       "reg-p1",
			Name:     "Registry Provider",
			APIKey:   "key",
			AuthMode: "bearer",
			Enabled:  true,
			APITypes: []model.ProviderAPIType{{ProviderID: "reg-p1", APIType: "codex", BaseURL: upstream.URL}},
		},
	}

	registry := NewActiveRequestRegistry()
	handler := NewHandler(Config{
		Store:          store,
		Logger:         zap.NewNop(),
		ActiveRegistry: registry,
	})

	proxyServer := httptest.NewServer(handler)
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(proxyServer)+"/responses", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	// Send a message to ensure connection is fully established.
	conn.Write(ctx, websocket.MessageText, []byte("ping"))
	conn.Read(ctx)

	// While connection is active, check the registry.
	active := registry.List()
	if len(active) != 1 {
		t.Fatalf("expected 1 active request, got %d", len(active))
	}
	if !active[0].IsWebSocket {
		t.Error("expected IsWebSocket=true in active request")
	}
	if !active[0].HasReceivedData {
		t.Error("expected HasReceivedData=true after successful connect")
	}
	if active[0].ProviderID != "reg-p1" {
		t.Errorf("active.ProviderID = %q, want 'reg-p1'", active[0].ProviderID)
	}

	// Close client.
	conn.Close(websocket.StatusNormalClosure, "")

	// Wait for cleanup.
	waitFor(t, func() bool {
		return len(registry.List()) == 0
	}, testPollTimeout)
}

// TestHandler_ServeHTTP_WebSocket_RegularHTTPNotAffected verifies that normal
// HTTP POST requests to the same path are still handled as regular HTTP proxy.
func TestHandler_ServeHTTP_WebSocket_RegularHTTPNotAffected(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	store := newMockStore()
	store.providers = []model.Provider{
		{
			ID:       "http-p1",
			Name:     "HTTP Provider",
			APIKey:   "key",
			AuthMode: "bearer",
			Enabled:  true,
			APITypes: []model.ProviderAPIType{{ProviderID: "http-p1", APIType: "codex", BaseURL: upstream.URL}},
		},
	}

	handler := NewHandler(Config{
		Store:  store,
		Logger: zap.NewNop(),
	})

	// Regular POST request — should NOT go through WebSocket path.
	req := httptest.NewRequest(http.MethodPost, "/responses", strings.NewReader(`{"model":"gpt-4"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Errorf("body = %q, expected JSON response", w.Body.String())
	}
}

// TestHandler_ServeHTTP_WebSocket_WithSelector tests the WebSocket proxy flow
// with a Selector configured — the production path where sticky sessions and
// active provider fallback are evaluated.
func TestHandler_ServeHTTP_WebSocket_WithSelector(t *testing.T) {
	upstream := newEchoWSServer(t)
	defer upstream.Close()

	wsProvider := &model.Provider{
		ID:       "ws-sel-p1",
		Name:     "WS Selector Provider",
		APIKey:   "ws-key",
		AuthMode: "bearer",
		Enabled:  true,
		APITypes: []model.ProviderAPIType{{ProviderID: "ws-sel-p1", APIType: "codex", BaseURL: upstream.URL}},
	}

	store := newMockStore()
	store.providers = []model.Provider{*wsProvider}

	mockSel := &mockSelector{
		selectWithMetadataFunc: func(_ context.Context, _ *model.SelectRequest) (*selectResult, error) {
			return &selectResult{
				Provider:        wsProvider,
				FromStickyCache: false,
			}, nil
		},
	}

	registry := NewActiveRequestRegistry()
	handler := NewHandler(Config{
		Store:          store,
		Logger:         zap.NewNop(),
		Selector:       mockSel,
		ActiveRegistry: registry,
	})

	proxyServer := httptest.NewServer(handler)
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(proxyServer)+"/responses?model=gpt-4o-realtime", nil)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	msg := "selector test"
	if err := conn.Write(ctx, websocket.MessageText, []byte(msg)); err != nil {
		t.Fatalf("write: %v", err)
	}
	msgType, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if msgType != websocket.MessageText || string(data) != msg {
		t.Errorf("echo = %q, want %q", string(data), msg)
	}

	conn.Close(websocket.StatusNormalClosure, "done")

	waitFor(t, func() bool {
		return store.LogsLen() > 0
	}, testPollTimeout)

	log := store.LastLog()
	if log == nil {
		t.Fatal("expected log entry")
	}
	if !log.IsWebSocket {
		t.Error("expected IsWebSocket=true")
	}
	if log.ProviderID != "ws-sel-p1" {
		t.Errorf("log.ProviderID = %q, want 'ws-sel-p1'", log.ProviderID)
	}
	if !log.Success {
		t.Errorf("expected log.Success=true, got false (err: %s)", log.ErrorMsg)
	}
	if log.ErrorMsg != "" {
		t.Errorf("expected empty ErrorMsg for successful WS, got %q", log.ErrorMsg)
	}
}

// TestHandler_ServeHTTP_WebSocket_SuccessLogHasNoError verifies that successful
// WebSocket sessions produce log entries with empty ErrorMsg.
func TestHandler_ServeHTTP_WebSocket_SuccessLogHasNoError(t *testing.T) {
	upstream := newEchoWSServer(t)
	defer upstream.Close()

	store := newMockStore()
	store.providers = []model.Provider{
		{
			ID: "ws-p1", Name: "WS Provider", APIKey: "key", AuthMode: "bearer", Enabled: true,
			APITypes: []model.ProviderAPIType{{ProviderID: "ws-p1", APIType: "codex", BaseURL: upstream.URL}},
		},
	}

	handler := NewHandler(Config{Store: store, Logger: zap.NewNop()})
	proxyServer := httptest.NewServer(handler)
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(proxyServer)+"/responses", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	conn.Write(ctx, websocket.MessageText, []byte("ping"))
	conn.Read(ctx)
	conn.Close(websocket.StatusNormalClosure, "done")

	waitFor(t, func() bool { return store.LogsLen() > 0 }, testPollTimeout)

	log := store.LastLog()
	if log == nil {
		t.Fatal("expected log entry")
	}
	if log.ErrorMsg != "" {
		t.Errorf("expected empty ErrorMsg for successful WS, got %q", log.ErrorMsg)
	}
	if !log.Success {
		t.Error("expected log.Success=true")
	}
}

func TestHandler_ServeHTTP_WebSocket_CapturesObservedModelAndTokenUsage(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept upstream websocket: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		ctx := r.Context()
		conn.SetReadLimit(wsReadLimit)

		if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"session.created","event_id":"evt_session","session":{"model":"gpt-realtime-2025-08-28"}}`)); err != nil {
			t.Errorf("write session.created: %v", err)
			return
		}
		if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"response.completed","event_id":"evt_response","response":{"id":"resp_1","model":"gpt-realtime-2025-08-28","usage":{"input_tokens":64,"output_tokens":16,"total_tokens":80,"input_tokens_details":{"cached_tokens":11}}}}`)); err != nil {
			t.Errorf("write response.completed: %v", err)
			return
		}
	}))
	defer upstream.Close()

	store := newMockStore()
	store.providers = []model.Provider{
		{
			ID:       "ws-p1",
			Name:     "WS Provider",
			APIKey:   "key",
			AuthMode: "bearer",
			Enabled:  true,
			APITypes: []model.ProviderAPIType{{ProviderID: "ws-p1", APIType: "codex", BaseURL: upstream.URL}},
		},
	}

	handler := NewHandler(Config{Store: store, Logger: zap.NewNop()})
	proxyServer := httptest.NewServer(handler)
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(proxyServer)+"/responses", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	for {
		_, _, err := conn.Read(ctx)
		if err == nil {
			continue
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if !isNormalClose(err) {
			t.Fatalf("read websocket events: %v", err)
		}
		break
	}

	waitFor(t, func() bool { return store.LogsLen() > 0 }, testPollTimeout)

	log := store.LastLog()
	if log == nil {
		t.Fatal("expected log entry")
	}
	if log.Model != "gpt-realtime-2025-08-28" {
		t.Fatalf("log.Model = %q, want %q", log.Model, "gpt-realtime-2025-08-28")
	}
	if log.PromptTokens == nil || *log.PromptTokens != 64 {
		t.Fatalf("PromptTokens = %v, want 64", log.PromptTokens)
	}
	if log.CompletionTokens == nil || *log.CompletionTokens != 16 {
		t.Fatalf("CompletionTokens = %v, want 16", log.CompletionTokens)
	}
	if log.TotalTokens == nil || *log.TotalTokens != 80 {
		t.Fatalf("TotalTokens = %v, want 80", log.TotalTokens)
	}
	if log.CacheReadInputTokens == nil || *log.CacheReadInputTokens != 11 {
		t.Fatalf("CacheReadInputTokens = %v, want 11", log.CacheReadInputTokens)
	}
}

func TestHandler_ServeHTTP_WebSocket_UpdatesActiveModelDuringSession(t *testing.T) {
	releaseUpstream := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept upstream websocket: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		ctx := r.Context()
		conn.SetReadLimit(wsReadLimit)

		if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"response.created","response":{"id":"resp_live","model":"gpt-5.4"}}`)); err != nil {
			t.Errorf("write response.created: %v", err)
			return
		}

		select {
		case <-releaseUpstream:
		case <-ctx.Done():
		}
	}))
	defer upstream.Close()

	store := newMockStore()
	store.providers = []model.Provider{
		{
			ID:       "ws-p1",
			Name:     "WS Provider",
			APIKey:   "key",
			AuthMode: "bearer",
			Enabled:  true,
			APITypes: []model.ProviderAPIType{{ProviderID: "ws-p1", APIType: "codex", BaseURL: upstream.URL}},
		},
	}

	registry := NewActiveRequestRegistry()
	handler := NewHandler(Config{
		Store:          store,
		Logger:         zap.NewNop(),
		ActiveRegistry: registry,
	})
	proxyServer := httptest.NewServer(handler)
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(proxyServer)+"/responses", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	if _, _, err := conn.Read(ctx); err != nil {
		t.Fatalf("read websocket event: %v", err)
	}

	waitFor(t, func() bool {
		requests := registry.List()
		return len(requests) == 1 && requests[0].Model == "gpt-5.4"
	}, testPollTimeout)

	requests := registry.List()
	if len(requests) != 1 {
		t.Fatalf("expected 1 active request, got %d", len(requests))
	}
	if requests[0].Model != "gpt-5.4" {
		t.Fatalf("active model = %q, want %q", requests[0].Model, "gpt-5.4")
	}

	close(releaseUpstream)
}

func TestHandler_ServeHTTP_WebSocket_StickyUpdateUsesResolvedModel(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept upstream websocket: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		ctx := r.Context()
		conn.SetReadLimit(wsReadLimit)

		if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"response.created","event_id":"evt_response","response":{"id":"resp_live","model":"gpt-5.4"}}`)); err != nil {
			t.Errorf("write response.created: %v", err)
			return
		}
	}))
	defer upstream.Close()

	wsProvider := &model.Provider{
		ID:       "ws-sticky-p1",
		Name:     "WS Sticky Provider",
		APIKey:   "key",
		AuthMode: "bearer",
		Enabled:  true,
		APITypes: []model.ProviderAPIType{{ProviderID: "ws-sticky-p1", APIType: "codex", BaseURL: upstream.URL}},
	}

	store := newMockStore()
	store.providers = []model.Provider{*wsProvider}

	mockSel := &mockSelector{
		selectWithMetadataFunc: func(_ context.Context, req *model.SelectRequest) (*selectResult, error) {
			if req.Model != ModelUnknown {
				t.Fatalf("selection model = %q, want %q before websocket observation", req.Model, ModelUnknown)
			}
			return &selectResult{
				Provider:        wsProvider,
				FromStickyCache: false,
			}, nil
		},
	}

	handler := NewHandler(Config{
		Store:    store,
		Logger:   zap.NewNop(),
		Selector: mockSel,
	})
	proxyServer := httptest.NewServer(handler)
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(proxyServer)+"/responses", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	for {
		_, _, err := conn.Read(ctx)
		if err == nil {
			continue
		}
		if errors.Is(err, io.EOF) || isNormalClose(err) {
			break
		}
		t.Fatalf("read websocket events: %v", err)
	}

	waitFor(t, func() bool { return mockSel.StickyUpdatesLen() == 1 }, testPollTimeout)

	update, ok := mockSel.LastStickyUpdate()
	if !ok {
		t.Fatal("expected sticky update")
	}
	if update.ProviderID != "ws-sticky-p1" {
		t.Fatalf("sticky provider = %q, want %q", update.ProviderID, "ws-sticky-p1")
	}
	if update.Model != "gpt-5.4" {
		t.Fatalf("sticky model = %q, want %q", update.Model, "gpt-5.4")
	}
}

// TestHandler_ServeHTTP_WebSocket_AcceptFailureNoMarkFailure verifies that a client
// accept failure does NOT degrade the provider's health status.
func TestHandler_ServeHTTP_WebSocket_AcceptFailureNoMarkFailure(t *testing.T) {
	upstream := newEchoWSServer(t)
	defer upstream.Close()

	store := newMockStore()
	store.providers = []model.Provider{
		{
			ID: "ws-p1", Name: "WS Provider", APIKey: "key", AuthMode: "bearer", Enabled: true,
			APITypes: []model.ProviderAPIType{{ProviderID: "ws-p1", APIType: "codex", BaseURL: upstream.URL}},
		},
	}

	healthMgr := newTrackingHealthManager()
	handler := NewHandler(Config{Store: store, Logger: zap.NewNop(), Health: healthMgr})

	// Send a request with WebSocket upgrade headers but via httptest.NewRecorder,
	// which doesn't support hijacking — Accept will fail (client-side issue).
	req := httptest.NewRequest(http.MethodGet, "/responses", nil)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	// Give async log goroutine time to fire.
	waitFor(t, func() bool { return store.LogsLen() > 0 }, testPollTimeout)

	// The key assertion: accept failure is a client issue, NOT a provider failure.
	if len(healthMgr.getMarkFailureCalls()) > 0 {
		t.Errorf("expected 0 MarkFailure calls for client accept failure, got %d", len(healthMgr.getMarkFailureCalls()))
	}
}

// TestHandler_ServeHTTP_WebSocket_NonCodexAPIType_Rejected verifies that
// WebSocket upgrade requests on non-Codex API paths are rejected with 400.
func TestHandler_ServeHTTP_WebSocket_NonCodexAPIType_Rejected(t *testing.T) {
	store := newMockStore()
	store.providers = []model.Provider{
		{
			ID: "p1", Name: "Provider", APIKey: "key", AuthMode: "bearer", Enabled: true,
			APITypes: []model.ProviderAPIType{{ProviderID: "p1", APIType: "claude", BaseURL: "http://example.com"}},
		},
	}

	handler := NewHandler(Config{Store: store, Logger: zap.NewNop()})

	req := httptest.NewRequest(http.MethodGet, "/v1/messages", nil)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if !strings.Contains(w.Body.String(), "not supported") {
		t.Errorf("body = %q, expected 'not supported' message", w.Body.String())
	}
}

// TestHandler_ServeHTTP_NonUpgradeGET_Returns426 verifies that GET /responses
// without a WebSocket Upgrade header returns 426 Upgrade Required.
func TestHandler_ServeHTTP_NonUpgradeGET_Returns426(t *testing.T) {
	store := newMockStore()
	store.providers = []model.Provider{
		{
			ID: "p1", Name: "Provider", APIKey: "key", AuthMode: "bearer", Enabled: true,
			APITypes: []model.ProviderAPIType{{ProviderID: "p1", APIType: "codex", BaseURL: "http://example.com"}},
		},
	}

	handler := NewHandler(Config{Store: store, Logger: zap.NewNop()})

	req := httptest.NewRequest(http.MethodGet, "/responses", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUpgradeRequired {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUpgradeRequired)
	}
	if !strings.Contains(w.Body.String(), "WebSocket upgrade") {
		t.Errorf("body = %q, expected WebSocket upgrade message", w.Body.String())
	}
}

func TestBuildWebSocketDialHeaders_FiltersSecWebSocketHeaders(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequest(http.MethodGet, "/responses", nil)
	r.Header.Set("OpenAI-Beta", "realtime=v1")
	r.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	r.Header.Set("Sec-WebSocket-Version", "13")
	r.Header.Set("Sec-WebSocket-Extensions", "permessage-deflate")
	r.Header.Set("Sec-WebSocket-Protocol", "graphql-ws")
	r.Header.Set("Connection", "Upgrade")
	r.Header.Set("Upgrade", "websocket")

	provider := &model.Provider{APIKey: "sk-key", AuthMode: "bearer"}
	headers := buildWebSocketDialHeaders(r, provider, "codex", "auto")

	// Business headers should pass through.
	if got := headers.Get("OpenAI-Beta"); got != "realtime=v1" {
		t.Errorf("OpenAI-Beta = %q, want 'realtime=v1'", got)
	}

	// WebSocket handshake headers must NOT be forwarded.
	for _, h := range []string{"Sec-WebSocket-Key", "Sec-WebSocket-Version", "Sec-WebSocket-Extensions", "Sec-WebSocket-Protocol"} {
		if got := headers.Get(h); got != "" {
			t.Errorf("%s should be filtered, got %q", h, got)
		}
	}
}

// TestIsWebSocketHandshakeHeader tests the handshake header classification.
func TestIsWebSocketHandshakeHeader(t *testing.T) {
	t.Parallel()
	tests := []struct {
		key      string
		expected bool
	}{
		{"Sec-WebSocket-Key", true},
		{"Sec-Websocket-Version", true},
		{"Sec-WebSocket-Extensions", true},
		{"Sec-WebSocket-Protocol", true},
		{"sec-websocket-key", true},
		{"Authorization", false},
		{"OpenAI-Beta", false},
		{"Sec-Fetch-Mode", false}, // 14 chars prefix doesn't match
		{"Sec-", false},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			t.Parallel()
			got := isWebSocketHandshakeHeader(tt.key)
			if got != tt.expected {
				t.Errorf("isWebSocketHandshakeHeader(%q) = %v, want %v", tt.key, got, tt.expected)
			}
		})
	}
}

func TestBytesTrackingObserver_CountsAndTimestamp(t *testing.T) {
	t.Parallel()

	tracker := &LiveBytesTracker{}
	obs := newBytesTrackingObserver(nil, tracker)

	// Simulate client → upstream messages.
	obs.ObserveClientMessage(websocket.MessageText, []byte("hello")) // 5 bytes
	obs.ObserveClientMessage(websocket.MessageText, []byte("world")) // 5 bytes

	// Simulate upstream → client messages.
	obs.ObserveUpstreamMessage(websocket.MessageText, []byte("response data 1234567890")) // 24 bytes

	if got := tracker.BytesSent.Load(); got != 10 {
		t.Errorf("BytesSent = %d, want 10", got)
	}
	if got := tracker.MsgsSent.Load(); got != 2 {
		t.Errorf("MsgsSent = %d, want 2", got)
	}
	if got := tracker.BytesReceived.Load(); got != 24 {
		t.Errorf("BytesReceived = %d, want 24", got)
	}
	if got := tracker.MsgsReceived.Load(); got != 1 {
		t.Errorf("MsgsReceived = %d, want 1", got)
	}
	if got := tracker.LastActivityAt.Load(); got == 0 {
		t.Error("LastActivityAt should be non-zero after messages")
	}
}

func TestBytesTrackingObserver_DelegatesToInner(t *testing.T) {
	t.Parallel()

	var clientCalls, upstreamCalls int
	inner := &stubObserver{
		onClient:   func() { clientCalls++ },
		onUpstream: func() { upstreamCalls++ },
	}
	tracker := &LiveBytesTracker{}
	obs := newBytesTrackingObserver(inner, tracker)

	obs.ObserveClientMessage(websocket.MessageText, []byte("a"))
	obs.ObserveUpstreamMessage(websocket.MessageText, []byte("b"))

	if clientCalls != 1 {
		t.Errorf("inner.ObserveClientMessage called %d times, want 1", clientCalls)
	}
	if upstreamCalls != 1 {
		t.Errorf("inner.ObserveUpstreamMessage called %d times, want 1", upstreamCalls)
	}
}

func TestBytesTrackingObserver_SnapshotDelegatesToInner(t *testing.T) {
	t.Parallel()

	inner := &stubObserver{
		snapshot: WebSocketObservation{Model: "gpt-5"},
	}
	tracker := &LiveBytesTracker{}
	obs := newBytesTrackingObserver(inner, tracker)

	snap := obs.Snapshot()
	if snap.Model != "gpt-5" {
		t.Errorf("Snapshot().Model = %q, want %q", snap.Model, "gpt-5")
	}
}

func TestBytesTrackingObserver_NilInner(t *testing.T) {
	t.Parallel()

	tracker := &LiveBytesTracker{}
	obs := newBytesTrackingObserver(nil, tracker)

	// Should not panic with nil inner observer.
	obs.ObserveClientMessage(websocket.MessageText, []byte("data"))
	obs.ObserveUpstreamMessage(websocket.MessageBinary, []byte("data"))
	snap := obs.Snapshot()

	if snap.Model != "" {
		t.Errorf("expected empty Model from nil inner Snapshot, got %q", snap.Model)
	}
	if tracker.MsgsSent.Load() != 1 || tracker.MsgsReceived.Load() != 1 {
		t.Error("counters should still increment with nil inner")
	}
}

// stubObserver is a minimal test double for WebSocketMessageObserver.
type stubObserver struct {
	onClient   func()
	onUpstream func()
	snapshot   WebSocketObservation
}

func (s *stubObserver) ObserveClientMessage(_ websocket.MessageType, _ []byte) {
	if s.onClient != nil {
		s.onClient()
	}
}

func (s *stubObserver) ObserveUpstreamMessage(_ websocket.MessageType, _ []byte) {
	if s.onUpstream != nil {
		s.onUpstream()
	}
}

func (s *stubObserver) Snapshot() WebSocketObservation {
	return s.snapshot
}
