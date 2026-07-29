package proxy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"

	"go.uber.org/zap"
)

// Test timeout constants for consistent timing across tests.
const (
	testPollTimeout      = 100 * time.Millisecond
	testResponseMaxDur   = 500 * time.Millisecond
	testSlowDBDelay      = 5 * time.Second
	testTimeoutWaitDelay = 3 * time.Second
)

// waitFor polls a condition until it returns true or timeout is reached.
// This avoids flaky tests that rely on fixed time.Sleep durations.
func waitFor(t *testing.T, cond func() bool, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}

// mockStore implements the Store interface for testing.
// It is thread-safe to allow concurrent use with async logRequest goroutines.
// Thread-safety is critical here because logs are inserted via `go h.logRequest(...)`
// which runs concurrently with test assertions. Without mutex protection, test assertions
// reading logs could race with the background goroutine writing logs, causing flaky tests
// or data races detectable by the race detector.
type mockStore struct {
	mu               sync.Mutex
	providers        []model.Provider
	configs          map[string]string
	authStates       map[string]*model.ProviderAuthState
	routingPolicies  []model.RoutingPolicy
	routingPolicyErr error
	logs             []model.RequestLog
	attempts         []model.RequestAttempt // Captures attempts for verification
	err              error
}

func newMockStore() *mockStore {
	return &mockStore{
		providers: []model.Provider{},
		configs: map[string]string{
			ConfigKeyTrustProxyHeaders:      "true",
			ConfigKeyUserHeader:             "X-User-ID",
			ConfigKeyMaxBodySize:            "10",
			ConfigKeyAuthMode:               "auto",
			ConfigKeyGlobalMaxAttempts:      "0",
			ConfigKeyUpstreamConnectTimeout: "10",
			ConfigKeyUpstreamReadTimeout:    "0",
			ConfigKeyStickyMode:             "model",
			ConfigKeyStickyTTL:              "300",
		},
		authStates: make(map[string]*model.ProviderAuthState),
		logs:       []model.RequestLog{},
		attempts:   []model.RequestAttempt{},
	}
}

func stringPtr(value string) *string {
	return &value
}

func (m *mockStore) ListProvidersByAPIType(_ context.Context, _ string) ([]model.Provider, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.providers, nil
}

func (m *mockStore) GetConfig(_ context.Context, key string) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	return m.configs[key], nil
}

func (m *mockStore) InsertLog(_ context.Context, log *model.RequestLog) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	m.logs = append(m.logs, *log)
	return nil
}

func (m *mockStore) InsertAttempts(_ context.Context, attempts []model.RequestAttempt) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	m.attempts = append(m.attempts, attempts...)
	return nil
}

func (m *mockStore) GetProviderAuthState(_ context.Context, providerID string) (*model.ProviderAuthState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return nil, m.err
	}
	if authState, ok := m.authStates[providerID]; ok {
		return authState.Clone(), nil
	}
	return nil, nil
}

func (m *mockStore) ListRoutingPoliciesByAPIType(_ context.Context, apiType string) ([]model.RoutingPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.routingPolicyErr != nil {
		return nil, m.routingPolicyErr
	}
	if m.err != nil {
		return nil, m.err
	}
	policies := make([]model.RoutingPolicy, 0, len(m.routingPolicies))
	for _, policy := range m.routingPolicies {
		if policy.APIType == apiType {
			policies = append(policies, policy)
		}
	}
	return policies, nil
}

// AttemptsLen returns the number of attempts in a thread-safe manner.
func (m *mockStore) AttemptsLen() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.attempts)
}

// LastAttempts returns the most recent N attempt entries in a thread-safe manner.
// Returns nil if no attempts exist.
func (m *mockStore) LastAttempts(n int) []model.RequestAttempt {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.attempts) == 0 {
		return nil
	}
	if n > len(m.attempts) {
		n = len(m.attempts)
	}
	result := make([]model.RequestAttempt, n)
	copy(result, m.attempts[len(m.attempts)-n:])
	return result
}

// LogsLen returns the number of logs in a thread-safe manner.
func (m *mockStore) LogsLen() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.logs)
}

// LastLog returns the most recent log entry in a thread-safe manner.
// Returns nil if no logs exist.
func (m *mockStore) LastLog() *model.RequestLog {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.logs) == 0 {
		return nil
	}
	log := m.logs[len(m.logs)-1]
	return &log
}

func TestNewHandler_NilStorePanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic when Store is nil, but did not panic")
		} else {
			msg, ok := r.(string)
			if !ok || !strings.Contains(msg, "Store is required") {
				t.Errorf("unexpected panic message: %v", r)
			}
		}
	}()

	// This should panic
	NewHandler(Config{
		Store:  nil,
		Logger: zap.NewNop(),
	})
}

