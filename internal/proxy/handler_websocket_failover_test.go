package proxy

import (
	"context"
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

func TestHandler_ServeHTTP_WebSocket_NoProvider(t *testing.T) {
	store := newMockStore()

	handler := NewHandler(Config{
		Store:  store,
		Logger: zap.NewNop(),
	})

	proxyServer := httptest.NewServer(handler)
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(proxyServer)+"/responses", nil)
	if err != nil {
		t.Fatalf("dial websocket through proxy: %v", err)
	}
	event := readTerminalGatewayErrorEvent(t, ctx, conn, http.StatusServiceUnavailable, ErrCodeProviderUnavailable)
	if !strings.Contains(event.Error.Message, "No available provider") {
		t.Fatalf("gateway error message = %q, want no-provider detail", event.Error.Message)
	}

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
			errorSnippet: "base_url",
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
			errorSnippet: "api_key",
		},
		{
			name: "chatgpt provider without auth service",
			provider: model.Provider{
				ID:             "ws-chatgpt-no-auth",
				Name:           "ChatGPT Without Auth Service",
				Enabled:        true,
				CredentialType: model.ProviderCredentialTypeChatGPT,
				Credential: model.ProviderCredentialFromLegacy(
					"ws-chatgpt-no-auth",
					model.ProviderCredentialTypeChatGPT,
					testChatGPTCredentialData(t, "access-token", "refresh-token", "acct-test"),
				),
				APITypes: []model.ProviderAPIType{{
					ProviderID: "ws-chatgpt-no-auth",
					APIType:    "codex",
					BaseURL:    "https://example.invalid",
				}},
			},
			errorSnippet: "credentials",
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

			conn, _, err := websocket.Dial(ctx, wsURL(proxyServer)+"/responses", nil)
			if err != nil {
				t.Fatalf("dial websocket through proxy: %v", err)
			}
			event := readTerminalGatewayErrorEvent(t, ctx, conn, http.StatusBadGateway, ErrCodeWebSocketUpgrade)
			if !strings.Contains(event.Error.Message, tt.errorSnippet) {
				t.Fatalf("gateway error message = %q, want snippet %q", event.Error.Message, tt.errorSnippet)
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

func TestHandler_ServeHTTP_WebSocket_ProviderConfigurationFailureBeforeVisibleSwitchesProvider(t *testing.T) {
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

	conn, _, err := websocket.Dial(ctx, wsURL(proxyServer)+"/responses", nil)
	if err != nil {
		t.Fatalf("dial websocket through proxy: %v", err)
	}
	readTerminalGatewayErrorEvent(t, ctx, conn, http.StatusOK, ErrCodeWebSocketUpgrade)

	waitFor(t, func() bool { return store.LogsLen() > 0 }, testPollTimeout)
	waitFor(t, func() bool { return store.AttemptsLen() > 0 }, testPollTimeout)

	if got := atomic.LoadInt32(&selectRetryCalls); got != 2 {
		t.Fatalf("retry selections = %d, want 2 (fallback attempt plus exhaustion check)", got)
	}
	if got := atomic.LoadInt32(&fallbackHits); got != 1 {
		t.Fatalf("fallback hits = %d, want 1", got)
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
	if log.StatusCode != http.StatusOK {
		t.Fatalf("log.StatusCode = %d, want %d", log.StatusCode, http.StatusOK)
	}

	attempts := store.LastAttempts(2)
	if len(attempts) != 2 {
		t.Fatalf("expected 2 attempts, got %d", len(attempts))
	}
	if attempts[0].ProviderID != providerPrimary.ID {
		t.Fatalf("attempt.ProviderID = %q, want %q", attempts[0].ProviderID, providerPrimary.ID)
	}
	if attempts[0].SwitchReason != string(model.TerminalProviderConfigurationError) {
		t.Fatalf("first attempt switch reason = %q, want %q", attempts[0].SwitchReason, model.TerminalProviderConfigurationError)
	}
	if attempts[1].ProviderID != providerFallback.ID {
		t.Fatalf("attempt.ProviderID = %q, want %q", attempts[1].ProviderID, providerFallback.ID)
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

	conn, _, err := websocket.Dial(ctx, wsURL(proxyServer)+"/responses", &websocket.DialOptions{
		HTTPHeader: http.Header{
			"OpenAI-Beta": {"responses_websockets=2026-02-06"},
		},
	})
	if err != nil {
		t.Fatalf("dial websocket through proxy: %v", err)
	}
	event := readTerminalGatewayErrorEvent(t, ctx, conn, http.StatusUpgradeRequired, ErrCodeWebSocketUpgrade)
	if !strings.Contains(event.Error.Message, "fallback to http") {
		t.Fatalf("gateway error message = %q, want upstream fallback detail", event.Error.Message)
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
		Credential: model.ProviderCredentialFromLegacy(
			"ws-chatgpt-refresh",
			model.ProviderCredentialTypeChatGPT,
			testChatGPTCredentialData(t, "access-old", "refresh-old", "acct-123"),
		),
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
