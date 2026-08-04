package selector

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/errorrule"
	"github.com/doraemonkeys/switch-a/internal/model"
)

func dispatchPermitRequest(current *ProviderLease) SameProviderDispatchRequest {
	return SameProviderDispatchRequest{
		Current: current,
		Request: &model.SelectRequest{APIType: "claude"},
	}
}

func TestProviderDispatchPermitAdoptsValidatedSnapshotOnActivation(t *testing.T) {
	provider := retryTestProvider("provider-a")
	provider.Name = "before-refresh"
	store := newLifecycleStore(provider)
	selector, limiter := newRetryTestSelector(store, nil)
	current := selectRetryCurrent(t, selector)
	defer current.Lease.Release()

	original := current.Lease.Provider()
	store.mutate(func(store *lifecycleStore) {
		store.providers[provider.ID].Name = "after-refresh"
		store.providers[provider.ID].APIKey = "rotated-key"
	})

	permit, err := selector.ReserveSameProviderDispatch(
		context.Background(),
		dispatchPermitRequest(current.Lease),
	)
	if err != nil {
		t.Fatalf("ReserveSameProviderDispatch() error = %v", err)
	}
	validated := permit.Provider()
	if validated == nil || validated.Name != "after-refresh" || validated.APIKey != "rotated-key" {
		t.Fatalf("validated provider = %#v", validated)
	}
	if current.Lease.Provider() != original || current.Lease.Provider().Name != "before-refresh" {
		t.Fatal("reservation changed the active snapshot before activation")
	}
	if got := limiter.Current(provider.ID); got != 1 {
		t.Fatalf("dispatch reservation acquired another slot: Current() = %d", got)
	}

	activated, err := permit.Activate()
	if err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	if activated != validated || current.Lease.Provider() != validated {
		t.Fatal("activation did not atomically adopt the validated snapshot")
	}
	if second, err := permit.Activate(); second != nil || !errors.Is(err, ErrDispatchPermitActivated) {
		t.Fatalf("second Activate() = (%#v, %v), want ErrDispatchPermitActivated", second, err)
	}
	if permit.Release() {
		t.Fatal("Release() succeeded after activation")
	}
}

func TestProviderDispatchPermitReleaseIsCopySafeAndReusable(t *testing.T) {
	store := newLifecycleStore(retryTestProvider("provider-a"))
	selector, limiter := newRetryTestSelector(store, nil)
	current := selectRetryCurrent(t, selector)
	defer current.Lease.Release()
	original := current.Lease.Provider()

	permit, err := selector.ReserveSameProviderDispatch(
		context.Background(),
		dispatchPermitRequest(current.Lease),
	)
	if err != nil {
		t.Fatalf("ReserveSameProviderDispatch() error = %v", err)
	}
	copyOfPermit := *permit
	if !copyOfPermit.Release() || permit.Release() {
		t.Fatal("release was not copy-safe and idempotent")
	}
	if provider, err := permit.Activate(); provider != nil || !errors.Is(err, ErrDispatchPermitReleased) {
		t.Fatalf("Activate() after release = (%#v, %v), want ErrDispatchPermitReleased", provider, err)
	}
	if current.Lease.Provider() != original || !current.Lease.Held() {
		t.Fatal("permit release changed the active provider lease")
	}
	if got := limiter.Current(current.Lease.ProviderID()); got != 1 {
		t.Fatalf("permit release changed slot count to %d", got)
	}

	next, err := selector.ReserveSameProviderDispatch(
		context.Background(),
		dispatchPermitRequest(current.Lease),
	)
	if err != nil {
		t.Fatalf("reservation after release error = %v", err)
	}
	if !next.Release() {
		t.Fatal("reservation ownership was not reusable after release")
	}
}