func TestNewHandler_NilLoggerPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic when Logger is nil, but did not panic")
		} else {
			msg, ok := r.(string)
			if !ok || !strings.Contains(msg, "Logger is required") {
				t.Errorf("unexpected panic message: %v", r)
			}
		}
	}()

	// This should panic
	NewHandler(Config{
		Store:  newMockStore(),
		Logger: nil,
	})
}

func TestHandler_ServeHTTP_UnknownAPIType(t *testing.T) {
	store := newMockStore()
	logger := zap.NewNop()

	handler := NewHandler(Config{
		Store:  store,
		Logger: logger,
	})

	req := httptest.NewRequest(http.MethodPost, "/unknown/path", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	var errResp model.GatewayError
	if err := json.NewDecoder(w.Body).Decode(&errResp); err != nil {
		t.Fatalf("failed to decode error response: %v", err)
	}
	if errResp.Error.Code != ErrCodeUnknownAPIType {
		t.Errorf("error code = %q, want %q", errResp.Error.Code, ErrCodeUnknownAPIType)
	}
}

func TestHandler_ServeHTTP_NoProviders(t *testing.T) {
	store := newMockStore()
	store.providers = []model.Provider{} // No providers
	logger := zap.NewNop()

	handler := NewHandler(Config{
		Store:  store,
		Logger: logger,
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"test"}`))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}

	var errResp model.GatewayError
	if err := json.NewDecoder(w.Body).Decode(&errResp); err != nil {
		t.Fatalf("failed to decode error response: %v", err)
	}
	if errResp.Error.Code != ErrCodeProviderUnavailable {
		t.Errorf("error code = %q, want %q", errResp.Error.Code, ErrCodeProviderUnavailable)
	}

	waitFor(t, func() bool {
		return store.LogsLen() > 0
	}, testPollTimeout)

	log := store.LastLog()
	if log == nil {
		t.Fatal("expected log entry")
	}
	if requestLogClientTransportStatusCode(log) != http.StatusServiceUnavailable {
		t.Fatalf("ClientTransportStatusCode = %d, want %d", requestLogClientTransportStatusCode(log), http.StatusServiceUnavailable)
	}
	if requestLogServiceOutcome(log) != model.ServiceOutcomeNeverStarted {
		t.Fatalf("ServiceOutcome = %q, want %q", requestLogServiceOutcome(log), model.ServiceOutcomeNeverStarted)
	}
	if requestLogCompletionState(log) != model.CompletionStateIncomplete {
		t.Fatalf("CompletionState = %q, want %q", requestLogCompletionState(log), model.CompletionStateIncomplete)
	}
}

func TestHandler_ServeHTTP_BodyTooLarge(t *testing.T) {
	store := newMockStore()
	store.configs[ConfigKeyMaxBodySize] = "1" // 1MB limit
	store.providers = []model.Provider{
		{ID: "p1", APIKey: "key", APITypes: []model.ProviderAPIType{{ProviderID: "p1", APIType: "claude", BaseURL: "https://api.example.com"}}},
	}
	logger := zap.NewNop()

	handler := NewHandler(Config{
		Store:  store,
		Logger: logger,
	})

	// Create body larger than 1MB
	largeBody := strings.Repeat("x", 2*1024*1024)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(largeBody))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want %d", w.Code, http.StatusRequestEntityTooLarge)
	}

	var errResp model.GatewayError
	if err := json.NewDecoder(w.Body).Decode(&errResp); err != nil {
		t.Fatalf("failed to decode error response: %v", err)
	}
	if errResp.Error.Code != ErrCodeBodyTooLarge {
		t.Errorf("error code = %q, want %q", errResp.Error.Code, ErrCodeBodyTooLarge)
	}
}

func TestHandler_ServeHTTP_EmptyBaseURL_FailsFast(t *testing.T) {
	// Provider has APIType "codex" but request is for "claude" - no matching BaseURL.
	// The proxy should fail fast instead of forwarding to an invalid URL.
	store := newMockStore()
	store.configs[ConfigKeyGlobalMaxAttempts] = "1"
	store.providers = []model.Provider{
		{
			ID:       "p1",
			Name:     "Wrong API Type Provider",
			APIKey:   "test-api-key",
			AuthMode: "bearer",
			Enabled:  true,
			APITypes: []model.ProviderAPIType{{ProviderID: "p1", APIType: "codex", BaseURL: "https://api.example.com"}},
		},
	}
	logger := zap.NewNop()

	handler := NewHandler(Config{
		Store:  store,
		Logger: logger,
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"test"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	// Should return an error (exhausted retries) since provider has no base_url for claude
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}

	// Wait for async log
	waitFor(t, func() bool {
		return store.LogsLen() > 0
	}, testPollTimeout)

	log := store.LastLog()
	if log == nil {
		t.Fatal("expected log entry")
	}
	if requestLogClientTransportStatusCode(log) != http.StatusServiceUnavailable {
		t.Fatalf("ClientTransportStatusCode = %d, want %d", requestLogClientTransportStatusCode(log), http.StatusServiceUnavailable)
	}
	if requestLogServiceOutcome(log) != model.ServiceOutcomeNeverStarted {
		t.Fatalf("ServiceOutcome = %q, want %q", requestLogServiceOutcome(log), model.ServiceOutcomeNeverStarted)
	}
	if requestLogCompletionState(log) != model.CompletionStateIncomplete {
		t.Fatalf("CompletionState = %q, want %q", requestLogCompletionState(log), model.CompletionStateIncomplete)
	}
}

func TestHandler_ServeHTTP_SuccessfulProxy(t *testing.T) {
	// Create upstream server
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify auth header is set
		if r.Header.Get("Authorization") == "" && r.Header.Get("x-api-key") == "" {
			t.Error("expected auth header to be set")
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"response":"success"}`))
	}))
	defer upstreamServer.Close()

	store := newMockStore()
	store.providers = []model.Provider{
		{
			ID:       "p1",
			Name:     "Test Provider",
			APIKey:   "test-api-key",
			AuthMode: "bearer",
			Enabled:  true,
			APITypes: []model.ProviderAPIType{{ProviderID: "p1", APIType: "claude", BaseURL: upstreamServer.URL}},
		},
	}
	logger := zap.NewNop()

	handler := NewHandler(Config{
		Store:  store,
		Logger: logger,
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-3","message":"hello"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-ID", "testuser")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	body := w.Body.String()
	if !strings.Contains(body, "success") {
		t.Errorf("body = %q, should contain 'success'", body)
	}

	// Wait for async log using polling helper
	waitFor(t, func() bool {
		return store.LogsLen() > 0
	}, testPollTimeout)

	// Verify attempts were recorded for retry tracking (TEST-003)
	waitFor(t, func() bool {
		return store.AttemptsLen() > 0
	}, testPollTimeout)

	attempts := store.LastAttempts(1)
	if len(attempts) != 1 {
		t.Fatalf("expected 1 attempt, got %d", len(attempts))
	}
	if attempts[0].ProviderID != "p1" {
		t.Errorf("attempt provider_id = %q, want %q", attempts[0].ProviderID, "p1")
	}
	if attempts[0].StatusCode != http.StatusOK {
		t.Errorf("attempt status_code = %d, want %d", attempts[0].StatusCode, http.StatusOK)
	}
	if attempts[0].SemanticsVersion != model.RequestSemanticsVersionNormalizedV1 {
		t.Errorf("attempt semantics_version = %q, want %q", attempts[0].SemanticsVersion, model.RequestSemanticsVersionNormalizedV1)
	}
	// Attempt number is 0-indexed (first attempt = 0)
	if attempts[0].Attempt != 0 {
		t.Errorf("attempt number = %d, want 0 (first attempt)", attempts[0].Attempt)
	}
}

