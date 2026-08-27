package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/codex/http"
	"github.com/doraemonkeys/switch-a/internal/codex/startup"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/providerauth"

	"go.uber.org/zap"
)

const headerHygieneRequestID = "logical-request-42"

func TestHandlerHTTPAttemptsStartFromCleanHeadersOnCredentialRefresh(t *testing.T) {
	var calls atomic.Int32
	captured := make(chan http.Header, 4)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured <- r.Header.Clone()
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("refreshed"))
	}))
	defer upstream.Close()

	provider := headerHygieneTestProvider("provider", upstream.URL, AuthModeBearer, "unused", 0)
	store := newMockStore()
	store.providers = []model.Provider{provider}
	auth := &headerHygieneRefreshAuthenticator{}
	handler := NewHandler(Config{
		Store: store, Auth: auth, CodexHTTP: headerHygieneCodexRuntime(true), Logger: zap.NewNop(),
	})

	request := dirtyHeaderHygieneRequest()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK || response.Body.String() != "refreshed" {
		t.Fatalf("gateway response = (%d, %q), want (200, refreshed)", response.Code, response.Body.String())
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("upstream calls = %d, want 2", got)
	}
	initial, refreshed := <-captured, <-captured
	assertAttemptHeaders(t, initial, "Bearer stale-token", "", "actual-account")
	assertAttemptHeaders(t, refreshed, "", "fresh-key", "")
	if auth.ApplyCalls() != 2 {
		t.Fatalf("credential apply calls = %d, want 2", auth.ApplyCalls())
	}
}

func TestHandlerHTTPAttemptsStartFromCleanHeadersOnSameProviderRetry(t *testing.T) {
	var calls atomic.Int32
	captured := make(chan http.Header, 4)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured <- r.Header.Clone()
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("retried"))
	}))
	defer upstream.Close()

	provider := headerHygieneTestProvider("provider", upstream.URL, AuthModeBearer, "provider-key", 1)
	store := newMockStore()
	store.providers = []model.Provider{provider}
	selector := &mockSelector{
		selectWithMetadataFunc: func(context.Context, *model.SelectRequest) (*selectResult, error) {
			return &selectResult{Provider: &provider}, nil
		},
	}
	handler := NewHandler(Config{
		Store:     store,
		Selector:  selector,
		CodexHTTP: headerHygieneCodexRuntime(true),
		Logger:    zap.NewNop(),
	})

	request := dirtyHeaderHygieneRequest()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK || response.Body.String() != "retried" {
		t.Fatalf("gateway response = (%d, %q), want (200, retried)", response.Code, response.Body.String())
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("upstream calls = %d, want 2", got)
	}
	assertAttemptHeaders(t, <-captured, "Bearer provider-key", "", "")
	assertAttemptHeaders(t, <-captured, "Bearer provider-key", "", "")
}

func TestHandlerHTTPAttemptsStartFromCleanHeadersOnProviderSwitch(t *testing.T) {
	primaryHeaders := make(chan http.Header, 4)
	primaryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryHeaders <- r.Header.Clone()
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer primaryServer.Close()

	fallbackHeaders := make(chan http.Header, 4)
	fallbackServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackHeaders <- r.Header.Clone()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("fallback"))
	}))
	defer fallbackServer.Close()

	primary := headerHygieneTestProvider("primary", primaryServer.URL, AuthModeBearer, "primary-key", 0)
	fallback := headerHygieneTestProvider("fallback", fallbackServer.URL, AuthModeXAPI, "fallback-key", 0)
	store := newMockStore()
	store.configs[ConfigKeyGlobalMaxAttempts] = "2"
	store.providers = []model.Provider{primary, fallback}
	selector := &mockSelector{
		selectWithMetadataFunc: func(context.Context, *model.SelectRequest) (*selectResult, error) {
			return &selectResult{Provider: &primary}, nil
		},
		selectExcludingFunc: func(_ context.Context, _ *model.SelectRequest, excluded map[string]bool) (*model.Provider, error) {
			if !excluded[primary.ID] {
				t.Fatalf("excluded providers = %#v, want primary excluded", excluded)
			}
			return &fallback, nil
		},
	}
	handler := NewHandler(Config{
		Store:     store,
		Selector:  selector,
		CodexHTTP: headerHygieneCodexRuntime(true),
		Logger:    zap.NewNop(),
	})

	request := dirtyHeaderHygieneRequest()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK || response.Body.String() != "fallback" {
		t.Fatalf("gateway response = (%d, %q), want (200, fallback)", response.Code, response.Body.String())
	}
	if len(primaryHeaders) != 1 || len(fallbackHeaders) != 1 {
		t.Fatalf("physical attempts = primary:%d fallback:%d, want 1 each", len(primaryHeaders), len(fallbackHeaders))
	}
	assertAttemptHeaders(t, <-primaryHeaders, "Bearer primary-key", "", "")
	assertAttemptHeaders(t, <-fallbackHeaders, "", "fallback-key", "")
}

