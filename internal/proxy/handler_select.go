package proxy

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/errorrule"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/selector"

	"go.uber.org/zap"
)

// providerLease is defined at its consumer so execution tests can assert exact
// capability transfer without exporting selector constructors.
type providerLease interface {
	Provider() *model.Provider
	ProviderID() string
	Generation() uint64
	CapabilityIdentity() uintptr
	Held() bool
	Release() bool
}

type providerSelection struct {
	provider *model.Provider
	lease    providerLease
	metadata selector.SelectionMetadata
}

type retryPermit interface {
	Provider() *model.Provider
	Activate() (errorrule.RetryLedger, error)
	Release() bool
}

type sameProviderDispatchPermit interface {
	Provider() *model.Provider
	Activate() (*model.Provider, error)
	Release() bool
}

type alternateProviderReservation interface {
	Provider() *model.Provider
	Metadata() selector.SelectionMetadata
	PrepareActivation(context.Context) error
	Activate() providerLease
	Release() bool
}

type sameProviderRetryReservation struct {
	current           providerLease
	request           *model.SelectRequest
	ruleKey           errorrule.ProviderRuleKey
	ledger            errorrule.RetryLedger
	globalMaxAttempts uint
}

type httpProviderSelector interface {
	SelectInitial(context.Context, *model.SelectRequest) (*providerSelection, error)
	SelectActive(context.Context, *model.SelectRequest, providerLease) (*providerSelection, error)
	ReserveSameProviderDispatch(context.Context, providerLease, *model.SelectRequest) (sameProviderDispatchPermit, error)
	ReserveSameProviderRetry(context.Context, sameProviderRetryReservation) (retryPermit, error)
	ReserveAlternate(context.Context, *model.SelectRequest, map[string]bool) (alternateProviderReservation, error)
}

type selectorHTTPAdapter struct {
	selector *selector.Selector
}

func newHTTPProviderSelector(source Selector) httpProviderSelector {
	if source == nil {
		return nil
	}
	if adapted, ok := source.(httpProviderSelector); ok {
		return adapted
	}
	if concrete, ok := source.(*selector.Selector); ok {
		return selectorHTTPAdapter{selector: concrete}
	}
	panic("proxy: Selector must provide lease-aware HTTP selection")
}

func (a selectorHTTPAdapter) SelectInitial(ctx context.Context, request *model.SelectRequest) (*providerSelection, error) {
	result, err := a.selector.SelectWithMetadata(ctx, request)
	return adaptSelection(result, err)
}

func (a selectorHTTPAdapter) SelectActive(
	ctx context.Context,
	request *model.SelectRequest,
	active providerLease,
) (*providerSelection, error) {
	lease, ok := active.(*selector.ProviderLease)
	if !ok {
		return nil, internal.ErrNoProvider
	}
	result, err := a.selector.SelectActive(ctx, request, lease)
	return adaptSelection(result, err)
}

func (a selectorHTTPAdapter) ReserveSameProviderRetry(
	ctx context.Context,
	input sameProviderRetryReservation,
) (retryPermit, error) {
	lease, ok := input.current.(*selector.ProviderLease)
	if !ok {
		return nil, fmt.Errorf("selector retry requires a selector-owned provider lease")
	}
	return a.selector.ReserveSameProviderRetry(ctx, selector.SameProviderRetryRequest{
		Current:           lease,
		Request:           input.request,
		RuleKey:           input.ruleKey,
		Ledger:            input.ledger,
		GlobalMaxAttempts: input.globalMaxAttempts,
	})
}

func (a selectorHTTPAdapter) ReserveSameProviderDispatch(
	ctx context.Context,
	current providerLease,
	request *model.SelectRequest,
) (sameProviderDispatchPermit, error) {
	lease, ok := current.(*selector.ProviderLease)
	if !ok {
		return nil, fmt.Errorf("selector dispatch requires a selector-owned provider lease")
	}
	return a.selector.ReserveSameProviderDispatch(ctx, selector.SameProviderDispatchRequest{
		Current: lease,
		Request: request,
	})
}

func (a selectorHTTPAdapter) ReserveAlternate(
	ctx context.Context,
	request *model.SelectRequest,
	excluded map[string]bool,
) (alternateProviderReservation, error) {
	reservation, err := a.selector.ReserveAlternate(ctx, selector.AlternateReservationRequest{
		Request:            request,
		ExcludeProviderIDs: excluded,
	})
	if err != nil {
		return nil, err
	}
	return selectorAlternateReservation{reservation: reservation}, nil
}

type selectorAlternateReservation struct {
	reservation *selector.ProviderReservation
}

func (r selectorAlternateReservation) Provider() *model.Provider {
	return r.reservation.Provider()
}

func (r selectorAlternateReservation) Metadata() selector.SelectionMetadata {
	return r.reservation.Metadata()
}

func (r selectorAlternateReservation) PrepareActivation(ctx context.Context) error {
	return r.reservation.PrepareActivation(ctx)
}

