package selector

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/errorrule"
	"github.com/doraemonkeys/switch-a/internal/model"
	storepkg "github.com/doraemonkeys/switch-a/internal/store"

	"go.uber.org/zap"
)

func TestLeaseCapabilitiesHaveSafeZeroValuesAndVisibleOwnership(t *testing.T) {
	var nilSlot *SlotLease
	if nilSlot.Release() || nilSlot.Held() || nilSlot.ProviderID() != "" || nilSlot.Generation() != 0 {
		t.Fatal("nil SlotLease exposed ownership")
	}
	zeroSlot := &SlotLease{}
	if zeroSlot.Release() || zeroSlot.Held() || zeroSlot.ProviderID() != "" || zeroSlot.Generation() != 0 {
		t.Fatal("zero SlotLease exposed ownership")
	}

	var nilProviderLease *ProviderLease
	if nilProviderLease.Provider() != nil || nilProviderLease.Slot() != nil || nilProviderLease.Release() ||
		nilProviderLease.Held() || nilProviderLease.ProviderID() != "" || nilProviderLease.Generation() != 0 {
		t.Fatal("nil ProviderLease exposed ownership")
	}
	if newProviderLease(nil, zeroSlot) != nil || newProviderLease(&model.Provider{ID: "provider-a"}, nil) != nil {
		t.Fatal("newProviderLease accepted incomplete ownership")
	}

	limiter := NewConcurrencyLimiter()
	slot, acquired := limiter.Acquire("provider-a", 1)
	if !acquired {
		t.Fatal("Acquire() = false")
	}
	lease := newProviderLease(&model.Provider{ID: "provider-a"}, slot)
	if !slot.Held() || !lease.Held() || lease.Provider().ID != "provider-a" || lease.Slot() == nil {
		t.Fatal("valid lease did not expose held ownership")
	}
	lease.replaceProvider(nil)
	lease.replaceProvider(&model.Provider{ID: "provider-a", Name: "live"})
	if lease.Provider().Name != "live" {
		t.Fatal("provider snapshot was not replaced")
	}
	if !lease.Release() || lease.Held() {
		t.Fatal("provider lease did not transition to released")
	}
}

func TestConcurrencyLimiterZeroValueAndLifecycleGuards(t *testing.T) {
	var nilLimiter *ConcurrencyLimiter
	if lease, ok := nilLimiter.Acquire("provider-a", 1); ok || lease != nil {
		t.Fatal("nil limiter acquired capacity")
	}
	if nilLimiter.Current("provider-a") != 0 || nilLimiter.isCurrent(nil) || nilLimiter.prepare(nil, nil) {
		t.Fatal("nil limiter exposed lifecycle state")
	}
	nilLimiter.retireGeneration("provider-a")

	limiter := &ConcurrencyLimiter{}
	if lease, ok := limiter.Acquire("", 1); ok || lease != nil {
		t.Fatal("empty provider ID acquired capacity")
	}
	lease, acquired := limiter.Acquire("provider-a", 1)
	if !acquired {
		t.Fatal("zero-value limiter Acquire() = false")
	}
	if _, acquired := limiter.Acquire("provider-a", 1); acquired {
		t.Fatal("Acquire() exceeded limit")
	}
	if !limiter.prepare(lease, nil) {
		t.Fatal("prepare() rejected current lease")
	}
	if limiter.prepare(lease, func() bool { return false }) {
		t.Fatal("prepare() ignored transition failure")
	}
	if _, acquired := limiter.acquireInGeneration(nil, 1); acquired {
		t.Fatal("acquireInGeneration(nil) succeeded")
	}
	if !lease.Release() {
		t.Fatal("Release() = false")
	}
	if lease.Release() {
		t.Fatal("duplicate capability release changed the counter twice")
	}
	limiter.retireGeneration("")
	if limiter.isCurrent(lease) || limiter.prepare(lease, nil) {
		t.Fatal("released lease remained current")
	}
}

