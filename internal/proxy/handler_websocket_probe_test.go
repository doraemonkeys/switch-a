package proxy

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/selector"

	"github.com/coder/websocket"
	"go.uber.org/zap"
)

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
	store.configs[ConfigKeyStickyMode] = string(model.StickyModeModel)

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
	defer conn.CloseNow()

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
	// Sticky continuity is written when the relay session terminates. This fixture
	// does not read the proxy's close frame, so use an immediate client teardown
	// rather than waiting for a reciprocal close handshake.
	_ = conn.CloseNow()

	waitFor(t, func() bool { return mockSel.StickyUpdatesLen() == 1 }, testPollTimeout)

	update, ok := mockSel.LastStickyUpdate()
	if !ok {
		t.Fatal("expected sticky update after committed websocket session")
	}
	if update.Model != "client-model" {
		t.Fatalf("sticky update model = %q, want %q", update.Model, "client-model")
	}
}

func TestHandler_ServeHTTP_WebSocket_ContinuitySeedLookupWaitsForProbeResolvedModel(t *testing.T) {
	t.Parallel()

	const prompt = `{"type":"response.create","response":{"model":"client-model","instructions":"hello"}}`

	seedStore := NewVisibleContinuitySeedStore()
	seedKey := selector.BuildContinuityKey(&model.SelectRequest{
		ClientIP:   "198.51.100.44",
		User:       "seed-user",
		APIType:    APITypeCodex,
		Model:      "client-model",
		StickyMode: model.StickyModeModel,
	})
	seedStore.Store(model.VisibleContinuitySeed{
		SeedID:           "ws-probe-seed-1",
		ContinuityKey:    seedKey,
		OriginProviderID: "ws-probe-seed-provider",
		OriginVendor:     "vendor-a",
		ObservedAt:       time.Now().Add(-1200 * time.Millisecond),
	})

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept upstream websocket: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		if _, _, err := conn.Read(r.Context()); err != nil {
			t.Errorf("read client prompt: %v", err)
			return
		}
		if err := conn.Write(r.Context(), websocket.MessageText, []byte(`{"type":"response.created","response":{"id":"resp_probe"}}`)); err != nil {
			t.Errorf("write response.created: %v", err)
		}
	}))
	defer upstream.Close()

	store := newMockStore()
	store.configs[ConfigKeyStickyMode] = string(model.StickyModeModel)
	store.configs[ConfigKeyTrustProxyHeaders] = "true"
	provider := &model.Provider{
		ID:       "ws-probe-seed-provider",
		Name:     "WS Probe Seed Provider",
		APIKey:   "ws-key",
		AuthMode: "bearer",
		Enabled:  true,
		APITypes: []model.ProviderAPIType{{ProviderID: "ws-probe-seed-provider", APIType: APITypeCodex, BaseURL: upstream.URL}},
	}
	store.providers = []model.Provider{*provider}

	mockSel := &mockSelector{
		selectWithMetadataFunc: func(_ context.Context, req *model.SelectRequest) (*selectResult, error) {
			if req.Model != "client-model" {
				t.Fatalf("selection model = %q, want %q after probe resolution", req.Model, "client-model")
			}
			if req.VisibleContinuitySeedCandidate == nil {
				t.Fatal("expected continuity seed candidate after probe resolved the model")
			}
			if req.VisibleContinuitySeedCandidate.SeedID != "ws-probe-seed-1" {
				t.Fatalf("candidate SeedID = %q, want %q", req.VisibleContinuitySeedCandidate.SeedID, "ws-probe-seed-1")
			}
			if req.VisibleContinuitySeedCandidate.OriginProviderID != provider.ID {
				t.Fatalf("candidate origin = %q, want %q", req.VisibleContinuitySeedCandidate.OriginProviderID, provider.ID)
			}
			return &selectResult{Provider: provider, FromStickyCache: false}, nil
		},
	}

	handler := NewHandler(Config{
		Store:                      store,
		Selector:                   mockSel,
		VisibleContinuitySeedStore: seedStore,
		Logger:                     zap.NewNop(),
	})
	proxyServer := httptest.NewServer(handler)
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	headers := http.Header{}
	headers.Set("X-User-ID", "seed-user")
	headers.Set("X-Forwarded-For", "198.51.100.44")
	conn, _, err := websocket.Dial(ctx, wsURL(proxyServer)+"/responses", &websocket.DialOptions{
		HTTPHeader: headers,
	})
	if err != nil {
		t.Fatalf("dial websocket through proxy: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	if err := conn.Write(ctx, websocket.MessageText, []byte(prompt)); err != nil {
		t.Fatalf("write client prompt: %v", err)
	}
	if _, _, err := conn.Read(ctx); err != nil {
		t.Fatalf("read proxied response: %v", err)
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
	store.routingPolicies = []model.RoutingPolicy{{
		Enabled:         true,
		APIType:         APITypeCodex,
		ModelMatchType:  model.RoutingPolicyModelMatchTypePrefix,
		ModelMatchValue: "client-",
	}}

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

func TestHandler_ServeHTTP_WebSocket_RoutingPolicyDemandUsesClientModel(t *testing.T) {
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
		ID:       "ws-routing-policy-lookup-p1",
		Name:     "WS Routing Policy Lookup Provider",
		APIKey:   "ws-key",
		AuthMode: "bearer",
		Enabled:  true,
		APITypes: []model.ProviderAPIType{{ProviderID: "ws-routing-policy-lookup-p1", APIType: "codex", BaseURL: upstream.URL}},
	}

	store := newMockStore()
	store.providers = []model.Provider{*provider}
	store.configs[ConfigKeyStickyMode] = string(model.StickyModeOff)
	store.routingPolicies = []model.RoutingPolicy{
		{
			Enabled:         true,
			APIType:         "codex",
			ModelMatchType:  model.RoutingPolicyModelMatchTypePrefix,
			ModelMatchValue: "client-",
		},
	}

	mockSel := &mockSelector{
		selectWithMetadataFunc: func(_ context.Context, req *model.SelectRequest) (*selectResult, error) {
			if req.Model != "client-model" {
				t.Fatalf("initial selection model = %q, want %q when routing policy triggers probing", req.Model, "client-model")
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

	var retrySelections atomic.Int32
	mockSel := &mockSelector{
		selectWithMetadataFunc: func(_ context.Context, req *model.SelectRequest) (*selectResult, error) {
			if req.Model != "client-model" {
				t.Fatalf("initial selection model = %q, want %q", req.Model, "client-model")
			}
			return &selectResult{Provider: primaryProvider, FromStickyCache: false}, nil
		},
		selectExcludingFunc: func(_ context.Context, req *model.SelectRequest, excludeIDs map[string]bool) (*model.Provider, error) {
			retrySelections.Add(1)
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

	if got := retrySelections.Load(); got != 1 {
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
