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

	handler.logWebSocketSession(info, &WebSocketSessionResult{
		RequestID:     "req-ws-handshake",
		FinalProvider: &model.Provider{ID: "ws-p1"},
		FinalResult:   result,
		FinalErr:      result.Err,
	}, 250*time.Millisecond)

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

	handler.logWebSocketSession(info, &WebSocketSessionResult{
		RequestID:     "req-ws-semantic-error",
		FinalProvider: &model.Provider{ID: "ws-p1"},
		FinalResult:   result,
		FinalErr:      result.Err,
	}, 250*time.Millisecond)

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

	handler.logWebSocketSession(info, &WebSocketSessionResult{
		RequestID:     "req-ws-commit-source",
		FinalProvider: &model.Provider{ID: "ws-p1"},
		FinalResult:   result,
		StickyWritten: true,
	}, 250*time.Millisecond)

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

func TestHandler_logWebSocketRequest_UsesLifecycleProviderAttributionAndPersistsAttempts(t *testing.T) {
	t.Parallel()

	store := newMockStore()
	handler := NewHandler(Config{
		Store:  store,
		Logger: zap.NewNop(),
	})

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
	attempts := []WebSocketAttemptResult{
		{
			Provider:   &model.Provider{ID: "provider-origin"},
			Attempt:    0,
			Result:     newWebSocketGatewayFailureResult(http.StatusForbidden, model.TerminalUpstreamHandshakeRejected, errors.New("provider-scoped semantic error")),
			ForwardErr: errors.New("provider-scoped semantic error"),
			CreatedAt:  time.Now(),
		},
		{
			Provider:  &model.Provider{ID: "provider-final"},
			Attempt:   1,
			Result:    &WebSocketResult{HandshakeAccepted: true},
			CreatedAt: time.Now().Add(time.Millisecond),
		},
	}
	result := &WebSocketResult{
		HandshakeAccepted: true,
		SessionCommitted:  true,
		TerminalCause:     model.TerminalCleanClose,
		CommitSource:      model.CommitSemantic,
	}

	handler.logWebSocketSession(info, &WebSocketSessionResult{
		RequestID:     "req-ws-attribution",
		FinalProvider: &model.Provider{ID: "provider-final"},
		FinalResult:   result,
		Attempts:      attempts,
		StickyWritten: true,
	}, 250*time.Millisecond)

	log := store.LastLog()
	if log == nil {
		t.Fatal("expected log entry")
	}
	if log.ProviderID != "provider-final" {
		t.Fatalf("ProviderID = %q, want %q", log.ProviderID, "provider-final")
	}
	if log.RetryCount != 1 {
		t.Fatalf("RetryCount = %d, want 1", log.RetryCount)
	}

	storedAttempts := store.LastAttempts(2)
	if len(storedAttempts) != 2 {
		t.Fatalf("expected 2 attempts, got %d", len(storedAttempts))
	}
	if storedAttempts[0].ProviderID != "provider-origin" || storedAttempts[1].ProviderID != "provider-final" {
		t.Fatalf("attempt provider order = [%s %s], want [provider-origin provider-final]", storedAttempts[0].ProviderID, storedAttempts[1].ProviderID)
	}
}

