package proxy

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/errorrule"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/selector"

	"go.uber.org/zap"
)

const (
	x3AdapterAPIType         = "claude"
	x3AdapterProviderID      = "adapter-provider"
	x3AdapterRuleID          = errorrule.RuleID("123e4567-e89b-42d3-a456-426614174000")
	x3AdapterMaxAttempts     = uint(3)
	x3AdapterConcurrency     = 4
	x3AdapterEvictedProvider = "evicted-provider"
)

type x3AdapterStore struct {
	providers []model.Provider
}

func (s *x3AdapterStore) ListProvidersByAPIType(_ context.Context, apiType string) ([]model.Provider, error) {
	providers := make([]model.Provider, 0, len(s.providers))
	for i := range s.providers {
		if _, ok := s.providers[i].APITypeConfig(apiType); !ok {
			continue
		}
		provider := s.providers[i]
		provider.APITypes = append([]model.ProviderAPIType(nil), provider.APITypes...)
		providers = append(providers, provider)
	}
	return providers, nil
}

func (s *x3AdapterStore) GetProvider(_ context.Context, providerID string) (*model.Provider, error) {
	for i := range s.providers {
		if s.providers[i].ID != providerID {
			continue
		}
		provider := s.providers[i]
		provider.APITypes = append([]model.ProviderAPIType(nil), provider.APITypes...)
		return &provider, nil
	}
	return nil, nil
}

func (*x3AdapterStore) GetGroup(context.Context, string) (*model.Group, error) {
	return nil, errors.New("x3 adapter test store has no groups")
}

func (*x3AdapterStore) GetConfig(context.Context, string) (string, error) {
	return selector.StrategyPriority, nil
}

func (*x3AdapterStore) GetProviderAuthState(_ context.Context, providerID string) (*model.ProviderAuthState, error) {
	return &model.ProviderAuthState{
		ProviderID: providerID,
		Status:     model.ProviderAuthStatusActive,
	}, nil
}

func (*x3AdapterStore) ListRoutingPoliciesByAPIType(context.Context, string) ([]model.RoutingPolicy, error) {
	return nil, nil
}

func newX3ConcreteSelectorAdapter() (*selector.Selector, httpProviderSelector, *selector.ConcurrencyLimiter) {
	limiter := selector.NewConcurrencyLimiter()
	store := &x3AdapterStore{providers: []model.Provider{{
		ID:          x3AdapterProviderID,
		Name:        "Adapter Provider",
		APIKey:      "adapter-api-key",
		Enabled:     true,
		Concurrency: x3AdapterConcurrency,
		APITypes: []model.ProviderAPIType{{
			ProviderID: x3AdapterProviderID,
			APIType:    x3AdapterAPIType,
		}},
	}}}
	concrete := selector.NewSelector(selector.Config{
		Store:   store,
		Limiter: limiter,
		Logger:  zap.NewNop(),
	})
	return concrete, newHTTPProviderSelector(concrete), limiter
}

type x3AdapterCapability struct {
	activeSelection *providerSelection
	activeErr       error
	dispatchPermit  sameProviderDispatchPermit
	dispatchErr     error
	reservation     alternateProviderReservation
	reservationErr  error
	activeCalls     int
	dispatchCalls   int
	evictions       []string
}

func (c *x3AdapterCapability) SelectInitial(context.Context, *model.SelectRequest) (*providerSelection, error) {
	return nil, internal.ErrNoProvider
}

func (c *x3AdapterCapability) SelectActive(
	context.Context,
	*model.SelectRequest,
	providerLease,
) (*providerSelection, error) {
	c.activeCalls++
	return c.activeSelection, c.activeErr
}

func (c *x3AdapterCapability) ReserveSameProviderDispatch(
	context.Context,
	providerLease,
	*model.SelectRequest,
) (sameProviderDispatchPermit, error) {
	c.dispatchCalls++
	return c.dispatchPermit, c.dispatchErr
}

func (c *x3AdapterCapability) ReserveSameProviderRetry(
	context.Context,
	sameProviderRetryReservation,
) (retryPermit, error) {
	return nil, internal.ErrNoProvider
}

