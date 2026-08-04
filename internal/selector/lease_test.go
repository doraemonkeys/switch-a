package selector

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/model"
)

func TestProviderLeaseCapabilityIdentityDistinguishesSlotsWithinGeneration(t *testing.T) {
	limiter := NewConcurrencyLimiter()
	firstSlot, firstOK := limiter.Acquire("provider-a", 2)
	secondSlot, secondOK := limiter.Acquire("provider-a", 2)
	if !firstOK || !secondOK {
		t.Fatal("failed to acquire independent slots")
	}
	first := newProviderLease(&model.Provider{ID: "provider-a"}, firstSlot)
	second := newProviderLease(&model.Provider{ID: "provider-a"}, secondSlot)
	copyOfFirst := *first

	if first.CapabilityIdentity() == 0 || first.CapabilityIdentity() != copyOfFirst.CapabilityIdentity() {
		t.Fatal("copied provider lease lost shared capability identity")
	}
	if first.Generation() != second.Generation() || first.CapabilityIdentity() == second.CapabilityIdentity() {
		t.Fatal("distinct slots in one generation shared capability identity")
	}
	first.Release()
	second.Release()

	var nilLease *ProviderLease
	if nilLease.CapabilityIdentity() != 0 || (&ProviderLease{}).CapabilityIdentity() != 0 {
		t.Fatal("empty provider lease exposed capability identity")
	}
}

func TestSlotLeaseReleaseIsCopySafeAndIdempotent(t *testing.T) {
	limiter := NewConcurrencyLimiter()
	lease, acquired := limiter.Acquire("provider-a", 1)
	if !acquired {
		t.Fatal("Acquire() = false, want true")
	}
	copyOfLease := *lease

	if !copyOfLease.Release() {
		t.Fatal("copied Release() = false, want first owner")
	}
	if lease.Release() {
		t.Fatal("original Release() = true after copied release")
	}
	if got := limiter.Current("provider-a"); got != 0 {
		t.Fatalf("Current() = %d, want 0", got)
	}
}

func TestSlotLeaseUnlimitedAcquisitionsRemainExplicitAndIndependent(t *testing.T) {
	limiter := NewConcurrencyLimiter()
	first, acquired := limiter.Acquire("provider-a", 0)
	if !acquired {
		t.Fatal("first unlimited Acquire() = false")
	}
	second, acquired := limiter.Acquire("provider-a", -1)
	if !acquired {
		t.Fatal("second unlimited Acquire() = false")
	}
	if first.state == second.state {
		t.Fatal("unlimited acquisitions shared release state")
	}
	if got := limiter.Current("provider-a"); got != 0 {
		t.Fatalf("Current() = %d for unlimited provider, want 0", got)
	}
	if !first.Release() || !second.Release() {
		t.Fatal("each unlimited lease must release exactly once")
	}
	if first.Release() || second.Release() {
		t.Fatal("unlimited lease double release succeeded")
	}
}

func TestSlotLeaseClearAndRecreateCannotRedirectOldRelease(t *testing.T) {
	limiter := NewConcurrencyLimiter()
	oldLease, acquired := limiter.Acquire("provider-a", 1)
	if !acquired {
		t.Fatal("old Acquire() = false")
	}
	oldGeneration := oldLease.Generation()

	limiter.retireGeneration("provider-a")
	newLease, acquired := limiter.Acquire("provider-a", 1)
	if !acquired {
		t.Fatal("new Acquire() = false")
	}
	if newLease.Generation() == oldGeneration {
		t.Fatal("recreated provider reused retired generation")
	}

	if !oldLease.Release() {
		t.Fatal("old Release() = false")
	}
	if got := limiter.Current("provider-a"); got != 1 {
		t.Fatalf("old release changed new generation count to %d, want 1", got)
	}
	if !newLease.Release() {
		t.Fatal("new Release() = false")
	}
}

func TestSlotLeaseConcurrentCopiesReleaseExactlyOnce(t *testing.T) {
	limiter := NewConcurrencyLimiter()
	lease, acquired := limiter.Acquire("provider-a", 1)
	if !acquired {
		t.Fatal("Acquire() = false")
	}

	const releasers = 64
	var winners atomic.Int64
	var group sync.WaitGroup
	group.Add(releasers)
	for range releasers {
		copyOfLease := *lease
		go func() {
			defer group.Done()
			if copyOfLease.Release() {
				winners.Add(1)
			}
		}()
	}
	group.Wait()

	if got := winners.Load(); got != 1 {
		t.Fatalf("release winners = %d, want 1", got)
	}
	if got := limiter.Current("provider-a"); got != 0 {
		t.Fatalf("Current() = %d, want 0", got)
	}
}

func TestSlotLeaseReleaseRacingClearNeverTouchesReplacement(t *testing.T) {
	const iterations = 250
	for iteration := range iterations {
		limiter := NewConcurrencyLimiter()
		oldLease, acquired := limiter.Acquire("provider-a", 1)
		if !acquired {
			t.Fatalf("iteration %d: old Acquire() = false", iteration)
		}

		start := make(chan struct{})
		newLease := make(chan *SlotLease, 1)
		var group sync.WaitGroup
		group.Add(2)
		go func() {
			defer group.Done()
			<-start
			oldLease.Release()
		}()
		go func() {
			defer group.Done()
			<-start
			limiter.retireGeneration("provider-a")
			lease, ok := limiter.Acquire("provider-a", 1)
			if !ok {
				newLease <- nil
				return
			}
			newLease <- lease
		}()
		close(start)
		group.Wait()

		replacement := <-newLease
		if replacement == nil {
			t.Fatalf("iteration %d: replacement Acquire() failed", iteration)
		}
		if got := limiter.Current("provider-a"); got != 1 {
			t.Fatalf("iteration %d: replacement count = %d, want 1", iteration, got)
		}
		replacement.Release()
	}
}
