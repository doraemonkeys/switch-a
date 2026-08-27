package selector

import (
	"context"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/model"
)

// SelectActive reuses an active provider only within the same lifecycle
// generation and acquires a distinct lease for the new request. Sharing the
// original capability would let either request release capacity for both.
func (s *Selector) SelectActive(
	ctx context.Context,
	req *model.SelectRequest,
	active *ProviderLease,
) (*SelectResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s == nil || s.store == nil || s.limiter == nil || active == nil {
		return nil, internal.ErrNoProvider
	}
	lifecycle := s.limiter.beginLifecycleRead()
	defer lifecycle.Release()
	if !s.limiter.isCurrent(active.Slot()) {
		return nil, internal.ErrNoProvider
	}

	scope, err := s.selectionScope(ctx, req)
	if err != nil {
		return nil, err
	}
	provider := scope.Provider(active.ProviderID())
	allowed, _, err := scope.allowsExistingRoute(ctx, provider, true)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, internal.ErrNoProvider
	}
	candidate, resolved := scope.CandidateSnapshot(provider.ID)
	activeCandidate, activeResolved := active.CandidateSnapshot()
	if activeResolved && (!resolved || !sameCandidateIdentity(activeCandidate, candidate)) {
		return nil, internal.ErrNoProvider
	}

	slot, acquired := s.limiter.acquireInGenerationUnderLifecycle(active.Slot(), provider.Concurrency)
	if !acquired {
		return nil, internal.ErrNoProvider
	}
	lease := newProviderLeaseWithCandidate(provider, slot, candidate, resolved)
	return &SelectResult{
		Lease:    lease,
		Metadata: BuildSelectionMetadataAt(req, SelectionSourceActiveContinuity, s.selectionTimestamp()),
	}, nil
}
