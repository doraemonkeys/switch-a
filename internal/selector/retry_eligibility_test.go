package selector

import (
	"context"
	"crypto/sha256"
	"errors"
	"sync"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/errorrule"
	"github.com/doraemonkeys/switch-a/internal/model"
	storepkg "github.com/doraemonkeys/switch-a/internal/store"

	"go.uber.org/zap"
)

const retryTestRuleID = errorrule.RuleID("123e4567-e89b-42d3-a456-426614174000")

type lifecycleStore struct {
	mu sync.RWMutex

	providers       map[string]*model.Provider
	providerErr     error
	groups          map[string]*model.Group
	groupErr        error
	authErr         error
	routingPolicies []model.RoutingPolicy
	routingErr      error

	getProviderEntered  chan struct{}
	getProviderContinue chan struct{}
}

func newLifecycleStore(providers ...model.Provider) *lifecycleStore {
	result := &lifecycleStore{
		providers: make(map[string]*model.Provider, len(providers)),
		groups:    make(map[string]*model.Group),
	}
	for i := range providers {
		provider := cloneTestProvider(&providers[i])
		result.providers[provider.ID] = provider
	}
	return result
}

func (s *lifecycleStore) ListProvidersByAPIType(_ context.Context, apiType string) ([]model.Provider, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.providerErr != nil {
		return nil, s.providerErr
	}
	if s.authErr != nil {
		return nil, s.authErr
	}
	providers := make([]model.Provider, 0, len(s.providers))
	for _, provider := range s.providers {
		if provider.Enabled && providerSupportsAPIType(provider, apiType) {
			providers = append(providers, *cloneTestProvider(provider))
		}
	}
	return providers, nil
}

