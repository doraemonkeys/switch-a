package selector

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/doraemonkeys/switch-a/internal/errorrule"
	"github.com/doraemonkeys/switch-a/internal/model"

	"go.uber.org/zap"
)

var (
	ErrReservationReleased = errors.New("provider reservation released")
)

// AlternateReservationRequest is a value-only selection command. Selector
// choice and slot reservation do not mutate continuity or retry trackers.
type AlternateReservationRequest struct {
	Request            *model.SelectRequest
	ExcludeProviderIDs map[string]bool
}

const (
	reservationReserved uint32 = iota + 1
	reservationPrepared
	reservationActivated
	reservationReleased
)

type providerReservationState struct {
	status         atomic.Uint32
	lifecycleGuard atomic.Pointer[lifecycleReadLease]
	selector       *Selector
	request        *model.SelectRequest
	lease          *ProviderLease
	metadata       SelectionMetadata
	logger         *zap.Logger
}

// ProviderReservation owns alternate capacity until it is either activated or
// released. Copies share the same transition state and slot capability.
type ProviderReservation struct {
	state *providerReservationState
}

// ReserveAlternate selects and reserves an alternate without changing caller
// continuity. That lets the proxy commit its preview only after Discard succeeds.
func (s *Selector) ReserveAlternate(
	ctx context.Context,
	input AlternateReservationRequest,
) (*ProviderReservation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	lifecycle := s.limiter.beginLifecycleRead()
	defer lifecycle.Release()
	requestSnapshot := cloneSelectRequest(input.Request)
	scope, err := s.selectionScope(ctx, requestSnapshot)
	if err != nil {
		return nil, err
	}
	lease, err := s.selectExcludingInternal(ctx, scope, cloneExclusions(input.ExcludeProviderIDs))
	if err != nil {
		return nil, err
	}

	state := &providerReservationState{
		selector: s,
		request:  requestSnapshot,
		lease:    lease,
		metadata: BuildSelectionMetadataAt(requestSnapshot, SelectionSourceAlternate, s.selectionTimestamp()),
		logger:   s.logger,
	}
	state.status.Store(reservationReserved)
	s.logger.Debug("selector.alternate_reserved",
		zap.String("provider_id", lease.ProviderID()),
		zap.Uint64("provider_generation", lease.Generation()),
	)
	return &ProviderReservation{state: state}, nil
}

// Provider returns the alternate snapshot currently owned by the reservation.
func (r *ProviderReservation) Provider() *model.Provider {
	if r == nil || r.state == nil || r.state.lease == nil {
		return nil
	}
	return r.state.lease.Provider()
}

// Metadata returns selection provenance without exposing an active lease before
// activation succeeds.
func (r *ProviderReservation) Metadata() SelectionMetadata {
	if r == nil || r.state == nil {
		return SelectionMetadata{}
	}
	return r.state.metadata
}

// PrepareActivation revalidates live provider facts and linearizes against
// lifecycle invalidation. Context is accepted here so reservations never retain
// request contexts inside long-lived state.
func (r *ProviderReservation) PrepareActivation(ctx context.Context) error {
	if r == nil || r.state == nil {
		return ErrReservationReleased
	}
	status := r.state.status.Load()
	if status == reservationPrepared || status == reservationActivated {
		return nil
	}
	if status != reservationReserved {
		return ErrReservationReleased
	}
	lifecycle := r.state.selector.limiter.beginLifecycleRead()
	retainedLifecycle := false
	defer func() {
		if !retainedLifecycle {
			lifecycle.Release()
		}
	}()

	live, err := r.state.selector.revalidateProvider(ctx, r.state.request, r.state.lease)
	if err != nil {
		// A concurrent prepare may have established the dispatch boundary while
		// this caller was blocked in storage. Its committed state outranks this
		// stale observation and must not be rolled back.
		switch r.state.status.Load() {
		case reservationPrepared, reservationActivated:
			return nil
		case reservationReleased:
			return ErrReservationReleased
		}
		r.state.selector.logProviderRejection("selector.alternate_rejected", r.state.lease, err)
		r.Release()
		return err
	}
	prepared := r.state.selector.limiter.prepareUnderLifecycle(r.state.lease.Slot(), func() bool {
		if r.state.status.Load() != reservationReserved {
			return false
		}
		// Publish the snapshot before making the prepared state observable. Both
		// prepare and rollback own the generation lock here, so activation can
		// never transfer a lease whose live provider snapshot is still changing.
		r.state.lease.replaceProvider(live.provider)
		r.state.lifecycleGuard.Store(lifecycle)
		retainedLifecycle = true
		r.state.status.Store(reservationPrepared)
		return true
	})
	if !prepared {
		switch r.state.status.Load() {
		case reservationPrepared, reservationActivated:
			return nil
		case reservationReleased:
			return ErrReservationReleased
		}
		err = rejectProvider(errorrule.ReasonProviderDeleted, nil)
		r.Release()
		return err
	}

	r.state.logger.Debug("selector.alternate_prepared",
		zap.String("provider_id", r.state.lease.ProviderID()),
		zap.Uint64("provider_generation", r.state.lease.Generation()),
	)
	return nil
}

