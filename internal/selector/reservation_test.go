package selector

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/errorrule"
	"github.com/doraemonkeys/switch-a/internal/model"
)

func reserveTestAlternate(
	t *testing.T,
	selector *Selector,
	req *model.SelectRequest,
) *ProviderReservation {
	t.Helper()
	reservation, err := selector.ReserveAlternate(context.Background(), AlternateReservationRequest{
		Request:            req,
		ExcludeProviderIDs: map[string]bool{"provider-current": true},
	})
	if err != nil {
		t.Fatalf("ReserveAlternate() error = %v", err)
	}
	if reservation.Provider() == nil || reservation.Provider().ID != "provider-alternate" {
		t.Fatalf("ReserveAlternate() provider = %+v", reservation.Provider())
	}
	return reservation
}

func newAlternateTestSelector() (*Selector, *ConcurrencyLimiter, *lifecycleStore) {
	current := retryTestProvider("provider-current")
	alternate := retryTestProvider("provider-alternate")
	store := newLifecycleStore(current, alternate)
	selector, limiter := newRetryTestSelector(store, nil)
	return selector, limiter, store
}

func TestProviderReservationPrepareActivateTransfersExactLease(t *testing.T) {
	selector, limiter, _ := newAlternateTestSelector()
	req := &model.SelectRequest{APIType: "claude", Model: "model-a"}
	exclusions := map[string]bool{"provider-current": true}
	reservation, err := selector.ReserveAlternate(context.Background(), AlternateReservationRequest{
		Request:            req,
		ExcludeProviderIDs: exclusions,
	})
	if err != nil {
		t.Fatalf("ReserveAlternate() error = %v", err)
	}
	if got := limiter.Current("provider-alternate"); got != 1 {
		t.Fatalf("reserved Current() = %d, want 1", got)
	}
	if reservation.Metadata().Source != SelectionSourceAlternate {
		t.Fatalf("reservation source = %q", reservation.Metadata().Source)
	}

	// Reservation owns an immutable selection command; later tracker/request
	// mutation cannot rewrite the provider that PrepareActivation validates.
	req.APIType = "openai"
	req.Model = "mutated"
	exclusions["provider-alternate"] = true
	if err := reservation.PrepareActivation(context.Background()); err != nil {
		t.Fatalf("PrepareActivation() error = %v", err)
	}
	if err := reservation.PrepareActivation(context.Background()); err != nil {
		t.Fatalf("second PrepareActivation() error = %v", err)
	}

	lease := reservation.Activate()
	if lease == nil || lease.ProviderID() != "provider-alternate" {
		t.Fatalf("Activate() lease = %+v", lease)
	}
	if reservation.Activate() != lease {
		t.Fatal("second Activate() did not return transferred lease")
	}
	if reservation.Release() {
		t.Fatal("reservation released after lease transfer")
	}
	if got := limiter.Current("provider-alternate"); got != 1 {
		t.Fatalf("activation changed capacity count to %d", got)
	}
	if !lease.Release() || lease.Release() {
		t.Fatal("activated lease did not release exactly once")
	}
}

