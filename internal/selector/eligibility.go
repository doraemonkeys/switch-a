package selector

import (
	"context"
	"strings"

	"switch-a/internal/model"
)

const unknownModelSentinel = "unknown"

type routingPolicySource interface {
	ListRoutingPoliciesByAPIType(ctx context.Context, apiType string) ([]model.RoutingPolicy, error)
}

type providerAuthStateSource interface {
	GetProviderAuthState(ctx context.Context, providerID string) (*model.ProviderAuthState, error)
}

type routingPolicyResolution struct {
	constrained         bool
	matched             bool
	consumesHiddenModel bool
	groupIDs            map[string]struct{}
	vendors             map[string]struct{}
}

// ProviderSelectionEligibility keeps every routing entry point aligned on the
// same hard constraints so sticky hits, retries, and fallback mode cannot drift
// into different candidate semantics over time.
type ProviderSelectionEligibility struct {
	source  any
	req     *model.SelectRequest
	health  HealthChecker
	routing routingPolicyResolution
}

// Request returns the request snapshot that this eligibility object was built for.
func (e *ProviderSelectionEligibility) Request() *model.SelectRequest {
	if e == nil {
		return nil
	}
	return e.req
}

// WouldConsumeHiddenModel reports whether initial selection would use a model
// that is not currently available on the request. The selector owns this seam so
// sticky degradation and routing-policy fallback stay synchronized when callers
// decide whether pre-selection probing is worth attempting.
func (e *ProviderSelectionEligibility) WouldConsumeHiddenModel() bool {
	if e == nil {
		return false
	}
	if hasUsableRequestModel(e.req) {
		return false
	}
	return stickyModeConsumesModel(reqStickyMode(e.req)) || e.routing.consumesHiddenModel
}

// ResolveSelectionHiddenModelDemand reports whether initial provider selection
// would consume a model that is not currently present on the request. Model
// sticky is an independent continuity consumer, so callers can answer the probe
// decision immediately instead of letting a routing-policy lookup failure erase
// a known model-affinity demand.
func ResolveSelectionHiddenModelDemand(
	ctx context.Context,
	policySource any,
	req *model.SelectRequest,
) (bool, error) {
	if hasUsableRequestModel(req) {
		return false, nil
	}
	if stickyModeConsumesModel(reqStickyMode(req)) {
		return true, nil
	}

	policies, err := listRoutingPoliciesByAPIType(ctx, policySource, reqAPIType(req))
	if err != nil {
		return false, err
	}
	return resolveRoutingPolicy(policies, req).consumesHiddenModel, nil
}

// NewProviderSelectionEligibility resolves the request-scoped hard constraints
// once so callers can reuse the same policy/auth/health decision across all
// selection entry points in the current attempt.
func NewProviderSelectionEligibility(
	ctx context.Context,
	policySource any,
	health HealthChecker,
	req *model.SelectRequest,
) (*ProviderSelectionEligibility, error) {
	policies, err := listRoutingPoliciesByAPIType(ctx, policySource, reqAPIType(req))
	if err != nil {
		return nil, err
	}

	return &ProviderSelectionEligibility{
		source:  policySource,
		req:     req,
		health:  health,
		routing: resolveRoutingPolicy(policies, req),
	}, nil
}

// IsEligible reports whether the provider can participate in the current
// request's candidate set after applying routing policy, auth lifecycle, health,
// and failover-closure checks.
func (e *ProviderSelectionEligibility) IsEligible(ctx context.Context, provider *model.Provider) bool {
	allowed, err := e.AllowsProvider(ctx, provider)
	return err == nil && allowed
}

// AllowsProvider evaluates the provider against the full request-scoped
// eligibility closure and returns an error when the backing auth-state source
// cannot be read safely.
func (e *ProviderSelectionEligibility) AllowsProvider(ctx context.Context, provider *model.Provider) (bool, error) {
	if provider == nil {
		return false, nil
	}
	if !provider.Enabled {
		return false, nil
	}
	if !providerSupportsAPIType(provider, reqAPIType(e.req)) {
		return false, nil
	}
	if !e.routing.allowsProvider(provider) {
		return false, nil
	}

	authState, err := e.providerAuthState(ctx, provider)
	if err != nil {
		return false, err
	}
	provider.AuthState = authState
	if authState.Status != model.ProviderAuthStatusActive {
		return false, nil
	}

	if e.health != nil {
		e.health.RecoverIfExpired(ctx, provider.ID)
		if !e.health.IsAvailable(ctx, provider.ID) {
			return false, nil
		}
	}

	if failoverCtx := e.reqFailoverContext(); failoverCtx != nil {
		if !model.IsFailoverAllowed(provider, failoverCtx, e.reqMaxProviderSwitches()) {
			return false, nil
		}
	}

	return true, nil
}

