package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"switch-a/internal/model"

	"github.com/coder/websocket"
	"go.uber.org/zap"
)

func TestHandler_ServeHTTP_WebSocket_FallbackSelectionUsesLearnedModelAfterMidSessionObservation(t *testing.T) {
	t.Parallel()

	const prompt = `{"type":"response.create","response":{"model":"client-model","instructions":"hello"}}`

	var retrySelections atomic.Int32

	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept primary websocket: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		if _, _, err := conn.Read(r.Context()); err != nil {
			return
		}
		if err := conn.Write(r.Context(), websocket.MessageText, []byte(`{"type":"error","status":403,"error":{"type":"auth_error","code":"model_not_allowed","message":"model access denied"}}`)); err != nil {
			t.Errorf("write primary semantic payload: %v", err)
		}
	}))
	defer primary.Close()

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
			t.Errorf("read replayed prompt: %v", err)
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
		ID:       "ws-model-retry-primary",
		Name:     "WS Model Retry Primary",
		APIKey:   "primary-key",
		AuthMode: "bearer",
		Enabled:  true,
		APITypes: []model.ProviderAPIType{{ProviderID: "ws-model-retry-primary", APIType: "codex", BaseURL: primary.URL}},
	}
	fallbackProvider := &model.Provider{
		ID:       "ws-model-retry-fallback",
		Name:     "WS Model Retry Fallback",
		APIKey:   "fallback-key",
		AuthMode: "bearer",
		Enabled:  true,
		APITypes: []model.ProviderAPIType{{ProviderID: "ws-model-retry-fallback", APIType: "codex", BaseURL: fallback.URL}},
	}

	store := newMockStore()
	store.providers = []model.Provider{*primaryProvider, *fallbackProvider}
	store.configs[ConfigKeyWebSocketProbeClientModel] = "false"

	mockSel := &mockSelector{
		selectWithMetadataFunc: func(_ context.Context, req *model.SelectRequest) (*selectResult, error) {
			if req.Model != ModelUnknown {
				t.Fatalf("initial selection model = %q, want %q when probing is disabled", req.Model, ModelUnknown)
			}
			return &selectResult{Provider: primaryProvider, FromStickyCache: false}, nil
		},
		selectExcludingFunc: func(_ context.Context, req *model.SelectRequest, excludeIDs map[string]bool) (*model.Provider, error) {
			retrySelections.Add(1)
			if req.Model != "client-model" {
				t.Fatalf("retry selection model = %q, want %q after observer learned the model", req.Model, "client-model")
			}
			if !excludeIDs[primaryProvider.ID] {
				t.Fatalf("excludeIDs = %v, want %q excluded", excludeIDs, primaryProvider.ID)
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
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	if err := conn.Write(ctx, websocket.MessageText, []byte(prompt)); err != nil {
		t.Fatalf("write client prompt: %v", err)
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
