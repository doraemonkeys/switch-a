package proxy

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"switch-a/internal/model"
	"switch-a/internal/providerauth"

	"github.com/coder/websocket"
	"go.uber.org/zap"
)

type mockOAuthHTTPClient struct {
	do func(req *http.Request) (*http.Response, error)
}

func (m mockOAuthHTTPClient) Do(req *http.Request) (*http.Response, error) {
	if m.do != nil {
		return m.do(req)
	}
	return nil, nil
}

func testUnsignedJWT(t *testing.T, claims map[string]any) string {
	t.Helper()

	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal jwt payload: %v", err)
	}

	return "eyJhbGciOiJub25lIn0." + base64.RawURLEncoding.EncodeToString(payload) + "."
}

func testChatGPTCredentialData(t *testing.T, accessToken, refreshToken, accountID string) string {
	t.Helper()

	expiresAt := time.Now().UTC().Add(1 * time.Hour)
	idToken := testUnsignedJWT(t, map[string]any{
		"iss":   "https://auth.openai.com",
		"aud":   "app_EMoamEEZ73f0CkXaXp7hrann",
		"email": "codex@example.com",
		"exp":   expiresAt.Unix(),
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": accountID,
		},
	})

	raw, err := json.Marshal(model.ChatGPTProviderCredential{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		IDToken:      idToken,
		AccountID:    accountID,
		Email:        "codex@example.com",
		LastRefresh:  time.Now().UTC(),
		ExpiresAt:    expiresAt,
	})
	if err != nil {
		t.Fatalf("marshal chatgpt credential: %v", err)
	}

	return string(raw)
}

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
		TerminalCause:        model.TerminalUpstreamHandshakeRejected,
		Err:                  errors.New("failed to WebSocket dial: expected handshake response status code 101 but got 402"),
	}

	handler.logWebSocketRequest("req-ws-handshake", info, &model.Provider{ID: "ws-p1"}, false, false, result, result.Err, 250*time.Millisecond)

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
	if log.TerminalCause == nil || *log.TerminalCause != model.TerminalUpstreamHandshakeRejected {
		t.Fatalf("TerminalCause = %v, want %q", log.TerminalCause, model.TerminalUpstreamHandshakeRejected)
	}
	if log.SessionCommitted == nil || *log.SessionCommitted {
		t.Fatal("SessionCommitted must be false for failed handshake")
	}
	if log.CommitSource == nil || *log.CommitSource != model.CommitUnknown {
		t.Fatalf("CommitSource = %v, want %q", log.CommitSource, model.CommitUnknown)
	}
}

func TestHandler_logWebSocketRequest_UsesSemanticUpstreamError(t *testing.T) {
	t.Parallel()

	store := newMockStore()
	handler := NewHandler(Config{
		Store:  store,
		Logger: zap.NewNop(),
	})

	const errorPayload = `{"error":{"message":"Model 'gpt-5.4' is not allowed","type":"model_not_allowed"},"status":403,"type":"error"}`
	info := RequestInfo{
		APIType:   "codex",
		Model:     "gpt-5.4",
		ClientIP:  "127.0.0.1",
		UserID:    "user-1",
		Path:      "/responses",
		Method:    http.MethodGet,
		UserAgent: "codex-test",
		RequestID: "upstream-request-id",
	}
	result := &WebSocketResult{
		HandshakeAccepted: true,
		TerminalCause:     model.TerminalUpstreamSemanticError,
		CloseCode:         websocket.StatusNoStatusRcvd,
		Err:               errors.New("failed to get reader: received close frame: status = StatusNoStatusRcvd and reason = \"\""),
		UpstreamError: &WebSocketUpstreamError{
			EventType:  "model_not_allowed",
			StatusCode: http.StatusForbidden,
			Message:    "Model 'gpt-5.4' is not allowed",
			Raw:        errorPayload,
		},
	}

	handler.logWebSocketRequest("req-ws-semantic-error", info, &model.Provider{ID: "ws-p1"}, false, false, result, result.Err, 250*time.Millisecond)

	log := store.LastLog()
	if log == nil {
		t.Fatal("expected log entry")
	}
	if log.StatusCode != http.StatusForbidden {
		t.Fatalf("StatusCode = %d, want %d", log.StatusCode, http.StatusForbidden)
	}
	if log.ErrorMsg != errorPayload {
		t.Fatalf("ErrorMsg = %q, want %q", log.ErrorMsg, errorPayload)
	}
	if log.Success {
		t.Fatal("expected Success=false for semantic upstream error")
	}
	if !log.IsWebSocket {
		t.Fatal("expected IsWebSocket=true")
	}
	if log.TerminalCause == nil || *log.TerminalCause != model.TerminalUpstreamSemanticError {
		t.Fatalf("TerminalCause = %v, want %q", log.TerminalCause, model.TerminalUpstreamSemanticError)
	}
	if log.SessionCommitted == nil || *log.SessionCommitted {
		t.Fatal("SessionCommitted must be false for pre-commit semantic errors")
	}
	if log.CommitSource == nil || *log.CommitSource != model.CommitUnknown {
		t.Fatalf("CommitSource = %v, want %q", log.CommitSource, model.CommitUnknown)
	}
}

