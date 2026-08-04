package selector

import (
	"fmt"
	"sync"
	"testing"
)

func TestConcurrencyLimiterUnlimitedLeasesAreExplicitCapabilities(t *testing.T) {
	limiter := NewConcurrencyLimiter()

	for _, limit := range []int{0, -1} {
		lease, acquired := limiter.Acquire("p1", limit)
		if !acquired || lease == nil || !lease.Held() {
			t.Fatalf("Acquire(limit=%d) = (%#v, %v), want held lease", limit, lease, acquired)
		}
		if limiter.Current("p1") != 0 {
			t.Fatalf("Current() = %d for unlimited lease, want 0", limiter.Current("p1"))
		}
		if !lease.Release() {
			t.Fatalf("Release(limit=%d) = false, want true", limit)
		}
	}
}

func TestConcurrencyLimiterEnforcesLimitAndReleasesExactLease(t *testing.T) {
	limiter := NewConcurrencyLimiter()
	const providerID = "p1"
	const limit = 3

	leases := make([]*SlotLease, 0, limit)
	for range limit {
		lease, acquired := limiter.Acquire(providerID, limit)
		if !acquired {
			t.Fatal("Acquire() before limit = false")
		}
		leases = append(leases, lease)
	}
	if got := limiter.Current(providerID); got != limit {
		t.Fatalf("Current() = %d, want %d", got, limit)
	}
	if lease, acquired := limiter.Acquire(providerID, limit); acquired || lease != nil {
		t.Fatalf("Acquire() at limit = (%#v, %v), want nil, false", lease, acquired)
	}

	if !leases[1].Release() {
		t.Fatal("Release() = false")
	}
	replacement, acquired := limiter.Acquire(providerID, limit)
	if !acquired || replacement == nil {
		t.Fatal("Acquire() after release did not recover capacity")
	}
	if leases[1].Release() {
		t.Fatal("duplicate Release() = true")
	}
}

func TestConcurrencyLimiterProvidersAreIndependent(t *testing.T) {
	limiter := NewConcurrencyLimiter()
	first, firstOK := limiter.Acquire("p1", 1)
	second, secondOK := limiter.Acquire("p2", 1)
	if !firstOK || !secondOK {
		t.Fatal("independent first acquisitions must succeed")
	}
	t.Cleanup(func() {
		first.Release()
		second.Release()
	})

	if lease, ok := limiter.Acquire("p1", 1); ok || lease != nil {
		t.Fatal("p1 acquired beyond its limit")
	}
	if lease, ok := limiter.Acquire("p2", 1); ok || lease != nil {
		t.Fatal("p2 acquired beyond its limit")
	}
}

func TestConcurrencyLimiterConcurrentAcquireReturnsOnlyOwnedLeases(t *testing.T) {
	limiter := NewConcurrencyLimiter()
	const providerID = "p1"
	const limit = 10
	const goroutines = 100

	var (
		mu     sync.Mutex
		leases []*SlotLease
		wg     sync.WaitGroup
	)
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			lease, acquired := limiter.Acquire(providerID, limit)
			if !acquired {
				return
			}
			mu.Lock()
			leases = append(leases, lease)
			mu.Unlock()
		}()
	}
	wg.Wait()

	if len(leases) != limit {
		t.Fatalf("successful leases = %d, want %d", len(leases), limit)
	}
	if got := limiter.Current(providerID); got != limit {
		t.Fatalf("Current() = %d, want %d", got, limit)
	}
	for _, lease := range leases {
		lease.Release()
	}
	if got := limiter.Current(providerID); got != 0 {
		t.Fatalf("Current() after exact releases = %d, want 0", got)
	}
}

func TestConcurrencyLimiterUnknownReadsDoNotCreateGenerations(t *testing.T) {
	limiter := NewConcurrencyLimiter()
	for i := range 1000 {
		_ = limiter.Current(fmt.Sprintf("unknown-%d", i))
	}
	if len(limiter.generations) != 0 {
		t.Fatalf("unknown Current calls created %d generations", len(limiter.generations))
	}

	lease, acquired := limiter.Acquire("known", 1)
	if !acquired {
		t.Fatal("Acquire() after unknown reads = false")
	}
	limiter.retireGeneration("known")
	if limiter.Current("known") != 0 {
		t.Fatal("Clear() left current generation visible")
	}
	if !lease.Release() {
		t.Fatal("detached generation lease did not retain exact release ownership")
	}
}
