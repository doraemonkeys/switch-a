package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"

	"switch-a/internal/model"
)

// trackingHealthManager tracks calls to MarkFailure for testing.
type trackingHealthManager struct {
	mu               sync.Mutex
	markFailureCalls []markFailureCall
	available        map[string]bool
}

type markFailureCall struct {
	providerID string
	err        error
}

func newTrackingHealthManager() *trackingHealthManager {
	return &trackingHealthManager{
		available: make(map[string]bool),
	}
}

func (m *trackingHealthManager) MarkSuccess(_ context.Context, _ string) {}

func (m *trackingHealthManager) MarkFailure(_ context.Context, providerID string, err error) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.markFailureCalls = append(m.markFailureCalls, markFailureCall{providerID: providerID, err: err})
	return false
}

func (m *trackingHealthManager) RecoverIfExpired(_ context.Context, _ string) bool {
	return false
}

func (m *trackingHealthManager) IsAvailable(_ context.Context, providerID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	avail, ok := m.available[providerID]
	if !ok {
		return true
	}
	return avail
}

func (m *trackingHealthManager) ManualDisable(_ context.Context, _ string, _ string) error {
	return nil
}

func (m *trackingHealthManager) ManualEnable(_ context.Context, _ string) error {
	return nil
}

func (m *trackingHealthManager) ResetCircuitBreaker(_ string) {}

func (m *trackingHealthManager) getMarkFailureCalls() []markFailureCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]markFailureCall, len(m.markFailureCalls))
	copy(result, m.markFailureCalls)
	return result
}

// TestContextCancellation_DoesNotTriggerCircuitBreaker verifies that client-side
// context cancellation does NOT trigger circuit breaker. This is important because:
//   - context.Canceled typically means the CLIENT disconnected, not provider failure
//   - Treating client disconnects as provider failures would unfairly penalize providers
//   - This was a bug where providers were disabled due to user cancelling requests
func TestContextCancellation_DoesNotTriggerCircuitBreaker(t *testing.T) {
	// Create an upstream server that blocks until we signal it to stop
	serverReady := make(chan struct{})
	serverDone := make(chan struct{})
	var serverHit atomic.Bool

	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverHit.Store(true)
		close(serverReady)
		// Block until test signals or request is cancelled
		select {
		case <-r.Context().Done():
		case <-serverDone:
		}
	}))
	defer func() {
		close(serverDone)
		upstreamServer.Close()
	}()

	store := newMockStore()
	store.providers = []model.Provider{
		{
			ID:       "p1",
			Name:     "Test Provider",
			BaseURL:  upstreamServer.URL,
			APIKey:   "test-key",
			Enabled:  true,
			AuthMode: "bearer",
		},
	}

	healthManager := newTrackingHealthManager()
	logger := zap.NewNop()

	handler := NewHandler(Config{
		Store:  store,
		Health: healthManager,
		Logger: logger,
	})

	// Create a request with a cancellable context
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"test"}`))
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()

	// Run the handler in a goroutine
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(w, req)
		close(done)
	}()

	// Wait for the server to receive the request
	select {
	case <-serverReady:
		// Server received the request, now cancel the context
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for server to receive request")
	}

	// Cancel the context to simulate client disconnect
	cancel()

	// Wait for the handler to complete
	select {
	case <-done:
		// Handler completed
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for handler to complete")
	}

	// Verify the server was actually hit
	if !serverHit.Load() {
		t.Fatal("expected upstream server to be hit")
	}

	// The key assertion: MarkFailure should NOT have been called
	// because context cancellation is a client-side issue, not provider failure
	calls := healthManager.getMarkFailureCalls()
	if len(calls) > 0 {
		t.Errorf("MarkFailure should NOT be called for context cancellation, but got %d calls:", len(calls))
		for i, call := range calls {
			t.Errorf("  call %d: providerID=%q, err=%v", i, call.providerID, call.err)
		}
	}
}

// TestDeadlineExceeded_DoesNotTriggerCircuitBreaker verifies that client-side
// deadline exceeded does NOT trigger circuit breaker.
func TestDeadlineExceeded_DoesNotTriggerCircuitBreaker(t *testing.T) {
	// Create an upstream server that takes longer than the client timeout
	serverDone := make(chan struct{})
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Wait for context to be cancelled or test to finish
		select {
		case <-r.Context().Done():
		case <-serverDone:
		}
	}))
	defer func() {
		close(serverDone)
		upstreamServer.Close()
	}()

	store := newMockStore()
	store.providers = []model.Provider{
		{
			ID:       "p1",
			Name:     "Test Provider",
			BaseURL:  upstreamServer.URL,
			APIKey:   "test-key",
			Enabled:  true,
			AuthMode: "bearer",
		},
	}

	healthManager := newTrackingHealthManager()
	logger := zap.NewNop()

	handler := NewHandler(Config{
		Store:  store,
		Health: healthManager,
		Logger: logger,
	})

	// Create a request with a very short deadline
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"test"}`))
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	// The key assertion: MarkFailure should NOT be called
	calls := healthManager.getMarkFailureCalls()
	if len(calls) > 0 {
		t.Errorf("MarkFailure should NOT be called for deadline exceeded, but got %d calls:", len(calls))
		for i, call := range calls {
			t.Errorf("  call %d: providerID=%q, err=%v", i, call.providerID, call.err)
		}
	}
}