func TestHandlerHeaderHygieneRolloutIsCodexScoped(t *testing.T) {
	tests := []struct {
		name        string
		apiType     string
		hygiene     bool
		requestURL  string
		body        string
		wantAPIKey  string
		wantAccount string
	}{
		{name: "Codex enabled", apiType: APITypeCodex, hygiene: true, requestURL: "/codex/v1/responses", body: `{"model":"gpt-5"}`},
		{name: "Codex disabled", apiType: APITypeCodex, requestURL: "/codex/v1/responses", body: `{"model":"gpt-5"}`, wantAPIKey: "client-key", wantAccount: "client-account"},
		{name: "non-Codex enabled", apiType: "claude", hygiene: true, requestURL: "/v1/messages", body: `{"model":"claude-3"}`, wantAPIKey: "client-key", wantAccount: "client-account"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			captured := make(chan http.Header, 1)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				captured <- r.Header.Clone()
				w.WriteHeader(http.StatusOK)
			}))
			defer upstream.Close()

			provider := headerHygieneTestProviderForAPI("provider", upstream.URL, test.apiType, AuthModeBearer, "provider-key", 0)
			store := newMockStore()
			store.providers = []model.Provider{provider}
			handler := NewHandler(Config{
				Store: store, Auth: providerauth.NewService(providerauth.Config{}),
				CodexHTTP: headerHygieneCodexRuntime(test.hygiene), Logger: zap.NewNop(),
			})
			request := dirtyHeaderHygieneRequestFor(test.requestURL, test.body)
			handler.ServeHTTP(httptest.NewRecorder(), request)

			headers := <-captured
			assertAttemptHeaders(t, headers, "Bearer provider-key", test.wantAPIKey, test.wantAccount)
		})
	}
}

func headerHygieneTestProvider(id, baseURL, authMode, apiKey string, maxRetries int) model.Provider {
	return headerHygieneTestProviderForAPI(id, baseURL, APITypeCodex, authMode, apiKey, maxRetries)
}

func headerHygieneTestProviderForAPI(id, baseURL, apiType, authMode, apiKey string, maxRetries int) model.Provider {
	return withTestStaticCredential(model.Provider{
		ID:         id,
		Name:       id,
		AuthMode:   authMode,
		Enabled:    true,
		MaxRetries: maxRetries,
		APITypes: []model.ProviderAPIType{{
			ProviderID: id,
			APIType:    apiType,
			BaseURL:    baseURL,
		}},
	}, apiType, apiKey)
}

func dirtyHeaderHygieneRequest() *http.Request {
	return dirtyHeaderHygieneRequestFor("/codex/v1/responses", `{"model":"gpt-5"}`)
}

func dirtyHeaderHygieneRequestFor(requestURL, body string) *http.Request {
	request := httptest.NewRequest(
		http.MethodPost,
		requestURL,
		strings.NewReader(body),
	)
	request.Header = http.Header{
		"Content-Type":        {"application/json"},
		"authorization":       {"Bearer client-token"},
		"X-API-KEY":           {"client-key"},
		"chatgpt-account-id":  {"client-account"},
		"X-Client-Request-Id": {headerHygieneRequestID},
		"X-Ordinary":          {"preserved"},
	}
	return request
}

func headerHygieneCodexRuntime(enabled bool) *codexhttp.Runtime {
	return codexhttp.New(codexhttp.Config{Features: codexhttp.FeatureSourceFunc(func() codexstartup.Snapshot {
		return codexstartup.Snapshot{UpstreamHeaderHygiene: enabled}
	})})
}

func assertAttemptHeaders(t *testing.T, headers http.Header, authorization, apiKey, accountID string) {
	t.Helper()
	for name, want := range map[string]string{
		"Authorization":       authorization,
		"X-Api-Key":           apiKey,
		"ChatGPT-Account-Id":  accountID,
		"X-Client-Request-Id": headerHygieneRequestID,
		"X-Ordinary":          "preserved",
	} {
		if got := headers.Get(name); got != want {
			t.Errorf("%s = %q, want %q; headers=%#v", name, got, want, headers)
		}
	}
}

type headerHygieneRefreshAuthenticator struct {
	mu         sync.Mutex
	refreshed  bool
	applyCalls int
}

func (a *headerHygieneRefreshAuthenticator) ApplyProviderCredentials(
	_ context.Context,
	headers http.Header,
	_ testAuthCandidate,
	_, _ string,
	_ *http.Request,
	_ *testUpstreamURL,
) (testAppliedIdentity, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.applyCalls++
	if a.refreshed {
		headers.Set("X-Api-Key", "fresh-key")
		return testAppliedIdentity{}, nil
	}
	headers.Set("Authorization", "Bearer stale-token")
	headers.Set("ChatGPT-Account-Id", "actual-account")
	return testAppliedIdentity{}, nil
}

func (a *headerHygieneRefreshAuthenticator) RefreshCredentialSession(
	_ context.Context,
	_ testCredentialSnapshot,
) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.refreshed = true
	return true, nil
}

func (a *headerHygieneRefreshAuthenticator) ApplyCalls() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.applyCalls
}