func TestHandler_logWebSocketRequest_PersistsCommitSource(t *testing.T) {
	t.Parallel()

	store := newMockStore()
	handler := NewHandler(Config{
		Store:  store,
		Logger: zap.NewNop(),
	})

	committed := true
	info := RequestInfo{
		APIType:   "codex",
		Model:     "gpt-5.4",
		ClientIP:  "127.0.0.1",
		UserID:    "user-1",
		Path:      "/responses",
		Method:    http.MethodGet,
		UserAgent: "codex-test",
		RequestID: "upstream-request-id",
	}
	result := &WebSocketResult{
		HandshakeAccepted: true,
		SessionCommitted:  committed,
		TerminalCause:     model.TerminalCleanClose,
		CommitSource:      model.CommitSemantic,
	}

	handler.logWebSocketRequest("req-ws-commit-source", info, &model.Provider{ID: "ws-p1"}, false, true, result, nil, 250*time.Millisecond)

	log := store.LastLog()
	if log == nil {
		t.Fatal("expected log entry")
	}
	if log.CommitSource == nil || *log.CommitSource != model.CommitSemantic {
		t.Fatalf("CommitSource = %v, want %q", log.CommitSource, model.CommitSemantic)
	}
	if log.SessionCommitted == nil || !*log.SessionCommitted {
		t.Fatalf("SessionCommitted = %v, want true", log.SessionCommitted)
	}
}

func TestApplyWebSocketHealthOutcome_PostCommitTransportErrorMarksSuccess(t *testing.T) {
	t.Parallel()

	store := newMockStore()
	healthMgr := newTrackingHealthManager()
	handler := NewHandler(Config{
		Store:  store,
		Logger: zap.NewNop(),
		Health: healthMgr,
	})

	applyWebSocketHealthOutcome(context.Background(), handler, "ws-p1", &WebSocketResult{
		HandshakeAccepted: true,
		SessionCommitted:  true,
		TerminalCause:     model.TerminalUpstreamTransportError,
		Err:               io.EOF,
	})

	if len(healthMgr.getMarkFailureCalls()) != 0 {
		t.Fatalf("mark failure count = %d, want 0", len(healthMgr.getMarkFailureCalls()))
	}
	if successIDs := healthMgr.getMarkSuccessIDs(); len(successIDs) != 1 || successIDs[0] != "ws-p1" {
		t.Fatalf("mark success IDs = %v, want [ws-p1]", successIDs)
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
	if log.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("StatusCode = %d, want %d", log.StatusCode, http.StatusServiceUnavailable)
	}
	if log.TerminalCause == nil || *log.TerminalCause != model.TerminalProviderUnavailable {
		t.Fatalf("TerminalCause = %v, want %q", log.TerminalCause, model.TerminalProviderUnavailable)
	}
	if log.CommitSource == nil || *log.CommitSource != model.CommitUnknown {
		t.Fatalf("CommitSource = %v, want %q", log.CommitSource, model.CommitUnknown)
	}
}

