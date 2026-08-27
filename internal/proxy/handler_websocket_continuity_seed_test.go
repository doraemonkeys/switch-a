package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/selector"

	"github.com/coder/websocket"
	"go.uber.org/zap"
)

const postVisibleFailoverObservationTimeout = 10 * testPollTimeout

func TestHandler_ServeHTTP_WebSocket_PostVisibleFailureKeepsRouteTargetFixed(t *testing.T) {
	t.Parallel()

	var (
		retrySelections atomic.Int32
		fallbackAccepts atomic.Int32
	)

	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept primary websocket: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		conn.SetReadLimit(wsReadLimit)

		if _, _, err := conn.Read(r.Context()); err != nil {
			t.Errorf("read initial client message: %v", err)
			return
		}
		if err := conn.Write(r.Context(), websocket.MessageText, []byte(`{"type":"response.created","response":{"id":"origin-visible"}}`)); err != nil {
			t.Errorf("write visible origin event: %v", err)
			return
		}
		if err := conn.Write(r.Context(), websocket.MessageText, []byte(`{"type":"error","status":401,"error":{"type":"invalid_api_key","code":"invalid_api_key","message":"credential expired"}}`)); err != nil {
			t.Errorf("write suppressed provider-scoped error: %v", err)
		}
	}))
	defer primary.Close()

	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackAccepts.Add(1)
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept fallback websocket: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		conn.SetReadLimit(wsReadLimit)
		if err := conn.Write(r.Context(), websocket.MessageText, []byte(`{"type":"response.created","response":{"id":"fallback-visible"}}`)); err != nil {
			t.Errorf("write fallback visible event: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}))
	defer fallback.Close()

	primaryProvider := withTestStaticCredential(&model.Provider{
		ID:   "ws-visible-origin",
		Name: "WS Visible Origin",

		AuthMode:       "bearer",
		Enabled:        true,
		Vendor:         "vendor-a",
		FailoverScope:  model.ScopeVendor,
		AcceptFailover: model.ScopeAny,
		APITypes:       []model.ProviderAPIType{{ProviderID: "ws-visible-origin", APIType: "codex", BaseURL: primary.URL}},
	}, "", "origin-key")
	fallbackProvider := withTestStaticCredential(&model.Provider{
		ID:   "ws-visible-fallback",
		Name: "WS Visible Fallback",

		AuthMode:       "bearer",
		Enabled:        true,
		Vendor:         "vendor-a",
		FailoverScope:  model.ScopeAny,
		AcceptFailover: model.ScopeVendor,
		APITypes:       []model.ProviderAPIType{{ProviderID: "ws-visible-fallback", APIType: "codex", BaseURL: fallback.URL}},
	}, "", "fallback-key")

	store := newMockStore()
	store.providers = []model.Provider{*primaryProvider, *fallbackProvider}
	mockSel := &mockSelector{
		selectWithMetadataFunc: func(_ context.Context, req *model.SelectRequest) (*selectResult, error) {
			if req.SwitchMode != model.SwitchModeInitial {
				t.Fatalf("initial selection SwitchMode = %q, want %q", req.SwitchMode, model.SwitchModeInitial)
			}
			return &selectResult{Provider: primaryProvider, FromStickyCache: false}, nil
		},
		selectExcludingFunc: func(_ context.Context, req *model.SelectRequest, excludeIDs map[string]bool) (*model.Provider, error) {
			retrySelections.Add(1)
			if !excludeIDs[primaryProvider.ID] {
				t.Fatalf("excludeIDs = %+v, want %q excluded", excludeIDs, primaryProvider.ID)
			}
			if req.SwitchMode != model.SwitchModeFailover {
				t.Fatalf("post-visible failover SwitchMode = %q, want %q", req.SwitchMode, model.SwitchModeFailover)
			}
			if req.ProviderContinuityContext == nil {
				t.Fatal("expected request-local continuity context after visible origin")
			}
			if req.ProviderContinuityContext.VisibleOriginProviderID != primaryProvider.ID {
				t.Fatalf("VisibleOriginProviderID = %q, want %q", req.ProviderContinuityContext.VisibleOriginProviderID, primaryProvider.ID)
			}
			if req.FailoverContext != nil {
				t.Fatalf("explicit failover state should not rely on legacy FailoverContext, got %+v", req.FailoverContext)
			}
			return fallbackProvider, nil
		},
	}

	handler := newProxyCodexTestHandler(t, Config{
		Store:    store,
		Selector: mockSel,
		Logger:   zap.NewNop(),
	})

	proxyServer := httptest.NewServer(handler)
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(proxyServer)+"/responses", proxyCodexDialOptions())
	if err != nil {
		t.Fatalf("dial websocket through proxy: %v", err)
	}
	defer conn.CloseNow()

	if err := conn.Write(ctx, websocket.MessageText, []byte("first")); err != nil {
		t.Fatalf("write initial client message: %v", err)
	}
	msgType, payload, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read visible origin message: %v", err)
	}
	if msgType != websocket.MessageText || string(payload) != `{"type":"response.created","response":{"id":"origin-visible"}}` {
		t.Fatalf("origin payload = (%v, %q), want origin visible response", msgType, string(payload))
	}
	if _, _, err := conn.Read(ctx); err == nil {
		t.Fatal("post-visible provider failure must terminate the fixed-route session")
	}
	// Persistence is intentionally asynchronous after the WebSocket session ends.
	// Explicit teardown makes that lifecycle edge deterministic instead of waiting
	// for a close handshake while this test is no longer reading control frames.
	_ = conn.CloseNow()

	if got := retrySelections.Load(); got != 0 {
		t.Fatalf("retry selections = %d, want 0 after client-visible data", got)
	}
	if got := fallbackAccepts.Load(); got != 0 {
		t.Fatalf("fallback accepts = %d, want 0 after client-visible data", got)
	}
	waitFor(t, func() bool { return store.AttemptsLen() >= 1 }, postVisibleFailoverObservationTimeout)
	attempts := store.LastAttempts(1)
	if len(attempts) != 1 {
		t.Fatalf("attempts = %d, want one fixed-route attempt", len(attempts))
	}
	if attempts[0].SwitchMode != model.RequestAttemptSwitchModeInitial {
		t.Fatalf("first attempt SwitchMode = %q, want %q", attempts[0].SwitchMode, model.RequestAttemptSwitchModeInitial)
	}
	if attempts[0].ProviderSwitchCount != 0 {
		t.Fatalf("provider switch count = %d, want 0", attempts[0].ProviderSwitchCount)
	}
}

