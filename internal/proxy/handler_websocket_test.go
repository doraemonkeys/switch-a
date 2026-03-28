package proxy

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"switch-a/internal/model"

	"github.com/coder/websocket"
	"go.uber.org/zap"
)

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
	waitFor(t, func() bool {
		return store.AttemptsLen() > 0
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
	if log.RetryCount != 0 {
		t.Errorf("log.RetryCount = %d, want 0", log.RetryCount)
	}

	attempts := store.LastAttempts(1)
	if len(attempts) != 1 {
		t.Fatalf("expected 1 websocket attempt, got %d", len(attempts))
	}
	if attempts[0].ProviderID != "ws-p1" {
		t.Errorf("attempt.ProviderID = %q, want %q", attempts[0].ProviderID, "ws-p1")
	}
	if attempts[0].Attempt != 0 {
		t.Errorf("attempt.Attempt = %d, want 0", attempts[0].Attempt)
	}
	if attempts[0].StatusCode != http.StatusSwitchingProtocols {
		t.Errorf("attempt.StatusCode = %d, want %d", attempts[0].StatusCode, http.StatusSwitchingProtocols)
	}

	// Active registry should be cleaned up.
	if len(registry.List()) != 0 {
		t.Errorf("expected 0 active requests after cleanup, got %d", len(registry.List()))
	}
}
func TestHandler_ServeHTTP_WebSocket_StickySelectionSkipsPreAcceptFailover(t *testing.T) {
	var fallbackAttempts int32

	initialUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUpgradeRequired)
		_, _ = io.WriteString(w, `{"error":"fallback to http"}`)
	}))
	defer initialUpstream.Close()

	fallbackUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&fallbackAttempts, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer fallbackUpstream.Close()

	initialProvider := &model.Provider{
		ID:       "ws-sticky-preaccept-p1",
		Name:     "WS Sticky PreAccept P1",
		APIKey:   "key-1",
		AuthMode: "bearer",
		Enabled:  true,
		APITypes: []model.ProviderAPIType{{ProviderID: "ws-sticky-preaccept-p1", APIType: "codex", BaseURL: initialUpstream.URL}},
	}
	fallbackProvider := &model.Provider{
		ID:       "ws-sticky-preaccept-p2",
		Name:     "WS Sticky PreAccept P2",
		APIKey:   "key-2",
		AuthMode: "bearer",
		Enabled:  true,
		APITypes: []model.ProviderAPIType{{ProviderID: "ws-sticky-preaccept-p2", APIType: "codex", BaseURL: fallbackUpstream.URL}},
	}

	store := newMockStore()
	store.providers = []model.Provider{*initialProvider, *fallbackProvider}

	var selectExcludingCalls int32
	mockSel := &mockSelector{
		selectWithMetadataFunc: func(_ context.Context, req *model.SelectRequest) (*selectResult, error) {
			if req.FailoverContext != nil {
				t.Fatal("initial sticky selection must not carry failover context")
			}
			return &selectResult{Provider: initialProvider, FromStickyCache: true}, nil
		},
		selectExcludingFunc: func(_ context.Context, _ *model.SelectRequest, _ map[string]bool) (*model.Provider, error) {
			atomic.AddInt32(&selectExcludingCalls, 1)
			return fallbackProvider, nil
		},
	}

	handler := NewHandler(Config{
		Store:    store,
		Selector: mockSel,
		Logger:   zap.NewNop(),
	})

	proxyServer := httptest.NewServer(handler)
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(proxyServer)+"/responses", nil)
	if err != nil {
		t.Fatalf("dial websocket through proxy: %v", err)
	}
	event := readTerminalGatewayErrorEvent(t, ctx, conn, http.StatusUpgradeRequired, ErrCodeWebSocketUpgrade)
	if !strings.Contains(event.Error.Message, "fallback to http") {
		t.Fatalf("gateway error message = %q, want upstream fallback detail", event.Error.Message)
	}

	waitFor(t, func() bool {
		return store.LogsLen() > 0 && store.AttemptsLen() > 0
	}, testPollTimeout)

	if got := atomic.LoadInt32(&selectExcludingCalls); got != 0 {
		t.Fatalf("SelectExcluding calls = %d, want 0", got)
	}
	if got := atomic.LoadInt32(&fallbackAttempts); got != 0 {
		t.Fatalf("fallback upstream attempts = %d, want 0", got)
	}

	log := store.LastLog()
	if log == nil {
		t.Fatal("expected log entry")
	}
	if log.ProviderID != initialProvider.ID {
		t.Fatalf("ProviderID = %q, want %q", log.ProviderID, initialProvider.ID)
	}
	if !log.IsSticky {
		t.Fatal("expected sticky selection to remain sticky after terminal pre-accept failure")
	}
	if log.RetryCount != 0 {
		t.Fatalf("RetryCount = %d, want 0", log.RetryCount)
	}

	attempts := store.LastAttempts(1)
	if len(attempts) != 1 {
		t.Fatalf("expected 1 attempt, got %d", len(attempts))
	}
	if attempts[0].ProviderID != initialProvider.ID {
		t.Fatalf("attempt.ProviderID = %q, want %q", attempts[0].ProviderID, initialProvider.ID)
	}
	if attempts[0].SwitchReason != "" {
		t.Fatalf("attempt.SwitchReason = %q, want empty", attempts[0].SwitchReason)
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

	waitFor(t, func() bool { return len(registry.List()) == 1 }, testPollTimeout)

	active := registry.List()
	if len(active) != 1 {
		t.Fatalf("expected 1 active request after handshake, got %d", len(active))
	}
	if active[0].HasReceivedData {
		t.Fatal("HasReceivedData must stay false until committed upstream service arrives")
	}

	// Send a message to ensure the upstream echoes a committed frame.
	conn.Write(ctx, websocket.MessageText, []byte("ping"))
	conn.Read(ctx)

	waitFor(t, func() bool {
		requests := registry.List()
		return len(requests) == 1 && requests[0].HasReceivedData
	}, testPollTimeout)

	// While connection is active, check the registry again.
	active = registry.List()
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

func TestHandler_ServeHTTP_WebSocket_StickyUpdateKeepsHandshakeModelDimensions(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name            string
		queryModel      string
		wantStickyModel string
	}{
		{
			name:            "unknown handshake model falls back to api_type stickiness",
			queryModel:      "",
			wantStickyModel: ModelUnknown,
		},
		{
			name:            "known handshake model keeps model stickiness",
			queryModel:      "handshake-model",
			wantStickyModel: "handshake-model",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := websocket.Accept(w, r, nil)
				if err != nil {
					t.Errorf("accept upstream websocket: %v", err)
					return
				}
				defer conn.Close(websocket.StatusNormalClosure, "")

				writeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if err := conn.Write(writeCtx, websocket.MessageText, []byte(`{"type":"response.created","response":{"model":"resolved-model"}}`)); err != nil {
					t.Errorf("write upstream semantic event: %v", err)
				}
			}))
			defer upstream.Close()

			wsProvider := &model.Provider{
				ID:       "ws-sticky-model-p1",
				Name:     "WS Sticky Model Provider",
				APIKey:   "ws-key",
				AuthMode: "bearer",
				Enabled:  true,
				APITypes: []model.ProviderAPIType{{ProviderID: "ws-sticky-model-p1", APIType: "codex", BaseURL: upstream.URL}},
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

			handler := NewHandler(Config{
				Store:    store,
				Logger:   zap.NewNop(),
				Selector: mockSel,
			})

			proxyServer := httptest.NewServer(handler)
			defer proxyServer.Close()

			wsPath := wsURL(proxyServer) + "/responses"
			if tc.queryModel != "" {
				wsPath += "?model=" + url.QueryEscape(tc.queryModel)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			conn, _, err := websocket.Dial(ctx, wsPath, nil)
			if err != nil {
				t.Fatalf("dial proxy: %v", err)
			}
			defer conn.Close(websocket.StatusNormalClosure, "")

			msgType, data, err := conn.Read(ctx)
			if err != nil {
				t.Fatalf("read semantic event: %v", err)
			}
			if msgType != websocket.MessageText || !strings.Contains(string(data), `"response.created"`) {
				t.Fatalf("unexpected websocket payload: type=%v body=%q", msgType, string(data))
			}
			_ = conn.Close(websocket.StatusNormalClosure, "done")

			waitFor(t, func() bool {
				return mockSel.StickyUpdatesLen() == 1 && store.LogsLen() > 0
			}, testPollTimeout)

			update, ok := mockSel.LastStickyUpdate()
			if !ok {
				t.Fatal("expected sticky update after committed websocket session")
			}
			if update.Model != tc.wantStickyModel {
				t.Fatalf("sticky update model = %q, want %q", update.Model, tc.wantStickyModel)
			}

			log := store.LastLog()
			if log == nil {
				t.Fatal("expected websocket log entry")
			}
			if log.Model != "resolved-model" {
				t.Fatalf("log.Model = %q, want %q", log.Model, "resolved-model")
			}
		})
	}
}