func TestHandler_logWebSocketRequest_PrefersLifecycleProviderOverLastAttempt(t *testing.T) {
	t.Parallel()

	store := newMockStore()
	handler := NewHandler(Config{
		Store:  store,
		Logger: zap.NewNop(),
	})

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
	attempts := []WebSocketAttemptResult{
		{
			Provider:   &model.Provider{ID: "provider-origin"},
			Attempt:    0,
			Result:     newWebSocketGatewayFailureResult(http.StatusForbidden, model.TerminalUpstreamHandshakeRejected, errors.New("provider-scoped semantic error")),
			ForwardErr: errors.New("provider-scoped semantic error"),
			CreatedAt:  time.Now(),
		},
		{
			Provider:   &model.Provider{ID: "provider-fallback"},
			Attempt:    1,
			Result:     newWebSocketGatewayFailureResult(http.StatusBadGateway, model.TerminalUpstreamTransportError, errors.New("fallback replay failed")),
			ForwardErr: errors.New("fallback replay failed"),
			CreatedAt:  time.Now().Add(time.Millisecond),
		},
	}
	result := &WebSocketResult{
		HandshakeAccepted: true,
		TerminalCause:     model.TerminalUpstreamSemanticError,
		Err:               errors.New("original provider semantic payload returned to client"),
	}

	handler.logWebSocketSession(info, &WebSocketSessionResult{
		RequestID:     "req-ws-origin-attribution",
		FinalProvider: &model.Provider{ID: "provider-origin"},
		FinalResult:   result,
		FinalErr:      result.Err,
		Attempts:      attempts,
	}, 250*time.Millisecond)

	log := store.LastLog()
	if log == nil {
		t.Fatal("expected log entry")
	}
	if log.ProviderID != "provider-origin" {
		t.Fatalf("ProviderID = %q, want %q", log.ProviderID, "provider-origin")
	}

	storedAttempts := store.LastAttempts(2)
	if len(storedAttempts) != 2 {
		t.Fatalf("expected 2 attempts, got %d", len(storedAttempts))
	}
	if storedAttempts[1].ProviderID != "provider-fallback" {
		t.Fatalf("last attempt provider = %q, want %q", storedAttempts[1].ProviderID, "provider-fallback")
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

func TestApplyWebSocketHealthOutcome_PostCommitSemanticErrorMarksFailure(t *testing.T) {
	t.Parallel()

	store := newMockStore()
	healthMgr := newTrackingHealthManager()
	handler := NewHandler(Config{
		Store:  store,
		Logger: zap.NewNop(),
		Health: healthMgr,
	})

	semanticErr := errors.New("model not allowed")
	applyWebSocketHealthOutcome(context.Background(), handler, "ws-p1", &WebSocketResult{
		HandshakeAccepted: true,
		SessionCommitted:  true,
		TerminalCause:     model.TerminalUpstreamSemanticError,
		Err:               semanticErr,
	})

	if successIDs := healthMgr.getMarkSuccessIDs(); len(successIDs) != 0 {
		t.Fatalf("mark success IDs = %v, want none", successIDs)
	}
	failures := healthMgr.getMarkFailureCalls()
	if len(failures) != 1 {
		t.Fatalf("mark failure count = %d, want 1", len(failures))
	}
	if failures[0].providerID != "ws-p1" {
		t.Fatalf("providerID = %q, want ws-p1", failures[0].providerID)
	}
	if !errors.Is(failures[0].err, semanticErr) {
		t.Fatalf("err = %v, want %v", failures[0].err, semanticErr)
	}
}

func TestPrepareWebSocketDialHeaders_ManagedAuthErrorAndLogHelpers(t *testing.T) {
	t.Parallel()

	store := newMockStore()
	handler := NewHandler(Config{
		Store:  store,
		Logger: zap.NewNop(),
		Auth:   providerauth.NewService(providerauth.Config{}),
	})

	req := httptest.NewRequest(http.MethodGet, "http://proxy.test/v1/realtime?model=gpt-5.4", nil)
	provider := &model.Provider{
		ID:             "ws-chatgpt-invalid",
		AuthMode:       "bearer",
		CredentialType: model.ProviderCredentialTypeChatGPT,
	}

	headers, err := handler.prepareWebSocketDialHeaders(context.Background(), req, provider, APITypeCodex, "bearer")
	if err == nil {
		t.Fatal("prepareWebSocketDialHeaders() error = nil, want managed auth error")
	}
	if headers != nil {
		t.Fatalf("prepareWebSocketDialHeaders() headers = %v, want nil on error", headers)
	}

	if got := websocketLogStatusCode(nil); got != StatusCodeNoResponse {
		t.Fatalf("websocketLogStatusCode(nil) = %d, want %d", got, StatusCodeNoResponse)
	}
	fallbackErr := errors.New("fallback transport error")
	if got := websocketLogErrorMessage(nil, fallbackErr); got != fallbackErr.Error() {
		t.Fatalf("websocketLogErrorMessage(nil, fallback) = %q, want %q", got, fallbackErr.Error())
	}
	if got := websocketLogSuccess(nil); got {
		t.Fatal("websocketLogSuccess(nil) = true, want false")
	}

	recorder := httptest.NewRecorder()
	statusCode, errorCode, message := websocketGatewayFailure(&WebSocketResult{
		HandshakeStatusCode: http.StatusUpgradeRequired,
		TerminalCause:       model.TerminalUpstreamHandshakeRejected,
	})
	handler.writeGatewayError(recorder, statusCode, errorCode, message)
	if recorder.Code != http.StatusUpgradeRequired {
		t.Fatalf("writeGatewayError status = %d, want %d", recorder.Code, http.StatusUpgradeRequired)
	}
	if !strings.Contains(recorder.Body.String(), "HTTP fallback") {
		t.Fatalf("writeGatewayError body = %q, want upgrade-required message", recorder.Body.String())
	}
}

func TestApplyWebSocketSessionHealthOutcomesAndBytesTrackingObserver_NoOpBranches(t *testing.T) {
	t.Parallel()

	store := newMockStore()
	healthMgr := newTrackingHealthManager()
	handler := NewHandler(Config{
		Store:  store,
		Logger: zap.NewNop(),
		Health: healthMgr,
	})

	applyWebSocketSessionHealthOutcomes(context.Background(), handler, nil)
	applyWebSocketSessionHealthOutcomes(context.Background(), handler, &WebSocketSessionResult{
		Attempts: []WebSocketAttemptResult{
			{},
			{
				Provider: &model.Provider{ID: "ws-p1"},
			},
		},
	})
	applyWebSocketHealthOutcome(context.Background(), handler, "ws-p1", nil)
	if failures := healthMgr.getMarkFailureCalls(); len(failures) != 0 {
		t.Fatalf("mark failure calls = %v, want none", failures)
	}
	if successes := healthMgr.getMarkSuccessIDs(); len(successes) != 0 {
		t.Fatalf("mark success IDs = %v, want none", successes)
	}

	tracker := &LiveBytesTracker{}
	observer := newBytesTrackingObserver(nil, tracker)
	observer.ObserveClientMessage(websocket.MessageText, []byte("ping"))
	observer.ObserveUpstreamMessage(websocket.MessageText, []byte("pong"))
	if snapshot := observer.Snapshot(); snapshot != (WebSocketObservation{}) {
		t.Fatalf("Snapshot() = %+v, want zero observation without inner observer", snapshot)
	}
	if observer.ParseDegraded() {
		t.Fatal("ParseDegraded() = true, want false without inner observer")
	}
	if observer.HasSemanticObservation() {
		t.Fatal("HasSemanticObservation() = true, want false without inner observer")
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

func TestHandler_ServeHTTP_WebSocket_PreAcceptHandshakeFailureSwitchesProvider(t *testing.T) {
	var (
		primaryAttempts   int32
		fallbackAccepts   int32
		selectRetryCalls  int32
		failoverCtxChains [][]string
	)

	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&primaryAttempts, 1)
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, `{"error":"primary handshake failed"}`)
	}))
	defer primary.Close()

	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&fallbackAccepts, 1)
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept fallback websocket: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		conn.SetReadLimit(wsReadLimit)

		if _, _, err := conn.Read(r.Context()); err != nil {
			t.Errorf("read fallback client message: %v", err)
			return
		}

		if err := conn.Write(r.Context(), websocket.MessageText, []byte(`{"type":"response.created","provider":"fallback"}`)); err != nil {
			t.Errorf("write fallback websocket event: %v", err)
		}
	}))
	defer fallback.Close()

	providerPrimary := &model.Provider{
		ID:       "ws-preaccept-primary",
		Name:     "WS PreAccept Primary",
		APIKey:   "primary-key",
		AuthMode: "bearer",
		Enabled:  true,
		APITypes: []model.ProviderAPIType{{ProviderID: "ws-preaccept-primary", APIType: "codex", BaseURL: primary.URL}},
	}
	providerFallback := &model.Provider{
		ID:       "ws-preaccept-fallback",
		Name:     "WS PreAccept Fallback",
		APIKey:   "fallback-key",
		AuthMode: "bearer",
		Enabled:  true,
		APITypes: []model.ProviderAPIType{{ProviderID: "ws-preaccept-fallback", APIType: "codex", BaseURL: fallback.URL}},
	}

	store := newMockStore()
	store.providers = []model.Provider{*providerPrimary, *providerFallback}
	mockSel := &mockSelector{
		selectWithMetadataFunc: func(_ context.Context, req *model.SelectRequest) (*selectResult, error) {
			if req.FailoverContext != nil {
				t.Fatalf("initial selection should not have failover context, got %+v", req.FailoverContext)
			}
			return &selectResult{Provider: providerPrimary, FromStickyCache: false}, nil
		},
		selectExcludingFunc: func(_ context.Context, req *model.SelectRequest, excludeIDs map[string]bool) (*model.Provider, error) {
			atomic.AddInt32(&selectRetryCalls, 1)
			if !excludeIDs[providerPrimary.ID] {
				t.Fatalf("excludeIDs = %v, want %q excluded", excludeIDs, providerPrimary.ID)
			}
			if req.FailoverContext == nil {
				t.Fatal("expected failover context on retry selection")
			}
			failoverCtxChains = append(failoverCtxChains, append([]string(nil), req.FailoverContext.AttemptChain...))
			return providerFallback, nil
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

	if err := conn.Write(ctx, websocket.MessageText, []byte("ping")); err != nil {
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
	if len(failoverCtxChains) != 1 || len(failoverCtxChains[0]) != 1 || failoverCtxChains[0][0] != providerPrimary.ID {
		t.Fatalf("failover attempt chains = %v, want [[%s]]", failoverCtxChains, providerPrimary.ID)
	}

	log := store.LastLog()
	if log == nil {
		t.Fatal("expected log entry")
	}
	if log.ProviderID != providerFallback.ID {
		t.Fatalf("log.ProviderID = %q, want %q", log.ProviderID, providerFallback.ID)
	}
	if log.RetryCount != 1 {
		t.Fatalf("log.RetryCount = %d, want 1", log.RetryCount)
	}

	attempts := store.LastAttempts(2)
	if len(attempts) != 2 {
		t.Fatalf("expected 2 attempts, got %d", len(attempts))
	}
	if attempts[0].ProviderID != providerPrimary.ID || attempts[1].ProviderID != providerFallback.ID {
		t.Fatalf("attempt provider order = [%s %s], want [%s %s]", attempts[0].ProviderID, attempts[1].ProviderID, providerPrimary.ID, providerFallback.ID)
	}
}

func TestHandler_ServeHTTP_WebSocket_ProviderConfigurationFailureDoesNotSwitchProvider(t *testing.T) {
	var (
		fallbackHits     int32
		selectRetryCalls int32
	)

	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&fallbackHits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer fallback.Close()

	providerPrimary := &model.Provider{
		ID:       "ws-config-primary",
		Name:     "WS Config Primary",
		APIKey:   "",
		AuthMode: "bearer",
		Enabled:  true,
		APITypes: []model.ProviderAPIType{{ProviderID: "ws-config-primary", APIType: "codex", BaseURL: "https://example.invalid"}},
	}
	providerFallback := &model.Provider{
		ID:       "ws-config-fallback",
		Name:     "WS Config Fallback",
		APIKey:   "fallback-key",
		AuthMode: "bearer",
		Enabled:  true,
		APITypes: []model.ProviderAPIType{{ProviderID: "ws-config-fallback", APIType: "codex", BaseURL: fallback.URL}},
	}

	store := newMockStore()
	store.providers = []model.Provider{*providerPrimary, *providerFallback}
	mockSel := &mockSelector{
		selectWithMetadataFunc: func(_ context.Context, _ *model.SelectRequest) (*selectResult, error) {
			return &selectResult{Provider: providerPrimary, FromStickyCache: false}, nil
		},
		selectExcludingFunc: func(_ context.Context, _ *model.SelectRequest, _ map[string]bool) (*model.Provider, error) {
			atomic.AddInt32(&selectRetryCalls, 1)
			return providerFallback, nil
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

	_, resp, err := websocket.Dial(ctx, wsURL(proxyServer)+"/responses", nil)
	if err == nil {
		t.Fatal("expected websocket dial to fail before upgrade")
	}
	if resp == nil {
		t.Fatal("expected HTTP response from proxy")
	}
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadGateway)
	}

	waitFor(t, func() bool { return store.LogsLen() > 0 }, testPollTimeout)
	waitFor(t, func() bool { return store.AttemptsLen() > 0 }, testPollTimeout)

	if got := atomic.LoadInt32(&selectRetryCalls); got != 0 {
		t.Fatalf("retry selections = %d, want 0", got)
	}
	if got := atomic.LoadInt32(&fallbackHits); got != 0 {
		t.Fatalf("fallback hits = %d, want 0", got)
	}

	log := store.LastLog()
	if log == nil {
		t.Fatal("expected log entry")
	}
	if log.ProviderID != providerPrimary.ID {
		t.Fatalf("log.ProviderID = %q, want %q", log.ProviderID, providerPrimary.ID)
	}
	if log.RetryCount != 0 {
		t.Fatalf("log.RetryCount = %d, want 0", log.RetryCount)
	}

	attempts := store.LastAttempts(1)
	if len(attempts) != 1 {
		t.Fatalf("expected 1 attempt, got %d", len(attempts))
	}
	if attempts[0].ProviderID != providerPrimary.ID {
		t.Fatalf("attempt.ProviderID = %q, want %q", attempts[0].ProviderID, providerPrimary.ID)
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
		conn.SetReadLimit(wsReadLimit)

		if _, _, err := conn.Read(r.Context()); err != nil {
			t.Errorf("read refreshed client message: %v", err)
			return
		}

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
		selectExcludingFunc: func(_ context.Context, _ *model.SelectRequest, _ map[string]bool) (*model.Provider, error) {
			t.Fatal("same-provider credential refresh must not trigger provider reselection")
			return nil, nil
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

	if err := conn.Write(ctx, websocket.MessageText, []byte("ping")); err != nil {
		t.Fatalf("write client message: %v", err)
	}

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

	_ = conn.Close(websocket.StatusNormalClosure, "done")

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
	if log.ProviderID != "ws-chatgpt-refresh" {
		t.Fatalf("log.ProviderID = %q, want %q", log.ProviderID, "ws-chatgpt-refresh")
	}
	if log.RetryCount != 0 {
		t.Fatalf("log.RetryCount = %d, want 0", log.RetryCount)
	}

	attempts := store.LastAttempts(1)
	if len(attempts) != 1 {
		t.Fatalf("expected 1 provider attempt, got %d", len(attempts))
	}
	if attempts[0].ProviderID != "ws-chatgpt-refresh" {
		t.Fatalf("attempt.ProviderID = %q, want %q", attempts[0].ProviderID, "ws-chatgpt-refresh")
	}
	if attempts[0].Attempt != 0 {
		t.Fatalf("attempt.Attempt = %d, want 0", attempts[0].Attempt)
	}
	if attempts[0].StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("attempt.StatusCode = %d, want %d", attempts[0].StatusCode, http.StatusSwitchingProtocols)
	}
}

func TestHandler_ServeHTTP_WebSocket_PreAcceptHandshakeFailoverSwitchesProvider(t *testing.T) {
	var (
		initialAttempts int32
		finalAttempts   int32
	)

	initialUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&initialAttempts, 1)
		w.WriteHeader(http.StatusUpgradeRequired)
		_, _ = io.WriteString(w, `{"error":"fallback to http"}`)
	}))
	defer initialUpstream.Close()

	finalUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&finalAttempts, 1)
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept upstream websocket: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		conn.SetReadLimit(wsReadLimit)

		if _, _, err := conn.Read(r.Context()); err != nil {
			t.Errorf("read fallback client message: %v", err)
			return
		}

		if err := conn.Write(r.Context(), websocket.MessageText, []byte(`{"type":"response.created"}`)); err != nil {
			t.Errorf("write upstream event: %v", err)
		}
	}))
	defer finalUpstream.Close()

	initialProvider := &model.Provider{
		ID:       "ws-preaccept-p1",
		Name:     "WS PreAccept P1",
		APIKey:   "key-1",
		AuthMode: "bearer",
		Enabled:  true,
		APITypes: []model.ProviderAPIType{{ProviderID: "ws-preaccept-p1", APIType: "codex", BaseURL: initialUpstream.URL}},
	}
	finalProvider := &model.Provider{
		ID:       "ws-preaccept-p2",
		Name:     "WS PreAccept P2",
		APIKey:   "key-2",
		AuthMode: "bearer",
		Enabled:  true,
		APITypes: []model.ProviderAPIType{{ProviderID: "ws-preaccept-p2", APIType: "codex", BaseURL: finalUpstream.URL}},
	}

	store := newMockStore()
	store.providers = []model.Provider{*initialProvider, *finalProvider}

	var selectExcludingCalls int32
	mockSel := &mockSelector{
		selectWithMetadataFunc: func(_ context.Context, req *model.SelectRequest) (*selectResult, error) {
			if req.FailoverContext != nil {
				t.Fatal("first selection must not have failover context")
			}
			return &selectResult{Provider: initialProvider}, nil
		},
		selectExcludingFunc: func(_ context.Context, req *model.SelectRequest, excludeIDs map[string]bool) (*model.Provider, error) {
			atomic.AddInt32(&selectExcludingCalls, 1)
			if !excludeIDs[initialProvider.ID] {
				t.Fatalf("excludeIDs = %+v, want %q excluded", excludeIDs, initialProvider.ID)
			}
			if req.FailoverContext == nil {
				t.Fatal("failover selection must receive failover context")
			}
			if req.FailoverContext.OriginProviderID != initialProvider.ID {
				t.Fatalf("OriginProviderID = %q, want %q", req.FailoverContext.OriginProviderID, initialProvider.ID)
			}
			return finalProvider, nil
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
		t.Fatalf("dial proxy websocket: %v", err)
	}

	if err := conn.Write(ctx, websocket.MessageText, []byte("ping")); err != nil {
		t.Fatalf("write client message: %v", err)
	}

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
	_ = conn.Close(websocket.StatusNormalClosure, "done")

	waitFor(t, func() bool {
		return store.LogsLen() > 0 && store.AttemptsLen() >= 2
	}, testPollTimeout)

	if got := atomic.LoadInt32(&initialAttempts); got != 1 {
		t.Fatalf("initial upstream attempts = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&finalAttempts); got != 1 {
		t.Fatalf("final upstream attempts = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&selectExcludingCalls); got != 1 {
		t.Fatalf("SelectExcluding calls = %d, want 1", got)
	}

	log := store.LastLog()
	if log == nil {
		t.Fatal("expected log entry")
	}
	if log.ProviderID != finalProvider.ID {
		t.Fatalf("ProviderID = %q, want %q", log.ProviderID, finalProvider.ID)
	}
	if log.RetryCount != 1 {
		t.Fatalf("RetryCount = %d, want 1", log.RetryCount)
	}

	attempts := store.LastAttempts(2)
	if len(attempts) != 2 {
		t.Fatalf("expected 2 attempts, got %d", len(attempts))
	}
	if attempts[0].ProviderID != initialProvider.ID || attempts[1].ProviderID != finalProvider.ID {
		t.Fatalf("attempt provider order = [%s %s], want [%s %s]", attempts[0].ProviderID, attempts[1].ProviderID, initialProvider.ID, finalProvider.ID)
	}
	if attempts[0].StatusCode != http.StatusUpgradeRequired {
		t.Fatalf("first attempt status = %d, want %d", attempts[0].StatusCode, http.StatusUpgradeRequired)
	}
	if attempts[0].SwitchReason != string(model.TerminalUpstreamHandshakeRejected) {
		t.Fatalf("first attempt switch reason = %q, want %q", attempts[0].SwitchReason, model.TerminalUpstreamHandshakeRejected)
	}
}

func TestHandler_ServeHTTP_WebSocket_PreAcceptTransportFailoverSwitchesProvider(t *testing.T) {
	var finalAttempts int32

	finalUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&finalAttempts, 1)
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept upstream websocket: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		conn.SetReadLimit(wsReadLimit)

		if _, _, err := conn.Read(r.Context()); err != nil {
			t.Errorf("read transport failover client message: %v", err)
			return
		}

		if err := conn.Write(r.Context(), websocket.MessageText, []byte(`{"type":"response.created"}`)); err != nil {
			t.Errorf("write upstream event: %v", err)
		}
	}))
	defer finalUpstream.Close()

	initialProvider := &model.Provider{
		ID:       "ws-transport-p1",
		Name:     "WS Transport P1",
		APIKey:   "key-1",
		AuthMode: "bearer",
		Enabled:  true,
		APITypes: []model.ProviderAPIType{{ProviderID: "ws-transport-p1", APIType: "codex", BaseURL: "https://ws-transport.invalid"}},
	}
	finalProvider := &model.Provider{
		ID:       "ws-transport-p2",
		Name:     "WS Transport P2",
		APIKey:   "key-2",
		AuthMode: "bearer",
		Enabled:  true,
		APITypes: []model.ProviderAPIType{{ProviderID: "ws-transport-p2", APIType: "codex", BaseURL: finalUpstream.URL}},
	}

	store := newMockStore()
	store.providers = []model.Provider{*initialProvider, *finalProvider}

	mockSel := &mockSelector{
		selectWithMetadataFunc: func(_ context.Context, _ *model.SelectRequest) (*selectResult, error) {
			return &selectResult{Provider: initialProvider}, nil
		},
		selectExcludingFunc: func(_ context.Context, req *model.SelectRequest, excludeIDs map[string]bool) (*model.Provider, error) {
			if !excludeIDs[initialProvider.ID] {
				t.Fatalf("excludeIDs = %+v, want %q excluded", excludeIDs, initialProvider.ID)
			}
			if req.FailoverContext == nil || req.FailoverContext.OriginProviderID != initialProvider.ID {
				t.Fatalf("unexpected failover context: %+v", req.FailoverContext)
			}
			return finalProvider, nil
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
				if strings.Contains(url, "ws-transport.invalid") {
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
		t.Fatalf("dial proxy websocket: %v", err)
	}

	if err := conn.Write(ctx, websocket.MessageText, []byte("ping")); err != nil {
		t.Fatalf("write client message: %v", err)
	}

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
	_ = conn.Close(websocket.StatusNormalClosure, "done")

	waitFor(t, func() bool {
		return store.LogsLen() > 0 && store.AttemptsLen() >= 2
	}, testPollTimeout)

	if got := atomic.LoadInt32(&finalAttempts); got != 1 {
		t.Fatalf("final upstream attempts = %d, want 1", got)
	}

	log := store.LastLog()
	if log == nil {
		t.Fatal("expected log entry")
	}
	if log.ProviderID != finalProvider.ID {
		t.Fatalf("ProviderID = %q, want %q", log.ProviderID, finalProvider.ID)
	}
	if log.RetryCount != 1 {
		t.Fatalf("RetryCount = %d, want 1", log.RetryCount)
	}

	attempts := store.LastAttempts(2)
	if len(attempts) != 2 {
		t.Fatalf("expected 2 attempts, got %d", len(attempts))
	}
	if attempts[0].StatusCode != StatusCodeNoResponse {
		t.Fatalf("first attempt status = %d, want %d", attempts[0].StatusCode, StatusCodeNoResponse)
	}
	if attempts[0].SwitchReason != string(model.TerminalUpstreamTransportError) {
		t.Fatalf("first attempt switch reason = %q, want %q", attempts[0].SwitchReason, model.TerminalUpstreamTransportError)
	}
	if attempts[1].ProviderID != finalProvider.ID {
		t.Fatalf("second attempt provider = %q, want %q", attempts[1].ProviderID, finalProvider.ID)
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

	_, resp, err := websocket.Dial(ctx, wsURL(proxyServer)+"/responses", nil)
	if err == nil {
		t.Fatal("expected sticky-selected websocket dial to fail before upgrade")
	}
	if resp == nil {
		t.Fatal("expected HTTP response from proxy")
	}
	if resp.StatusCode != http.StatusUpgradeRequired {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUpgradeRequired)
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
	if got := atomic.LoadInt32(&selectRetryCalls); got != 1 {
		t.Fatalf("retry selections = %d, want 1", got)
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
	if attempts[1].SwitchReason != "" {
		t.Fatalf("second attempt switch reason = %q, want empty", attempts[1].SwitchReason)
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