func TestHandler_ServeHTTP_WebSocket_PostVisibleFailureStoresContinuitySeed(t *testing.T) {
	t.Parallel()

	seedStore := NewVisibleContinuitySeedStore()

	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept primary websocket: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		conn.SetReadLimit(wsReadLimit)

		if _, _, err := conn.Read(r.Context()); err != nil {
			t.Errorf("read initial client message: %v", err)
			return
		}
		if err := conn.Write(r.Context(), websocket.MessageText, []byte(`{"type":"response.created","response":{"id":"origin-visible"}}`)); err != nil {
			t.Errorf("write visible origin event: %v", err)
			return
		}
		if err := conn.Write(r.Context(), websocket.MessageText, []byte(`{"type":"error","status":401,"error":{"type":"invalid_api_key","code":"invalid_api_key","message":"credential expired"}}`)); err != nil {
			t.Errorf("write suppressed provider-scoped error: %v", err)
		}
	}))
	defer primary.Close()

	primaryProvider := withTestStaticCredential(&model.Provider{
		ID:   "ws-visible-seed-origin",
		Name: "WS Visible Seed Origin",

		AuthMode:       "bearer",
		Enabled:        true,
		Vendor:         "vendor-a",
		FailoverScope:  model.ScopeVendor,
		AcceptFailover: model.ScopeAny,
		APITypes:       []model.ProviderAPIType{{ProviderID: "ws-visible-seed-origin", APIType: "codex", BaseURL: primary.URL}},
	}, "", "origin-key")

	store := newMockStore()
	store.providers = []model.Provider{*primaryProvider}
	store.configs[ConfigKeyStickyMode] = string(model.StickyModeModel)
	store.configs[ConfigKeyTrustProxyHeaders] = "true"
	mockSel := &mockSelector{
		selectWithMetadataFunc: func(_ context.Context, _ *model.SelectRequest) (*selectResult, error) {
			return &selectResult{Provider: primaryProvider, FromStickyCache: false}, nil
		},
		selectExcludingFunc: func(_ context.Context, req *model.SelectRequest, excludeIDs map[string]bool) (*model.Provider, error) {
			if !excludeIDs[primaryProvider.ID] {
				t.Fatalf("excludeIDs = %+v, want %q excluded", excludeIDs, primaryProvider.ID)
			}
			if req.SwitchMode != model.SwitchModeFailover {
				t.Fatalf("retry SwitchMode = %q, want %q", req.SwitchMode, model.SwitchModeFailover)
			}
			return nil, internal.ErrNoProvider
		},
	}

	handler := newProxyCodexTestHandler(t, Config{
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
	headers.Set("Authorization", proxyCodexTestAuthorization)
	headers.Set("X-User-ID", "seed-user")
	headers.Set("X-Forwarded-For", "198.51.100.77")
	conn, _, err := websocket.Dial(ctx, wsURL(proxyServer)+"/responses?model=gpt-5.4", &websocket.DialOptions{
		HTTPHeader: headers,
	})
	if err != nil {
		t.Fatalf("dial websocket through proxy: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	if err := conn.Write(ctx, websocket.MessageText, []byte("first")); err != nil {
		t.Fatalf("write initial client message: %v", err)
	}
	if _, _, err := conn.Read(ctx); err != nil {
		t.Fatalf("read visible origin message: %v", err)
	}
	_, _, _ = conn.Read(ctx)

	key := selector.BuildContinuityKey(&model.SelectRequest{
		ClientIP:   "198.51.100.77",
		User:       "seed-user",
		APIType:    APITypeCodex,
		Model:      "gpt-5.4",
		StickyMode: model.StickyModeModel,
	})
	waitFor(t, func() bool { return seedStore.Len() == 1 }, testPollTimeout)
	candidate, ok := seedStore.Lookup(key)
	if !ok {
		t.Fatal("expected visible continuity seed after post-visible failure")
	}
	if candidate.OriginProviderID != primaryProvider.ID {
		t.Fatalf("candidate origin = %q, want %q", candidate.OriginProviderID, primaryProvider.ID)
	}
}

func TestHandler_ServeHTTP_WebSocket_NormalCompletionDoesNotStoreContinuitySeed(t *testing.T) {
	t.Parallel()

	seedStore := NewVisibleContinuitySeedStore()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept websocket: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		conn.SetReadLimit(wsReadLimit)

		if _, _, err := conn.Read(r.Context()); err != nil {
			t.Errorf("read initial client message: %v", err)
			return
		}
		if err := conn.Write(r.Context(), websocket.MessageText, []byte(`{"type":"response.created","response":{"id":"normal-visible"}}`)); err != nil {
			t.Errorf("write visible event: %v", err)
		}
	}))
	defer upstream.Close()

	provider := withTestStaticCredential(model.Provider{
		ID:   "ws-normal-provider",
		Name: "WS Normal Provider",

		AuthMode: "bearer",
		Enabled:  true,
		APITypes: []model.ProviderAPIType{{ProviderID: "ws-normal-provider", APIType: "codex", BaseURL: upstream.URL}},
	}, "", "normal-key")

	store := newMockStore()
	store.providers = []model.Provider{provider}

	handler := newProxyCodexTestHandler(t, Config{
		Store:                      store,
		VisibleContinuitySeedStore: seedStore,
		Logger:                     zap.NewNop(),
	})

	proxyServer := httptest.NewServer(handler)
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(proxyServer)+"/responses?model=gpt-5.4", proxyCodexDialOptions())
	if err != nil {
		t.Fatalf("dial websocket through proxy: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	if err := conn.Write(ctx, websocket.MessageText, []byte("first")); err != nil {
		t.Fatalf("write initial client message: %v", err)
	}
	msgType, payload, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read visible event: %v", err)
	}
	if msgType != websocket.MessageText || string(payload) != `{"type":"response.created","response":{"id":"normal-visible"}}` {
		t.Fatalf("payload = (%v, %q), want visible response.created event", msgType, string(payload))
	}
	if _, _, err := conn.Read(ctx); err == nil || (!errors.Is(err, io.EOF) && !isNormalClose(err)) {
		t.Fatalf("expected websocket close after normal completion, got %v", err)
	}

	waitFor(t, func() bool { return store.AttemptsLen() > 0 }, testPollTimeout)
	if seedStore.Len() != 0 {
		t.Fatalf("seed store len = %d, want 0 after normal WebSocket completion", seedStore.Len())
	}
}