func TestHandler_ServeHTTP_WebSocket_SelectionProbeUsesClientModel(t *testing.T) {
	t.Parallel()

	const prompt = `{"type":"response.create","response":{"model":"client-model","instructions":"hello"}}`

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept upstream websocket: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		msgType, payload, err := conn.Read(r.Context())
		if err != nil {
			t.Errorf("read client prompt: %v", err)
			return
		}
		if msgType != websocket.MessageText || string(payload) != prompt {
			t.Errorf("prompt = (%v, %q), want text/%q", msgType, string(payload), prompt)
			return
		}
		if err := conn.Write(r.Context(), websocket.MessageText, []byte(`{"type":"response.created","response":{"model":"client-model"}}`)); err != nil {
			t.Errorf("write upstream semantic event: %v", err)
		}
	}))
	defer upstream.Close()

	provider := &model.Provider{
		ID:       "ws-probe-select-p1",
		Name:     "WS Probe Select Provider",
		APIKey:   "ws-key",
		AuthMode: "bearer",
		Enabled:  true,
		APITypes: []model.ProviderAPIType{{ProviderID: "ws-probe-select-p1", APIType: "codex", BaseURL: upstream.URL}},
	}

	store := newMockStore()
	store.providers = []model.Provider{*provider}

	mockSel := &mockSelector{
		selectWithMetadataFunc: func(_ context.Context, req *model.SelectRequest) (*selectResult, error) {
			if req.Model != "client-model" {
				t.Fatalf("initial selection model = %q, want %q", req.Model, "client-model")
			}
			return &selectResult{Provider: provider, FromStickyCache: false}, nil
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
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	if err := conn.Write(ctx, websocket.MessageText, []byte(prompt)); err != nil {
		t.Fatalf("write client message: %v", err)
	}

	msgType, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read semantic event: %v", err)
	}
	if msgType != websocket.MessageText || !strings.Contains(string(data), `"response.created"`) {
		t.Fatalf("unexpected websocket payload: type=%v body=%q", msgType, string(data))
	}
}

