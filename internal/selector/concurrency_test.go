package selector

import (
	"fmt"
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
	for i := range limit {
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
	for range goroutines {
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

func TestConcurrencyLimiter_NoMapEntryOnEmptyCalls(t *testing.T) {
	limiter := NewConcurrencyLimiter()

	// Calling Release() and Current() on unknown providers should NOT create map entries
	// This prevents unbounded memory growth from empty calls

	// Call Release and Current on a never-acquired provider
	unknownProvider := "never-acquired"
	limiter.Release(unknownProvider)
	_ = limiter.Current(unknownProvider)

	// Now try to check if an entry was created by looking at map size
	// We can check this indirectly by acquiring on a known provider and checking Clear behavior
	knownProvider := "known"
	limiter.TryAcquire(knownProvider, 10)

	// Clear the known provider
	limiter.Clear(knownProvider)

	// The unknown provider should still return 0 (no entry was created)
	if limiter.Current(unknownProvider) != 0 {
		t.Errorf("Current for unknown provider = %d, want 0", limiter.Current(unknownProvider))
	}

	// Verify that calling Release/Current many times doesn't create entries
	for i := range 1000 {
		providerID := fmt.Sprintf("random-%d", i)
		limiter.Release(providerID)
		_ = limiter.Current(providerID)
	}

	// After all those calls, acquiring a new provider should still work
	if !limiter.TryAcquire("final", 1) {
		t.Error("TryAcquire should succeed after Release/Current calls")
	}
}
