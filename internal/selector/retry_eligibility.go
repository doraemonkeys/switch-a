package selector

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/doraemonkeys/switch-a/internal/errorrule"
	"github.com/doraemonkeys/switch-a/internal/model"
	storepkg "github.com/doraemonkeys/switch-a/internal/store"

	"go.uber.org/zap"
)

var (
	ErrDispatchPermitReleased    = errors.New("same-provider dispatch permit released")
	ErrDispatchPermitActivated   = errors.New("same-provider dispatch permit already activated")
	ErrDispatchPermitOutstanding = errors.New("same-provider dispatch permit already reserved")
)

// ProviderRejectionError preserves the stable decision reason while retaining a
// diagnostic cause for structured logs. Callers should branch on Reason, not text.
type ProviderRejectionError struct {
	Reason errorrule.DecisionReason
	Cause  error
}

func (e *ProviderRejectionError) Error() string {
	if e == nil {
		return "provider rejected"
	}
	if e.Cause != nil {
		return fmt.Sprintf("provider rejected: %s: %v", e.Reason, e.Cause)
	}
	return fmt.Sprintf("provider rejected: %s", e.Reason)
}

func (e *ProviderRejectionError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// ProviderRejectionReason extracts a stable live-eligibility reason.
func ProviderRejectionReason(err error) (errorrule.DecisionReason, bool) {
	var rejection *ProviderRejectionError
	if !errors.As(err, &rejection) {
		return "", false
	}
	return rejection.Reason, true
}

type liveProvider struct {
	provider *model.Provider
}

// revalidateProvider checks only live lifecycle/configuration facts. Automatic
// circuit health and concurrency are deliberately absent: the former is ignored
// by an approved semantic retry, and the latter is already owned by the held lease.
func (s *Selector) revalidateProvider(
	ctx context.Context,
	req *model.SelectRequest,
	lease *ProviderLease,
) (*liveProvider, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !s.hasCurrentProviderLease(lease) {
		return nil, rejectProvider(errorrule.ReasonProviderDeleted, nil)
	}

	provider, err := s.loadCurrentProvider(ctx, lease)
	if err != nil {
		return nil, err
	}

	if err := validateCurrentProvider(provider, lease.ProviderID(), reqAPIType(req)); err != nil {
		return nil, err
	}
	scope, err := newProviderSelectionEligibility(
		ctx,
		s.store,
		nil,
		s.resolver,
		req,
		[]model.Provider{*provider},
	)
	if err != nil {
		return nil, providerLookupFailure(err, "")
	}
	allowed, reason, err := scope.allowsExistingRoute(ctx, provider, false)
	if err != nil {
		return nil, providerLookupFailure(err, "")
	}
	if !allowed {
		return nil, rejectProvider(reason, nil)
	}
	liveCandidate, liveResolved := scope.CandidateSnapshot(provider.ID)
	leaseCandidate, leaseResolved := lease.CandidateSnapshot()
	if leaseResolved && (!liveResolved || !sameCandidateIdentity(leaseCandidate, liveCandidate)) {
		return nil, rejectProvider(errorrule.ReasonAuthUnavailable, nil)
	}
	provider = scope.Provider(provider.ID)

	// A lifecycle retirement that won while store reads were in flight must invalidate the
	// result before a permit or reservation can establish its dispatch boundary.
	if !s.hasCurrentProviderLease(lease) {
		return nil, rejectProvider(errorrule.ReasonProviderDeleted, nil)
	}
	return &liveProvider{provider: provider}, nil
}

func (s *Selector) hasCurrentProviderLease(lease *ProviderLease) bool {
	return s != nil && s.store != nil && s.limiter != nil && lease != nil && s.limiter.isCurrent(lease.Slot())
}

func (s *Selector) loadCurrentProvider(ctx context.Context, lease *ProviderLease) (*model.Provider, error) {
	provider, err := s.store.GetProvider(ctx, lease.ProviderID())
	if err != nil {
		return nil, providerLookupFailure(err, errorrule.ReasonProviderDeleted)
	}
	return provider, nil
}

func validateCurrentProvider(provider *model.Provider, providerID, apiType string) error {
	switch {
	case provider == nil || provider.ID != providerID:
		return rejectProvider(errorrule.ReasonProviderDeleted, nil)
	case !provider.Enabled:
		return rejectProvider(errorrule.ReasonProviderDisabled, nil)
	case !providerSupportsAPIType(provider, apiType):
		return rejectProvider(errorrule.ReasonAPIRemoved, nil)
	default:
		return nil
	}
}

func providerLookupFailure(err error, notFoundReason errorrule.DecisionReason) error {
	if contextError(err) != nil {
		return err
	}
	if notFoundReason != "" && errors.Is(err, storepkg.ErrNotFound) {
		return rejectProvider(notFoundReason, err)
	}
	return rejectProvider(errorrule.ReasonProviderLookupError, err)
}

func sameCandidateIdentity(left, right codexidentity.CandidateSnapshot) bool {
	return left.RouteTargetID() == right.RouteTargetID() &&
		left.CredentialSessionID() == right.CredentialSessionID() &&
		left.CredentialVersion() == right.CredentialVersion() &&
		left.APIType() == right.APIType() &&
		left.Authority().Equal(right.Authority())
}

func contextError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return nil
}