func TestHandler_ServeHTTP_WebSocket_ProbeDisabledKeepsHandshakeOnlySelection(t *testing.T) {
	t.Parallel()

	const prompt = `{"type":"response.create","response":{"model":"client-model","instructions":"hello"}}`

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept upstream websocket: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		msgType, payload, err := conn.Read(r.Context())
		if err != nil {
			t.Errorf("read client prompt: %v", err)
			return
		}
		if msgType != websocket.MessageText || string(payload) != prompt {
			t.Errorf("prompt = (%v, %q), want text/%q", msgType, string(payload), prompt)
			return
		}
		if err := conn.Write(r.Context(), websocket.MessageText, []byte(`{"type":"response.created","response":{"model":"client-model"}}`)); err != nil {
			t.Errorf("write upstream semantic event: %v", err)
		}
	}))
	defer upstream.Close()

	provider := &model.Provider{
		ID:       "ws-probe-disabled-p1",
		Name:     "WS Probe Disabled Provider",
		APIKey:   "ws-key",
		AuthMode: "bearer",
		Enabled:  true,
		APITypes: []model.ProviderAPIType{{ProviderID: "ws-probe-disabled-p1", APIType: "codex", BaseURL: upstream.URL}},
	}

	store := newMockStore()
	store.providers = []model.Provider{*provider}
	store.configs[ConfigKeyWebSocketProbeClientModel] = "false"

	mockSel := &mockSelector{
		selectWithMetadataFunc: func(_ context.Context, req *model.SelectRequest) (*selectResult, error) {
			if req.Model != ModelUnknown {
				t.Fatalf("initial selection model = %q, want %q when probing is disabled", req.Model, ModelUnknown)
			}
			return &selectResult{Provider: provider, FromStickyCache: false}, nil
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
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	if err := conn.Write(ctx, websocket.MessageText, []byte(prompt)); err != nil {
		t.Fatalf("write client message: %v", err)
	}

	msgType, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read semantic event: %v", err)
	}
	if msgType != websocket.MessageText || !strings.Contains(string(data), `"response.created"`) {
		t.Fatalf("unexpected websocket payload: type=%v body=%q", msgType, string(data))
	}
}