func (c *x3AdapterCapability) ReserveAlternate(
	context.Context,
	*model.SelectRequest,
	map[string]bool,
) (alternateProviderReservation, error) {
	return c.reservation, c.reservationErr
}

func (*x3AdapterCapability) UpdateStickyWithTTL(*model.SelectRequest, string, time.Duration) {}

func (c *x3AdapterCapability) EvictProviderContinuity(providerID string) {
	c.evictions = append(c.evictions, providerID)
}

type x3AdapterRoutingOnly struct {
	evictions []string
}

func (*x3AdapterRoutingOnly) UpdateStickyWithTTL(*model.SelectRequest, string, time.Duration) {}

func (r *x3AdapterRoutingOnly) EvictProviderContinuity(providerID string) {
	r.evictions = append(r.evictions, providerID)
}

type x3AdapterReservation struct {
	provider      *model.Provider
	metadata      selector.SelectionMetadata
	lease         providerLease
	prepareErr    error
	prepareCalls  int
	activateCalls int
	releaseCalls  int
	released      bool
}

func (r *x3AdapterReservation) Provider() *model.Provider { return r.provider }

func (r *x3AdapterReservation) Metadata() selector.SelectionMetadata { return r.metadata }

func (r *x3AdapterReservation) PrepareActivation(context.Context) error {
	r.prepareCalls++
	return r.prepareErr
}

func (r *x3AdapterReservation) Activate() providerLease {
	r.activateCalls++
	return r.lease
}

func (r *x3AdapterReservation) Release() bool {
	r.releaseCalls++
	if r.released {
		return false
	}
	r.released = true
	if r.lease != nil {
		r.lease.Release()
	}
	return true
}

func TestNewHTTPProviderSelectorRequiresLeaseAwareCapabilities(t *testing.T) {
	if got := newHTTPProviderSelector(nil); got != nil {
		t.Fatalf("nil source adapted to %#v", got)
	}

	capability := &x3AdapterCapability{}
	if got := newHTTPProviderSelector(capability); got != capability {
		t.Fatalf("lease-aware source was wrapped: got %#v, want original capability", got)
	}

	concrete, _, _ := newX3ConcreteSelectorAdapter()
	if _, ok := newHTTPProviderSelector(concrete).(selectorHTTPAdapter); !ok {
		t.Fatal("concrete selector was not adapted at the HTTP ownership boundary")
	}

	defer func() {
		if recovered := recover(); recovered == nil {
			t.Fatal("routing-only selector did not fail closed")
		}
	}()
	newHTTPProviderSelector(&x3AdapterRoutingOnly{})
}

func TestSelectorHTTPAdapterTransfersConcreteInitialAndActiveLeases(t *testing.T) {
	_, adapter, limiter := newX3ConcreteSelectorAdapter()
	request := &model.SelectRequest{APIType: x3AdapterAPIType}

	initial, err := adapter.SelectInitial(context.Background(), request)
	if err != nil {
		t.Fatalf("SelectInitial() error = %v", err)
	}
	if initial.provider == nil || initial.provider.ID != x3AdapterProviderID || initial.lease == nil || !initial.lease.Held() {
		t.Fatalf("initial selection = %#v", initial)
	}
	if initial.metadata.Source != selector.SelectionSourceStrategy {
		t.Fatalf("initial source = %q", initial.metadata.Source)
	}

	active, err := adapter.SelectActive(context.Background(), request, initial.lease)
	if err != nil {
		t.Fatalf("SelectActive() error = %v", err)
	}
	if active.provider == nil || active.provider.ID != x3AdapterProviderID || active.lease == nil || !active.lease.Held() {
		t.Fatalf("active selection = %#v", active)
	}
	if active.metadata.Source != selector.SelectionSourceActiveContinuity {
		t.Fatalf("active source = %q", active.metadata.Source)
	}
	if active.lease.CapabilityIdentity() == initial.lease.CapabilityIdentity() {
		t.Fatal("active continuity reused the existing request's cleanup capability")
	}

	if !active.lease.Release() || !initial.lease.Release() {
		t.Fatal("transferred selector leases were not independently releasable")
	}
	if current := limiter.Current(x3AdapterProviderID); current != 0 {
		t.Fatalf("selector capacity after cleanup = %d", current)
	}
}

