package selector

import (
	"context"
	"strings"

	"github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/doraemonkeys/switch-a/internal/errorrule"
	"github.com/doraemonkeys/switch-a/internal/model"
)

// ProviderSelectionEligibility keeps every routing entry point aligned on the
// same hard constraints so sticky hits, retries, and fallback mode cannot drift
// into different candidate semantics over time.
type ProviderSelectionEligibility struct {
	source     any
	req        *model.SelectRequest
	health     HealthChecker
	resolver   CandidateAuthorityResolver
	routing    routingPolicyResolution
	candidates map[string]providerCandidateSnapshot
	order      []string
	// Hidden-model demand is resolved once from the request, sticky mode, and
	// active routing catalog so websocket probe gating can share the same
	// pre-selection semantics as the selector closure.
	hiddenModelDemand bool
}

// Request returns the request snapshot that this eligibility object was built for.
func (e *ProviderSelectionEligibility) Request() *model.SelectRequest {
	if e == nil {
		return nil
	}
	return e.req
}

// WouldConsumeHiddenModel reports whether initial selection would benefit from a
// hidden model before provider selection. Model-sticky continuity needs the
// model for key precision, and active model-scoped routing rules need it to
// determine whether they narrow the candidate set ahead of selection.
func (e *ProviderSelectionEligibility) WouldConsumeHiddenModel() bool {
	if e == nil {
		return false
	}
	if hasUsableRequestModel(e.req) {
		return false
	}
	return e.hiddenModelDemand
}

// NewProviderSelectionEligibility resolves the request-scoped hard constraints
// once so callers can reuse the same policy/auth/health decision across all
// selection entry points in the current attempt.
func NewProviderSelectionEligibility(
	ctx context.Context,
	policySource any,
	health HealthChecker,
	req *model.SelectRequest,
	providers ...model.Provider,
) (*ProviderSelectionEligibility, error) {
	return newProviderSelectionEligibility(
		ctx,
		policySource,
		health,
		codexidentity.NewAuthorityResolver(),
		req,
		providers,
	)
}

func newProviderSelectionEligibility(
	ctx context.Context,
	policySource any,
	health HealthChecker,
	resolver CandidateAuthorityResolver,
	req *model.SelectRequest,
	providers []model.Provider,
) (*ProviderSelectionEligibility, error) {
	policies, err := listRoutingPoliciesByAPIType(ctx, policySource, reqAPIType(req))
	if err != nil {
		return nil, err
	}
	if resolver == nil {
		resolver = codexidentity.NewAuthorityResolver()
	}
	if required := reqRequiredAuthority(req); required != nil {
		if _, err := required.MarshalBinary(); err != nil {
			return nil, err
		}
	}

	eligibility := &ProviderSelectionEligibility{
		source:            policySource,
		req:               req,
		health:            health,
		resolver:          resolver,
		routing:           resolveRoutingPolicy(policies, req),
		hiddenModelDemand: selectionConsumesHiddenModel(policies, req),
	}
	if len(providers) == 0 {
		return eligibility, nil
	}
	eligibility.candidates = make(map[string]providerCandidateSnapshot, len(providers))
	eligibility.order = make([]string, 0, len(providers))
	for index := range providers {
		candidate := eligibility.resolveCandidate(ctx, &providers[index])
		providerID := strings.TrimSpace(providers[index].ID)
		if providerID == "" {
			continue
		}
		if candidate.groupErr != nil && reqRequiredAuthority(req) != nil {
			return nil, candidate.groupErr
		}
		if _, exists := eligibility.candidates[providerID]; !exists {
			eligibility.order = append(eligibility.order, providerID)
		}
		eligibility.candidates[providerID] = candidate
	}
	return eligibility, nil
}

// IsEligible reports whether the provider can participate in the current
// request's candidate set after applying routing policy, auth lifecycle, health,
// and switch-mode-specific closure checks.
func (e *ProviderSelectionEligibility) IsEligible(ctx context.Context, provider *model.Provider) bool {
	allowed, err := e.AllowsProvider(ctx, provider)
	return err == nil && allowed
}

// AllowsProvider evaluates the provider against the full request-scoped
// eligibility closure and returns an error when the backing auth-state source
// cannot be read safely.
func (e *ProviderSelectionEligibility) AllowsProvider(ctx context.Context, provider *model.Provider) (bool, error) {
	allowed, _, err := e.evaluateProvider(ctx, provider, selectionEligibilityMode())
	return allowed, err
}

// allowsExistingRoute reuses the immutable candidate eligibility boundary while
// omitting switch-only guards. A retry or active request already owns its route;
// its presence in switch history cannot make that same route ineligible.
func (e *ProviderSelectionEligibility) allowsExistingRoute(
	ctx context.Context,
	provider *model.Provider,
	checkHealth bool,
) (bool, errorrule.DecisionReason, error) {
	return e.evaluateProvider(ctx, provider, existingRouteEligibilityMode(checkHealth))
}

func (e *ProviderSelectionEligibility) evaluateProvider(
	ctx context.Context,
	provider *model.Provider,
	mode providerEligibilityMode,
) (bool, errorrule.DecisionReason, error) {
	if e == nil || provider == nil {
		return false, errorrule.ReasonProviderDeleted, nil
	}
	candidate := e.candidate(ctx, provider)
	provider = candidate.provider
	if provider == nil {
		return false, errorrule.ReasonProviderDeleted, nil
	}
	if !provider.Enabled {
		return false, errorrule.ReasonProviderDisabled, nil
	}
	if !providerSupportsAPIType(provider, reqAPIType(e.req)) {
		return false, errorrule.ReasonAPIRemoved, nil
	}
	// Routing policy defines the candidate boundary itself. Every entry point,
	// including sticky reuse, must re-check it so cached providers cannot outlive
	// a stricter policy match.
	if !e.routing.allowsProvider(provider) {
		return false, errorrule.ReasonRoutingChanged, nil
	}
	if candidate.groupErr != nil {
		return false, errorrule.ReasonProviderLookupError, candidate.groupErr
	}
	if candidate.group != nil && !candidate.group.Enabled {
		return false, errorrule.ReasonGroupDisabled, nil
	}
	if !credentialSessionUsable(candidate.credential) {
		return false, errorrule.ReasonAuthUnavailable, nil
	}
	if required := reqRequiredAuthority(e.req); required != nil {
		if !candidate.identityResolved || !candidate.identity.Authority().Equal(*required) {
			return false, errorrule.ReasonAuthUnavailable, nil
		}
	}

	if mode.checkHealth && e.health != nil {
		e.health.RecoverIfExpired(ctx, provider.ID)
		if !e.health.IsAvailable(ctx, provider.ID) {
			return false, errorrule.ReasonProviderDisabled, nil
		}
	}

	if mode.checkRouteTransition &&
		!routeTransitionAllowsProvider(provider, e.req, e.reqMaxProviderSwitches()) {
		return false, errorrule.ReasonRoutingChanged, nil
	}

	return true, "", nil
}