func TestProviderDispatchPermitSerializesGenericAndRetryReservations(t *testing.T) {
	tests := []struct {
		name  string
		first func(*testing.T, *Selector, *ProviderLease) func()
		try   func(*testing.T, *Selector, *ProviderLease) (func(), error)
	}{
		{
			name: "generic blocks retry",
			first: func(t *testing.T, selector *Selector, current *ProviderLease) func() {
				t.Helper()
				permit, err := selector.ReserveSameProviderDispatch(
					context.Background(),
					dispatchPermitRequest(current),
				)
				if err != nil {
					t.Fatalf("ReserveSameProviderDispatch() error = %v", err)
				}
				return func() { permit.Release() }
			},
			try: func(t *testing.T, selector *Selector, current *ProviderLease) (func(), error) {
				t.Helper()
				permit, err := selector.ReserveSameProviderRetry(
					context.Background(),
					retryPermitInput(t, current),
				)
				if err != nil {
					return nil, err
				}
				return func() { permit.Release() }, nil
			},
		},
		{
			name: "retry blocks generic",
			first: func(t *testing.T, selector *Selector, current *ProviderLease) func() {
				t.Helper()
				permit, err := selector.ReserveSameProviderRetry(
					context.Background(),
					retryPermitInput(t, current),
				)
				if err != nil {
					t.Fatalf("ReserveSameProviderRetry() error = %v", err)
				}
				return func() { permit.Release() }
			},
			try: func(_ *testing.T, selector *Selector, current *ProviderLease) (func(), error) {
				permit, err := selector.ReserveSameProviderDispatch(
					context.Background(),
					dispatchPermitRequest(current),
				)
				if err != nil {
					return nil, err
				}
				return func() { permit.Release() }, nil
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newLifecycleStore(retryTestProvider("provider-a"))
			selector, _ := newRetryTestSelector(store, nil)
			current := selectRetryCurrent(t, selector)
			defer current.Lease.Release()

			releaseFirst := test.first(t, selector, current.Lease)
			if release, err := test.try(t, selector, current.Lease); !errors.Is(err, ErrDispatchPermitOutstanding) {
				if release != nil {
					release()
				}
				t.Fatalf("competing reservation error = %v, want ErrDispatchPermitOutstanding", err)
			}
			releaseFirst()
			releaseNext, err := test.try(t, selector, current.Lease)
			if err != nil {
				t.Fatalf("reservation after ownership release error = %v", err)
			}
			if releaseNext == nil {
				t.Fatal("successful reservation returned no cleanup ownership")
			}
			releaseNext()
		})
	}
}

func TestProviderDispatchPermitActivationRejectsRetiredGeneration(t *testing.T) {
	tests := []struct {
		name   string
		retire func(*lifecycleStore, *ConcurrencyLimiter)
	}{
		{
			name: "clear",
			retire: func(_ *lifecycleStore, limiter *ConcurrencyLimiter) {
				limiter.retireGeneration("provider-a")
			},
		},
		{
			name: "delete and clear",
			retire: func(store *lifecycleStore, limiter *ConcurrencyLimiter) {
				store.mutate(func(store *lifecycleStore) { delete(store.providers, "provider-a") })
				limiter.retireGeneration("provider-a")
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newLifecycleStore(retryTestProvider("provider-a"))
			selector, limiter := newRetryTestSelector(store, nil)
			current := selectRetryCurrent(t, selector)
			permit, err := selector.ReserveSameProviderDispatch(
				context.Background(),
				dispatchPermitRequest(current.Lease),
			)
			if err != nil {
				t.Fatalf("ReserveSameProviderDispatch() error = %v", err)
			}

			test.retire(store, limiter)
			provider, err := permit.Activate()
			if provider != nil {
				t.Fatalf("Activate() provider = %#v, want nil", provider)
			}
			if reason, ok := ProviderRejectionReason(err); !ok || reason != errorrule.ReasonProviderDeleted {
				t.Fatalf("Activate() rejection = (%q, %v), want provider_deleted; error = %v", reason, ok, err)
			}
			if permit.Release() {
				t.Fatal("rejected activation retained permit ownership")
			}

			replacement, acquired := limiter.Acquire("provider-a", 1)
			if !acquired {
				t.Fatal("recreated generation acquisition failed")
			}
			current.Lease.Release()
			if got := limiter.Current("provider-a"); got != 1 {
				t.Fatalf("old cleanup changed replacement generation count to %d", got)
			}
			replacement.Release()
		})
	}
}