func TestSelectorHTTPAdapterRejectsForeignLeaseWithoutTakingOwnership(t *testing.T) {
	_, adapter, _ := newX3ConcreteSelectorAdapter()
	foreign := newLocalProviderLease(&model.Provider{ID: x3AdapterProviderID})

	selection, err := adapter.SelectActive(
		context.Background(),
		&model.SelectRequest{APIType: x3AdapterAPIType},
		foreign,
	)
	if selection != nil || !errors.Is(err, internal.ErrNoProvider) {
		t.Fatalf("SelectActive() = (%#v, %v), want ErrNoProvider", selection, err)
	}
	if !foreign.Held() {
		t.Fatal("adapter released a foreign capability it never accepted")
	}
	foreign.Release()
}

func TestSelectorHTTPAdapterForwardsConcreteSameProviderDispatchPermit(t *testing.T) {
	_, adapter, limiter := newX3ConcreteSelectorAdapter()
	request := &model.SelectRequest{APIType: x3AdapterAPIType}
	current, err := adapter.SelectInitial(context.Background(), request)
	if err != nil {
		t.Fatalf("SelectInitial() error = %v", err)
	}

	foreign := newLocalProviderLease(current.provider)
	if permit, reserveErr := adapter.ReserveSameProviderDispatch(context.Background(), foreign, request); permit != nil || reserveErr == nil {
		t.Fatalf("foreign dispatch reservation = (%#v, %v)", permit, reserveErr)
	}
	if !foreign.Held() {
		t.Fatal("failed dispatch reservation consumed its foreign lease")
	}
	foreign.Release()

	permit, err := adapter.ReserveSameProviderDispatch(context.Background(), current.lease, request)
	if err != nil {
		t.Fatalf("ReserveSameProviderDispatch() error = %v", err)
	}
	if permit.Provider() == nil || permit.Provider().ID != x3AdapterProviderID {
		t.Fatalf("dispatch permit provider = %#v", permit.Provider())
	}
	provider, err := permit.Activate()
	if err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	if provider == nil || provider.ID != x3AdapterProviderID || !current.lease.Held() {
		t.Fatalf("activated provider = %#v, lease held = %v", provider, current.lease.Held())
	}
	if !current.lease.Release() {
		t.Fatal("current lease was not releasable after dispatch activation")
	}
	if current := limiter.Current(x3AdapterProviderID); current != 0 {
		t.Fatalf("selector capacity after dispatch cleanup = %d", current)
	}
}

func TestSelectorHTTPAdapterForwardsConcreteRetryReservation(t *testing.T) {
	_, adapter, limiter := newX3ConcreteSelectorAdapter()
	request := &model.SelectRequest{APIType: x3AdapterAPIType}
	current, err := adapter.SelectInitial(context.Background(), request)
	if err != nil {
		t.Fatalf("SelectInitial() error = %v", err)
	}

	foreign := newLocalProviderLease(current.provider)
	if permit, reserveErr := adapter.ReserveSameProviderRetry(context.Background(), sameProviderRetryReservation{
		current: foreign,
	}); permit != nil || reserveErr == nil {
		t.Fatalf("foreign retry reservation = (%#v, %v)", permit, reserveErr)
	}
	if !foreign.Held() {
		t.Fatal("failed retry reservation consumed its foreign lease")
	}
	foreign.Release()

	ledger, err := (errorrule.RetryLedger{}).StartAttempt(
		errorrule.ProviderID(x3AdapterProviderID),
		x3AdapterMaxAttempts,
	)
	if err != nil {
		t.Fatalf("StartAttempt() error = %v", err)
	}
	ruleKey := errorrule.ProviderRuleKey{
		ProviderID: errorrule.ProviderID(x3AdapterProviderID),
		RuleID:     x3AdapterRuleID,
	}
	permit, err := adapter.ReserveSameProviderRetry(context.Background(), sameProviderRetryReservation{
		current:           current.lease,
		request:           request,
		ruleKey:           ruleKey,
		ledger:            ledger,
		globalMaxAttempts: x3AdapterMaxAttempts,
	})
	if err != nil {
		t.Fatalf("ReserveSameProviderRetry() error = %v", err)
	}
	if permit.Provider() == nil || permit.Provider().ID != x3AdapterProviderID {
		t.Fatalf("permit provider = %#v", permit.Provider())
	}
	activated, err := permit.Activate()
	if err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	if got := activated.LogicalAttemptsStarted(); got != 2 {
		t.Fatalf("logical attempts after activation = %d", got)
	}
	if got := activated.RuleRetriesScheduled(ruleKey); got != 1 {
		t.Fatalf("rule retries after activation = %d", got)
	}
	if !current.lease.Release() {
		t.Fatal("current lease was not releasable after retry activation")
	}
	if current := limiter.Current(x3AdapterProviderID); current != 0 {
		t.Fatalf("selector capacity after retry cleanup = %d", current)
	}
}

