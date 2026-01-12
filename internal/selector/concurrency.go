package selector

import (
	"sync"
	"sync/atomic"
)

// ConcurrencyLimiter tracks concurrent request counts per provider.
//
// Memory Management: The internal sync.Map holds counters indefinitely until explicitly
// cleared via Clear(). When a provider is deleted through the admin API, the caller
// MUST call Selector.ClearConcurrency(providerID) to remove the counter entry.
// Failure to do so causes a memory leak proportional to the number of deleted providers.
type ConcurrencyLimiter struct {
	counts sync.Map // map[string]*atomic.Int64
}

// NewConcurrencyLimiter creates a new concurrency limiter.
func NewConcurrencyLimiter() *ConcurrencyLimiter {
	return &ConcurrencyLimiter{}
}

// getCounter returns the counter for a provider, creating if needed.
func (l *ConcurrencyLimiter) getCounter(providerID string) *atomic.Int64 {
	if counter, ok := l.counts.Load(providerID); ok {
		return counter.(*atomic.Int64)
	}

	// Create new counter
	newCounter := &atomic.Int64{}
	actual, _ := l.counts.LoadOrStore(providerID, newCounter)
	return actual.(*atomic.Int64)
}

// TryAcquire attempts to acquire a slot for the provider.
// Returns true if acquired, false if limit reached.
// If limit is 0, always returns true (unlimited).
func (l *ConcurrencyLimiter) TryAcquire(providerID string, limit int) bool {
	if limit <= 0 {
		return true // No limit
	}

	counter := l.getCounter(providerID)

	// Atomic compare-and-swap loop to ensure we don't exceed limit
	for {
		current := counter.Load()
		if current >= int64(limit) {
			return false
		}
		if counter.CompareAndSwap(current, current+1) {
			return true
		}
	}
}

// Release releases a slot for the provider.
// If the counter is already at zero or doesn't exist, this is a no-op.
// Does not create a map entry if the provider was never acquired.
func (l *ConcurrencyLimiter) Release(providerID string) {
	counter, ok := l.counts.Load(providerID)
	if !ok {
		return // No entry exists, nothing to release
	}
	c := counter.(*atomic.Int64)
	// Use CAS loop to prevent counter going negative
	for {
		current := c.Load()
		if current <= 0 {
			return // Already at zero or below, nothing to release
		}
		if c.CompareAndSwap(current, current-1) {
			return
		}
	}
}

// Current returns the current concurrency count for a provider.
// Returns 0 if the provider has no active concurrency tracking (never acquired).
// Does not create a map entry if the provider was never acquired.
func (l *ConcurrencyLimiter) Current(providerID string) int64 {
	counter, ok := l.counts.Load(providerID)
	if !ok {
		return 0 // No entry exists, return 0
	}
	return counter.(*atomic.Int64).Load()
}

// Clear removes the counter for a provider.
// Call this when a provider is deleted to prevent unbounded memory growth.
//
// Usage: When deleting a provider via the API, the server should call
// Selector.ClearConcurrency(providerID) which delegates to this method.
// If not called, the sync.Map entry persists forever (memory leak for high churn scenarios).
func (l *ConcurrencyLimiter) Clear(providerID string) {
	l.counts.Delete(providerID)
}