// BuildContinuityKey derives the sticky/continuity key from the request
// dimensions already known before provider selection. Unknown models degrade to
// api_type scope even when sticky mode prefers model affinity.
func BuildContinuityKey(req *model.SelectRequest) model.StickyKey {
	key := model.StickyKey{
		IP:      reqClientIP(req),
		User:    reqUser(req),
		APIType: reqAPIType(req),
	}
	if stickyModeConsumesModel(reqStickyMode(req)) {
		key.Model = requestSelectionModel(req)
	}
	return key
}

func listRoutingPoliciesByAPIType(ctx context.Context, source any, apiType string) ([]model.RoutingPolicy, error) {
	if apiType == "" {
		return nil, nil
	}

	policyStore, ok := source.(routingPolicySource)
	if !ok {
		return nil, nil
	}

	return policyStore.ListRoutingPoliciesByAPIType(ctx, apiType)
}

func resolveRoutingPolicy(policies []model.RoutingPolicy, req *model.SelectRequest) routingPolicyResolution {
	if len(policies) == 0 {
		return routingPolicyResolution{}
	}

	requestModel := requestSelectionModel(req)
	requestModelKnown := requestModel != ""
	bestIndex := -1
	bestRank := -1
	bestPrefixLen := -1
	hasAPITypePolicy := false
	hasModelSpecificPolicy := false

	for i := range policies {
		policy := &policies[i]
		if strings.TrimSpace(policy.APIType) != reqAPIType(req) {
			continue
		}
		hasAPITypePolicy = true
		if routingPolicyConsumesModel(policy) {
			hasModelSpecificPolicy = true
			if !requestModelKnown {
				continue
			}
		}

		rank, prefixLen, matched := routingPolicyRank(policy, requestModel)
		if !matched {
			continue
		}
		if rank > bestRank || (rank == bestRank && prefixLen > bestPrefixLen) {
			bestIndex = i
			bestRank = rank
			bestPrefixLen = prefixLen
		}
	}

	if bestIndex < 0 {
		if !hasAPITypePolicy {
			return routingPolicyResolution{}
		}
		if !requestModelKnown && hasModelSpecificPolicy {
			return routingPolicyResolution{consumesHiddenModel: true}
		}
		return routingPolicyResolution{constrained: true}
	}

	selected := policies[bestIndex]
	return routingPolicyResolution{
		constrained:         true,
		matched:             true,
		consumesHiddenModel: !requestModelKnown && hasModelSpecificPolicy,
		groupIDs:            buildRoutingPolicyGroupSet(selected.Groups),
		vendors:             buildRoutingPolicyVendorSet(selected.Vendors),
	}
}

func routingPolicyConsumesModel(policy *model.RoutingPolicy) bool {
	if policy == nil {
		return false
	}
	switch policy.ModelMatchType {
	case model.RoutingPolicyModelMatchTypeExact, model.RoutingPolicyModelMatchTypePrefix:
		return strings.TrimSpace(policy.ModelMatchValue) != ""
	default:
		return false
	}
}

func routingPolicyRank(policy *model.RoutingPolicy, requestModel string) (rank int, prefixLen int, matched bool) {
	if policy == nil {
		return 0, 0, false
	}

	matchValue := strings.TrimSpace(policy.ModelMatchValue)
	switch policy.ModelMatchType {
	case model.RoutingPolicyModelMatchTypeNone:
		return 1, 0, matchValue == ""
	case model.RoutingPolicyModelMatchTypeExact:
		if requestModel == "" || matchValue == "" {
			return 0, 0, false
		}
		return 3, len(matchValue), requestModel == matchValue
	case model.RoutingPolicyModelMatchTypePrefix:
		if requestModel == "" || matchValue == "" {
			return 0, 0, false
		}
		return 2, len(matchValue), strings.HasPrefix(requestModel, matchValue)
	default:
		return 0, 0, false
	}
}