func TestProviderLifecycleMutationClosesRetirementPersistenceGap(t *testing.T) {
	store := newLifecycleStore(retryTestProvider("provider-a"))
	selector, limiter := newRetryTestSelector(store, nil)
	current := selectRetryCurrent(t, selector)
	defer current.Lease.Release()

	mutationStarted := make(chan struct{})
	finishMutation := make(chan struct{})
	mutationDone := make(chan error, 1)
	go func() {
		mutationDone <- selector.RetireProviderGeneration("provider-a", func() error {
			close(mutationStarted)
			<-finishMutation
			store.mutate(func(store *lifecycleStore) {
				store.providers["provider-a"].Name = "persisted-after-retirement"
			})
			return nil
		})
	}()
	<-mutationStarted

	type acquisition struct {
		lease    *SlotLease
		acquired bool
	}
	acquiredGeneration := make(chan acquisition, 1)
	go func() {
		lease, acquired := limiter.Acquire("provider-a", 1)
		acquiredGeneration <- acquisition{lease: lease, acquired: acquired}
	}()

	select {
	case result := <-acquiredGeneration:
		if result.lease != nil {
			result.lease.Release()
		}
		t.Fatal("fresh generation became visible before persistence completed")
	case <-time.After(25 * time.Millisecond):
	}

	close(finishMutation)
	if err := <-mutationDone; err != nil {
		t.Fatalf("RetireProviderGeneration() error = %v", err)
	}
	result := <-acquiredGeneration
	if !result.acquired || result.lease == nil {
		t.Fatal("fresh generation was not available after persistence completed")
	}
	defer result.lease.Release()
	if result.lease.Generation() == current.Lease.Generation() {
		t.Fatalf("generation = %d, want a post-mutation generation", result.lease.Generation())
	}
}

func TestProviderDispatchPermitActivateReleaseRaceHasOneOwner(t *testing.T) {
	const iterations = 100
	for iteration := range iterations {
		store := newLifecycleStore(retryTestProvider("provider-a"))
		selector, _ := newRetryTestSelector(store, nil)
		current := selectRetryCurrent(t, selector)
		permit, err := selector.ReserveSameProviderDispatch(
			context.Background(),
			dispatchPermitRequest(current.Lease),
		)
		if err != nil {
			t.Fatalf("iteration %d: reserve error = %v", iteration, err)
		}
		copyOfPermit := *permit
		start := make(chan struct{})
		activated := make(chan error, 1)
		released := make(chan bool, 1)
		var group sync.WaitGroup
		group.Add(2)
		go func() {
			defer group.Done()
			<-start
			_, activateErr := permit.Activate()
			activated <- activateErr
		}()
		go func() {
			defer group.Done()
			<-start
			released <- copyOfPermit.Release()
		}()
		close(start)
		group.Wait()

		activateErr := <-activated
		releaseWon := <-released
		if activateErr == nil {
			if releaseWon {
				t.Fatalf("iteration %d: activation and release both won", iteration)
			}
		} else if !errors.Is(activateErr, ErrDispatchPermitReleased) || !releaseWon {
			t.Fatalf("iteration %d: Activate() error = %v, Release() = %v", iteration, activateErr, releaseWon)
		}

		next, err := selector.ReserveSameProviderDispatch(
			context.Background(),
			dispatchPermitRequest(current.Lease),
		)
		if err != nil {
			t.Fatalf("iteration %d: ownership was not reusable: %v", iteration, err)
		}
		next.Release()
		current.Lease.Release()
	}
}

func TestProviderDispatchPermitNilSafety(t *testing.T) {
	var permit *ProviderDispatchPermit
	if permit.Provider() != nil || permit.Release() {
		t.Fatal("nil permit exposed provider or release ownership")
	}
	if provider, err := permit.Activate(); provider != nil || !errors.Is(err, ErrDispatchPermitReleased) {
		t.Fatalf("nil Activate() = (%#v, %v), want ErrDispatchPermitReleased", provider, err)
	}

	var selector *Selector
	if reserved, err := selector.ReserveSameProviderDispatch(
		context.Background(),
		SameProviderDispatchRequest{},
	); reserved != nil || err == nil {
		t.Fatalf("invalid ReserveSameProviderDispatch() = (%#v, %v)", reserved, err)
	}
}