func TestHandler_ServeHTTP_WebSocket_HandshakeModelWinsOverProbe(t *testing.T) {
	t.Parallel()

	const prompt = `{"type":"response.create","response":{"model":"client-model","instructions":"hello"}}`

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept upstream websocket: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		msgType, payload, err := conn.Read(r.Context())
		if err != nil {
			t.Errorf("read client prompt: %v", err)
			return
		}
		if msgType != websocket.MessageText || string(payload) != prompt {
			t.Errorf("prompt = (%v, %q), want text/%q", msgType, string(payload), prompt)
			return
		}
		if err := conn.Write(r.Context(), websocket.MessageText, []byte(`{"type":"response.created","response":{"model":"handshake-model"}}`)); err != nil {
			t.Errorf("write upstream semantic event: %v", err)
		}
	}))
	defer upstream.Close()

	provider := &model.Provider{
		ID:       "ws-probe-handshake-p1",
		Name:     "WS Probe Handshake Provider",
		APIKey:   "ws-key",
		AuthMode: "bearer",
		Enabled:  true,
		APITypes: []model.ProviderAPIType{{ProviderID: "ws-probe-handshake-p1", APIType: "codex", BaseURL: upstream.URL}},
	}

	store := newMockStore()
	store.providers = []model.Provider{*provider}

	mockSel := &mockSelector{
		selectWithMetadataFunc: func(_ context.Context, req *model.SelectRequest) (*selectResult, error) {
			if req.Model != "handshake-model" {
				t.Fatalf("initial selection model = %q, want %q from handshake", req.Model, "handshake-model")
			}
			return &selectResult{Provider: provider, FromStickyCache: false}, nil
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

	conn, _, err := websocket.Dial(ctx, wsURL(proxyServer)+"/responses?model=handshake-model", nil)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	if err := conn.Write(ctx, websocket.MessageText, []byte(prompt)); err != nil {
		t.Fatalf("write client message: %v", err)
	}

	msgType, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read semantic event: %v", err)
	}
	if msgType != websocket.MessageText || !strings.Contains(string(data), `"response.created"`) {
		t.Fatalf("unexpected websocket payload: type=%v body=%q", msgType, string(data))
	}
}