func (r selectorAlternateReservation) Activate() providerLease {
	return r.reservation.Activate()
}

func (r selectorAlternateReservation) Release() bool {
	return r.reservation.Release()
}

func adaptSelection(result *selector.SelectResult, err error) (*providerSelection, error) {
	if err != nil {
		if result != nil && result.Lease != nil {
			result.Lease.Release()
		}
		return nil, err
	}
	if result == nil || result.Lease == nil {
		return nil, internal.ErrNoProvider
	}
	provider := result.Provider()
	if provider == nil || provider.ID == "" || result.Lease.ProviderID() != provider.ID ||
		result.Lease.CapabilityIdentity() == 0 || !result.Lease.Held() {
		result.Lease.Release()
		return nil, internal.ErrNoProvider
	}
	return &providerSelection{provider: provider, lease: result.Lease, metadata: result.Metadata}, nil
}

// localProviderLease keeps selectorless deployments on the same explicit
// cleanup contract without pretending that they participate in concurrency.
type localProviderLease struct {
	provider *model.Provider
	released atomic.Bool
}

func newLocalProviderLease(provider *model.Provider) *localProviderLease {
	return &localProviderLease{provider: provider}
}

func (l *localProviderLease) Provider() *model.Provider { return l.provider }
func (l *localProviderLease) ProviderID() string {
	if l == nil || l.provider == nil {
		return ""
	}
	return l.provider.ID
}
func (l *localProviderLease) Generation() uint64 { return 1 }
func (l *localProviderLease) CapabilityIdentity() uintptr {
	if l == nil {
		return 0
	}
	return reflect.ValueOf(l).Pointer()
}
func (l *localProviderLease) Held() bool {
	return l != nil && l.provider != nil && !l.released.Load()
}
func (l *localProviderLease) Release() bool {
	return l != nil && l.released.CompareAndSwap(false, true)
}

func (h *Handler) selectInitialProvider(
	ctx context.Context,
	request *model.SelectRequest,
	attempt int,
	excluded map[string]bool,
) (*providerSelection, error) {
	if h.httpSelector == nil {
		provider, err := normalizeSelectedProvider(h.selectProviderFallback(ctx, request, attempt, excluded))
		if err != nil {
			return nil, err
		}
		return &providerSelection{
			provider: provider,
			lease:    newLocalProviderLease(provider),
			metadata: selector.BuildSelectionMetadataAt(request, selector.SelectionSourceStrategy, time.Now()),
		}, nil
	}

	result, err := h.httpSelector.SelectInitial(ctx, request)
	if err != nil {
		return nil, err
	}
	if result == nil || result.provider == nil || result.lease == nil || !result.lease.Held() {
		return nil, internal.ErrNoProvider
	}
	if result.metadata.UsesContinuity() || h.activeRegistry == nil || request.StickyMode == model.StickyModeOff {
		return result, nil
	}

	active, found := h.activeRegistry.FindActiveLeaseForRequest(request)
	if !found {
		return result, nil
	}
	activeResult, activeErr := h.httpSelector.SelectActive(ctx, request, active)
	if activeErr != nil {
		if !errors.Is(activeErr, internal.ErrNoProvider) {
			h.logger.Warn("failed to select active continuity provider", zap.Error(activeErr))
		}
		return result, nil
	}
	result.lease.Release()
	return activeResult, nil
}

func normalizeSelectedProvider(provider *model.Provider, err error) (*model.Provider, error) {
	if err != nil {
		return nil, err
	}
	if provider == nil {
		return nil, internal.ErrNoProvider
	}
	return provider, nil
}

func (h *Handler) eligibleFallbackProviderByID(
	ctx context.Context,
	selectReq *model.SelectRequest,
	providerID string,
) (*model.Provider, error) {
	if selectReq == nil || providerID == "" {
		return nil, nil
	}
	scope, err := h.selectionScope(ctx, selectRequestForSameProviderRetry(selectReq))
	if err != nil {
		return nil, err
	}
	providers, err := h.store.ListProvidersByAPIType(ctx, scope.Request().APIType)
	if err != nil {
		return nil, err
	}
	for index := range providers {
		provider := &providers[index]
		allowed, eligibilityErr := scope.AllowsProvider(ctx, provider)
		if eligibilityErr != nil {
			return nil, eligibilityErr
		}
		if provider.ID == providerID && allowed {
			return provider, nil
		}
	}
	return nil, nil
}

const (
	localDispatchPermitReserved uint32 = iota + 1
	localDispatchPermitActivated
	localDispatchPermitReleased
)

type localSameProviderDispatchPermit struct {
	provider *model.Provider
	current  providerLease
	state    atomic.Uint32
}

func newLocalSameProviderDispatchPermit(
	provider *model.Provider,
	current providerLease,
) *localSameProviderDispatchPermit {
	permit := &localSameProviderDispatchPermit{provider: provider, current: current}
	permit.state.Store(localDispatchPermitReserved)
	return permit
}