func TestSelectorHTTPAdapterWrapsConcreteAlternateReservation(t *testing.T) {
	_, adapter, limiter := newX3ConcreteSelectorAdapter()
	request := &model.SelectRequest{APIType: x3AdapterAPIType}

	released, err := adapter.ReserveAlternate(context.Background(), request, nil)
	if err != nil {
		t.Fatalf("ReserveAlternate() error = %v", err)
	}
	if released.Provider() == nil || released.Provider().ID != x3AdapterProviderID {
		t.Fatalf("reservation provider = %#v", released.Provider())
	}
	if released.Metadata().Source != selector.SelectionSourceAlternate {
		t.Fatalf("reservation source = %q", released.Metadata().Source)
	}
	if !released.Release() || released.Release() {
		t.Fatal("unactivated reservation did not transfer rollback ownership exactly once")
	}

	cancelled, err := adapter.ReserveAlternate(context.Background(), request, nil)
	if err != nil {
		t.Fatalf("ReserveAlternate() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := cancelled.PrepareActivation(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("PrepareActivation() error = %v", err)
	}
	if cancelled.Release() {
		t.Fatal("failed preparation left rollback ownership outstanding")
	}

	activatedReservation, err := adapter.ReserveAlternate(context.Background(), request, nil)
	if err != nil {
		t.Fatalf("ReserveAlternate() error = %v", err)
	}
	if err := activatedReservation.PrepareActivation(context.Background()); err != nil {
		t.Fatalf("PrepareActivation() error = %v", err)
	}
	activated := activatedReservation.Activate()
	if activated == nil || !activated.Held() || activated.ProviderID() != x3AdapterProviderID {
		t.Fatalf("activated lease = %#v", activated)
	}
	if !activated.Release() {
		t.Fatal("activated alternate lease did not transfer to caller cleanup")
	}

	failed, err := adapter.ReserveAlternate(context.Background(), request, map[string]bool{
		x3AdapterProviderID: true,
	})
	if failed != nil || !errors.Is(err, internal.ErrNoProvider) {
		t.Fatalf("excluded reservation = (%#v, %v), want ErrNoProvider", failed, err)
	}
	if current := limiter.Current(x3AdapterProviderID); current != 0 {
		t.Fatalf("selector capacity after alternate paths = %d", current)
	}
}

func TestAdaptSelectionReleasesConcreteLeaseOnBoundaryFailure(t *testing.T) {
	concrete, _, limiter := newX3ConcreteSelectorAdapter()
	raw, err := concrete.SelectWithMetadata(context.Background(), &model.SelectRequest{APIType: x3AdapterAPIType})
	if err != nil {
		t.Fatalf("SelectWithMetadata() error = %v", err)
	}
	boundaryErr := errors.New("adapter boundary failure")
	selection, err := adaptSelection(raw, boundaryErr)
	if selection != nil || !errors.Is(err, boundaryErr) {
		t.Fatalf("adaptSelection() = (%#v, %v)", selection, err)
	}
	if raw.Lease.Held() {
		t.Fatal("failed HTTP adaptation retained selector capacity")
	}
	if current := limiter.Current(x3AdapterProviderID); current != 0 {
		t.Fatalf("selector capacity after failed adaptation = %d", current)
	}

	selection, err = adaptSelection(nil, nil)
	if selection != nil || !errors.Is(err, internal.ErrNoProvider) {
		t.Fatalf("nil adaptation = (%#v, %v), want ErrNoProvider", selection, err)
	}
	selection, err = adaptSelection(&selector.SelectResult{Lease: &selector.ProviderLease{}}, nil)
	if selection != nil || !errors.Is(err, internal.ErrNoProvider) {
		t.Fatalf("invalid capability adaptation = (%#v, %v), want ErrNoProvider", selection, err)
	}
}

func TestLocalSelectionOwnershipHelpersFailClosed(t *testing.T) {
	var nilLease *localProviderLease
	if nilLease.ProviderID() != "" || nilLease.CapabilityIdentity() != 0 || nilLease.Held() || nilLease.Release() {
		t.Fatal("nil local lease reported ownership")
	}

	provider := &model.Provider{ID: x3AdapterProviderID}
	cancelledLease := newLocalProviderLease(provider)
	cancelled := &localAlternateReservation{provider: provider, lease: cancelledLease}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := cancelled.PrepareActivation(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled preparation error = %v", err)
	}
	if !cancelled.Release() || cancelledLease.Held() {
		t.Fatal("cancelled local reservation did not roll back its lease")
	}

	releasedLease := newLocalProviderLease(provider)
	released := &localAlternateReservation{provider: provider, lease: releasedLease}
	if !released.Release() || released.Release() {
		t.Fatal("local reservation rollback was not exactly once")
	}
	if err := released.PrepareActivation(context.Background()); !errors.Is(err, internal.ErrNoProvider) {
		t.Fatalf("released preparation error = %v", err)
	}
	if lease := released.Activate(); lease != nil {
		t.Fatalf("released reservation activated lease %#v", lease)
	}
}

func TestLocalSameProviderDispatchPermitKeepsLeaseOwnershipWithCaller(t *testing.T) {
	var nilPermit *localSameProviderDispatchPermit
	if nilPermit.Provider() != nil || nilPermit.Release() {
		t.Fatal("nil dispatch permit reported ownership")
	}
	if provider, err := nilPermit.Activate(); provider != nil || !errors.Is(err, selector.ErrDispatchPermitReleased) {
		t.Fatalf("nil permit activation = (%#v, %v)", provider, err)
	}

	provider := &model.Provider{ID: x3AdapterProviderID}
	current := newLocalProviderLease(provider)
	activatedPermit := newLocalSameProviderDispatchPermit(provider, current)
	if activatedPermit.Provider() != provider {
		t.Fatalf("permit provider = %#v", activatedPermit.Provider())
	}
	activatedProvider, err := activatedPermit.Activate()
	if err != nil || activatedProvider != provider {
		t.Fatalf("permit activation = (%#v, %v)", activatedProvider, err)
	}
	if activatedProvider, err = activatedPermit.Activate(); activatedProvider != nil || !errors.Is(err, selector.ErrDispatchPermitActivated) {
		t.Fatalf("second permit activation = (%#v, %v)", activatedProvider, err)
	}
	if activatedPermit.Release() || !current.Held() {
		t.Fatal("activated permit took cleanup ownership from the current request")
	}
	current.Release()

	releasedCurrent := newLocalProviderLease(provider)
	releasedPermit := newLocalSameProviderDispatchPermit(provider, releasedCurrent)
	if !releasedPermit.Release() || releasedPermit.Release() {
		t.Fatal("dispatch permit release was not exactly once")
	}
	if activatedProvider, err = releasedPermit.Activate(); activatedProvider != nil || !errors.Is(err, selector.ErrDispatchPermitReleased) {
		t.Fatalf("released permit activation = (%#v, %v)", activatedProvider, err)
	}
	if !releasedCurrent.Held() {
		t.Fatal("permit release consumed the current request's lease")
	}
	releasedCurrent.Release()

	invalidatedCurrent := newLocalProviderLease(provider)
	invalidatedPermit := newLocalSameProviderDispatchPermit(provider, invalidatedCurrent)
	invalidatedCurrent.Release()
	if activatedProvider, err = invalidatedPermit.Activate(); activatedProvider != nil || !errors.Is(err, selector.ErrDispatchPermitReleased) {
		t.Fatalf("invalidated permit activation = (%#v, %v)", activatedProvider, err)
	}
}

func TestWebSocketSelectorAdapterTransfersActiveOwnershipAndEvictsContinuity(t *testing.T) {
	provider := &model.Provider{ID: x3AdapterProviderID}
	active := newLocalProviderLease(provider)
	routing := &x3AdapterRoutingOnly{}
	capability := &x3AdapterCapability{}
	adapter := newWebSocketSelectorAdapter(routing, capability)

	selection, err := adapter.SelectActive(
		context.Background(),
		&model.SelectRequest{APIType: x3AdapterAPIType},
		nil,
	)
	if selection.Lease != nil || !errors.Is(err, internal.ErrNoProvider) || capability.activeCalls != 0 {
		t.Fatalf("nil active selection = (%#v, %v), calls=%d", selection, err, capability.activeCalls)
	}

	selectedLease := newLocalProviderLease(provider)
	capability.activeSelection = &providerSelection{
		provider: provider,
		lease:    selectedLease,
		metadata: selector.BuildSelectionMetadata(
			&model.SelectRequest{APIType: x3AdapterAPIType},
			selector.SelectionSourceActiveContinuity,
		),
	}
	selection, err = adapter.SelectActive(
		context.Background(),
		&model.SelectRequest{APIType: x3AdapterAPIType},
		active,
	)
	if err != nil || selection.Lease != selectedLease || capability.activeCalls != 1 {
		t.Fatalf("active selection = (%#v, %v), calls=%d", selection, err, capability.activeCalls)
	}
	if selection.Metadata.Source != selector.SelectionSourceActiveContinuity {
		t.Fatalf("active metadata source = %q", selection.Metadata.Source)
	}
	selectedLease.Release()

	boundaryErr := errors.New("websocket boundary failure")
	failedLease := newLocalProviderLease(provider)
	capability.activeSelection = &providerSelection{provider: provider, lease: failedLease}
	capability.activeErr = boundaryErr
	selection, err = adapter.SelectActive(
		context.Background(),
		&model.SelectRequest{APIType: x3AdapterAPIType},
		active,
	)
	if selection.Lease != failedLease || !errors.Is(err, boundaryErr) {
		t.Fatalf("result-plus-error selection = (%#v, %v)", selection, err)
	}
	if !failedLease.Held() {
		t.Fatal("adapter cleaned a lease after ownership crossed to the websocket gateway")
	}
	failedLease.Release()

	adapter.EvictProviderContinuity(x3AdapterEvictedProvider)
	if len(routing.evictions) != 1 || routing.evictions[0] != x3AdapterEvictedProvider {
		t.Fatalf("continuity evictions = %v", routing.evictions)
	}
	active.Release()
}

func TestWebSocketSelectionTranslatesEmptyFailuresWithoutInventingOwnership(t *testing.T) {
	boundaryErr := errors.New("empty websocket selection failure")
	selection, err := websocketSelection(nil, boundaryErr)
	if selection.Lease != nil || !errors.Is(err, boundaryErr) {
		t.Fatalf("error-only selection = (%#v, %v)", selection, err)
	}

	selection, err = websocketSelection(nil, nil)
	if selection.Lease != nil || !errors.Is(err, internal.ErrNoProvider) {
		t.Fatalf("empty selection = (%#v, %v), want ErrNoProvider", selection, err)
	}
}

func TestWebSocketSelectorAdapterAlternateFailureOwnership(t *testing.T) {
	provider := &model.Provider{ID: x3AdapterProviderID}
	request := &model.SelectRequest{APIType: x3AdapterAPIType}
	reservationErr := errors.New("alternate reservation failure")
	prepareErr := errors.New("alternate preparation failure")

	t.Run("reservation error creates no ownership", func(t *testing.T) {
		capability := &x3AdapterCapability{reservationErr: reservationErr}
		adapter := newWebSocketSelectorAdapter(&x3AdapterRoutingOnly{}, capability)
		selection, err := adapter.SelectAlternate(context.Background(), request, nil)
		if selection.Lease != nil || !errors.Is(err, reservationErr) {
			t.Fatalf("SelectAlternate() = (%#v, %v)", selection, err)
		}
	})

	t.Run("preparation failure rolls reservation back", func(t *testing.T) {
		lease := newLocalProviderLease(provider)
		reservation := &x3AdapterReservation{provider: provider, lease: lease, prepareErr: prepareErr}
		capability := &x3AdapterCapability{reservation: reservation}
		adapter := newWebSocketSelectorAdapter(&x3AdapterRoutingOnly{}, capability)
		selection, err := adapter.SelectAlternate(context.Background(), request, nil)
		if selection.Lease != nil || !errors.Is(err, prepareErr) {
			t.Fatalf("SelectAlternate() = (%#v, %v)", selection, err)
		}
		if reservation.prepareCalls != 1 || reservation.activateCalls != 0 || reservation.releaseCalls != 1 || lease.Held() {
			t.Fatalf("reservation calls prepare=%d activate=%d release=%d held=%v",
				reservation.prepareCalls, reservation.activateCalls, reservation.releaseCalls, lease.Held())
		}
	})

	t.Run("missing activation lease rolls reservation back", func(t *testing.T) {
		reservation := &x3AdapterReservation{provider: provider}
		capability := &x3AdapterCapability{reservation: reservation}
		adapter := newWebSocketSelectorAdapter(&x3AdapterRoutingOnly{}, capability)
		selection, err := adapter.SelectAlternate(context.Background(), request, nil)
		if selection.Lease != nil || !errors.Is(err, internal.ErrNoProvider) {
			t.Fatalf("SelectAlternate() = (%#v, %v)", selection, err)
		}
		if reservation.prepareCalls != 1 || reservation.activateCalls != 1 || reservation.releaseCalls != 1 {
			t.Fatalf("reservation calls prepare=%d activate=%d release=%d",
				reservation.prepareCalls, reservation.activateCalls, reservation.releaseCalls)
		}
	})

	t.Run("released activation lease rolls reservation back", func(t *testing.T) {
		lease := newLocalProviderLease(provider)
		lease.Release()
		reservation := &x3AdapterReservation{provider: provider, lease: lease}
		capability := &x3AdapterCapability{reservation: reservation}
		adapter := newWebSocketSelectorAdapter(&x3AdapterRoutingOnly{}, capability)
		selection, err := adapter.SelectAlternate(context.Background(), request, nil)
		if selection.Lease != nil || !errors.Is(err, internal.ErrNoProvider) {
			t.Fatalf("SelectAlternate() = (%#v, %v)", selection, err)
		}
		if reservation.releaseCalls != 1 {
			t.Fatalf("reservation release calls = %d", reservation.releaseCalls)
		}
	})

	t.Run("successful activation transfers lease to gateway", func(t *testing.T) {
		lease := newLocalProviderLease(provider)
		metadata := selector.BuildSelectionMetadata(request, selector.SelectionSourceAlternate)
		reservation := &x3AdapterReservation{provider: provider, lease: lease, metadata: metadata}
		capability := &x3AdapterCapability{reservation: reservation}
		adapter := newWebSocketSelectorAdapter(&x3AdapterRoutingOnly{}, capability)
		selection, err := adapter.SelectAlternate(context.Background(), request, nil)
		if err != nil || selection.Lease != lease || selection.Metadata.Source != selector.SelectionSourceAlternate {
			t.Fatalf("SelectAlternate() = (%#v, %v)", selection, err)
		}
		if reservation.releaseCalls != 0 {
			t.Fatalf("activated reservation release calls = %d", reservation.releaseCalls)
		}
		if !selection.Lease.Release() {
			t.Fatal("gateway-owned alternate lease was not releasable")
		}
	})
}