func TestHandler_ServeHTTP_UsesAPITypeKeyOverride(t *testing.T) {
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer claude-override-key" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer claude-override-key")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"response":"success"}`))
	}))
	defer upstreamServer.Close()

	store := newMockStore()
	store.providers = []model.Provider{{
		ID:       "p1",
		Name:     "Test Provider",
		APIKey:   "default-key",
		AuthMode: "bearer",
		Enabled:  true,
		APITypes: []model.ProviderAPIType{{
			ProviderID: "p1",
			APIType:    "claude",
			BaseURL:    upstreamServer.URL,
			APIKey:     "claude-override-key",
		}},
	}}
	logger := zap.NewNop()

	handler := NewHandler(Config{
		Store:  store,
		Logger: logger,
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-3","message":"hello"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandler_ServeHTTP_SSEProxy(t *testing.T) {
	// Create upstream server with SSE
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("ResponseWriter doesn't support Flusher")
		}
		_, _ = w.Write([]byte("data: {\"event\":1}\n\n"))
		flusher.Flush()
		_, _ = w.Write([]byte("data: {\"event\":2}\n\n"))
		flusher.Flush()
	}))
	defer upstreamServer.Close()

	store := newMockStore()
	store.providers = []model.Provider{
		{
			ID:       "p1",
			Name:     "Test Provider",
			APIKey:   "test-api-key",
			AuthMode: "bearer",
			Enabled:  true,
			APITypes: []model.ProviderAPIType{{ProviderID: "p1", APIType: "claude", BaseURL: upstreamServer.URL}},
		},
	}
	logger := zap.NewNop()

	handler := NewHandler(Config{
		Store:  store,
		Logger: logger,
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-3","stream":true}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	if w.Header().Get("Content-Type") != "text/event-stream" {
		t.Errorf("Content-Type = %q, want %q", w.Header().Get("Content-Type"), "text/event-stream")
	}

	body := w.Body.String()
	if !strings.Contains(body, "event") {
		t.Errorf("body should contain SSE events, got %q", body)
	}
}

func TestShouldFailover(t *testing.T) {
	tests := []struct {
		statusCode int
		want       bool
	}{
		{200, false},
		{201, false},
		{400, false},
		{401, true},  // Unauthorized - provider misconfiguration, try another
		{402, true},  // Payment Required - quota exhausted, try another
		{403, true},  // Forbidden - provider misconfiguration, try another
		{404, false}, // Not Found - client error, don't failover
		{429, true},  // Rate limit
		{500, true},  // Server error
		{502, true},  // Bad gateway
		{503, true},  // Service unavailable
		{504, true},  // Gateway timeout
	}

	for _, tt := range tests {
		got := shouldFailover(tt.statusCode)
		if got != tt.want {
			t.Errorf("shouldFailover(%d) = %v, want %v", tt.statusCode, got, tt.want)
		}
	}
}

func TestShouldForceProviderSwitch(t *testing.T) {
	tests := []struct {
		statusCode int
		want       bool
	}{
		{200, false},
		{201, false},
		{400, false},
		{401, true},  // Unauthorized - permanent failure, switch provider immediately
		{402, true},  // Payment Required - permanent failure, switch provider immediately
		{403, true},  // Forbidden - permanent failure, switch provider immediately
		{404, false}, // Not Found - client error
		{429, false}, // Rate limit - transient, may succeed on retry with same provider
		{500, false}, // Server error - transient, may succeed on retry with same provider
		{502, false}, // Bad gateway - transient
		{503, false}, // Service unavailable - transient
		{504, false}, // Gateway timeout - transient
	}

	for _, tt := range tests {
		got := shouldForceProviderSwitch(tt.statusCode)
		if got != tt.want {
			t.Errorf("shouldForceProviderSwitch(%d) = %v, want %v", tt.statusCode, got, tt.want)
		}
	}
}

func TestHandler_LogsGatewayTransportStatus_ForFailoverStatusCodes(t *testing.T) {
	// Failover-eligible upstream responses are retried or exhausted before the client sees them,
	// so the persisted transport status must reflect the gateway's final 503 instead.
	tests := []struct {
		name       string
		statusCode int
	}{
		{"401_unauthorized", http.StatusUnauthorized},
		{"402_payment_required", http.StatusPaymentRequired},
		{"403_forbidden", http.StatusForbidden},
		{"429_too_many_requests", http.StatusTooManyRequests},
		{"500_internal_server_error", http.StatusInternalServerError},
		{"502_bad_gateway", http.StatusBadGateway},
		{"503_service_unavailable", http.StatusServiceUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create upstream server that returns the test status code
			upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(`{"error":"test error"}`))
			}))
			defer upstreamServer.Close()

			store := newMockStore()
			store.configs[ConfigKeyGlobalMaxAttempts] = "1" // Limit to a single upstream attempt
			store.providers = []model.Provider{
				{
					ID:       "p1",
					Name:     "Test Provider",
					APIKey:   "test-api-key",
					AuthMode: "bearer",
					Enabled:  true,
					APITypes: []model.ProviderAPIType{{ProviderID: "p1", APIType: "claude", BaseURL: upstreamServer.URL}},
				},
			}
			logger := zap.NewNop()

			handler := NewHandler(Config{
				Store:  store,
				Logger: logger,
			})

			req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"test"}`))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			// Wait for async log
			waitFor(t, func() bool {
				return store.LogsLen() > 0
			}, testPollTimeout)

			// Verify log entry has success=false
			log := store.LastLog()
			if log == nil {
				t.Fatal("expected log entry to be created")
			}
			if requestLogServiceOutcome(log) == model.ServiceOutcomeCompleted {
				t.Errorf("expected pre-start outcome for upstream status %d", tt.statusCode)
			}
			if requestLogServiceOutcome(log) != model.ServiceOutcomeNeverStarted {
				t.Errorf("ServiceOutcome = %q, want %q for upstream status %d", requestLogServiceOutcome(log), model.ServiceOutcomeNeverStarted, tt.statusCode)
			}
			if requestLogClientTransportStatusCode(log) != http.StatusServiceUnavailable {
				t.Errorf("expected ClientTransportStatusCode=%d, got %d", http.StatusServiceUnavailable, requestLogClientTransportStatusCode(log))
			}
		})
	}
}