func buildRoutingPolicyGroupSet(groups []model.RoutingPolicyGroup) map[string]struct{} {
	if len(groups) == 0 {
		return nil
	}

	allowed := make(map[string]struct{}, len(groups))
	for _, group := range groups {
		groupID := strings.TrimSpace(group.GroupID)
		if groupID == "" {
			continue
		}
		allowed[groupID] = struct{}{}
	}
	if len(allowed) == 0 {
		return nil
	}
	return allowed
}

func buildRoutingPolicyVendorSet(vendors []model.RoutingPolicyVendor) map[string]struct{} {
	if len(vendors) == 0 {
		return nil
	}

	allowed := make(map[string]struct{}, len(vendors))
	for _, vendor := range vendors {
		normalized := strings.TrimSpace(vendor.Vendor)
		if normalized == "" {
			continue
		}
		allowed[normalized] = struct{}{}
	}
	if len(allowed) == 0 {
		return nil
	}
	return allowed
}

func (r routingPolicyResolution) allowsProvider(provider *model.Provider) bool {
	if !r.constrained {
		return true
	}
	if !r.matched {
		return false
	}
	if len(r.groupIDs) > 0 {
		if provider.GroupID == nil {
			return false
		}
		if _, ok := r.groupIDs[strings.TrimSpace(*provider.GroupID)]; !ok {
			return false
		}
	}
	if len(r.vendors) > 0 {
		if _, ok := r.vendors[strings.TrimSpace(provider.Vendor)]; !ok {
			return false
		}
	}
	return true
}

func providerSupportsAPIType(provider *model.Provider, apiType string) bool {
	if provider == nil || apiType == "" {
		return false
	}
	if len(provider.APITypes) == 0 {
		return true
	}
	_, ok := provider.APITypeConfig(apiType)
	return ok
}

func normalizeRequestModel(modelName string) string {
	trimmed := strings.TrimSpace(modelName)
	if trimmed == "" || strings.EqualFold(trimmed, unknownModelSentinel) {
		return ""
	}
	return trimmed
}

func requestSelectionModel(req *model.SelectRequest) string {
	return normalizeRequestModel(reqModel(req))
}

func hasUsableRequestModel(req *model.SelectRequest) bool {
	return requestSelectionModel(req) != ""
}

func stickyModeConsumesModel(mode model.StickyMode) bool {
	return mode == model.StickyModeModel
}

func reqClientIP(req *model.SelectRequest) string {
	if req == nil {
		return ""
	}
	return req.ClientIP
}

func reqUser(req *model.SelectRequest) string {
	if req == nil {
		return ""
	}
	return req.User
}

func reqAPIType(req *model.SelectRequest) string {
	if req == nil {
		return ""
	}
	return strings.TrimSpace(req.APIType)
}

func reqModel(req *model.SelectRequest) string {
	if req == nil {
		return ""
	}
	return req.Model
}

func reqStickyMode(req *model.SelectRequest) model.StickyMode {
	if req == nil {
		return model.StickyModeOff
	}
	return req.StickyMode
}

func (e *ProviderSelectionEligibility) reqFailoverContext() *model.FailoverContext {
	if e == nil || e.req == nil {
		return nil
	}
	return e.req.FailoverContext
}

func (e *ProviderSelectionEligibility) reqMaxProviderSwitches() int {
	if e == nil || e.req == nil {
		return 0
	}
	return e.req.MaxProviderSwitches
}

func (e *ProviderSelectionEligibility) providerAuthState(
	ctx context.Context,
	provider *model.Provider,
) (*model.ProviderAuthState, error) {
	if provider == nil {
		return model.NormalizeProviderAuthStateRecord("", model.ProviderCredentialTypeAPIKey, nil), nil
	}
	if provider.AuthState != nil {
		return model.NormalizeProviderAuthStateRecord(provider.ID, provider.CredentialType, provider.AuthState), nil
	}
	if source, ok := e.source.(providerAuthStateSource); ok {
		authState, err := source.GetProviderAuthState(ctx, provider.ID)
		if err != nil {
			return nil, err
		}
		if authState != nil {
			return model.NormalizeProviderAuthStateRecord(provider.ID, provider.CredentialType, authState), nil
		}
	}
	return model.ProviderAuthStateFromCredential(provider.ID, provider.CredentialType, provider.Credential), nil
}
