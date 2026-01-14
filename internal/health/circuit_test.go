package health

import (
	"sync"
	"testing"
	"time"
)

// mockClock implements internal.Clock for testing.
// It is thread-safe to allow concurrent use in tests with cleanup goroutines.
type mockClock struct {
	mu  sync.RWMutex
	now time.Time
}

func (c *mockClock) Now() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.now
}

func (c *mockClock) NewTicker(d time.Duration) *time.Ticker {
	return time.NewTicker(d)
}

func (c *mockClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func TestCircuitBreaker_RecordFailure(t *testing.T) {
	clock := &mockClock{now: time.Now()}
	cb := NewCircuitBreaker(clock)

	providerID := "p1"
	window := time.Minute
	threshold := 3

	// First failure should not trigger
	if cb.RecordFailure(providerID, window, threshold) {
		t.Error("first failure should not trigger circuit breaker")
	}

	// Second failure should not trigger
	if cb.RecordFailure(providerID, window, threshold) {
		t.Error("second failure should not trigger circuit breaker")
	}

	// Third failure should trigger
	if !cb.RecordFailure(providerID, window, threshold) {
		t.Error("third failure should trigger circuit breaker")
	}
}

func TestCircuitBreaker_SlidingWindow(t *testing.T) {
	clock := &mockClock{now: time.Now()}
	cb := NewCircuitBreaker(clock)

	providerID := "p1"
	window := time.Minute
	threshold := 3

	// Record 2 failures
	cb.RecordFailure(providerID, window, threshold)
	cb.RecordFailure(providerID, window, threshold)

	// Advance time past window
	clock.Advance(2 * time.Minute)

	// Record failure - should start fresh
	if cb.RecordFailure(providerID, window, threshold) {
		t.Error("failure after window expiry should not trigger (count reset)")
	}

	if cb.GetFailureCount(providerID, window) != 1 {
		t.Errorf("failure count should be 1, got %d", cb.GetFailureCount(providerID, window))
	}
}

func TestCircuitBreaker_GetFailureCount(t *testing.T) {
	clock := &mockClock{now: time.Now()}
	cb := NewCircuitBreaker(clock)

	providerID := "p1"
	window := time.Minute

	// No failures
	if cb.GetFailureCount(providerID, window) != 0 {
		t.Error("expected 0 failures initially")
	}

	// Add failures
	cb.RecordFailure(providerID, window, 10)
	cb.RecordFailure(providerID, window, 10)

	if cb.GetFailureCount(providerID, window) != 2 {
		t.Errorf("expected 2 failures, got %d", cb.GetFailureCount(providerID, window))
	}
}

func TestCircuitBreaker_Reset(t *testing.T) {
	clock := &mockClock{now: time.Now()}
	cb := NewCircuitBreaker(clock)

	providerID := "p1"
	window := time.Minute

	// Add failures
	cb.RecordFailure(providerID, window, 10)
	cb.RecordFailure(providerID, window, 10)

	// Reset
	cb.Reset(providerID)

	if cb.GetFailureCount(providerID, window) != 0 {
		t.Errorf("expected 0 failures after reset, got %d", cb.GetFailureCount(providerID, window))
	}
}

func TestCircuitBreaker_Cleanup(t *testing.T) {
	clock := &mockClock{now: time.Now()}
	cb := NewCircuitBreaker(clock)

	window := time.Minute

	// Add failures for multiple providers
	cb.RecordFailure("p1", window, 10)
	cb.RecordFailure("p2", window, 10)

	// Advance time past window
	clock.Advance(2 * time.Minute)

	// Cleanup old records
	cb.Cleanup(time.Minute)

	// Both should be cleaned up
	if cb.GetFailureCount("p1", window) != 0 {
		t.Error("p1 failures should be cleaned up")
	}
	if cb.GetFailureCount("p2", window) != 0 {
		t.Error("p2 failures should be cleaned up")
	}
}

func TestCircuitBreaker_Cleanup_PreservesRecentFailures(t *testing.T) {
	clock := &mockClock{
		now: time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC),
	}
	cb := NewCircuitBreaker(clock)

	const providerID = "p1"
	const threshold = 10

	// Keep the sliding window wide enough so the old failure remains in the internal slice,
	// then rely on Cleanup(maxAge) to drop only the expired entries.
	recordWindow := 10 * time.Minute
	maxAge := time.Minute

	cb.RecordFailure(providerID, recordWindow, threshold) // t0
	clock.Advance(2 * time.Minute)
	cb.RecordFailure(providerID, recordWindow, threshold) // t0 + 2m

	cb.Cleanup(maxAge)

	if got := cb.GetFailureCount(providerID, recordWindow); got != 1 {
		t.Fatalf("failure count after Cleanup = %d, want %d", got, 1)
	}
}

func TestCircuitBreaker_MultipleProviders(t *testing.T) {
	clock := &mockClock{now: time.Now()}
	cb := NewCircuitBreaker(clock)

	window := time.Minute
	threshold := 2

	// Provider 1 hits threshold
	cb.RecordFailure("p1", window, threshold)
	if !cb.RecordFailure("p1", window, threshold) {
		t.Error("p1 should trigger")
	}

	// Provider 2 is independent
	if cb.RecordFailure("p2", window, threshold) {
		t.Error("p2 should not trigger yet")
	}
}

func TestCircuitBreaker_StartCleanupLoop(t *testing.T) {
	clock := &mockClock{now: time.Now()}
	cb := NewCircuitBreaker(clock)

	window := time.Minute

	// Add failures
	cb.RecordFailure("p1", window, 10)
	cb.RecordFailure("p2", window, 10)

	// Start cleanup loop with very short interval for testing
	stop := cb.StartCleanupLoop(10*time.Millisecond, time.Minute)

	// Advance clock past maxAge
	clock.Advance(2 * time.Minute)

	// Wait for at least one cleanup cycle
	time.Sleep(50 * time.Millisecond)

	// Stop the loop
	stop()

	// Failures should be cleaned up
	if cb.GetFailureCount("p1", window) != 0 {
		t.Error("p1 failures should be cleaned up by loop")
	}
	if cb.GetFailureCount("p2", window) != 0 {
		t.Error("p2 failures should be cleaned up by loop")
	}
}