// Activate transfers the prepared provider lease to dispatch ownership. Once
// PrepareActivation succeeds this transition has no external failure point.
func (r *ProviderReservation) Activate() *ProviderLease {
	if r == nil || r.state == nil {
		return nil
	}
	if r.state.status.CompareAndSwap(reservationPrepared, reservationActivated) {
		r.state.releaseLifecycleGuard()
		r.state.logger.Debug("selector.alternate_activated",
			zap.String("provider_id", r.state.lease.ProviderID()),
			zap.Uint64("provider_generation", r.state.lease.Generation()),
		)
		return r.state.lease
	}
	if r.state.status.Load() == reservationActivated {
		return r.state.lease
	}
	return nil
}

// Release rolls a reservation back exactly once. Activated leases belong to the
// caller and can no longer be released through the reservation handle.
func (r *ProviderReservation) Release() bool {
	if r == nil || r.state == nil {
		return false
	}
	released := r.state.selector.limiter.releaseWithTransition(r.state.lease.Slot(), func() bool {
		for {
			status := r.state.status.Load()
			if status == reservationActivated || status == reservationReleased {
				return false
			}
			if r.state.status.CompareAndSwap(status, reservationReleased) {
				return true
			}
		}
	})
	if released {
		r.state.releaseLifecycleGuard()
		r.state.logger.Debug("selector.alternate_released",
			zap.String("provider_id", r.state.lease.ProviderID()),
			zap.Uint64("provider_generation", r.state.lease.Generation()),
		)
	}
	return released
}

func (s *providerReservationState) releaseLifecycleGuard() {
	if s == nil {
		return
	}
	if lifecycle := s.lifecycleGuard.Swap(nil); lifecycle != nil {
		lifecycle.Release()
	}
}

func cloneExclusions(source map[string]bool) map[string]bool {
	if len(source) == 0 {
		return nil
	}
	clone := make(map[string]bool, len(source))
	for providerID, excluded := range source {
		clone[providerID] = excluded
	}
	return clone
}

func cloneSelectRequest(source *model.SelectRequest) *model.SelectRequest {
	if source == nil {
		return nil
	}
	clone := *source
	if source.RequiredAuthority != nil {
		authority := *source.RequiredAuthority
		clone.RequiredAuthority = &authority
	}
	clone.ProviderSwitchHistory = source.ProviderSwitchHistory.Clone()
	clone.ProviderContinuityContext = source.ProviderContinuityContext.Clone()
	if source.VisibleContinuitySeedCandidate != nil {
		candidate := *source.VisibleContinuitySeedCandidate
		clone.VisibleContinuitySeedCandidate = &candidate
	}
	if source.FailoverContext != nil {
		failover := *source.FailoverContext
		failover.ContaminatedVendors = append([]string(nil), source.FailoverContext.ContaminatedVendors...)
		failover.AttemptChain = append([]string(nil), source.FailoverContext.AttemptChain...)
		clone.FailoverContext = &failover
	}
	return &clone
}
