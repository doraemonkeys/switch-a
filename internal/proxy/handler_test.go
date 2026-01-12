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
type mockStore struct {
	mu        sync.Mutex
	providers []model.Provider
	configs   map[string]string
	logs      []model.RequestLog
	err       error
}

func newMockStore() *mockStore {
	return &mockStore{
		providers: []model.Provider{},
		configs: map[string]string{
			"trust_proxy_headers":      "true",
			"user_header":              "X-User-ID",
			"max_body_size":            "10",
			"auth_mode":                "auto",
			"max_retries":              "3",
			"upstream_connect_timeout": "10",
			"upstream_read_timeout":    "0",
			"sticky_enabled":           "true",
			"sticky_ttl":               "300",
		},
		logs: []model.RequestLog{},
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

// LogsLen returns the number of logs in a thread-safe manner.
func (m *mockStore) LogsLen() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.logs)
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

func TestShouldRetry(t *testing.T) {
	tests := []struct {
		statusCode int
		want       bool
	}{
		{200, false},
		{201, false},
		{400, false},
		{401, true}, // Unauthorized - provider misconfiguration, try another
		{403, true}, // Forbidden - provider misconfiguration, try another
		{404, false},
		{429, true}, // Rate limit
		{500, true}, // Server error
		{502, true}, // Bad gateway
		{503, true}, // Service unavailable
		{504, true}, // Gateway timeout
	}

	for _, tt := range tests {
		got := shouldRetry(tt.statusCode)
		if got != tt.want {
			t.Errorf("shouldRetry(%d) = %v, want %v", tt.statusCode, got, tt.want)
		}
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
	if cfg.maxBodySizeMB != 10 {
		t.Errorf("maxBodySizeMB = %d, want default %d", cfg.maxBodySizeMB, 10)
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