func TestHandler_LogsSuccessTrue_For2xxStatusCodes(t *testing.T) {
	// Create upstream server that returns 200 OK
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"response":"success"}`))
	}))
	defer upstreamServer.Close()

	store := newMockStore()
	store.providers = []model.Provider{
		{
			ID:       "p1",
			Name:     "Test Provider",
			APIKey:   "test-api-key",
			AuthMode: "bearer",
			Enabled:  true,
			APITypes: []model.ProviderAPIType{{ProviderID: "p1", APIType: "claude", BaseURL: upstreamServer.URL}},
		},
	}
	logger := zap.NewNop()

	handler := NewHandler(Config{
		Store:  store,
		Logger: logger,
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"test"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	// Wait for async log
	waitFor(t, func() bool {
		return store.LogsLen() > 0
	}, testPollTimeout)

	// Verify log entry has success=true
	log := store.LastLog()
	if log == nil {
		t.Fatal("expected log entry to be created")
	}
	if requestLogServiceOutcome(log) != model.ServiceOutcomeCompleted {
		t.Error("expected completed outcome for status 200")
	}
	if requestLogClientTransportStatusCode(log) != http.StatusOK {
		t.Errorf("expected ClientTransportStatusCode=200, got %d", requestLogClientTransportStatusCode(log))
	}
}

func TestHandler_LogsNeverStartedOutcome_ForNonRetryable4xxStatusCodes(t *testing.T) {
	// A proxied 4xx request rejection writes a client response but still ends before
	// the upstream service actually starts.
	tests := []struct {
		name       string
		statusCode int
	}{
		{"400_bad_request", http.StatusBadRequest},
		{"404_not_found", http.StatusNotFound},
		{"422_unprocessable_entity", http.StatusUnprocessableEntity},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create upstream server that returns the test status code
			upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(`{"error":"client error"}`))
			}))
			defer upstreamServer.Close()

			store := newMockStore()
			store.providers = []model.Provider{
				{
					ID:       "p1",
					Name:     "Test Provider",
					APIKey:   "test-api-key",
					AuthMode: "bearer",
					Enabled:  true,
					APITypes: []model.ProviderAPIType{{ProviderID: "p1", APIType: "claude", BaseURL: upstreamServer.URL}},
				},
			}
			logger := zap.NewNop()

			handler := NewHandler(Config{
				Store:  store,
				Logger: logger,
			})

			req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"test"}`))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			// Wait for async log
			waitFor(t, func() bool {
				return store.LogsLen() > 0
			}, testPollTimeout)

			// Verify the rejection stays pre-start even though the response reached the client.
			log := store.LastLog()
			if log == nil {
				t.Fatal("expected log entry to be created")
			}
			if requestLogServiceOutcome(log) != model.ServiceOutcomeNeverStarted {
				t.Errorf("expected never_started outcome for status %d, got %q", tt.statusCode, requestLogServiceOutcome(log))
			}
			if requestLogCompletionState(log) != model.CompletionStateIncomplete {
				t.Errorf("expected CompletionState=%q, got %q", model.CompletionStateIncomplete, requestLogCompletionState(log))
			}
			if requestLogClientTransportStatusCode(log) != tt.statusCode {
				t.Errorf("expected ClientTransportStatusCode=%d, got %d", tt.statusCode, requestLogClientTransportStatusCode(log))
			}
			if requestLogTerminationReason(log) != model.TerminationReasonClientRequestError {
				t.Errorf("expected TerminationReason=%q, got %q", model.TerminationReasonClientRequestError, requestLogTerminationReason(log))
			}
		})
	}
}