func TestHandler_ServeHTTP_WebSocket_ClientTerminationDoesNotStoreContinuitySeed(t *testing.T) {
	t.Parallel()

	seedStore := NewVisibleContinuitySeedStore()
	upstream := newEchoWSServer(t)
	defer upstream.Close()

	provider := withTestStaticCredential(model.Provider{
		ID:   "ws-client-close-provider",
		Name: "WS Client Close Provider",

		AuthMode: "bearer",
		Enabled:  true,
		APITypes: []model.ProviderAPIType{{ProviderID: "ws-client-close-provider", APIType: "codex", BaseURL: upstream.URL}},
	}, "", "client-close-key")

	store := newMockStore()
	store.providers = []model.Provider{provider}

	handler := newProxyCodexTestHandler(t, Config{
		Store:                      store,
		VisibleContinuitySeedStore: seedStore,
		Logger:                     zap.NewNop(),
	})

	proxyServer := httptest.NewServer(handler)
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(proxyServer)+"/responses?model=gpt-5.4", proxyCodexDialOptions())
	if err != nil {
		t.Fatalf("dial websocket through proxy: %v", err)
	}

	const visiblePayload = "first"
	if err := conn.Write(ctx, websocket.MessageText, []byte(visiblePayload)); err != nil {
		t.Fatalf("write initial client message: %v", err)
	}
	msgType, payload, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read visible event: %v", err)
	}
	if msgType != websocket.MessageText || string(payload) != visiblePayload {
		t.Fatalf("payload = (%v, %q), want echoed visible payload %q", msgType, string(payload), visiblePayload)
	}
	if err := conn.CloseNow(); err != nil {
		t.Fatalf("close client websocket immediately: %v", err)
	}

	waitFor(t, func() bool { return store.AttemptsLen() > 0 }, testPollTimeout)
	if seedStore.Len() != 0 {
		t.Fatalf("seed store len = %d, want 0 after client-initiated WebSocket termination", seedStore.Len())
	}
}