func (s *lifecycleStore) GetProvider(ctx context.Context, id string) (*model.Provider, error) {
	s.mu.RLock()
	entered := s.getProviderEntered
	proceed := s.getProviderContinue
	s.mu.RUnlock()
	if entered != nil {
		select {
		case entered <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if proceed != nil {
		select {
		case <-proceed:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.providerErr != nil {
		return nil, s.providerErr
	}
	if s.authErr != nil {
		return nil, s.authErr
	}
	provider := s.providers[id]
	return cloneTestProvider(provider), nil
}

func (s *lifecycleStore) GetGroup(_ context.Context, id string) (*model.Group, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.groupErr != nil {
		return nil, s.groupErr
	}
	group := s.groups[id]
	if group == nil {
		return nil, storepkg.ErrNotFound
	}
	clone := *group
	return &clone, nil
}

func (s *lifecycleStore) GetConfig(_ context.Context, _ string) (string, error) {
	return StrategyPriority, nil
}

func (s *lifecycleStore) ListRoutingPoliciesByAPIType(_ context.Context, apiType string) ([]model.RoutingPolicy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.routingErr != nil {
		return nil, s.routingErr
	}
	result := make([]model.RoutingPolicy, 0, len(s.routingPolicies))
	for _, policy := range s.routingPolicies {
		if policy.APIType == apiType {
			result = append(result, policy)
		}
	}
	return result, nil
}

func (s *lifecycleStore) mutate(mutation func(*lifecycleStore)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	mutation(s)
}

func (s *lifecycleStore) blockProviderLookup() (<-chan struct{}, chan<- struct{}) {
	entered := make(chan struct{}, 1)
	proceed := make(chan struct{})
	s.mutate(func(store *lifecycleStore) {
		store.getProviderEntered = entered
		store.getProviderContinue = proceed
	})
	return entered, proceed
}

func cloneTestProvider(provider *model.Provider) *model.Provider {
	if provider == nil {
		return nil
	}
	return cloneProviderSelectionSnapshot(provider)
}

func retryTestProvider(id string) model.Provider {
	digest := sha256.Sum256([]byte("test-subject-" + id))
	subject, _ := credentialsession.KeyedDigestSubject("test-hmac", digest[:])
	return model.Provider{
		ID:          id,
		Enabled:     true,
		Concurrency: 1,
		APITypes: []model.ProviderAPIType{{
			ProviderID: id,
			APIType:    "claude",
			BaseURL:    "https://" + id + ".example.test",
		}},
		CredentialSessions: []credentialsession.RouteSnapshot{{
			RouteTargetID: id,
			APIType:       "claude",
			VendorScope:   "test-vendor",
			Credential: credentialsession.Snapshot{
				SessionID:  "test-session-" + id,
				Kind:       credentialsession.KindAPIKey,
				SecretData: "test-key",
				Version:    1,
				Subject:    subject,
				AuthState:  credentialsession.AuthState{Status: credentialsession.AuthStatusActive},
			},
		}},
	}
}

func newRetryTestSelector(store *lifecycleStore, health HealthChecker) (*Selector, *ConcurrencyLimiter) {
	limiter := NewConcurrencyLimiter()
	return NewSelector(Config{
		Store:         store,
		HealthChecker: health,
		Limiter:       limiter,
		Logger:        zap.NewNop(),
	}), limiter
}

func selectRetryCurrent(t *testing.T, selector *Selector) *SelectResult {
	t.Helper()
	result, err := selector.SelectWithMetadata(context.Background(), &model.SelectRequest{APIType: "claude"})
	if err != nil {
		t.Fatalf("SelectWithMetadata() error = %v", err)
	}
	if result.Lease == nil {
		t.Fatal("SelectWithMetadata() returned no lease")
	}
	return result
}

func retryPermitInput(t *testing.T, current *ProviderLease) SameProviderRetryRequest {
	t.Helper()
	ledger, err := (errorrule.RetryLedger{}).StartAttempt(errorrule.ProviderID(current.ProviderID()), 3)
	if err != nil {
		t.Fatalf("StartAttempt() error = %v", err)
	}
	return SameProviderRetryRequest{
		Current: current,
		Request: &model.SelectRequest{APIType: "claude"},
		RuleKey: errorrule.ProviderRuleKey{
			ProviderID: errorrule.ProviderID(current.ProviderID()),
			RuleID:     retryTestRuleID,
		},
		Ledger:            ledger,
		GlobalMaxAttempts: 3,
	}
}

func TestReserveSameProviderRetryIgnoresCircuitAndConcurrencyUntilActivation(t *testing.T) {
	store := newLifecycleStore(retryTestProvider("provider-a"))
	health := newMockHealthChecker()
	selector, limiter := newRetryTestSelector(store, health)
	current := selectRetryCurrent(t, selector)
	health.available[current.Provider().ID] = false

	input := retryPermitInput(t, current.Lease)
	permit, err := selector.ReserveSameProviderRetry(context.Background(), input)
	if err != nil {
		t.Fatalf("ReserveSameProviderRetry() error = %v", err)
	}
	if got := input.Ledger.LogicalAttemptsStarted(); got != 1 {
		t.Fatalf("input ledger charged before activation: %d", got)
	}
	if got := limiter.Current("provider-a"); got != 1 {
		t.Fatalf("retry acquired another slot: Current() = %d", got)
	}
	if permit.CurrentLease() != current.Lease || permit.Provider() == nil {
		t.Fatal("permit did not retain current capability and live provider")
	}

	activated, err := permit.Activate()
	if err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	if got := activated.LogicalAttemptsStarted(); got != 2 {
		t.Fatalf("activated logical attempts = %d, want 2", got)
	}
	key := input.RuleKey
	if got := activated.RuleRetriesScheduled(key); got != 1 {
		t.Fatalf("activated rule retries = %d, want 1", got)
	}
	if _, err := permit.Activate(); !errors.Is(err, ErrDispatchPermitActivated) {
		t.Fatalf("second Activate() error = %v, want ErrDispatchPermitActivated", err)
	}
	if permit.Release() {
		t.Fatal("Release() succeeded after activation")
	}
	current.Lease.Release()
}

func TestRetryPermitReleaseDoesNotChargeLedger(t *testing.T) {
	store := newLifecycleStore(retryTestProvider("provider-a"))
	selector, limiter := newRetryTestSelector(store, nil)
	current := selectRetryCurrent(t, selector)
	input := retryPermitInput(t, current.Lease)
	permit, err := selector.ReserveSameProviderRetry(context.Background(), input)
	if err != nil {
		t.Fatalf("ReserveSameProviderRetry() error = %v", err)
	}
	copyOfPermit := *permit
	if !copyOfPermit.Release() || permit.Release() {
		t.Fatal("permit release was not copy-safe and idempotent")
	}
	if _, err := permit.Activate(); !errors.Is(err, ErrDispatchPermitReleased) {
		t.Fatalf("Activate() error = %v, want ErrDispatchPermitReleased", err)
	}
	if got := input.Ledger.LogicalAttemptsStarted(); got != 1 {
		t.Fatalf("released permit charged input ledger: %d", got)
	}
	if got := limiter.Current("provider-a"); got != 1 {
		t.Fatalf("released permit changed held slot count to %d", got)
	}
	current.Lease.Release()
}

func TestRetryPermitActivationRejectsRetiredProviderGeneration(t *testing.T) {
	store := newLifecycleStore(retryTestProvider("provider-a"))
	selector, limiter := newRetryTestSelector(store, nil)
	current := selectRetryCurrent(t, selector)
	input := retryPermitInput(t, current.Lease)
	permit, err := selector.ReserveSameProviderRetry(context.Background(), input)
	if err != nil {
		t.Fatalf("ReserveSameProviderRetry() error = %v", err)
	}

	limiter.retireGeneration("provider-a")
	if _, err := permit.Activate(); err == nil {
		t.Fatal("Activate() authorized a retired provider generation")
	} else if reason, ok := ProviderRejectionReason(err); !ok || reason != errorrule.ReasonProviderDeleted {
		t.Fatalf("Activate() rejection = (%q, %v), want provider_deleted", reason, ok)
	}
	if input.Ledger.LogicalAttemptsStarted() != 1 {
		t.Fatal("rejected activation charged the retry ledger")
	}
	if permit.Release() {
		t.Fatal("rejected activation retained permit ownership")
	}
	current.Lease.Release()
}

func TestRetryPermitActivationReportsReleasedCurrentLease(t *testing.T) {
	store := newLifecycleStore(retryTestProvider("provider-a"))
	selector, _ := newRetryTestSelector(store, nil)
	current := selectRetryCurrent(t, selector)
	permit, err := selector.ReserveSameProviderRetry(context.Background(), retryPermitInput(t, current.Lease))
	if err != nil {
		t.Fatalf("ReserveSameProviderRetry() error = %v", err)
	}
	current.Lease.Release()

	if _, err := permit.Activate(); !errors.Is(err, ErrDispatchPermitReleased) {
		t.Fatalf("Activate() error = %v, want ErrDispatchPermitReleased", err)
	}
	if permit.Release() {
		t.Fatal("failed activation retained permit ownership")
	}
}

func TestRetryPermitActivationLinearizesWithGenerationClear(t *testing.T) {
	const iterations = 100
	for iteration := range iterations {
		store := newLifecycleStore(retryTestProvider("provider-a"))
		selector, limiter := newRetryTestSelector(store, nil)
		current := selectRetryCurrent(t, selector)
		input := retryPermitInput(t, current.Lease)
		permit, err := selector.ReserveSameProviderRetry(context.Background(), input)
		if err != nil {
			t.Fatalf("iteration %d: reserve error = %v", iteration, err)
		}

		start := make(chan struct{})
		activation := make(chan error, 1)
		var group sync.WaitGroup
		group.Add(2)
		go func() {
			defer group.Done()
			<-start
			_, activateErr := permit.Activate()
			activation <- activateErr
		}()
		go func() {
			defer group.Done()
			<-start
			limiter.retireGeneration("provider-a")
		}()
		close(start)
		group.Wait()

		activateErr := <-activation
		if activateErr != nil {
			if reason, ok := ProviderRejectionReason(activateErr); !ok || reason != errorrule.ReasonProviderDeleted {
				t.Fatalf("iteration %d: activation error = %v", iteration, activateErr)
			}
		}
		replacement, acquired := limiter.Acquire("provider-a", 1)
		if !acquired {
			t.Fatalf("iteration %d: recreated generation acquisition failed", iteration)
		}
		current.Lease.Release()
		if got := limiter.Current("provider-a"); got != 1 {
			t.Fatalf("iteration %d: old cleanup changed replacement count to %d", iteration, got)
		}
		replacement.Release()
	}
}

func TestReserveSameProviderRetrySerializesOutstandingLedgerCapacity(t *testing.T) {
	store := newLifecycleStore(retryTestProvider("provider-a"))
	selector, _ := newRetryTestSelector(store, nil)
	current := selectRetryCurrent(t, selector)
	input := retryPermitInput(t, current.Lease)

	const contenders = 16
	entered := make(chan struct{}, contenders)
	proceed := make(chan struct{})
	store.mutate(func(store *lifecycleStore) {
		store.getProviderEntered = entered
		store.getProviderContinue = proceed
	})

	permits := make(chan *RetryPermit, contenders)
	errorsSeen := make(chan error, contenders)
	var group sync.WaitGroup
	group.Add(contenders)
	for range contenders {
		go func() {
			defer group.Done()
			permit, err := selector.ReserveSameProviderRetry(context.Background(), input)
			permits <- permit
			errorsSeen <- err
		}()
	}
	for range contenders {
		<-entered
	}
	close(proceed)
	group.Wait()
	close(permits)
	close(errorsSeen)

	var winner *RetryPermit
	for permit := range permits {
		if permit == nil {
			continue
		}
		if winner != nil {
			t.Fatal("multiple permits reserved the same ledger capacity")
		}
		winner = permit
	}
	if winner == nil {
		t.Fatal("no retry permit won reservation")
	}
	successes := 0
	for err := range errorsSeen {
		if err == nil {
			successes++
			continue
		}
		if !errors.Is(err, ErrDispatchPermitOutstanding) {
			t.Fatalf("losing ReserveSameProviderRetry() error = %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful reservations = %d, want 1", successes)
	}
	if !winner.Release() {
		t.Fatal("winning permit did not release capacity reservation")
	}
	current.Lease.Release()
}

func TestSameProviderRetryStableRejectionReasons(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*lifecycleStore)
		want   errorrule.DecisionReason
	}{
		{
			name: "provider deleted",
			mutate: func(store *lifecycleStore) {
				delete(store.providers, "provider-a")
			},
			want: errorrule.ReasonProviderDeleted,
		},
		{
			name: "provider disabled",
			mutate: func(store *lifecycleStore) {
				store.providers["provider-a"].Enabled = false
			},
			want: errorrule.ReasonProviderDisabled,
		},
		{
			name: "api removed",
			mutate: func(store *lifecycleStore) {
				store.providers["provider-a"].APITypes = nil
			},
			want: errorrule.ReasonAPIRemoved,
		},
		{
			name: "routing changed",
			mutate: func(store *lifecycleStore) {
				target := "provider-b"
				store.routingPolicies = []model.RoutingPolicy{{
					APIType:          "claude",
					Enabled:          true,
					TargetProviderID: &target,
				}}
			},
			want: errorrule.ReasonRoutingChanged,
		},
		{
			name: "group disabled",
			mutate: func(store *lifecycleStore) {
				groupID := "group-a"
				store.providers["provider-a"].GroupID = &groupID
				store.groups[groupID] = &model.Group{ID: groupID, Enabled: false}
			},
			want: errorrule.ReasonGroupDisabled,
		},
		{
			name: "auth unavailable",
			mutate: func(store *lifecycleStore) {
				store.providers["provider-a"].CredentialSessions[0].Credential.AuthState.Status =
					credentialsession.AuthStatusReauthRequired
			},
			want: errorrule.ReasonAuthUnavailable,
		},
		{
			name: "provider lookup error",
			mutate: func(store *lifecycleStore) {
				store.providerErr = errors.New("lookup unavailable")
			},
			want: errorrule.ReasonProviderLookupError,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newLifecycleStore(retryTestProvider("provider-a"))
			selector, _ := newRetryTestSelector(store, nil)
			current := selectRetryCurrent(t, selector)
			store.mutate(test.mutate)

			permit, err := selector.ReserveSameProviderRetry(
				context.Background(),
				retryPermitInput(t, current.Lease),
			)
			if permit != nil {
				t.Fatal("ReserveSameProviderRetry() returned permit for rejected provider")
			}
			reason, ok := ProviderRejectionReason(err)
			if !ok || reason != test.want {
				t.Fatalf("rejection = (%q, %v), want %q; error = %v", reason, ok, test.want, err)
			}
			current.Lease.Release()
		})
	}
}

func TestSameProviderDispatchAcceptsCredentialRevisionWithinFrozenAuthority(t *testing.T) {
	store := newLifecycleStore(retryTestProvider("provider-a"))
	selector, _ := newRetryTestSelector(store, nil)
	current := selectRetryCurrent(t, selector)
	store.mutate(func(store *lifecycleStore) {
		credential := &store.providers["provider-a"].CredentialSessions[0].Credential
		credential.Version++
		credential.SecretData = "rotated-secret-for-the-same-subject"
	})

	permit, err := selector.ReserveSameProviderDispatch(context.Background(), SameProviderDispatchRequest{
		Current: current.Lease,
		Request: &model.SelectRequest{OperationID: "credential-refresh", APIType: "claude"},
	})
	if err != nil {
		t.Fatalf("ReserveSameProviderDispatch() error = %v", err)
	}
	if permit == nil {
		t.Fatal("ReserveSameProviderDispatch() returned no permit")
	}
	permit.Release()
	current.Lease.Release()
}

func TestSameProviderRetryRevalidationRacesObserveLiveMutation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*lifecycleStore, *ConcurrencyLimiter)
		want   errorrule.DecisionReason
	}{
		{
			name: "delete and clear",
			mutate: func(store *lifecycleStore, limiter *ConcurrencyLimiter) {
				store.mutate(func(store *lifecycleStore) { delete(store.providers, "provider-a") })
				limiter.retireGeneration("provider-a")
			},
			want: errorrule.ReasonProviderDeleted,
		},
		{
			name: "manual disable",
			mutate: func(store *lifecycleStore, _ *ConcurrencyLimiter) {
				store.mutate(func(store *lifecycleStore) { store.providers["provider-a"].Enabled = false })
			},
			want: errorrule.ReasonProviderDisabled,
		},
		{
			name: "lookup failure",
			mutate: func(store *lifecycleStore, _ *ConcurrencyLimiter) {
				store.mutate(func(store *lifecycleStore) { store.providerErr = errors.New("database offline") })
			},
			want: errorrule.ReasonProviderLookupError,
		},
		{
			name: "route mutation",
			mutate: func(store *lifecycleStore, _ *ConcurrencyLimiter) {
				store.mutate(func(store *lifecycleStore) {
					target := "provider-b"
					store.routingPolicies = []model.RoutingPolicy{{APIType: "claude", Enabled: true, TargetProviderID: &target}}
				})
			},
			want: errorrule.ReasonRoutingChanged,
		},
		{
			name: "auth mutation",
			mutate: func(store *lifecycleStore, _ *ConcurrencyLimiter) {
				store.mutate(func(store *lifecycleStore) {
					store.providers["provider-a"].CredentialSessions[0].Credential.AuthState.Status =
						credentialsession.AuthStatusReauthRequired
				})
			},
			want: errorrule.ReasonAuthUnavailable,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newLifecycleStore(retryTestProvider("provider-a"))
			selector, limiter := newRetryTestSelector(store, nil)
			current := selectRetryCurrent(t, selector)
			entered, proceed := store.blockProviderLookup()
			input := retryPermitInput(t, current.Lease)

			errResult := make(chan error, 1)
			go func() {
				_, err := selector.ReserveSameProviderRetry(
					context.Background(),
					input,
				)
				errResult <- err
			}()
			<-entered
			test.mutate(store, limiter)
			close(proceed)

			err := <-errResult
			reason, ok := ProviderRejectionReason(err)
			if !ok || reason != test.want {
				t.Fatalf("rejection = (%q, %v), want %q; error = %v", reason, ok, test.want, err)
			}
			current.Lease.Release()
		})
	}
}

func TestReserveSameProviderRetryRejectsMismatchedLeaseAndExhaustedLedger(t *testing.T) {
	store := newLifecycleStore(retryTestProvider("provider-a"))
	selector, _ := newRetryTestSelector(store, nil)
	current := selectRetryCurrent(t, selector)
	input := retryPermitInput(t, current.Lease)
	input.RuleKey.ProviderID = "provider-b"
	if _, err := selector.ReserveSameProviderRetry(context.Background(), input); err == nil {
		t.Fatal("mismatched provider lease was accepted")
	}

	input = retryPermitInput(t, current.Lease)
	input.GlobalMaxAttempts = 1
	if _, err := selector.ReserveSameProviderRetry(context.Background(), input); !errors.Is(err, errorrule.ErrGlobalAttemptLimit) {
		t.Fatalf("exhausted ReserveSameProviderRetry() error = %v", err)
	}
	current.Lease.Release()
}