func rejectProvider(reason errorrule.DecisionReason, cause error) error {
	return &ProviderRejectionError{Reason: reason, Cause: cause}
}

const (
	dispatchPermitReserved uint32 = iota + 1
	dispatchPermitActivated
	dispatchPermitReleased
)

type providerDispatchPermitState struct {
	status   atomic.Uint32
	provider atomic.Pointer[model.Provider]
	current  *ProviderLease
	limiter  *ConcurrencyLimiter
	logger   *zap.Logger
}

// SameProviderDispatchRequest contains the live request facts and exact lease
// needed to authorize another dispatch without acquiring a second slot.
type SameProviderDispatchRequest struct {
	Current *ProviderLease
	Request *model.SelectRequest
}

// ProviderDispatchPermit is a copy-safe authorization barrier for any
// same-provider redispatch. It separates fallible live validation from the
// destructive response transition while still letting lifecycle retirement win
// at activation.
type ProviderDispatchPermit struct {
	state *providerDispatchPermitState
}

// ReserveSameProviderDispatch revalidates live provider facts while retaining
// the current request's exact concurrency capability.
func (s *Selector) ReserveSameProviderDispatch(
	ctx context.Context,
	input SameProviderDispatchRequest,
) (*ProviderDispatchPermit, error) {
	state, err := s.reserveSameProviderDispatch(ctx, input)
	if err != nil {
		return nil, err
	}
	if state.logger != nil {
		state.logger.Debug("selector.same_provider_dispatch_reserved",
			zap.String("provider_id", state.current.ProviderID()),
			zap.Uint64("provider_generation", state.current.Generation()),
		)
	}
	return &ProviderDispatchPermit{state: state}, nil
}

func (s *Selector) reserveSameProviderDispatch(
	ctx context.Context,
	input SameProviderDispatchRequest,
) (*providerDispatchPermitState, error) {
	if input.Current == nil || input.Current.ProviderID() == "" {
		return nil, fmt.Errorf("same-provider dispatch requires a held provider lease")
	}
	live, err := s.revalidateProvider(ctx, input.Request, input.Current)
	if err != nil {
		s.logProviderRejection("selector.same_provider_dispatch_rejected", input.Current, err)
		return nil, err
	}
	state := &providerDispatchPermitState{
		current: input.Current,
		limiter: s.limiter,
		logger:  s.logger,
	}
	state.status.Store(dispatchPermitReserved)
	state.provider.Store(live.provider)
	lifecycleCurrent := false
	if !s.limiter.prepare(input.Current.Slot(), func() bool {
		lifecycleCurrent = true
		return input.Current.state.dispatchPermitReserved.CompareAndSwap(false, true)
	}) {
		if lifecycleCurrent {
			return nil, ErrDispatchPermitOutstanding
		}
		return nil, rejectProvider(errorrule.ReasonProviderDeleted, nil)
	}
	return state, nil
}

// Provider returns the live configuration snapshot that will be adopted if the
// permit wins activation.
func (p *ProviderDispatchPermit) Provider() *model.Provider {
	if p == nil || p.state == nil {
		return nil
	}
	return p.state.provider.Load()
}

func (p *ProviderDispatchPermit) Activate() (*model.Provider, error) {
	if p == nil || p.state == nil {
		return nil, ErrDispatchPermitReleased
	}
	provider, err := p.state.activate()
	if err != nil {
		return nil, err
	}
	if p.state.logger != nil {
		p.state.logger.Debug("selector.same_provider_dispatch_activated",
			zap.String("provider_id", p.state.current.ProviderID()),
			zap.Uint64("provider_generation", p.state.current.Generation()),
		)
	}
	return provider, nil
}

func (p *ProviderDispatchPermit) Release() bool {
	if p == nil || p.state == nil {
		return false
	}
	if !p.state.release() {
		return false
	}
	if p.state.logger != nil {
		p.state.logger.Debug("selector.same_provider_dispatch_released",
			zap.String("provider_id", p.state.current.ProviderID()),
			zap.Uint64("provider_generation", p.state.current.Generation()),
		)
	}
	return true
}

