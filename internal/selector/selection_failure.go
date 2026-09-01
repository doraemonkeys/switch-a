package selector

import (
	"context"

	"github.com/doraemonkeys/switch-a/internal"

	"go.uber.org/zap"
)

const selectionFailureEvent = "selector.selection_failed"

// noProviderError promotes a provable hard-constraint conflict above the
// generic availability sentinel. Exclusions belong to retry history, so they
// cannot establish that the request's original routing domain is contradictory.
func (s *Selector) noProviderError(
	ctx context.Context,
	scope *ProviderSelectionEligibility,
	excludeIDs map[string]bool,
) error {
	if scope == nil || len(excludeIDs) > 0 || !s.hasContinuityRoutingConflict(ctx, scope) {
		return internal.ErrNoProvider
	}

	err := &internal.ProviderSelectionError{
		Reason:                        internal.ProviderSelectionFailureContinuityRoutingConflict,
		APIType:                       reqAPIType(scope.req),
		PreferredRouteTargetID:        reqPreferredRouteTargetID(scope.req),
		RoutingPolicyConstraint:       scope.routing.constraintKind(),
		RoutingPolicyTargetProviderID: scope.routing.targetProviderID,
	}
	if s.logger != nil {
		s.logger.Warn(selectionFailureEvent,
			zap.String("operation_id", reqOperationID(scope.req)),
			zap.String("api_type", err.APIType),
			zap.String("failure_reason", string(err.Reason)),
			zap.String("preferred_route_target_id", err.PreferredRouteTargetID),
			zap.String("routing_policy_constraint", err.RoutingPolicyConstraint),
			zap.String("routing_policy_target_provider_id", err.RoutingPolicyTargetProviderID),
		)
	}
	return err
}

func (s *Selector) hasContinuityRoutingConflict(
	ctx context.Context,
	e *ProviderSelectionEligibility,
) bool {
	if e == nil || reqRequiredAuthority(e.req) == nil || !e.routing.constrained || !e.routing.matched {
		return false
	}

	baseEligibility := selectionEligibilityMode()
	baseEligibility.checkRouting = false
	baseEligibility.checkAuthority = false
	requiredAuthority := reqRequiredAuthority(e.req)

	authorityCandidate := false
	routingCandidate := false
	for _, providerID := range e.order {
		candidate := e.candidates[providerID]
		if candidate.provider == nil {
			continue
		}

		baseAllowed, _, err := e.evaluateProvider(ctx, candidate.provider, baseEligibility)
		if err != nil || !baseAllowed {
			continue
		}
		if candidate.provider.Concurrency > 0 &&
			(s.limiter == nil || s.limiter.Current(providerID) >= int64(candidate.provider.Concurrency)) {
			continue
		}
		routingAllowed := e.routing.allowsProvider(candidate.provider)
		authorityAllowed := candidate.identity.Authority().Equal(*requiredAuthority)
		// An eligible intersection means selection failed for a different reason,
		// such as exhausted concurrency, and must retain the availability error.
		if routingAllowed && authorityAllowed {
			return false
		}
		authorityCandidate = authorityCandidate || authorityAllowed
		routingCandidate = routingCandidate || routingAllowed
	}
	return authorityCandidate && routingCandidate
}