// TestUpstreamNetworkError_TriggerCircuitBreaker verifies that actual upstream
// network errors DO trigger circuit breaker (regression test to ensure we don't
// break normal circuit breaker behavior while fixing the client cancellation bug).
func TestUpstreamNetworkError_TriggerCircuitBreaker(t *testing.T) {
	// Use a server that returns an error response, then close it immediately.
	// This guarantees a "connection refused" error when the client tries to connect.
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// This handler will never be reached since we close the server immediately
	}))
	serverURL := upstreamServer.URL
	upstreamServer.Close() // Close immediately - any connection attempt will fail

	store := newMockStore()
	store.providers = []model.Provider{
		{
			ID:       "p1",
			Name:     "Test Provider",
			BaseURL:  serverURL, // Server that refuses connections
			APIKey:   "test-key",
			Enabled:  true,
			AuthMode: "bearer",
		},
	}

	healthManager := newTrackingHealthManager()
	logger := zap.NewNop()

	handler := NewHandler(Config{
		Store:  store,
		Health: healthManager,
		Logger: logger,
	})

	// Add a timeout to prevent test from hanging
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"test"}`))
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	// For network errors (connection refused), MarkFailure SHOULD be called
	calls := healthManager.getMarkFailureCalls()
	if len(calls) == 0 {
		t.Error("MarkFailure SHOULD be called for upstream network errors")
	}
}

// TestUpstream5xx_TriggerCircuitBreaker verifies that upstream 5xx errors
// trigger circuit breaker (regression test).
func TestUpstream5xx_TriggerCircuitBreaker(t *testing.T) {
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"internal server error"}`))
	}))
	defer upstreamServer.Close()

	store := newMockStore()
	store.providers = []model.Provider{
		{
			ID:       "p1",
			Name:     "Test Provider",
			BaseURL:  upstreamServer.URL,
			APIKey:   "test-key",
			Enabled:  true,
			AuthMode: "bearer",
		},
	}

	healthManager := newTrackingHealthManager()
	logger := zap.NewNop()

	handler := NewHandler(Config{
		Store:  store,
		Health: healthManager,
		Logger: logger,
	})

	// Add a timeout to prevent test from hanging
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"test"}`))
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	// For 5xx errors, MarkFailure SHOULD be called
	calls := healthManager.getMarkFailureCalls()
	if len(calls) == 0 {
		t.Error("MarkFailure SHOULD be called for upstream 5xx errors")
	}
}