func TestProviderRejectionErrorContract(t *testing.T) {
	cause := errors.New("database unavailable")
	rejection := &ProviderRejectionError{Reason: errorrule.ReasonProviderLookupError, Cause: cause}
	if got := rejection.Error(); got == "" || !errors.Is(rejection, cause) {
		t.Fatalf("rejection error contract = %q, unwrap = %v", got, errors.Unwrap(rejection))
	}
	var nilRejection *ProviderRejectionError
	if nilRejection.Error() == "" || nilRejection.Unwrap() != nil {
		t.Fatal("nil rejection error contract is unsafe")
	}
	if reason, ok := ProviderRejectionReason(errors.New("other")); ok || reason != "" {
		t.Fatalf("non-rejection extracted as (%q, %v)", reason, ok)
	}
}

func TestRetryEligibilityFailsClosedOnDependencyAndCredentialLoss(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*lifecycleStore)
		mutate  func(*lifecycleStore)
		want    errorrule.DecisionReason
	}{
		{
			name: "not found error",
			mutate: func(store *lifecycleStore) {
				store.providerErr = storepkg.ErrNotFound
			},
			want: errorrule.ReasonProviderDeleted,
		},
		{
			name: "routing source error",
			mutate: func(store *lifecycleStore) {
				store.routingErr = errors.New("routing unavailable")
			},
			want: errorrule.ReasonProviderLookupError,
		},
		{
			name: "group lookup error",
			prepare: func(store *lifecycleStore) {
				groupID := "group-a"
				store.providers["provider-a"].GroupID = &groupID
				store.groups[groupID] = &model.Group{ID: groupID, Enabled: true}
			},
			mutate: func(store *lifecycleStore) {
				store.groupErr = errors.New("group unavailable")
			},
			want: errorrule.ReasonProviderLookupError,
		},
		{
			name: "auth source error",
			mutate: func(store *lifecycleStore) {
				store.authErr = errors.New("auth unavailable")
			},
			want: errorrule.ReasonProviderLookupError,
		},
		{
			name: "api key removed",
			mutate: func(store *lifecycleStore) {
				store.providers["provider-a"].APIKey = ""
			},
			want: errorrule.ReasonAuthUnavailable,
		},
		{
			name: "login credential removed",
			prepare: func(store *lifecycleStore) {
				provider := store.providers["provider-a"]
				provider.CredentialType = model.ProviderCredentialTypeChatGPT
				provider.Credential = &model.ProviderCredential{ProviderID: provider.ID, SecretData: "session"}
			},
			mutate: func(store *lifecycleStore) {
				store.providers["provider-a"].Credential = nil
			},
			want: errorrule.ReasonAuthUnavailable,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newLifecycleStore(retryTestProvider("provider-a"))
			if test.prepare != nil {
				store.mutate(test.prepare)
			}
			selector, _ := newRetryTestSelector(store, nil)
			current := selectRetryCurrent(t, selector)
			store.mutate(test.mutate)

			_, err := selector.ReserveSameProviderRetry(context.Background(), retryPermitInput(t, current.Lease))
			reason, ok := ProviderRejectionReason(err)
			if !ok || reason != test.want {
				t.Fatalf("rejection = (%q, %v), want %q; error = %v", reason, ok, test.want, err)
			}
			current.Lease.Release()
		})
	}
}

func TestRetryAndReservationNilAndCancellationContracts(t *testing.T) {
	var nilPermit *RetryPermit
	if nilPermit.Provider() != nil || nilPermit.CurrentLease() != nil || nilPermit.Release() {
		t.Fatal("nil retry permit exposed ownership")
	}
	if _, err := nilPermit.Activate(); !errors.Is(err, ErrDispatchPermitReleased) {
		t.Fatalf("nil permit Activate() error = %v", err)
	}

	var nilReservation *ProviderReservation
	if nilReservation.Provider() != nil || nilReservation.Metadata() != (SelectionMetadata{}) ||
		nilReservation.Activate() != nil || nilReservation.Release() {
		t.Fatal("nil reservation exposed ownership")
	}
	if err := nilReservation.PrepareActivation(context.Background()); !errors.Is(err, ErrReservationReleased) {
		t.Fatalf("nil reservation PrepareActivation() error = %v", err)
	}

	store := newLifecycleStore(retryTestProvider("provider-a"))
	selector, _ := newRetryTestSelector(store, nil)
	current := selectRetryCurrent(t, selector)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := selector.ReserveSameProviderRetry(cancelled, retryPermitInput(t, current.Lease)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled ReserveSameProviderRetry() error = %v", err)
	}
	current.Lease.Release()

	if reservation, err := selector.ReserveAlternate(context.Background(), AlternateReservationRequest{}); !errors.Is(err, internal.ErrNoProvider) || reservation != nil {
		t.Fatalf("empty ReserveAlternate() = (%v, %v)", reservation, err)
	}
}

