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

func TestHandler_ServeHTTP_WebSocket_PreCommitSemanticErrorSkipsStickyAndMarksFailure(t *testing.T) {
	const errorPayload = `{"error":{"message":"Model 'gpt-5.4' is not allowed","type":"model_not_allowed"},"status":403,"type":"error"}`

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept upstream websocket: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		if err := conn.Write(r.Context(), websocket.MessageText, []byte(errorPayload)); err != nil {
			t.Errorf("write semantic error: %v", err)
		}
	}))
	defer upstream.Close()

	wsProvider := &model.Provider{
		ID:       "ws-semantic-precommit",
		Name:     "WS Semantic Precommit Provider",
		APIKey:   "key",
		AuthMode: "bearer",
		Enabled:  true,
		APITypes: []model.ProviderAPIType{{ProviderID: "ws-semantic-precommit", APIType: "codex", BaseURL: upstream.URL}},
	}

	store := newMockStore()
	store.providers = []model.Provider{*wsProvider}
	mockSel := &mockSelector{
		selectWithMetadataFunc: func(_ context.Context, _ *model.SelectRequest) (*selectResult, error) {
			return &selectResult{Provider: wsProvider}, nil
		},
	}
	healthMgr := newTrackingHealthManager()
	handler := NewHandler(Config{
		Store:    store,
		Logger:   zap.NewNop(),
		Selector: mockSel,
		Health:   healthMgr,
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

	waitFor(t, func() bool { return store.LogsLen() > 0 }, testPollTimeout)
	if mockSel.StickyUpdatesLen() != 0 {
		t.Fatalf("sticky update count = %d, want 0", mockSel.StickyUpdatesLen())
	}
	if len(healthMgr.getMarkFailureCalls()) != 1 {
		t.Fatalf(
			"mark failure count = %d, want 1 (logs=%d attempts=%d lastLog=%#v storedAttempts=%#v)",
			len(healthMgr.getMarkFailureCalls()),
			store.LogsLen(),
			store.AttemptsLen(),
			store.LastLog(),
			store.LastAttempts(4),
		)
	}
	if len(healthMgr.getMarkSuccessIDs()) != 0 {
		t.Fatalf("mark success count = %d, want 0", len(healthMgr.getMarkSuccessIDs()))
	}

	log := store.LastLog()
	if log == nil {
		t.Fatal("expected log entry")
	}
	if log.SessionCommitted == nil || *log.SessionCommitted {
		t.Fatal("SessionCommitted must be false for pre-commit semantic error")
	}
	if requestLogTerminationReason(log) != model.TerminationReasonUpstreamSemanticError {
		t.Fatalf("TerminationReason = %q, want %q", requestLogTerminationReason(log), model.TerminationReasonUpstreamSemanticError)
	}
	if requestLogServiceOutcome(log) == model.ServiceOutcomeCompleted {
		t.Fatal("service outcome must stay non-completed for pre-commit semantic error")
	}
}

func TestHandler_ServeHTTP_WebSocket_PostCommitSemanticErrorKeepsStickyAndFailureVisibility(t *testing.T) {
	const errorPayload = `{"error":{"message":"provider failed after commit","type":"provider_failure"},"status":500,"type":"error"}`

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept upstream websocket: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		conn.SetReadLimit(wsReadLimit)

		if _, _, err := conn.Read(r.Context()); err != nil {
			t.Errorf("read post-commit client message: %v", err)
			return
		}

		ctx := r.Context()
		if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"response.created","response":{"id":"resp_live","model":"gpt-5.4"}}`)); err != nil {
			t.Errorf("write response.created: %v", err)
			return
		}
		if err := conn.Write(ctx, websocket.MessageText, []byte(errorPayload)); err != nil {
			t.Errorf("write semantic error: %v", err)
		}
	}))
	defer upstream.Close()

	wsProvider := &model.Provider{
		ID:       "ws-semantic-postcommit",
		Name:     "WS Semantic Postcommit Provider",
		APIKey:   "key",
		AuthMode: "bearer",
		Enabled:  true,
		APITypes: []model.ProviderAPIType{{ProviderID: "ws-semantic-postcommit", APIType: "codex", BaseURL: upstream.URL}},
	}

	store := newMockStore()
	store.providers = []model.Provider{*wsProvider}
	mockSel := &mockSelector{
		selectWithMetadataFunc: func(_ context.Context, _ *model.SelectRequest) (*selectResult, error) {
			return &selectResult{Provider: wsProvider}, nil
		},
	}
	healthMgr := newTrackingHealthManager()
	handler := NewHandler(Config{
		Store:    store,
		Logger:   zap.NewNop(),
		Selector: mockSel,
		Health:   healthMgr,
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

	if err := conn.Write(ctx, websocket.MessageText, []byte("ping")); err != nil {
		t.Fatalf("write: %v", err)
	}

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

	waitFor(t, func() bool { return store.LogsLen() > 0 }, testPollTimeout)
	waitFor(t, func() bool { return mockSel.StickyUpdatesLen() == 1 }, testPollTimeout)
	waitFor(t, func() bool { return len(healthMgr.getMarkFailureCalls()) == 1 }, 10*testPollTimeout)

	if len(healthMgr.getMarkFailureCalls()) != 1 {
		t.Fatalf("mark failure count = %d, want 1", len(healthMgr.getMarkFailureCalls()))
	}
	if len(healthMgr.getMarkSuccessIDs()) != 0 {
		t.Fatalf("mark success count = %d, want 0", len(healthMgr.getMarkSuccessIDs()))
	}

	log := store.LastLog()
	if log == nil {
		t.Fatal("expected log entry")
	}
	if log.SessionCommitted == nil || !*log.SessionCommitted {
		t.Fatal("SessionCommitted must be true after response.created")
	}
	if requestLogTerminationReason(log) != model.TerminationReasonUpstreamSemanticError {
		t.Fatalf("TerminationReason = %q, want %q", requestLogTerminationReason(log), model.TerminationReasonUpstreamSemanticError)
	}
	if requestLogServiceOutcome(log) == model.ServiceOutcomeCompleted {
		t.Fatal("service outcome must stay non-completed for post-commit semantic error")
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
	tests := []struct {
		apiType string
		method  string
		path    string
	}{
		// Bare Claude messages register POST only; the explicit namespace is the
		// GET shape the real mux can deliver for an attempted WebSocket upgrade.
		{apiType: "claude", method: http.MethodGet, path: "/claude/v1/messages"},
		// Grok routes register POST only, so POST is the only upgrade-carrying
		// shape the real mux can deliver to the handler (GET dies at the mux 404).
		{apiType: "grok", method: http.MethodPost, path: "/chat/completions"},
	}

	for _, test := range tests {
		t.Run(test.apiType, func(t *testing.T) {
			store := newMockStore()
			store.providers = []model.Provider{
				{
					ID: "p1", Name: "Provider", APIKey: "key", AuthMode: "bearer", Enabled: true,
					APITypes: []model.ProviderAPIType{{ProviderID: "p1", APIType: test.apiType, BaseURL: "http://example.com"}},
				},
			}

			handler := NewHandler(Config{Store: store, Logger: zap.NewNop()})

			req := httptest.NewRequest(test.method, test.path, nil)
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
		})
	}
}

// TestHandler_ServeHTTP_NonUpgradeGET_Returns426 verifies that a plain GET on
// the WebSocket-only Responses endpoint returns 426 Upgrade Required in every
// path form that normalizes to /responses.
func TestHandler_ServeHTTP_NonUpgradeGET_Returns426(t *testing.T) {
	for _, path := range []string{"/responses", "/v1/responses", "/codex/responses", "/codex/v1/responses"} {
		t.Run(path, func(t *testing.T) {
			store := newMockStore()
			store.providers = []model.Provider{
				{
					ID: "p1", Name: "Provider", APIKey: "key", AuthMode: "bearer", Enabled: true,
					APITypes: []model.ProviderAPIType{{ProviderID: "p1", APIType: "codex", BaseURL: "http://example.com"}},
				},
			}

			handler := NewHandler(Config{Store: store, Logger: zap.NewNop()})

			req := httptest.NewRequest(http.MethodGet, path, nil)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if w.Code != http.StatusUpgradeRequired {
				t.Errorf("status = %d, want %d", w.Code, http.StatusUpgradeRequired)
			}
			if !strings.Contains(w.Body.String(), "WebSocket upgrade") {
				t.Errorf("body = %q, expected WebSocket upgrade message", w.Body.String())
			}
		})
	}
}
