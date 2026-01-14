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

	"switch-a/internal/model"

	"go.uber.org/zap"
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
	mu        sync.Mutex
	providers []model.Provider
	configs   map[string]string
	logs      []model.RequestLog
	attempts  []model.RequestAttempt // Captures attempts for verification
	err       error
}

func newMockStore() *mockStore {
	return &mockStore{
		providers: []model.Provider{},
		configs: map[string]string{
			ConfigKeyTrustProxyHeaders:      "true",
			ConfigKeyUserHeader:             "X-User-ID",
			ConfigKeyMaxBodySize:            "10",
			ConfigKeyAuthMode:               "auto",
			ConfigKeyMaxRetries:             "3",
			ConfigKeyUpstreamConnectTimeout: "10",
			ConfigKeyUpstreamReadTimeout:    "0",
			ConfigKeyStickyEnabled:          "true",
			ConfigKeyStickyTTL:              "300",
		},
		logs:     []model.RequestLog{},
		attempts: []model.RequestAttempt{},
	}
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
}

func TestHandler_ServeHTTP_BodyTooLarge(t *testing.T) {
	store := newMockStore()
	store.configs["max_body_size"] = "1" // 1MB limit
	store.providers = []model.Provider{
		{ID: "p1", BaseURL: "https://api.example.com", APIKey: "key"},
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
			BaseURL:  upstreamServer.URL,
			APIKey:   "test-api-key",
			AuthMode: "bearer",
			Enabled:  true,
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
	}, 100*time.Millisecond)

	// Verify attempts were recorded for retry tracking (TEST-003)
	waitFor(t, func() bool {
		return store.AttemptsLen() > 0
	}, 100*time.Millisecond)

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
	// Attempt number is 0-indexed (first attempt = 0)
	if attempts[0].Attempt != 0 {
		t.Errorf("attempt number = %d, want 0 (first attempt)", attempts[0].Attempt)
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
			BaseURL:  upstreamServer.URL,
			APIKey:   "test-api-key",
			AuthMode: "bearer",
			Enabled:  true,
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

func TestHandler_LogsSuccessFalse_ForFailoverStatusCodes(t *testing.T) {
	// Test that failover-eligible status codes (401, 402, 403, 429, 5xx) are logged with success=false
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
			store.configs["max_retries"] = "0" // Disable retries to test single attempt
			store.providers = []model.Provider{
				{
					ID:       "p1",
					Name:     "Test Provider",
					BaseURL:  upstreamServer.URL,
					APIKey:   "test-api-key",
					AuthMode: "bearer",
					Enabled:  true,
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
			}, 100*time.Millisecond)

			// Verify log entry has success=false
			log := store.LastLog()
			if log == nil {
				t.Fatal("expected log entry to be created")
			}
			if log.Success {
				t.Errorf("expected Success=false for status %d, got Success=true", tt.statusCode)
			}
			if log.StatusCode != tt.statusCode {
				t.Errorf("expected StatusCode=%d, got %d", tt.statusCode, log.StatusCode)
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
			BaseURL:  upstreamServer.URL,
			APIKey:   "test-api-key",
			AuthMode: "bearer",
			Enabled:  true,
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
	}, 100*time.Millisecond)

	// Verify log entry has success=true
	log := store.LastLog()
	if log == nil {
		t.Fatal("expected log entry to be created")
	}
	if !log.Success {
		t.Error("expected Success=true for status 200, got Success=false")
	}
	if log.StatusCode != http.StatusOK {
		t.Errorf("expected StatusCode=200, got %d", log.StatusCode)
	}
}

func TestHandler_LogsSuccessFalse_ForNonRetryable4xxStatusCodes(t *testing.T) {
	// Test that non-retryable 4xx status codes (400, 404, etc.) are logged with success=false
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
					BaseURL:  upstreamServer.URL,
					APIKey:   "test-api-key",
					AuthMode: "bearer",
					Enabled:  true,
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
			}, 100*time.Millisecond)

			// Verify log entry has success=false (client errors are not "successful")
			log := store.LastLog()
			if log == nil {
				t.Fatal("expected log entry to be created")
			}
			if log.Success {
				t.Errorf("expected Success=false for status %d, got Success=true", tt.statusCode)
			}
			if log.StatusCode != tt.statusCode {
				t.Errorf("expected StatusCode=%d, got %d", tt.statusCode, log.StatusCode)
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
	if cfg.maxRetries != 3 {
		t.Errorf("maxRetries = %d, want %d", cfg.maxRetries, 3)
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
	if cfg.maxRetries != 3 {
		t.Errorf("maxRetries = %d, want default %d", cfg.maxRetries, 3)
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
		mockStore:     newMockStore(),
		insertDelay:   delay,
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
	store := newSlowMockStore(5 * time.Second)
	store.providers = []model.Provider{
		{
			ID:       "p1",
			Name:     "Test Provider",
			BaseURL:  upstreamServer.URL,
			APIKey:   "test-api-key",
			AuthMode: "bearer",
			Enabled:  true,
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
	case <-time.After(500 * time.Millisecond):
		t.Fatal("InsertLog goroutine did not start")
	}

	// The InsertLog should be cancelled by timeout (2s), not complete normally (5s)
	// Wait a bit longer than the timeout to ensure it completes
	select {
	case <-store.insertDone:
		t.Error("InsertLog completed normally, expected timeout cancellation")
	case <-time.After(3 * time.Second):
		// Good, InsertLog was cancelled by timeout (didn't complete in 3s, which is > 2s timeout but < 5s delay)
	}

	// Verify no log was inserted (because timeout cancelled it)
	if store.LogsLen() > 0 {
		t.Error("expected no logs due to timeout, but found logs")
	}
}