func TestStickySelectionCarriesLease(t *testing.T) {
	provider := retryTestProvider("provider-a")
	store := newLifecycleStore(provider)
	clock := &mockClock{now: time.Now()}
	cache := NewMemoryStickyCache(clock)
	limiter := NewConcurrencyLimiter()
	selector := NewSelector(Config{
		Store:       store,
		StickyCache: cache,
		Limiter:     limiter,
		Clock:       clock,
		Logger:      zap.NewNop(),
	})
	req := &model.SelectRequest{
		ClientIP:   "127.0.0.1",
		User:       "user-a",
		APIType:    "claude",
		StickyMode: model.StickyModeAPIType,
	}
	cache.Set(BuildContinuityKey(req), provider.ID, time.Minute)

	result, err := selector.SelectWithMetadata(context.Background(), req)
	if err != nil {
		t.Fatalf("SelectWithMetadata() error = %v", err)
	}
	if result.Lease == nil || result.Metadata.Source != SelectionSourceStickyContinuity {
		t.Fatalf("sticky result = %+v", result)
	}
	if !result.Lease.Release() || limiter.Current(provider.ID) != 0 {
		t.Fatal("sticky lease did not release exact capacity")
	}
}

func TestSelectActivePropagatesContextLookupAndEligibilityErrors(t *testing.T) {
	provider := retryTestProvider("provider-a")
	provider.Concurrency = 2
	store := newLifecycleStore(provider)
	selector, _ := newRetryTestSelector(store, nil)
	current := selectRetryCurrent(t, selector)

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := selector.SelectActive(cancelled, &model.SelectRequest{APIType: "claude"}, current.Lease); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled SelectActive() error = %v", err)
	}
	store.mutate(func(store *lifecycleStore) { store.providerErr = errors.New("lookup failed") })
	if _, err := selector.SelectActive(context.Background(), &model.SelectRequest{APIType: "claude"}, current.Lease); err == nil {
		t.Fatal("SelectActive() swallowed lookup error")
	}
	store.mutate(func(store *lifecycleStore) {
		store.providerErr = nil
		store.authErr = errors.New("auth failed")
	})
	if _, err := selector.SelectActive(context.Background(), &model.SelectRequest{APIType: "claude"}, current.Lease); err == nil {
		t.Fatal("SelectActive() swallowed eligibility error")
	}
	current.Lease.Release()
}

func TestCloneSelectRequestDetachesAllMutableContinuity(t *testing.T) {
	group := &model.ProviderSwitchHistory{AttemptChain: []string{"provider-a"}}
	continuity := &model.ProviderContinuityContext{ContaminatedVendors: []string{"vendor-a"}}
	seed := &model.VisibleContinuitySeedCandidate{SeedID: "seed-a"}
	failover := &model.FailoverContext{
		ContaminatedVendors: []string{"vendor-a"},
		AttemptChain:        []string{"provider-a"},
	}
	source := &model.SelectRequest{
		APIType:                        "claude",
		ProviderSwitchHistory:          group,
		ProviderContinuityContext:      continuity,
		VisibleContinuitySeedCandidate: seed,
		FailoverContext:                failover,
	}
	clone := cloneSelectRequest(source)
	group.AttemptChain[0] = "mutated"
	continuity.ContaminatedVendors[0] = "mutated"
	seed.SeedID = "mutated"
	failover.ContaminatedVendors[0] = "mutated"
	failover.AttemptChain[0] = "mutated"

	if clone.ProviderSwitchHistory.AttemptChain[0] != "provider-a" ||
		clone.ProviderContinuityContext.ContaminatedVendors[0] != "vendor-a" ||
		clone.VisibleContinuitySeedCandidate.SeedID != "seed-a" ||
		clone.FailoverContext.ContaminatedVendors[0] != "vendor-a" ||
		clone.FailoverContext.AttemptChain[0] != "provider-a" {
		t.Fatal("cloneSelectRequest shared mutable continuity state")
	}
	if cloneSelectRequest(nil) != nil || cloneExclusions(nil) != nil {
		t.Fatal("nil clone helpers returned state")
	}
}
