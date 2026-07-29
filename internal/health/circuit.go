// Package health provides health management and circuit breaking.
package health

import (
	"sync"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
)

// CircuitBreaker implements a sliding window circuit breaker.
type CircuitBreaker struct {
	mu       sync.RWMutex
	failures map[string][]time.Time // providerID -> failure timestamps
	clock    internal.Clock
}

// NewCircuitBreaker creates a new circuit breaker.
func NewCircuitBreaker(clock internal.Clock) *CircuitBreaker {
	return &CircuitBreaker{
		failures: make(map[string][]time.Time),
		clock:    clock,
	}
}

// RecordFailure records a failure for a provider.
// Returns true if the failure count in the window has reached the threshold.
func (cb *CircuitBreaker) RecordFailure(providerID string, window time.Duration, threshold int) bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	now := cb.clock.Now()
	cutoff := now.Add(-window)

	// Get existing failures and filter to window
	failures := cb.failures[providerID]
	var inWindow []time.Time
	for _, t := range failures {
		if t.After(cutoff) {
			inWindow = append(inWindow, t)
		}
	}

	// Add new failure
	inWindow = append(inWindow, now)
	cb.failures[providerID] = inWindow

	return len(inWindow) >= threshold
}

// GetFailureCount returns the number of failures in the window.
func (cb *CircuitBreaker) GetFailureCount(providerID string, window time.Duration) int {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	now := cb.clock.Now()
	cutoff := now.Add(-window)

	count := 0
	for _, t := range cb.failures[providerID] {
		if t.After(cutoff) {
			count++
		}
	}
	return count
}

// Reset clears the failure history for a provider.
func (cb *CircuitBreaker) Reset(providerID string) {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	delete(cb.failures, providerID)
}

// Cleanup removes old failure records to prevent memory bloat.
// Call periodically or use StartCleanupLoop for automatic cleanup.
func (cb *CircuitBreaker) Cleanup(maxAge time.Duration) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	now := cb.clock.Now()
	cutoff := now.Add(-maxAge)

	for providerID, failures := range cb.failures {
		var valid []time.Time
		for _, t := range failures {
			if t.After(cutoff) {
				valid = append(valid, t)
			}
		}
		if len(valid) == 0 {
			delete(cb.failures, providerID)
		} else {
			cb.failures[providerID] = valid
		}
	}
}

// StartCleanupLoop spawns a goroutine that periodically calls Cleanup().
// Returns a stop function to terminate the cleanup loop.
// The stop function waits for the cleanup goroutine to fully exit before returning.
// Example: stop := cb.StartCleanupLoop(5 * time.Minute, 10 * time.Minute); defer stop()
func (cb *CircuitBreaker) StartCleanupLoop(interval, maxAge time.Duration) (stop func()) {
	done := make(chan struct{})
	var wg sync.WaitGroup

	wg.Go(func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				cb.Cleanup(maxAge)
			case <-done:
				return
			}
		}
	})
	return func() {
		close(done)
		wg.Wait()
	}
}