func TestHandler_ServeHTTP_WebSocket_PreVisibleFailureDoesNotStoreContinuitySeed(t *testing.T) {
	t.Parallel()

	seedStore := NewVisibleContinuitySeedStore()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept websocket: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		conn.SetReadLimit(wsReadLimit)

		if _, _, err := conn.Read(r.Context()); err != nil {
			t.Errorf("read initial client message: %v", err)
			return
		}
		if err := conn.Write(r.Context(), websocket.MessageText, []byte(`{"type":"error","status":401,"error":{"type":"invalid_api_key","code":"invalid_api_key","message":"credential expired"}}`)); err != nil {
			t.Errorf("write pre-visible provider-scoped error: %v", err)
		}
	}))
	defer upstream.Close()

	provider := withTestStaticCredential(model.Provider{
		ID:   "ws-previsible-provider",
		Name: "WS PreVisible Provider",

		AuthMode: "bearer",
		Enabled:  true,
		APITypes: []model.ProviderAPIType{{ProviderID: "ws-previsible-provider", APIType: "codex", BaseURL: upstream.URL}},
	}, "", "previsible-key")

	store := newMockStore()
	store.providers = []model.Provider{provider}

	handler := newProxyCodexTestHandler(t, Config{
		Store:                      store,
		VisibleContinuitySeedStore: seedStore,
		Logger:                     zap.NewNop(),
	})

	proxyServer := httptest.NewServer(handler)
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(proxyServer)+"/responses?model=gpt-5.4", proxyCodexDialOptions())
	if err != nil {
		t.Fatalf("dial websocket through proxy: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	if err := conn.Write(ctx, websocket.MessageText, []byte("first")); err != nil {
		t.Fatalf("write initial client message: %v", err)
	}

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
	if _, _, err := conn.Read(ctx); err == nil || (!errors.Is(err, io.EOF) && !isNormalClose(err)) {
		t.Fatalf("expected websocket close after pre-visible failure, got %v", err)
	}

	waitFor(t, func() bool { return store.AttemptsLen() > 0 }, testPollTimeout)
	if seedStore.Len() != 0 {
		t.Fatalf("seed store len = %d, want 0 after pre-visible WebSocket failure", seedStore.Len())
	}
}