func TestHandler_ServeHTTP_WebSocket_ProviderPreflightConfigFailure(t *testing.T) {
	tests := []struct {
		name         string
		provider     model.Provider
		errorSnippet string
	}{
		{
			name: "missing base url",
			provider: model.Provider{
				ID:       "ws-missing-base-url",
				Name:     "Missing Base URL",
				APIKey:   "key",
				AuthMode: "bearer",
				Enabled:  true,
				APITypes: []model.ProviderAPIType{{ProviderID: "ws-missing-base-url", APIType: "codex", BaseURL: ""}},
			},
			errorSnippet: "no base_url",
		},
		{
			name: "missing api key",
			provider: model.Provider{
				ID:       "ws-missing-api-key",
				Name:     "Missing API Key",
				APIKey:   "",
				AuthMode: "bearer",
				Enabled:  true,
				APITypes: []model.ProviderAPIType{{ProviderID: "ws-missing-api-key", APIType: "codex", BaseURL: "https://example.invalid"}},
			},
			errorSnippet: "no api_key",
		},
		{
			name: "chatgpt provider without auth service",
			provider: model.Provider{
				ID:             "ws-chatgpt-no-auth",
				Name:           "ChatGPT Without Auth Service",
				Enabled:        true,
				CredentialType: model.ProviderCredentialTypeChatGPT,
				CredentialData: testChatGPTCredentialData(t, "access-token", "refresh-token", "acct-test"),
				APITypes: []model.ProviderAPIType{{
					ProviderID: "ws-chatgpt-no-auth",
					APIType:    "codex",
					BaseURL:    "https://example.invalid",
				}},
			},
			errorSnippet: "managed credentials",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newMockStore()
			store.providers = []model.Provider{tt.provider}

			handler := NewHandler(Config{
				Store:  store,
				Logger: zap.NewNop(),
			})

			proxyServer := httptest.NewServer(handler)
			defer proxyServer.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			_, resp, err := websocket.Dial(ctx, wsURL(proxyServer)+"/responses", nil)
			if err == nil {
				t.Fatal("expected dial to fail before upgrade")
			}
			if resp == nil {
				t.Fatal("expected HTTP response from server even on dial failure")
			}
			if resp.StatusCode != http.StatusBadGateway {
				t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadGateway)
			}

			waitFor(t, func() bool { return store.LogsLen() > 0 }, testPollTimeout)

			log := store.LastLog()
			if log == nil {
				t.Fatal("expected log entry")
			}
			if log.StatusCode != http.StatusBadGateway {
				t.Fatalf("StatusCode = %d, want %d", log.StatusCode, http.StatusBadGateway)
			}
			if log.TerminalCause == nil || *log.TerminalCause != model.TerminalProviderConfigurationError {
				t.Fatalf("TerminalCause = %v, want %q", log.TerminalCause, model.TerminalProviderConfigurationError)
			}
			if log.CommitSource == nil || *log.CommitSource != model.CommitUnknown {
				t.Fatalf("CommitSource = %v, want %q", log.CommitSource, model.CommitUnknown)
			}
			if !strings.Contains(log.ErrorMsg, tt.errorSnippet) {
				t.Fatalf("ErrorMsg = %q, want snippet %q", log.ErrorMsg, tt.errorSnippet)
			}
		})
	}
}

func TestHandler_ServeHTTP_WebSocket_UpstreamUpgradeRequiredPropagatesStatus(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUpgradeRequired)
		_, _ = io.WriteString(w, `{"error":"fallback to http"}`)
	}))
	defer upstream.Close()

	store := newMockStore()
	store.providers = []model.Provider{{
		ID:       "ws-http-fallback",
		Name:     "WS HTTP Fallback",
		APIKey:   "key",
		AuthMode: "bearer",
		Enabled:  true,
		APITypes: []model.ProviderAPIType{{ProviderID: "ws-http-fallback", APIType: "codex", BaseURL: upstream.URL}},
	}}

	handler := NewHandler(Config{
		Store:  store,
		Logger: zap.NewNop(),
	})

	proxyServer := httptest.NewServer(handler)
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, resp, err := websocket.Dial(ctx, wsURL(proxyServer)+"/responses", &websocket.DialOptions{
		HTTPHeader: http.Header{
			"OpenAI-Beta": {"responses_websockets=2026-02-06"},
		},
	})
	if err == nil {
		t.Fatal("expected websocket dial to fail before upgrade")
	}
	if resp == nil {
		t.Fatal("expected HTTP response from proxy")
	}
	if resp.StatusCode != http.StatusUpgradeRequired {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUpgradeRequired)
	}

	waitFor(t, func() bool { return store.LogsLen() > 0 }, testPollTimeout)

	log := store.LastLog()
	if log == nil {
		t.Fatal("expected log entry")
	}
	if log.StatusCode != http.StatusUpgradeRequired {
		t.Fatalf("StatusCode = %d, want %d", log.StatusCode, http.StatusUpgradeRequired)
	}
	if log.TerminalCause == nil || *log.TerminalCause != model.TerminalUpstreamHandshakeRejected {
		t.Fatalf("TerminalCause = %v, want %q", log.TerminalCause, model.TerminalUpstreamHandshakeRejected)
	}
}

