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

	"github.com/doraemonkeys/switch-a/internal/model"
)

// trackingHealthManager tracks calls to MarkFailure and MarkSuccess for testing.
type trackingHealthManager struct {
	mu               sync.Mutex
	markFailureCalls []markFailureCall
	markSuccessIDs   []string
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

func (m *trackingHealthManager) MarkSuccess(_ context.Context, providerID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.markSuccessIDs = append(m.markSuccessIDs, providerID)
}

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

func (m *trackingHealthManager) SuspendUntil(_ context.Context, _ string, _ time.Time, _ string) error {
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

func (m *trackingHealthManager) getMarkSuccessIDs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]string, len(m.markSuccessIDs))
	copy(result, m.markSuccessIDs)
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
		withTestStaticCredential(model.Provider{
			ID:   "p1",
			Name: "Test Provider",

			Enabled:  true,
			AuthMode: "bearer",
			APITypes: []model.ProviderAPIType{{ProviderID: "p1", APIType: "claude", BaseURL: upstreamServer.URL}},
		}, "", "test-key"),
	}

	healthManager := newTrackingHealthManager()
	logger := zap.NewNop()

	handler := newProxyCodexTestHandler(t, Config{
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

// TestDeadlineExceeded_DoesNotTriggerCircuitBreaker verifies that a client-side
// deadline remains health-neutral without being mislabeled as a disconnect.
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
		withTestStaticCredential(model.Provider{
			ID:   "p1",
			Name: "Test Provider",

			Enabled:  true,
			AuthMode: "bearer",
			APITypes: []model.ProviderAPIType{{ProviderID: "p1", APIType: "claude", BaseURL: upstreamServer.URL}},
		}, "", "test-key"),
	}

	healthManager := newTrackingHealthManager()
	logger := zap.NewNop()

	handler := newProxyCodexTestHandler(t, Config{
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
	waitFor(t, func() bool { return store.LogsLen() == 1 }, clientDisconnectIntegrationTimeout)
	log := store.LastLog()
	if reason := requestLogTerminationReason(log); reason != model.TerminationReasonTimeout {
		t.Fatalf("TerminationReason = %q, want %q", reason, model.TerminationReasonTimeout)
	}
	if log.TerminationActor == nil || *log.TerminationActor != model.TerminationActorClient {
		t.Fatalf("TerminationActor = %v, want %q", log.TerminationActor, model.TerminationActorClient)
	}
	if outcome := requestLogServiceOutcome(log); outcome != model.ServiceOutcomeAbandonedByClient {
		t.Fatalf("ServiceOutcome = %q, want %q", outcome, model.ServiceOutcomeAbandonedByClient)
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
		withTestStaticCredential(model.Provider{
			ID:   "p1",
			Name: "Test Provider",

			Enabled:  true,
			AuthMode: "bearer",
			APITypes: []model.ProviderAPIType{{ProviderID: "p1", APIType: "claude", BaseURL: serverURL}},
		}, "", "test-key"),
	}

	healthManager := newTrackingHealthManager()
	logger := zap.NewNop()

	handler := newProxyCodexTestHandler(t, Config{
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
		withTestStaticCredential(model.Provider{
			ID:   "p1",
			Name: "Test Provider",

			Enabled:  true,
			AuthMode: "bearer",
			APITypes: []model.ProviderAPIType{{ProviderID: "p1", APIType: "claude", BaseURL: upstreamServer.URL}},
		}, "", "test-key"),
	}

	healthManager := newTrackingHealthManager()
	logger := zap.NewNop()

	handler := newProxyCodexTestHandler(t, Config{
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

// TestClientDisconnectDuringSSE_IsHealthNeutralAndPersistsClientOutcome verifies
// that an incomplete upstream exchange changes neither side of provider health
// while the normalized lifecycle still attributes termination to the client.
func TestClientDisconnectDuringSSE_IsHealthNeutralAndPersistsClientOutcome(t *testing.T) {
	serverDone := make(chan struct{})

	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n"))
		flusher.Flush()
		// Block until test signals — simulates upstream still streaming
		<-serverDone
	}))
	defer func() {
		close(serverDone)
		upstreamServer.Close()
	}()

	store := newMockStore()
	store.providers = []model.Provider{
		withTestStaticCredential(model.Provider{
			ID:   "p1",
			Name: "Test Provider",

			Enabled:  true,
			AuthMode: "bearer",
			APITypes: []model.ProviderAPIType{{ProviderID: "p1", APIType: "claude", BaseURL: upstreamServer.URL}},
		}, "", "test-key"),
	}

	healthManager := newTrackingHealthManager()
	handler := newProxyCodexTestHandler(t, Config{
		Store:  store,
		Health: healthManager,
		Logger: zap.NewNop(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"test","stream":true}`))
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")

	// Use a ResponseWriter that signals when the proxy writes SSE data to the client.
	// This guarantees we cancel AFTER FetchUpstream succeeded and streaming started,
	// so we hit the writeErr path (not the FetchUpstream error path).
	clientGotData := make(chan struct{}, 1)
	nw := &notifyingResponseWriter{
		ResponseRecorder: httptest.NewRecorder(),
		onWrite:          func() { clientGotData <- struct{}{} },
	}

	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(nw, req)
		close(done)
	}()

	// Wait for data to flow through the proxy to the client, then disconnect
	select {
	case <-clientGotData:
		cancel()
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for SSE data to reach client")
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for handler to complete")
	}

	// markFailure must NOT be called
	failCalls := healthManager.getMarkFailureCalls()
	if len(failCalls) > 0 {
		t.Errorf("MarkFailure should NOT be called on client disconnect, got %d calls", len(failCalls))
	}

	// A client-aborted stream is health-neutral: it proves neither provider
	// success nor provider failure because the upstream exchange was incomplete.
	successIDs := healthManager.getMarkSuccessIDs()
	if len(successIDs) != 0 {
		t.Errorf("MarkSuccess should not be called on client disconnect, got %d calls", len(successIDs))
	}

	// The normalized lifecycle should attribute the truncated stream to the client
	// while the health assessment remains neutral.
	waitFor(t, func() bool { return store.LogsLen() > 0 }, testPollTimeout)
	log := store.LastLog()
	if requestLogServiceOutcome(log) != model.ServiceOutcomeAbandonedByClient {
		t.Errorf("ServiceOutcome = %q, want %q", requestLogServiceOutcome(log), model.ServiceOutcomeAbandonedByClient)
	}
	if got := requestLogEvidenceMessage(t, log); got != "client canceled response forwarding" {
		t.Errorf("SessionEvidenceJSON = %q, want client cancellation evidence", got)
	}
}

// notifyingResponseWriter wraps httptest.ResponseRecorder and calls onWrite
// (once) when the first body data is written. This lets tests synchronize on
// the proxy actually streaming data to the client, avoiding races where
// cancel() fires before FetchUpstream completes.
type notifyingResponseWriter struct {
	*httptest.ResponseRecorder
	onWrite func()
	once    sync.Once
}

func (n *notifyingResponseWriter) Write(p []byte) (int, error) {
	n.once.Do(n.onWrite)
	return n.ResponseRecorder.Write(p)
}

// Flush delegates to the inner recorder so forwardSSE detects Flusher support.
func (n *notifyingResponseWriter) Flush() {
	n.ResponseRecorder.Flush()
}
