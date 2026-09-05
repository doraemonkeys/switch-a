package codexcontinuity

import (
	"context"
	"errors"
)

type ResolutionStatus string

const (
	ResolutionOwned       ResolutionStatus = "owned"
	ResolutionUnknown     ResolutionStatus = "unknown"
	ResolutionExpired     ResolutionStatus = "expired"
	ResolutionUnavailable ResolutionStatus = "unavailable"
)

// Resolution separates source ownership from the lifetime of the durable proof.
// A retained expired owner still constrains the client and API that may use it.
type Resolution struct {
	Status ResolutionStatus
	Owner  *Owner
}

// Resolve does not select a route or grant commit authority. Strict callers can
// continue using ResolveOwner and AcquireExisting for their original contract.
func (s *Service) Resolve(ctx context.Context, request ResolveRequest) (Resolution, error) {
	lookup, err := s.prepareLookup(request.Evidence, request.ClientScopeCandidates, nil, request.OperationID)
	if err != nil {
		return Resolution{}, err
	}
	resolved, err := s.lookup(ctx, "resolve_source", lookup)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return Resolution{}, err
	}
	resolution := Resolution{Status: ResolutionOwned}
	if err != nil {
		switch {
		case IsError(err, ErrorUnknown):
			resolution.Status = ResolutionUnknown
		case IsError(err, ErrorExpired):
			resolution.Status = ResolutionExpired
		case IsError(err, ErrorUnavailable):
			resolution.Status = ResolutionUnavailable
		default:
			return Resolution{}, err
		}
	}
	if resolved.binding.Kind != "" {
		owner := resolved.binding.Owner
		if !clientScopeMatches(owner.ClientScope, lookup.ClientScopeCandidates) {
			s.emit("resolve_source", "client_scope_conflict", request.OperationID, resolved.binding)
			return Resolution{}, decisionError(StoreConflict, request.Evidence.Kind, request.OperationID)
		}
		resolution.Owner = &owner
	}
	return resolution, nil
}