func TestHandler_ServeHTTP_WebSocket_PreVisibleConfigFailureAfterProbeSwitchesProvider(t *testing.T) {
	t.Parallel()

	const prompt = `{"type":"response.create","response":{"model":"client-model","instructions":"hello"}}`

	replayedToFallback := make(chan webSocketReplayMessage, 1)
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept fallback websocket: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		messageType, data, err := conn.Read(r.Context())
		if err != nil {
			t.Errorf("read replayed client prompt: %v", err)
			return
		}
		replayedToFallback <- webSocketReplayMessage{
			MessageType: messageType,
			Data:        append([]byte(nil), data...),
		}
		if err := conn.Write(r.Context(), websocket.MessageText, []byte(`{"type":"response.created","provider":"fallback"}`)); err != nil {
			t.Errorf("write fallback response: %v", err)
		}
	}))
	defer fallback.Close()

	primaryProvider := &model.Provider{
		ID:       "ws-probe-config-primary",
		Name:     "WS Probe Config Primary",
		APIKey:   "primary-key",
		AuthMode: "bearer",
		Enabled:  true,
		APITypes: []model.ProviderAPIType{{ProviderID: "ws-probe-config-primary", APIType: "codex", BaseURL: ""}},
	}
	fallbackProvider := &model.Provider{
		ID:       "ws-probe-config-fallback",
		Name:     "WS Probe Config Fallback",
		APIKey:   "fallback-key",
		AuthMode: "bearer",
		Enabled:  true,
		APITypes: []model.ProviderAPIType{{ProviderID: "ws-probe-config-fallback", APIType: "codex", BaseURL: fallback.URL}},
	}

	store := newMockStore()
	store.providers = []model.Provider{*primaryProvider, *fallbackProvider}

	var retrySelections int32
	mockSel := &mockSelector{
		selectWithMetadataFunc: func(_ context.Context, req *model.SelectRequest) (*selectResult, error) {
			if req.Model != "client-model" {
				t.Fatalf("initial selection model = %q, want %q", req.Model, "client-model")
			}
			return &selectResult{Provider: primaryProvider, FromStickyCache: false}, nil
		},
		selectExcludingFunc: func(_ context.Context, req *model.SelectRequest, excludeIDs map[string]bool) (*model.Provider, error) {
			atomic.AddInt32(&retrySelections, 1)
			if req.Model != "client-model" {
				t.Fatalf("retry selection model = %q, want %q", req.Model, "client-model")
			}
			if !excludeIDs[primaryProvider.ID] {
				t.Fatalf("excludeIDs = %v, want %q excluded", excludeIDs, primaryProvider.ID)
			}
			return fallbackProvider, nil
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
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	if err := conn.Write(ctx, websocket.MessageText, []byte(prompt)); err != nil {
		t.Fatalf("write client message: %v", err)
	}

	msgType, payload, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read proxied websocket event: %v", err)
	}
	if msgType != websocket.MessageText || string(payload) != `{"type":"response.created","provider":"fallback"}` {
		t.Fatalf("payload = (%v, %q), want fallback response.created event", msgType, string(payload))
	}

	select {
	case replayed := <-replayedToFallback:
		if replayed.MessageType != websocket.MessageText || string(replayed.Data) != prompt {
			t.Fatalf("replayed prompt = (%v, %q), want text/%q", replayed.MessageType, string(replayed.Data), prompt)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for replayed prompt")
	}

	if got := atomic.LoadInt32(&retrySelections); got != 1 {
		t.Fatalf("retry selections = %d, want 1", got)
	}
}

func TestHandler_ServeHTTP_WebSocket_ProbeFailureReturnsGatewayErrorEvent(t *testing.T) {
	t.Parallel()

	const prompt = `{"type":"response.create","response":{"model":"client-model","instructions":"hello"}}`

	provider := &model.Provider{
		ID:       "ws-probe-terminal-error",
		Name:     "WS Probe Terminal Error",
		APIKey:   "error-key",
		AuthMode: "bearer",
		Enabled:  true,
		APITypes: []model.ProviderAPIType{{ProviderID: "ws-probe-terminal-error", APIType: "codex", BaseURL: ""}},
	}

	store := newMockStore()
	store.providers = []model.Provider{*provider}

	mockSel := &mockSelector{
		selectWithMetadataFunc: func(_ context.Context, req *model.SelectRequest) (*selectResult, error) {
			if req.Model != "client-model" {
				t.Fatalf("selection model = %q, want %q", req.Model, "client-model")
			}
			return &selectResult{Provider: provider, FromStickyCache: false}, nil
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
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	if err := conn.Write(ctx, websocket.MessageText, []byte(prompt)); err != nil {
		t.Fatalf("write client message: %v", err)
	}

	msgType, payload, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read terminal gateway event: %v", err)
	}
	body := string(payload)
	if msgType != websocket.MessageText {
		t.Fatalf("message type = %v, want %v", msgType, websocket.MessageText)
	}
	if !strings.Contains(body, `"type":"error"`) {
		t.Fatalf("payload = %q, want protocol error event", body)
	}
	if !strings.Contains(body, ErrCodeWebSocketUpgrade) {
		t.Fatalf("payload = %q, want error code %q", body, ErrCodeWebSocketUpgrade)
	}

	if _, _, err := conn.Read(ctx); err == nil || (!errors.Is(err, io.EOF) && !isNormalClose(err)) {
		t.Fatalf("expected websocket close after terminal gateway error, got %v", err)
	}
}

func TestHandler_ServeHTTP_WebSocket_SemanticFailoverSwitchesProviderBeforeClientVisible(t *testing.T) {
	var (
		primaryAttempts  int32
		fallbackAccepts  int32
		selectRetryCalls int32
	)

	semanticPayload := []byte(`{"type":"error","status":403,"error":{"type":"auth_error","code":"model_not_allowed","message":"model access denied"}}`)
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&primaryAttempts, 1)
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept primary websocket: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		conn.SetReadLimit(wsReadLimit)

		if _, _, err := conn.Read(r.Context()); err != nil {
			return
		}
		if err := conn.Write(r.Context(), websocket.MessageText, semanticPayload); err != nil {
			t.Errorf("write primary semantic payload: %v", err)
		}
	}))
	defer primary.Close()

	replayedToFallback := make(chan webSocketReplayMessage, 1)
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&fallbackAccepts, 1)
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept fallback websocket: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		conn.SetReadLimit(wsReadLimit)

		messageType, data, err := conn.Read(r.Context())
		if err != nil {
			t.Errorf("read replayed client message: %v", err)
			return
		}
		replayedToFallback <- webSocketReplayMessage{
			MessageType: messageType,
			Data:        append([]byte(nil), data...),
		}
		if err := conn.Write(r.Context(), websocket.MessageText, []byte(`{"type":"response.created","provider":"fallback"}`)); err != nil {
			t.Errorf("write fallback response: %v", err)
		}
	}))
	defer fallback.Close()

	primaryProvider := &model.Provider{
		ID:       "ws-semantic-primary",
		Name:     "WS Semantic Primary",
		APIKey:   "primary-key",
		AuthMode: "bearer",
		Enabled:  true,
		APITypes: []model.ProviderAPIType{{ProviderID: "ws-semantic-primary", APIType: "codex", BaseURL: primary.URL}},
	}
	fallbackProvider := &model.Provider{
		ID:       "ws-semantic-fallback",
		Name:     "WS Semantic Fallback",
		APIKey:   "fallback-key",
		AuthMode: "bearer",
		Enabled:  true,
		APITypes: []model.ProviderAPIType{{ProviderID: "ws-semantic-fallback", APIType: "codex", BaseURL: fallback.URL}},
	}

	store := newMockStore()
	store.providers = []model.Provider{*primaryProvider, *fallbackProvider}
	mockSel := &mockSelector{
		selectWithMetadataFunc: func(_ context.Context, req *model.SelectRequest) (*selectResult, error) {
			if req.FailoverContext != nil {
				t.Fatalf("initial selection should not have failover context, got %+v", req.FailoverContext)
			}
			return &selectResult{Provider: primaryProvider, FromStickyCache: false}, nil
		},
		selectExcludingFunc: func(_ context.Context, req *model.SelectRequest, excludeIDs map[string]bool) (*model.Provider, error) {
			atomic.AddInt32(&selectRetryCalls, 1)
			if !excludeIDs[primaryProvider.ID] {
				t.Fatalf("excludeIDs = %v, want %q excluded", excludeIDs, primaryProvider.ID)
			}
			if req.FailoverContext == nil || req.FailoverContext.OriginProviderID != primaryProvider.ID {
				t.Fatalf("unexpected failover context: %+v", req.FailoverContext)
			}
			return fallbackProvider, nil
		},
	}

	handler := NewHandler(Config{
		Store:    store,
		Selector: mockSel,
		Logger:   zap.NewNop(),
	})

	proxyServer := httptest.NewServer(handler)
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(proxyServer)+"/responses", nil)
	if err != nil {
		t.Fatalf("dial websocket through proxy: %v", err)
	}

	const prompt = `{"type":"response.create","response":{"instructions":"hello"}}`
	if err := conn.Write(ctx, websocket.MessageText, []byte(prompt)); err != nil {
		t.Fatalf("write client message: %v", err)
	}

	msgType, payload, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read proxied websocket event: %v", err)
	}
	if msgType != websocket.MessageText {
		t.Fatalf("message type = %v, want %v", msgType, websocket.MessageText)
	}
	if string(payload) != `{"type":"response.created","provider":"fallback"}` {
		t.Fatalf("payload = %q, want fallback response.created event", string(payload))
	}
	_ = conn.Close(websocket.StatusNormalClosure, "done")

	select {
	case replayed := <-replayedToFallback:
		if replayed.MessageType != websocket.MessageText {
			t.Fatalf("replayed message type = %v, want %v", replayed.MessageType, websocket.MessageText)
		}
		if string(replayed.Data) != prompt {
			t.Fatalf("replayed payload = %q, want %q", string(replayed.Data), prompt)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for replayed payload")
	}

	waitFor(t, func() bool { return store.LogsLen() > 0 }, testPollTimeout)
	waitFor(t, func() bool { return store.AttemptsLen() >= 2 }, testPollTimeout)

	if got := atomic.LoadInt32(&primaryAttempts); got != 1 {
		t.Fatalf("primary attempts = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&fallbackAccepts); got != 1 {
		t.Fatalf("fallback accepts = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&selectRetryCalls); got != 1 {
		t.Fatalf("retry selections = %d, want 1", got)
	}

	log := store.LastLog()
	if log == nil {
		t.Fatal("expected log entry")
	}
	if log.ProviderID != fallbackProvider.ID {
		t.Fatalf("ProviderID = %q, want %q", log.ProviderID, fallbackProvider.ID)
	}
	if log.RetryCount != 1 {
		t.Fatalf("RetryCount = %d, want 1", log.RetryCount)
	}

	attempts := store.LastAttempts(2)
	if len(attempts) != 2 {
		t.Fatalf("expected 2 attempts, got %d", len(attempts))
	}
	if attempts[0].ProviderID != primaryProvider.ID || attempts[1].ProviderID != fallbackProvider.ID {
		t.Fatalf(
			"attempt provider order = [%s %s], want [%s %s]",
			attempts[0].ProviderID,
			attempts[1].ProviderID,
			primaryProvider.ID,
			fallbackProvider.ID,
		)
	}
	if attempts[0].Phase == nil || *attempts[0].Phase != model.RequestAttemptPhasePostUpgradePreVisible {
		t.Fatalf("first attempt phase = %#v, want %q", attempts[0].Phase, model.RequestAttemptPhasePostUpgradePreVisible)
	}
	if attempts[0].Outcome == nil || *attempts[0].Outcome != model.RequestAttemptOutcomeUpstreamSemanticError {
		t.Fatalf("first attempt outcome = %#v, want %q", attempts[0].Outcome, model.RequestAttemptOutcomeUpstreamSemanticError)
	}
	if attempts[0].ResultVisibleToClient == nil || *attempts[0].ResultVisibleToClient {
		t.Fatalf("first attempt visibility = %#v, want false", attempts[0].ResultVisibleToClient)
	}
	if attempts[0].SwitchReason != model.RequestAttemptSwitchReasonProviderScopedSemanticError {
		t.Fatalf("first attempt switch reason = %q, want %q", attempts[0].SwitchReason, model.RequestAttemptSwitchReasonProviderScopedSemanticError)
	}
	if attempts[1].Phase == nil || *attempts[1].Phase != model.RequestAttemptPhaseVisible {
		t.Fatalf("second attempt phase = %#v, want %q", attempts[1].Phase, model.RequestAttemptPhaseVisible)
	}
	if attempts[1].Outcome == nil || *attempts[1].Outcome != model.RequestAttemptOutcomeVisibleSession {
		t.Fatalf("second attempt outcome = %#v, want %q", attempts[1].Outcome, model.RequestAttemptOutcomeVisibleSession)
	}
	if attempts[1].ResultVisibleToClient == nil || !*attempts[1].ResultVisibleToClient {
		t.Fatalf("second attempt visibility = %#v, want true", attempts[1].ResultVisibleToClient)
	}
}

func TestHandler_ServeHTTP_WebSocket_SemanticFailoverFallsBackToOriginalPayloadWhenReplacementFails(t *testing.T) {
	var (
		primaryAttempts  int32
		selectRetryCalls int32
	)

	semanticPayload := []byte(`{"type":"error","status":403,"error":{"type":"auth_error","code":"model_not_allowed","message":"model access denied"}}`)
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&primaryAttempts, 1)
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept primary websocket: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		conn.SetReadLimit(wsReadLimit)

		if _, _, err := conn.Read(r.Context()); err != nil {
			return
		}
		if err := conn.Write(r.Context(), websocket.MessageText, semanticPayload); err != nil {
			t.Errorf("write primary semantic payload: %v", err)
		}
	}))
	defer primary.Close()

	primaryProvider := &model.Provider{
		ID:       "ws-semantic-origin",
		Name:     "WS Semantic Origin",
		APIKey:   "origin-key",
		AuthMode: "bearer",
		Enabled:  true,
		APITypes: []model.ProviderAPIType{{ProviderID: "ws-semantic-origin", APIType: "codex", BaseURL: primary.URL}},
	}
	fallbackProvider := &model.Provider{
		ID:       "ws-semantic-broken",
		Name:     "WS Semantic Broken",
		APIKey:   "broken-key",
		AuthMode: "bearer",
		Enabled:  true,
		APITypes: []model.ProviderAPIType{{ProviderID: "ws-semantic-broken", APIType: "codex", BaseURL: "https://ws-semantic-broken.invalid"}},
	}

	store := newMockStore()
	store.providers = []model.Provider{*primaryProvider, *fallbackProvider}
	mockSel := &mockSelector{
		selectWithMetadataFunc: func(_ context.Context, _ *model.SelectRequest) (*selectResult, error) {
			return &selectResult{Provider: primaryProvider, FromStickyCache: false}, nil
		},
		selectExcludingFunc: func(_ context.Context, req *model.SelectRequest, excludeIDs map[string]bool) (*model.Provider, error) {
			atomic.AddInt32(&selectRetryCalls, 1)
			if !excludeIDs[primaryProvider.ID] {
				t.Fatalf("excludeIDs = %v, want %q excluded", excludeIDs, primaryProvider.ID)
			}
			if req.FailoverContext == nil || req.FailoverContext.OriginProviderID != primaryProvider.ID {
				t.Fatalf("unexpected failover context: %+v", req.FailoverContext)
			}
			return fallbackProvider, nil
		},
	}

	handler := NewHandler(Config{
		Store:    store,
		Selector: mockSel,
		Logger:   zap.NewNop(),
	})
	handler.wsForwarder = NewWebSocketForwarder(WebSocketForwarderConfig{
		Logger: zap.NewNop(),
		Dialer: &mockDialer{
			dialFunc: func(ctx context.Context, url string, opts *websocket.DialOptions) (*websocket.Conn, *http.Response, error) {
				if strings.Contains(url, "ws-semantic-broken.invalid") {
					return nil, nil, errors.New("dial tcp 127.0.0.1:443: connectex: connection refused")
				}
				return websocket.Dial(ctx, url, opts)
			},
		},
	})

	proxyServer := httptest.NewServer(handler)
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(proxyServer)+"/responses", nil)
	if err != nil {
		t.Fatalf("dial websocket through proxy: %v", err)
	}

	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"response.create","response":{"instructions":"hello"}}`)); err != nil {
		t.Fatalf("write client message: %v", err)
	}

	msgType, payload, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read proxied websocket event: %v", err)
	}
	if msgType != websocket.MessageText {
		t.Fatalf("message type = %v, want %v", msgType, websocket.MessageText)
	}
	if string(payload) != string(semanticPayload) {
		t.Fatalf("payload = %q, want original semantic payload", string(payload))
	}
	_ = conn.Close(websocket.StatusNormalClosure, "done")

	waitFor(t, func() bool { return store.LogsLen() > 0 }, testPollTimeout)
	waitFor(t, func() bool { return store.AttemptsLen() >= 2 }, testPollTimeout)

	if got := atomic.LoadInt32(&primaryAttempts); got != 1 {
		t.Fatalf("primary attempts = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&selectRetryCalls); got != 2 {
		t.Fatalf("retry selections = %d, want 2 (replacement attempt plus exhaustion check)", got)
	}

	log := store.LastLog()
	if log == nil {
		t.Fatal("expected log entry")
	}
	if log.ProviderID != primaryProvider.ID {
		t.Fatalf("ProviderID = %q, want %q", log.ProviderID, primaryProvider.ID)
	}
	if log.RetryCount != 1 {
		t.Fatalf("RetryCount = %d, want 1", log.RetryCount)
	}
	if log.StatusCode != http.StatusForbidden {
		t.Fatalf("StatusCode = %d, want %d", log.StatusCode, http.StatusForbidden)
	}
	if log.ErrorMsg != string(semanticPayload) {
		t.Fatalf("ErrorMsg = %q, want original semantic payload", log.ErrorMsg)
	}

	attempts := store.LastAttempts(2)
	if len(attempts) != 2 {
		t.Fatalf("expected 2 attempts, got %d", len(attempts))
	}
	if attempts[0].ProviderID != primaryProvider.ID || attempts[1].ProviderID != fallbackProvider.ID {
		t.Fatalf(
			"attempt provider order = [%s %s], want [%s %s]",
			attempts[0].ProviderID,
			attempts[1].ProviderID,
			primaryProvider.ID,
			fallbackProvider.ID,
		)
	}
	if attempts[0].Phase == nil || *attempts[0].Phase != model.RequestAttemptPhasePostUpgradePreVisible {
		t.Fatalf("first attempt phase = %#v, want %q", attempts[0].Phase, model.RequestAttemptPhasePostUpgradePreVisible)
	}
	if attempts[0].Outcome == nil || *attempts[0].Outcome != model.RequestAttemptOutcomeUpstreamSemanticError {
		t.Fatalf("first attempt outcome = %#v, want %q", attempts[0].Outcome, model.RequestAttemptOutcomeUpstreamSemanticError)
	}
	if attempts[0].ResultVisibleToClient == nil || *attempts[0].ResultVisibleToClient {
		t.Fatalf("first attempt visibility = %#v, want false", attempts[0].ResultVisibleToClient)
	}
	if attempts[0].SwitchReason != model.RequestAttemptSwitchReasonProviderScopedSemanticError {
		t.Fatalf("first attempt switch reason = %q, want %q", attempts[0].SwitchReason, model.RequestAttemptSwitchReasonProviderScopedSemanticError)
	}
	if attempts[1].Phase == nil || *attempts[1].Phase != model.RequestAttemptPhasePostUpgradePreVisible {
		t.Fatalf("second attempt phase = %#v, want %q", attempts[1].Phase, model.RequestAttemptPhasePostUpgradePreVisible)
	}
	if attempts[1].Outcome == nil || *attempts[1].Outcome != model.RequestAttemptOutcomeUpstreamTransportError {
		t.Fatalf("second attempt outcome = %#v, want %q", attempts[1].Outcome, model.RequestAttemptOutcomeUpstreamTransportError)
	}
	if attempts[1].ResultVisibleToClient == nil || *attempts[1].ResultVisibleToClient {
		t.Fatalf("second attempt visibility = %#v, want false", attempts[1].ResultVisibleToClient)
	}
	if attempts[1].SwitchReason != string(model.TerminalUpstreamTransportError) {
		t.Fatalf("second attempt switch reason = %q, want %q", attempts[1].SwitchReason, model.TerminalUpstreamTransportError)
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

func TestHandler_ServeHTTP_WebSocket_CloseNowStillLogsSuccess(t *testing.T) {
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
	if err := conn.Write(ctx, websocket.MessageText, []byte("ping")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, _, err := conn.Read(ctx); err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if err := conn.CloseNow(); err != nil {
		t.Fatalf("CloseNow: %v", err)
	}

	waitFor(t, func() bool { return store.LogsLen() > 0 }, testPollTimeout)

	log := store.LastLog()
	if log == nil {
		t.Fatal("expected log entry")
	}
	if log.ErrorMsg != "" {
		t.Errorf("expected empty ErrorMsg for CloseNow teardown, got %q", log.ErrorMsg)
	}
	if !log.Success {
		t.Errorf("expected log.Success=true, got false (status=%d)", log.StatusCode)
	}
	if log.StatusCode != http.StatusSwitchingProtocols {
		t.Errorf("StatusCode = %d, want %d", log.StatusCode, http.StatusSwitchingProtocols)
	}
}