func (p *localSameProviderDispatchPermit) Provider() *model.Provider {
	if p == nil {
		return nil
	}
	return p.provider
}

func (p *localSameProviderDispatchPermit) Activate() (*model.Provider, error) {
	if p == nil || p.current == nil || !p.current.Held() {
		return nil, selector.ErrDispatchPermitReleased
	}
	if p.state.CompareAndSwap(localDispatchPermitReserved, localDispatchPermitActivated) {
		return p.provider, nil
	}
	if p.state.Load() == localDispatchPermitActivated {
		return nil, selector.ErrDispatchPermitActivated
	}
	return nil, selector.ErrDispatchPermitReleased
}

func (p *localSameProviderDispatchPermit) Release() bool {
	return p != nil && p.state.CompareAndSwap(localDispatchPermitReserved, localDispatchPermitReleased)
}

func (h *Handler) reserveSameProviderDispatch(
	ctx context.Context,
	request *model.SelectRequest,
	current providerLease,
) (sameProviderDispatchPermit, error) {
	if current == nil || current.ProviderID() == "" || !current.Held() {
		return nil, selector.ErrDispatchPermitReleased
	}
	if h.httpSelector != nil {
		return h.httpSelector.ReserveSameProviderDispatch(ctx, current, request)
	}
	provider, err := h.eligibleFallbackProviderByID(ctx, request, current.ProviderID())
	if err != nil {
		return nil, err
	}
	if provider == nil {
		return nil, internal.ErrNoProvider
	}
	return newLocalSameProviderDispatchPermit(provider, current), nil
}

func selectRequestForSameProviderRetry(selectReq *model.SelectRequest) *model.SelectRequest {
	if selectReq == nil {
		return nil
	}
	cloned := *selectReq
	cloned.SwitchMode = model.SwitchModeInitial
	cloned.ProviderSwitchHistory = nil
	cloned.ProviderContinuityContext = nil
	cloned.VisibleContinuitySeedCandidate = nil
	cloned.FailoverContext = nil
	cloned.MaxProviderSwitches = 0
	return &cloned
}

func (h *Handler) selectProviderFallback(
	ctx context.Context,
	request *model.SelectRequest,
	attempt int,
	excluded map[string]bool,
) (*model.Provider, error) {
	scope, err := h.selectionScope(ctx, request)
	if err != nil {
		return nil, err
	}
	providers, err := h.store.ListProvidersByAPIType(ctx, request.APIType)
	if err != nil {
		return nil, err
	}
	available := providers[:0]
	for index := range providers {
		allowed, eligibilityErr := scope.AllowsProvider(ctx, &providers[index])
		if eligibilityErr != nil {
			return nil, eligibilityErr
		}
		if !excluded[providers[index].ID] && allowed {
			available = append(available, providers[index])
		}
	}
	if len(available) == 0 {
		return nil, internal.ErrNoProvider
	}
	index := h.fallbackCounter.Add(1)
	provider := available[int(uint64(index-1+int64(attempt))%uint64(len(available)))]
	return &provider, nil
}

func (h *Handler) selectionScope(ctx context.Context, request *model.SelectRequest) (*selector.ProviderSelectionEligibility, error) {
	return selector.NewProviderSelectionEligibility(ctx, h.store, h.health, request)
}

// localAlternateReservation supplies transactional replacement semantics for the
// deliberately reduced selectorless mode.
type localAlternateReservation struct {
	mu       sync.Mutex
	provider *model.Provider
	lease    *localProviderLease
	metadata selector.SelectionMetadata
	prepared bool
	released bool
}

func (r *localAlternateReservation) Provider() *model.Provider            { return r.provider }
func (r *localAlternateReservation) Metadata() selector.SelectionMetadata { return r.metadata }
func (r *localAlternateReservation) PrepareActivation(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.released || r.lease == nil || !r.lease.Held() {
		return internal.ErrNoProvider
	}
	r.prepared = true
	return nil
}
func (r *localAlternateReservation) Activate() providerLease {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.prepared || r.released {
		return nil
	}
	return r.lease
}
func (r *localAlternateReservation) Release() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.released || r.lease == nil {
		return false
	}
	r.released = true
	return r.lease.Release()
}

func (h *Handler) reserveAlternateProvider(
	ctx context.Context,
	request *model.SelectRequest,
	excluded map[string]bool,
) (alternateProviderReservation, error) {
	if h.httpSelector != nil {
		return h.httpSelector.ReserveAlternate(ctx, request, excluded)
	}
	provider, err := h.selectProviderFallback(ctx, request, 0, excluded)
	if err != nil {
		return nil, err
	}
	return &localAlternateReservation{
		provider: provider,
		lease:    newLocalProviderLease(provider),
		metadata: selector.BuildSelectionMetadataAt(request, selector.SelectionSourceAlternate, time.Now()),
	}, nil
}