func TestProviderReservationFailedPrepareRollsBackExactlyOnce(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*lifecycleStore, *ConcurrencyLimiter)
		want   errorrule.DecisionReason
	}{
		{
			name: "deleted",
			mutate: func(store *lifecycleStore, limiter *ConcurrencyLimiter) {
				delete(store.providers, "provider-alternate")
				limiter.retireGeneration("provider-alternate")
			},
			want: errorrule.ReasonProviderDeleted,
		},
		{
			name: "disabled",
			mutate: func(store *lifecycleStore, _ *ConcurrencyLimiter) {
				store.providers["provider-alternate"].Enabled = false
			},
			want: errorrule.ReasonProviderDisabled,
		},
		{
			name: "api removed",
			mutate: func(store *lifecycleStore, _ *ConcurrencyLimiter) {
				store.providers["provider-alternate"].APITypes = nil
			},
			want: errorrule.ReasonAPIRemoved,
		},
		{
			name: "routing changed",
			mutate: func(store *lifecycleStore, _ *ConcurrencyLimiter) {
				target := "provider-current"
				store.routingPolicies = []model.RoutingPolicy{{APIType: "claude", Enabled: true, TargetProviderID: &target}}
			},
			want: errorrule.ReasonRoutingChanged,
		},
		{
			name: "group disabled",
			mutate: func(store *lifecycleStore, _ *ConcurrencyLimiter) {
				groupID := "group-a"
				store.providers["provider-alternate"].GroupID = &groupID
				store.groups[groupID] = &model.Group{ID: groupID, Enabled: false}
			},
			want: errorrule.ReasonGroupDisabled,
		},
		{
			name: "auth unavailable",
			mutate: func(store *lifecycleStore, _ *ConcurrencyLimiter) {
				store.authStates["provider-alternate"].Status = model.ProviderAuthStatusReauthRequired
			},
			want: errorrule.ReasonAuthUnavailable,
		},
		{
			name: "lookup error",
			mutate: func(store *lifecycleStore, _ *ConcurrencyLimiter) {
				store.providerErr = errors.New("lookup failed")
			},
			want: errorrule.ReasonProviderLookupError,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			selector, limiter, store := newAlternateTestSelector()
			reservation := reserveTestAlternate(t, selector, &model.SelectRequest{APIType: "claude"})
			generation := reservation.state.lease.Slot().state.generation
			store.mutate(func(store *lifecycleStore) { test.mutate(store, limiter) })

			err := reservation.PrepareActivation(context.Background())
			reason, ok := ProviderRejectionReason(err)
			if !ok || reason != test.want {
				t.Fatalf("PrepareActivation() rejection = (%q, %v), want %q; error = %v", reason, ok, test.want, err)
			}
			if reservation.Release() {
				t.Fatal("failed reservation released twice")
			}
			if got := generation.active.Load(); got != 0 {
				t.Fatalf("detached reserved generation count = %d, want 0", got)
			}
			if reservation.Activate() != nil {
				t.Fatal("failed reservation activated")
			}
		})
	}
}

func TestProviderReservationLifecycleMutationWaitsForRollback(t *testing.T) {
	selector, _, store := newAlternateTestSelector()
	reservation := reserveTestAlternate(t, selector, &model.SelectRequest{APIType: "claude"})
	generation := reservation.state.lease.Slot().state.generation
	entered, proceed := store.blockProviderLookup()

	errResult := make(chan error, 1)
	go func() {
		errResult <- reservation.PrepareActivation(context.Background())
	}()
	<-entered
	mutationEntered := make(chan struct{})
	mutationDone := make(chan error, 1)
	go func() {
		mutationDone <- selector.RetireProviderGeneration("provider-alternate", func() error {
			close(mutationEntered)
			store.mutate(func(store *lifecycleStore) { delete(store.providers, "provider-alternate") })
			return nil
		})
	}()
	select {
	case <-mutationEntered:
		t.Fatal("lifecycle mutation crossed an in-flight prepare read")
	case <-time.After(25 * time.Millisecond):
	}
	close(proceed)

	if err := <-errResult; err != nil {
		t.Fatalf("PrepareActivation() error = %v", err)
	}
	select {
	case <-mutationEntered:
		t.Fatal("lifecycle mutation crossed the prepared-to-activation boundary")
	case <-time.After(25 * time.Millisecond):
	}
	if !reservation.Release() {
		t.Fatal("prepared reservation rollback did not release ownership")
	}
	if err := <-mutationDone; err != nil {
		t.Fatalf("RetireProviderGeneration() error = %v", err)
	}
	if got := generation.active.Load(); got != 0 {
		t.Fatalf("rolled-back reservation retained %d slots", got)
	}
}

