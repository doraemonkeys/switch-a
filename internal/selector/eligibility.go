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

type providerRejectionScope string

const (
	providerRejectionScopeRoute   providerRejectionScope = "route"
	providerRejectionScopeRequest providerRejectionScope = "request"
)

// A request can reject a healthy route without invalidating affinity shared by
// other conversations. Preserve that distinction at the decision's source.
type providerRejection struct {
	reason errorrule.DecisionReason
	scope  providerRejectionScope
}

func (r providerRejection) allowed() bool { return r.reason == "" }

func routeRejection(reason errorrule.DecisionReason) providerRejection {
	return providerRejection{reason: reason, scope: providerRejectionScopeRoute}
}

func requestRejection(reason errorrule.DecisionReason) providerRejection {
	return providerRejection{reason: reason, scope: providerRejectionScopeRequest}
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
	policies, err := selectionRoutingPolicies(ctx, policySource, req)
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
	rejection, err := e.evaluateProvider(ctx, provider, selectionEligibilityMode())
	return err == nil && rejection.allowed(), err
}

// allowsExistingRoute reuses the immutable candidate eligibility boundary while
// omitting switch-only guards. A retry or active request already owns its route;
// its presence in switch history cannot make that same route ineligible.
func (e *ProviderSelectionEligibility) allowsExistingRoute(
	ctx context.Context,
	provider *model.Provider,
	checkHealth bool,
) (bool, errorrule.DecisionReason, error) {
	rejection, err := e.evaluateProvider(ctx, provider, existingRouteEligibilityMode(checkHealth))
	return err == nil && rejection.allowed(), rejection.reason, err
}

func (e *ProviderSelectionEligibility) evaluateProvider(
	ctx context.Context,
	provider *model.Provider,
	mode providerEligibilityMode,
) (providerRejection, error) {
	if e == nil || provider == nil {
		return routeRejection(errorrule.ReasonProviderDeleted), nil
	}
	candidate := e.candidate(ctx, provider)
	provider = candidate.provider
	if provider == nil {
		return routeRejection(errorrule.ReasonProviderDeleted), nil
	}
	if !provider.Enabled {
		return routeRejection(errorrule.ReasonProviderDisabled), nil
	}
	if !providerSupportsAPIType(provider, reqAPIType(e.req)) {
		return routeRejection(errorrule.ReasonAPIRemoved), nil
	}
	if _, supported := provider.RouteConfig(reqAPIType(e.req), reqTransport(e.req)); !supported {
		return routeRejection(errorrule.ReasonTransportUnsupported), nil
	}
	// Routing policy defines the candidate boundary itself. Every entry point,
	// including sticky reuse, must re-check it so cached providers cannot outlive
	// a stricter policy match.
	if mode.checkRouting && !e.routing.allowsProvider(provider) {
		return requestRejection(errorrule.ReasonRoutingChanged), nil
	}
	if e.req != nil && e.req.ClientDisguise != nil && reqAPIType(e.req) == "codex" {
		disguise, err := e.req.ClientDisguise.Evaluate(ctx, provider)
		if err != nil {
			return requestRejection(errorrule.ReasonProviderLookupError), err
		}
		if !disguise.Decision.Allowed {
			return requestRejection(errorrule.ReasonClientPlatformExcluded), nil
		}
	}
	if candidate.groupErr != nil {
		return routeRejection(errorrule.ReasonProviderLookupError), candidate.groupErr
	}
	if candidate.group != nil && !candidate.group.Enabled {
		return routeRejection(errorrule.ReasonGroupDisabled), nil
	}
	if !credentialSessionUsable(candidate.credential) {
		return routeRejection(errorrule.ReasonAuthUnavailable), nil
	}
	if !candidate.identityResolved {
		return routeRejection(errorrule.ReasonAuthUnavailable), nil
	}
	if required := reqRequiredAuthority(e.req); mode.checkAuthority && required != nil {
		if !candidate.identity.Authority().Equal(*required) {
			return requestRejection(errorrule.ReasonAuthUnavailable), nil
		}
	}
	if !e.allowsCodexContinuation(provider, candidate.identity) {
		return requestRejection(errorrule.ReasonRoutingChanged), nil
	}

	if mode.checkHealth && e.health != nil {
		e.health.AvailabilityForRoute(reqAPIType(e.req), reqTransport(e.req)).RecoverIfExpired(ctx, provider.ID)
		if !e.health.AvailabilityForRoute(reqAPIType(e.req), reqTransport(e.req)).IsAvailable(ctx, provider.ID) {
			return routeRejection(errorrule.ReasonProviderDisabled), nil
		}
	}

	if mode.checkRouteTransition &&
		!routeTransitionAllowsProvider(provider, e.req, e.reqMaxProviderSwitches()) {
		return requestRejection(errorrule.ReasonRoutingChanged), nil
	}

	return providerRejection{}, nil
}

func (e *ProviderSelectionEligibility) allowsCodexContinuation(provider *model.Provider, identity codexidentity.CandidateSnapshot) bool {
	if reqAPIType(e.req) != "codex" || e.req.CodexContinuation == nil {
		return true
	}
	return e.req.CodexContinuation.Evaluate(provider.ID, identity.ProtocolScope(), provider.CodexContinuation).Allowed
}