func TestHandler_ServeHTTP_WebSocket_ChatGPTProviderRefreshesHandshakeUnauthorized(t *testing.T) {
	var (
		upstreamAttempts int32
		refreshCalls     int32
		capturedHeaders  http.Header
		capturedMu       sync.Mutex
	)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt := atomic.AddInt32(&upstreamAttempts, 1)
		if attempt == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":"expired access token"}`)
			return
		}

		capturedMu.Lock()
		capturedHeaders = r.Header.Clone()
		capturedMu.Unlock()

		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept upstream websocket: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		if err := conn.Write(r.Context(), websocket.MessageText, []byte(`{"type":"response.created"}`)); err != nil {
			t.Errorf("write upstream event: %v", err)
		}
	}))
	defer upstream.Close()

	provider := &model.Provider{
		ID:             "ws-chatgpt-refresh",
		Name:           "WS ChatGPT Refresh",
		Enabled:        true,
		AuthMode:       "bearer",
		CredentialType: model.ProviderCredentialTypeChatGPT,
		CredentialData: testChatGPTCredentialData(t, "access-old", "refresh-old", "acct-123"),
		APITypes: []model.ProviderAPIType{{
			ProviderID: "ws-chatgpt-refresh",
			APIType:    "codex",
			BaseURL:    upstream.URL,
		}},
	}

	authService := providerauth.NewService(providerauth.Config{
		HTTPClient: mockOAuthHTTPClient{
			do: func(req *http.Request) (*http.Response, error) {
				atomic.AddInt32(&refreshCalls, 1)
				if req.Method != http.MethodPost {
					t.Fatalf("refresh method = %s, want POST", req.Method)
				}
				if !strings.HasSuffix(req.URL.String(), "/oauth/token") {
					t.Fatalf("refresh url = %s, want /oauth/token", req.URL.String())
				}
				bodyBytes, err := io.ReadAll(req.Body)
				if err != nil {
					t.Fatalf("read refresh body: %v", err)
				}
				body := string(bodyBytes)
				if !strings.Contains(body, "grant_type=refresh_token") {
					t.Fatalf("refresh body = %q, missing grant_type", body)
				}
				if !strings.Contains(body, "refresh_token=refresh-old") {
					t.Fatalf("refresh body = %q, missing refresh token", body)
				}

				idToken := testUnsignedJWT(t, map[string]any{
					"iss":   "https://auth.openai.com",
					"aud":   "app_EMoamEEZ73f0CkXaXp7hrann",
					"email": "codex@example.com",
					"exp":   time.Now().UTC().Add(1 * time.Hour).Unix(),
					"https://api.openai.com/auth": map[string]any{
						"chatgpt_account_id": "acct-123",
					},
				})
				payload, err := json.Marshal(map[string]string{
					"access_token":  "access-new",
					"refresh_token": "refresh-new",
					"id_token":      idToken,
				})
				if err != nil {
					t.Fatalf("marshal refresh response: %v", err)
				}

				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(string(payload))),
					Header: http.Header{
						"Content-Type": {"application/json"},
					},
				}, nil
			},
		},
		Logger: zap.NewNop(),
	})

	store := newMockStore()
	store.providers = []model.Provider{*provider}
	mockSel := &mockSelector{
		selectWithMetadataFunc: func(_ context.Context, _ *model.SelectRequest) (*selectResult, error) {
			return &selectResult{Provider: provider, FromStickyCache: false}, nil
		},
	}

	handler := NewHandler(Config{
		Store:    store,
		Selector: mockSel,
		Auth:     authService,
		Logger:   zap.NewNop(),
	})

	proxyServer := httptest.NewServer(handler)
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(proxyServer)+"/responses", &websocket.DialOptions{
		HTTPHeader: http.Header{
			"OpenAI-Beta": {"responses_websockets=2026-02-06"},
			"X-Custom":    {"passthrough"},
		},
	})
	if err != nil {
		t.Fatalf("dial websocket through proxy: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	msgType, payload, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read proxied websocket event: %v", err)
	}
	if msgType != websocket.MessageText {
		t.Fatalf("message type = %v, want %v", msgType, websocket.MessageText)
	}
	if string(payload) != `{"type":"response.created"}` {
		t.Fatalf("payload = %q, want response.created event", string(payload))
	}

	if got := atomic.LoadInt32(&upstreamAttempts); got != 2 {
		t.Fatalf("upstream attempts = %d, want 2", got)
	}
	if got := atomic.LoadInt32(&refreshCalls); got != 1 {
		t.Fatalf("refresh calls = %d, want 1", got)
	}

	capturedMu.Lock()
	headers := capturedHeaders.Clone()
	capturedMu.Unlock()
	if got := headers.Get("Authorization"); got != "Bearer access-new" {
		t.Fatalf("Authorization = %q, want %q", got, "Bearer access-new")
	}
	if got := headers.Get("ChatGPT-Account-Id"); got != "acct-123" {
		t.Fatalf("ChatGPT-Account-Id = %q, want %q", got, "acct-123")
	}
	if got := headers.Get("Originator"); got != "codex_cli_rs" {
		t.Fatalf("Originator = %q, want %q", got, "codex_cli_rs")
	}
	if got := headers.Get("OpenAI-Beta"); got != "responses_websockets=2026-02-06" {
		t.Fatalf("OpenAI-Beta = %q, want %q", got, "responses_websockets=2026-02-06")
	}
	if got := headers.Get("X-Custom"); got != "passthrough" {
		t.Fatalf("X-Custom = %q, want %q", got, "passthrough")
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