func (p *providerDispatchPermitState) activate() (*model.Provider, error) {
	if p == nil || p.current == nil || p.limiter == nil {
		return nil, ErrDispatchPermitReleased
	}
	lifecycleCurrent := false
	activated := p.limiter.prepare(p.current.Slot(), func() bool {
		lifecycleCurrent = true
		if !p.status.CompareAndSwap(dispatchPermitReserved, dispatchPermitActivated) {
			return false
		}
		p.current.replaceProvider(p.provider.Load())
		p.current.state.dispatchPermitReserved.Store(false)
		return true
	})
	if activated {
		return p.provider.Load(), nil
	}
	if !lifecycleCurrent && p.status.CompareAndSwap(dispatchPermitReserved, dispatchPermitReleased) {
		p.current.state.dispatchPermitReserved.Store(false)
		if !p.current.Held() {
			return nil, ErrDispatchPermitReleased
		}
		err := rejectProvider(errorrule.ReasonProviderDeleted, nil)
		return nil, err
	}
	if p.status.Load() == dispatchPermitActivated {
		return nil, ErrDispatchPermitActivated
	}
	return nil, ErrDispatchPermitReleased
}

func (p *providerDispatchPermitState) release() bool {
	if p == nil || !p.status.CompareAndSwap(dispatchPermitReserved, dispatchPermitReleased) {
		return false
	}
	p.current.state.dispatchPermitReserved.Store(false)
	return true
}

// SameProviderRetryRequest adds an immutable ledger transition to a dispatch
// permit. The next value remains private until activation succeeds.
type SameProviderRetryRequest struct {
	Current           *ProviderLease
	Request           *model.SelectRequest
	RuleKey           errorrule.ProviderRuleKey
	Ledger            errorrule.RetryLedger
	GlobalMaxAttempts uint
}

type retryPermitState struct {
	dispatch *providerDispatchPermitState
	ledger   errorrule.RetryLedger
}

// RetryPermit composes exact dispatch authorization with an uncharged ledger
// value so either both become visible or neither does.
type RetryPermit struct {
	state *retryPermitState
}

func (s *Selector) ReserveSameProviderRetry(
	ctx context.Context,
	input SameProviderRetryRequest,
) (*RetryPermit, error) {
	if input.Current == nil || input.Current.ProviderID() == "" ||
		string(input.RuleKey.ProviderID) != input.Current.ProviderID() {
		return nil, fmt.Errorf("same-provider retry lease and rule provider must match")
	}
	if err := input.RuleKey.Validate(); err != nil {
		return nil, err
	}
	nextLedger, err := input.Ledger.StartRuleRetry(input.RuleKey, input.GlobalMaxAttempts)
	if err != nil {
		return nil, err
	}
	dispatch, err := s.reserveSameProviderDispatch(ctx, SameProviderDispatchRequest{
		Current: input.Current,
		Request: input.Request,
	})
	if err != nil {
		s.logProviderRejection("selector.retry_permit_rejected", input.Current, err)
		return nil, err
	}
	if dispatch.logger != nil {
		dispatch.logger.Debug("selector.retry_permit_reserved",
			zap.String("provider_id", input.Current.ProviderID()),
			zap.Uint64("provider_generation", input.Current.Generation()),
			zap.String("rule_id", string(input.RuleKey.RuleID)),
		)
	}
	return &RetryPermit{state: &retryPermitState{dispatch: dispatch, ledger: nextLedger}}, nil
}

func (p *RetryPermit) Provider() *model.Provider {
	if p == nil || p.state == nil || p.state.dispatch == nil {
		return nil
	}
	return p.state.dispatch.provider.Load()
}

func (p *RetryPermit) CurrentLease() *ProviderLease {
	if p == nil || p.state == nil || p.state.dispatch == nil {
		return nil
	}
	return p.state.dispatch.current
}

func (p *RetryPermit) Activate() (errorrule.RetryLedger, error) {
	if p == nil || p.state == nil || p.state.dispatch == nil {
		return errorrule.RetryLedger{}, ErrDispatchPermitReleased
	}
	if _, err := p.state.dispatch.activate(); err != nil {
		return errorrule.RetryLedger{}, err
	}
	if p.state.dispatch.logger != nil {
		p.state.dispatch.logger.Debug("selector.retry_permit_activated",
			zap.String("provider_id", p.state.dispatch.current.ProviderID()),
			zap.Uint64("provider_generation", p.state.dispatch.current.Generation()),
		)
	}
	return p.state.ledger, nil
}

func (p *RetryPermit) Release() bool {
	if p == nil || p.state == nil || p.state.dispatch == nil || !p.state.dispatch.release() {
		return false
	}
	if p.state.dispatch.logger != nil {
		p.state.dispatch.logger.Debug("selector.retry_permit_released",
			zap.String("provider_id", p.state.dispatch.current.ProviderID()),
			zap.Uint64("provider_generation", p.state.dispatch.current.Generation()),
		)
	}
	return true
}

func (s *Selector) logProviderRejection(event string, lease *ProviderLease, err error) {
	if s == nil || s.logger == nil {
		return
	}
	reason, _ := ProviderRejectionReason(err)
	s.logger.Debug(event,
		zap.String("provider_id", lease.ProviderID()),
		zap.Uint64("provider_generation", lease.Generation()),
		zap.String("reason", string(reason)),
		zap.Error(err),
	)
}