func TestProviderReservationCancellationAndExplicitReleaseRollback(t *testing.T) {
	selector, limiter, _ := newAlternateTestSelector()
	reservation := reserveTestAlternate(t, selector, &model.SelectRequest{APIType: "claude"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := reservation.PrepareActivation(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("PrepareActivation() error = %v, want context.Canceled", err)
	}
	if got := limiter.Current("provider-alternate"); got != 0 {
		t.Fatalf("cancelled reservation retained %d slots", got)
	}

	reservation = reserveTestAlternate(t, selector, &model.SelectRequest{APIType: "claude"})
	copyOfReservation := *reservation
	if !copyOfReservation.Release() || reservation.Release() {
		t.Fatal("explicit reservation release was not copy-safe and idempotent")
	}
	if reservation.Activate() != nil {
		t.Fatal("released reservation activated")
	}
}

func TestProviderReservationPrepareAndReleaseRaceLeavesNoCapacity(t *testing.T) {
	const iterations = 200
	for iteration := range iterations {
		selector, limiter, _ := newAlternateTestSelector()
		reservation := reserveTestAlternate(t, selector, &model.SelectRequest{APIType: "claude"})
		start := make(chan struct{})
		var group sync.WaitGroup
		group.Add(2)
		go func() {
			defer group.Done()
			<-start
			_ = reservation.PrepareActivation(context.Background())
		}()
		go func() {
			defer group.Done()
			<-start
			reservation.Release()
		}()
		close(start)
		group.Wait()

		if got := limiter.Current("provider-alternate"); got != 0 {
			t.Fatalf("iteration %d: Current() = %d, want 0", iteration, got)
		}
	}
}

func TestProviderReservationConcurrentPrepareIsIdempotent(t *testing.T) {
	selector, _, store := newAlternateTestSelector()
	reservation := reserveTestAlternate(t, selector, &model.SelectRequest{APIType: "claude"})

	const preparers = 16
	entered := make(chan struct{}, preparers)
	proceed := make(chan struct{})
	store.mutate(func(store *lifecycleStore) {
		store.getProviderEntered = entered
		store.getProviderContinue = proceed
	})

	errorsSeen := make(chan error, preparers)
	var group sync.WaitGroup
	group.Add(preparers)
	for range preparers {
		go func() {
			defer group.Done()
			errorsSeen <- reservation.PrepareActivation(context.Background())
		}()
	}
	for range preparers {
		<-entered
	}
	close(proceed)
	group.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		if err != nil {
			t.Fatalf("concurrent PrepareActivation() error = %v", err)
		}
	}
	lease := reservation.Activate()
	if lease == nil {
		t.Fatal("concurrently prepared reservation did not activate")
	}
	lease.Release()
}

func TestSelectActiveCarriesDistinctLeaseWithinExactGeneration(t *testing.T) {
	provider := retryTestProvider("provider-a")
	provider.Concurrency = 2
	store := newLifecycleStore(provider)
	selector, limiter := newRetryTestSelector(store, nil)
	current := selectRetryCurrent(t, selector)

	active, err := selector.SelectActive(
		context.Background(),
		&model.SelectRequest{APIType: "claude"},
		current.Lease,
	)
	if err != nil {
		t.Fatalf("SelectActive() error = %v", err)
	}
	if active.Lease == nil || active.Lease == current.Lease {
		t.Fatal("SelectActive() did not return a distinct lease")
	}
	if active.Lease.Generation() != current.Lease.Generation() {
		t.Fatal("SelectActive() crossed provider generations")
	}
	if active.Metadata.Source != SelectionSourceActiveContinuity {
		t.Fatalf("SelectActive() source = %q", active.Metadata.Source)
	}
	if got := limiter.Current("provider-a"); got != 2 {
		t.Fatalf("Current() = %d, want 2", got)
	}

	limiter.retireGeneration("provider-a")
	newSlot, acquired := limiter.Acquire("provider-a", 2)
	if !acquired {
		t.Fatal("replacement Acquire() = false")
	}
	if _, err := selector.SelectActive(context.Background(), &model.SelectRequest{APIType: "claude"}, current.Lease); !errors.Is(err, internal.ErrNoProvider) {
		t.Fatalf("SelectActive(old generation) error = %v, want ErrNoProvider", err)
	}
	current.Lease.Release()
	active.Lease.Release()
	if got := limiter.Current("provider-a"); got != 1 {
		t.Fatalf("old active releases changed replacement count to %d", got)
	}
	newSlot.Release()
}

func TestSelectActiveHonorsGeneralHealthAndConcurrency(t *testing.T) {
	provider := retryTestProvider("provider-a")
	provider.Concurrency = 1
	store := newLifecycleStore(provider)
	health := newMockHealthChecker()
	selector, _ := newRetryTestSelector(store, health)
	current := selectRetryCurrent(t, selector)

	if _, err := selector.SelectActive(context.Background(), &model.SelectRequest{APIType: "claude"}, current.Lease); !errors.Is(err, internal.ErrNoProvider) {
		t.Fatalf("SelectActive(at concurrency limit) error = %v", err)
	}
	health.available["provider-a"] = false
	current.Lease.Release()
	// The released capability cannot authorize active reuse even though capacity
	// became free; ownership and availability are independent constraints.
	if _, err := selector.SelectActive(context.Background(), &model.SelectRequest{APIType: "claude"}, current.Lease); !errors.Is(err, internal.ErrNoProvider) {
		t.Fatalf("SelectActive(released/unhealthy) error = %v", err)
	}
}