func TestBuildFullURL(t *testing.T) {
	// Create a handler with a no-op logger for testing
	h := &Handler{logger: zap.NewNop()}

	tests := []struct {
		name    string
		baseURL string
		path    string
		query   string
		want    string
	}{
		{
			name:    "simple path",
			baseURL: "https://api.example.com",
			path:    "/v1/messages",
			query:   "",
			want:    "https://api.example.com/v1/messages",
		},
		{
			name:    "with query string",
			baseURL: "https://api.example.com",
			path:    "/v1/messages",
			query:   "stream=true",
			want:    "https://api.example.com/v1/messages?stream=true",
		},
		{
			name:    "base with trailing slash",
			baseURL: "https://api.example.com/",
			path:    "/v1/messages",
			query:   "",
			want:    "https://api.example.com/v1/messages",
		},
		{
			name:    "invalid base URL",
			baseURL: "://invalid",
			path:    "/v1/messages",
			query:   "",
			want:    "://invalid/v1/messages",
		},
		{
			name:    "base URL with existing path",
			baseURL: "https://api.openai.com/v1",
			path:    "/chat/completions",
			query:   "",
			want:    "https://api.openai.com/v1/chat/completions",
		},
		{
			name:    "base URL with existing path and query",
			baseURL: "https://api.openai.com/v1",
			path:    "/chat/completions",
			query:   "stream=true",
			want:    "https://api.openai.com/v1/chat/completions?stream=true",
		},
		{
			name:    "base URL with trailing slash and existing path",
			baseURL: "https://api.openai.com/v1/",
			path:    "/chat/completions",
			query:   "",
			want:    "https://api.openai.com/v1/chat/completions",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := h.buildFullURL(tt.baseURL, tt.path, tt.query)
			if got != tt.want {
				t.Errorf("buildFullURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseIntOrDefault(t *testing.T) {
	tests := []struct {
		s    string
		def  int
		want int
	}{
		{"10", 5, 10},
		{"", 5, 5},
		{"invalid", 5, 5},
		{"0", 5, 0},
		{"-1", 5, -1},
	}

	for _, tt := range tests {
		got := parseIntOrDefault(tt.s, tt.def)
		if got != tt.want {
			t.Errorf("parseIntOrDefault(%q, %d) = %d, want %d", tt.s, tt.def, got, tt.want)
		}
	}
}

func TestHandler_loadConfig(t *testing.T) {
	store := newMockStore()
	logger := zap.NewNop()

	handler := NewHandler(Config{
		Store:  store,
		Logger: logger,
	})

	cfg, err := handler.loadConfig(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !cfg.trustProxy {
		t.Error("expected trustProxy to be true")
	}
	if cfg.userHeader != "X-User-ID" {
		t.Errorf("userHeader = %q, want %q", cfg.userHeader, "X-User-ID")
	}
	if cfg.maxBodySizeMB != 10 {
		t.Errorf("maxBodySizeMB = %d, want %d", cfg.maxBodySizeMB, 10)
	}
	if cfg.globalAuthMode != "auto" {
		t.Errorf("globalAuthMode = %q, want %q", cfg.globalAuthMode, "auto")
	}
	if cfg.globalMaxAttempts != 0 {
		t.Errorf("globalMaxAttempts = %d, want %d", cfg.globalMaxAttempts, 0)
	}
}

func TestHandler_loadConfig_defaults(t *testing.T) {
	store := newMockStore()
	store.configs = map[string]string{} // Empty configs
	logger := zap.NewNop()

	handler := NewHandler(Config{
		Store:  store,
		Logger: logger,
	})

	cfg, err := handler.loadConfig(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check defaults are applied
	if cfg.userHeader != "X-User-ID" {
		t.Errorf("userHeader = %q, want default %q", cfg.userHeader, "X-User-ID")
	}
	if cfg.maxBodySizeMB != 128 {
		t.Errorf("maxBodySizeMB = %d, want default %d", cfg.maxBodySizeMB, 128)
	}
	if cfg.globalAuthMode != "auto" {
		t.Errorf("globalAuthMode = %q, want default %q", cfg.globalAuthMode, "auto")
	}
	if cfg.globalMaxAttempts != 0 {
		t.Errorf("globalMaxAttempts = %d, want default %d", cfg.globalMaxAttempts, 0)
	}
}

func TestParseInt64OrDefault(t *testing.T) {
	tests := []struct {
		s    string
		def  int64
		want int64
	}{
		{"10", 5, 10},
		{"", 5, 5},
		{"invalid", 5, 5},
		{"0", 5, 0},
		{"-1", 5, -1},
		{"9223372036854775807", 0, 9223372036854775807}, // max int64
	}

	for _, tt := range tests {
		got := parseInt64OrDefault(tt.s, tt.def)
		if got != tt.want {
			t.Errorf("parseInt64OrDefault(%q, %d) = %d, want %d", tt.s, tt.def, got, tt.want)
		}
	}
}

func TestParseBoolOrDefault(t *testing.T) {
	tests := []struct {
		name string
		s    string
		def  bool
		want bool
	}{
		// Empty string returns default
		{"empty_default_true", "", true, true},
		{"empty_default_false", "", false, false},

		// Valid true values
		{"true_lowercase", "true", false, true},
		{"true_uppercase", "TRUE", false, true},
		{"true_mixedcase", "True", false, true},
		{"one", "1", false, true},

		// Valid false values
		{"false_lowercase", "false", true, false},
		{"false_uppercase", "FALSE", true, false},
		{"false_mixedcase", "False", true, false},
		{"zero", "0", true, false},

		// Invalid values should return default (BUG FIX: previously returned false)
		{"invalid_default_true", "invalid", true, true},
		{"invalid_default_false", "invalid", false, false},
		{"yes_default_true", "yes", true, true},
		{"no_default_true", "no", true, true},
		{"garbage_default_true", "xyz123", true, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseBoolOrDefault(tt.s, tt.def)
			if got != tt.want {
				t.Errorf("parseBoolOrDefault(%q, %v) = %v, want %v", tt.s, tt.def, got, tt.want)
			}
		})
	}
}

func TestHandler_getTransport_caching(t *testing.T) {
	store := newMockStore()
	logger := zap.NewNop()

	handler := NewHandler(Config{
		Store:  store,
		Logger: logger,
	})

	cfg := &runtimeConfig{
		connectTimeout: 10 * time.Second,
		readTimeout:    30 * time.Second,
	}

	// First call should create transport
	t1 := handler.getTransport(cfg)
	if t1 == nil {
		t.Fatal("expected transport to be created")
	}

	// Second call with same config should return cached transport
	t2 := handler.getTransport(cfg)
	if t1 != t2 {
		t.Error("expected same transport instance for same config")
	}

	// Call with different config should create new transport
	cfg2 := &runtimeConfig{
		connectTimeout: 20 * time.Second,
		readTimeout:    30 * time.Second,
	}
	t3 := handler.getTransport(cfg2)
	if t1 == t3 {
		t.Error("expected new transport instance for different config")
	}
}

// slowMockStore is a mock store that delays InsertLog calls to test timeout behavior.
type slowMockStore struct {
	*mockStore
	insertDelay   time.Duration
	insertStarted chan struct{} // signals when InsertLog starts executing
	insertDone    chan struct{} // signals when InsertLog completes
}

func newSlowMockStore(delay time.Duration) *slowMockStore {
	return &slowMockStore{
		mockStore:   newMockStore(),
		insertDelay: delay,
		// Buffer of 1 allows signaling without blocking the test goroutine.
		// Non-blocking sends prevent deadlock if test doesn't wait for signal.
		insertStarted: make(chan struct{}, 1),
		insertDone:    make(chan struct{}, 1),
	}
}

func (s *slowMockStore) InsertLog(ctx context.Context, log *model.RequestLog) error {
	select {
	case s.insertStarted <- struct{}{}:
	default:
	}
	select {
	case <-time.After(s.insertDelay):
		// Delay completed, proceed with insert
		select {
		case s.insertDone <- struct{}{}:
		default:
		}
		return s.mockStore.InsertLog(ctx, log)
	case <-ctx.Done():
		// Context cancelled (timeout), return error
		return ctx.Err()
	}
}

func TestFirstWriteResponseWriter(t *testing.T) {
	var callCount int
	recorder := httptest.NewRecorder()

	w := &firstWriteResponseWriter{
		ResponseWriter: recorder,
		onFirstWrite: func() {
			callCount++
		},
	}

	// First write should trigger callback
	n, err := w.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 5 {
		t.Errorf("expected 5 bytes written, got %d", n)
	}
	if callCount != 1 {
		t.Errorf("expected callback to be called once, got %d", callCount)
	}

	// Second write should NOT trigger callback again
	n, err = w.Write([]byte("world"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 5 {
		t.Errorf("expected 5 bytes written, got %d", n)
	}
	if callCount != 1 {
		t.Errorf("expected callback to still be 1, got %d", callCount)
	}

	// Verify body content
	if recorder.Body.String() != "helloworld" {
		t.Errorf("expected body 'helloworld', got %q", recorder.Body.String())
	}
}

func TestFirstWriteResponseWriter_Flush(t *testing.T) {
	recorder := httptest.NewRecorder()

	w := &firstWriteResponseWriter{
		ResponseWriter: recorder,
		onFirstWrite:   func() {},
	}

	// Write and flush
	_, _ = w.Write([]byte("data: test\n\n"))
	w.Flush()

	// Verify flushed was set (httptest.ResponseRecorder tracks this)
	if !recorder.Flushed {
		t.Error("expected Flush to be called on underlying ResponseWriter")
	}
}

func TestFirstWriteResponseWriter_NilCallback(t *testing.T) {
	recorder := httptest.NewRecorder()

	w := &firstWriteResponseWriter{
		ResponseWriter: recorder,
		onFirstWrite:   nil, // No callback
	}

	// Should not panic with nil callback
	n, err := w.Write([]byte("test"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 4 {
		t.Errorf("expected 4 bytes written, got %d", n)
	}
}

func TestHandler_LogRequest_Timeout(t *testing.T) {
	// Test that logRequest respects the timeout when the database is slow.
	// The logInsertTimeout constant is 2 seconds, so we use a delay longer than that.
	// We use a shorter timeout for testing by checking that the goroutine doesn't block.

	// Create upstream server that returns 200 OK quickly
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstreamServer.Close()

	// Create a slow store that takes longer than logInsertTimeout (2s)
	// We use 5 seconds to ensure it exceeds the timeout
	store := newSlowMockStore(testSlowDBDelay)
	store.providers = []model.Provider{
		{
			ID:       "p1",
			Name:     "Test Provider",
			APIKey:   "test-api-key",
			AuthMode: "bearer",
			Enabled:  true,
			APITypes: []model.ProviderAPIType{{ProviderID: "p1", APIType: "claude", BaseURL: upstreamServer.URL}},
		},
	}
	logger := zap.NewNop()

	handler := NewHandler(Config{
		Store:  store,
		Logger: logger,
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"test"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	startTime := time.Now()
	handler.ServeHTTP(w, req)
	responseTime := time.Since(startTime)

	// Response should complete quickly (not blocked by slow log insert)
	if responseTime > 500*time.Millisecond {
		t.Errorf("response took too long (%v), expected < 500ms - log insert may be blocking", responseTime)
	}

	// Wait for the InsertLog goroutine to start
	select {
	case <-store.insertStarted:
		// Good, insert started
	case <-time.After(testResponseMaxDur):
		t.Fatal("InsertLog goroutine did not start")
	}

	// The InsertLog should be cancelled by timeout (2s), not complete normally (5s)
	// Wait a bit longer than the timeout to ensure it completes
	select {
	case <-store.insertDone:
		t.Error("InsertLog completed normally, expected timeout cancellation")
	case <-time.After(testTimeoutWaitDelay):
		// Good, InsertLog was cancelled by timeout (didn't complete in 3s, which is > 2s timeout but < 5s delay)
	}

	// Verify no log was inserted (because timeout cancelled it)
	if store.LogsLen() > 0 {
		t.Error("expected no logs due to timeout, but found logs")
	}
}

func TestHandler_StickyCache_UpdatedOnClientDisconnect(t *testing.T) {
	// Bug: When a client disconnects during SSE streaming (e.g., Codex CLI receiving
	// a complete response and closing the connection), WriteToClient returns a writeErr.
	// The writeErr path in forwardToProvider returns early, skipping UpdateStickyWithTTL.
	// This means sticky sessions are never established for SSE requests where the client
	// disconnects before the server finishes — which is the normal Codex flow.

	// Set up upstream SSE server that streams data, then blocks until client disconnects.
	clientDisconnected := make(chan struct{})
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("ResponseWriter doesn't support Flusher")
		}

		// Send some data
		_, _ = w.Write([]byte("data: {\"chunk\":1}\n\n"))
		flusher.Flush()
		_, _ = w.Write([]byte("data: {\"chunk\":2}\n\n"))
		flusher.Flush()

		// Block until client disconnects — simulates a long SSE stream
		// where the client closes after receiving enough data.
		<-r.Context().Done()
		close(clientDisconnected)
	}))
	defer upstreamServer.Close()

	provider := model.Provider{
		ID:       "p1",
		Name:     "Test Provider",
		APIKey:   "test-api-key",
		AuthMode: "bearer",
		Enabled:  true,
		APITypes: []model.ProviderAPIType{{ProviderID: "p1", APIType: "codex", BaseURL: upstreamServer.URL}},
	}

	store := newMockStore()
	store.providers = []model.Provider{provider}

	sel := &mockSelector{
		selectWithMetadataFunc: func(_ context.Context, _ *model.SelectRequest) (*selectResult, error) {
			return &selectResult{
				Provider:        &provider,
				FromStickyCache: false,
			}, nil
		},
	}

	handler := NewHandler(Config{
		Store:    store,
		Selector: sel,
		Logger:   zap.NewNop(),
	})

	// Create a cancellable context to simulate client disconnect.
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/responses", strings.NewReader(`{"model":"o3-pro"}`))
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	// Run the proxy in a goroutine; cancel context shortly after to simulate disconnect.
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(w, req)
		close(done)
	}()

	// Wait a bit for the SSE stream to start flowing, then cancel (client disconnect).
	time.Sleep(50 * time.Millisecond)
	cancel()

	// Wait for proxy to finish.
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not return after client disconnect")
	}

	// Wait for upstream to see the disconnect.
	select {
	case <-clientDisconnected:
	case <-time.After(5 * time.Second):
		t.Fatal("upstream did not see client disconnect")
	}

	// The critical assertion: sticky cache MUST be updated even when client disconnects.
	// The upstream returned 200 OK and data was flowing — sticky should be set.
	waitFor(t, func() bool {
		return sel.StickyUpdatesLen() > 0
	}, testPollTimeout)

	sel.mu.Lock()
	if sel.stickyUpdates[0].ProviderID != "p1" {
		t.Errorf("sticky update provider_id = %q, want %q", sel.stickyUpdates[0].ProviderID, "p1")
	}
	sel.mu.Unlock()
}
