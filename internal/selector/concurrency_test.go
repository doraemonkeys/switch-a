package selector

import (
	"sync"
	"testing"
)

func TestConcurrencyLimiter_TryAcquire(t *testing.T) {
	limiter := NewConcurrencyLimiter()

	// Test with no limit (0)
	if !limiter.TryAcquire("p1", 0) {
		t.Error("TryAcquire with limit 0 should always succeed")
	}

	// Test with negative limit
	if !limiter.TryAcquire("p1", -1) {
		t.Error("TryAcquire with negative limit should always succeed")
	}
}

func TestConcurrencyLimiter_Limit(t *testing.T) {
	limiter := NewConcurrencyLimiter()
	providerID := "p1"
	limit := 3

	// Acquire up to limit
	for i := 0; i < limit; i++ {
		if !limiter.TryAcquire(providerID, limit) {
			t.Errorf("TryAcquire %d should succeed", i)
		}
	}

	// Current should be at limit
	if limiter.Current(providerID) != int64(limit) {
		t.Errorf("Current = %d, want %d", limiter.Current(providerID), limit)
	}

	// Next acquire should fail
	if limiter.TryAcquire(providerID, limit) {
		t.Error("TryAcquire at limit should fail")
	}

	// Release one
	limiter.Release(providerID)

	// Now acquire should succeed
	if !limiter.TryAcquire(providerID, limit) {
		t.Error("TryAcquire after release should succeed")
	}
}

func TestConcurrencyLimiter_MultipleProviders(t *testing.T) {
	limiter := NewConcurrencyLimiter()

	// Each provider has independent limit
	if !limiter.TryAcquire("p1", 1) {
		t.Error("p1 should acquire")
	}
	if !limiter.TryAcquire("p2", 1) {
		t.Error("p2 should acquire independently")
	}

	// p1 is at limit
	if limiter.TryAcquire("p1", 1) {
		t.Error("p1 should be at limit")
	}

	// p2 is also at limit
	if limiter.TryAcquire("p2", 1) {
		t.Error("p2 should be at limit")
	}
}

func TestConcurrencyLimiter_Concurrent(t *testing.T) {
	limiter := NewConcurrencyLimiter()
	providerID := "p1"
	limit := 10
	goroutines := 100

	var successCount int
	var mu sync.Mutex
	var wg sync.WaitGroup

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			if limiter.TryAcquire(providerID, limit) {
				mu.Lock()
				successCount++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	// Exactly limit goroutines should succeed
	if successCount != limit {
		t.Errorf("successCount = %d, want %d", successCount, limit)
	}

	// Current should be at limit
	if limiter.Current(providerID) != int64(limit) {
		t.Errorf("Current = %d, want %d", limiter.Current(providerID), limit)
	}
}

func TestConcurrencyLimiter_Release(t *testing.T) {
	limiter := NewConcurrencyLimiter()
	providerID := "p1"

	// Release without acquire should be a no-op (prevents negative count)
	limiter.Release(providerID)
	if limiter.Current(providerID) != 0 {
		t.Errorf("Current after release without acquire = %d, want 0", limiter.Current(providerID))
	}

	// Acquire should still work
	if !limiter.TryAcquire(providerID, 1) {
		t.Error("TryAcquire should succeed")
	}

	// Now release should work
	limiter.Release(providerID)
	if limiter.Current(providerID) != 0 {
		t.Errorf("Current after proper release = %d, want 0", limiter.Current(providerID))
	}

	// Double release should be a no-op
	limiter.Release(providerID)
	if limiter.Current(providerID) != 0 {
		t.Errorf("Current after double release = %d, want 0", limiter.Current(providerID))
	}
}
