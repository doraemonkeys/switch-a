package selector

import (
	"context"
	"strings"

	"switch-a/internal/model"
)

const (
	unknownModelSentinel     = "unknown"
	routingPolicyRankAPIType = 1
	routingPolicyRankPrefix  = 2
	routingPolicyRankExact   = 3
)

type routingPolicySource interface {
	ListRoutingPoliciesByAPIType(ctx context.Context, apiType string) ([]model.RoutingPolicy, error)
}

type providerAuthStateSource interface {
	GetProviderAuthState(ctx context.Context, providerID string) (*model.ProviderAuthState, error)
}

type routingPolicyResolution struct {
	constrained      bool
	matched          bool
	targetProviderID string
	groupIDs         map[string]struct{}
	vendors          map[string]struct{}
}

// ProviderSelectionEligibility keeps every routing entry point aligned on the
// same hard constraints so sticky hits, retries, and fallback mode cannot drift
// into different candidate semantics over time.
type ProviderSelectionEligibility struct {
	source  any
	req     *model.SelectRequest
	health  HealthChecker
	routing routingPolicyResolution
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

// ResolveSelectionHiddenModelDemand reports whether initial provider selection
// would consume a model that is not currently present on the request. Probe
// demand is driven by pre-selection consumers: model-sticky continuity and any
// active model-scoped routing rule that could narrow the candidate set once the
// model is known.
func ResolveSelectionHiddenModelDemand(
	ctx context.Context,
	policySource any,
	req *model.SelectRequest,
) (bool, error) {
	if hasUsableRequestModel(req) {
		return false, nil
	}
	if stickyModeConsumesModel(reqStickyMode(req)) {
		// Model sticky needs the hidden model even without routing-policy input,
		// because continuity precision depends on the model dimension of the key.
		return true, nil
	}
	policies, err := listRoutingPoliciesByAPIType(ctx, policySource, reqAPIType(req))
	if err != nil {
		return false, err
	}
	return routingPoliciesConsumeHiddenModel(policies, req), nil
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
		source:            policySource,
		req:               req,
		health:            health,
		routing:           resolveRoutingPolicy(policies, req),
		hiddenModelDemand: selectionConsumesHiddenModel(policies, req),
	}, nil
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
	if provider == nil {
		return false, nil
	}
	if !provider.Enabled {
		return false, nil
	}
	if !providerSupportsAPIType(provider, reqAPIType(e.req)) {
		return false, nil
	}
	// Routing policy defines the candidate boundary itself. Every entry point,
	// including sticky reuse, must re-check it so cached providers cannot outlive
	// a stricter policy match.
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

	if !model.IsProviderSwitchAllowed(provider, reqProviderSwitchHistory(e.req), e.reqMaxProviderSwitches()) {
		return false, nil
	}
	if reqSwitchMode(e.req) == model.SwitchModeFailover &&
		!model.IsFailoverVendorAllowed(provider, reqProviderContinuityContext(e.req)) {
		return false, nil
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

func selectionConsumesHiddenModel(policies []model.RoutingPolicy, req *model.SelectRequest) bool {
	if hasUsableRequestModel(req) {
		return false
	}
	if stickyModeConsumesModel(reqStickyMode(req)) {
		return true
	}
	return routingPoliciesConsumeHiddenModel(policies, req)
}

func routingPoliciesConsumeHiddenModel(policies []model.RoutingPolicy, req *model.SelectRequest) bool {
	if hasUsableRequestModel(req) {
		return false
	}
	apiType := reqAPIType(req)
	if apiType == "" {
		return false
	}
	for i := range policies {
		policy := &policies[i]
		if strings.TrimSpace(policy.APIType) != apiType {
			continue
		}
		if !routingPolicyIsActive(policy) {
			continue
		}
		if routingPolicyConsumesModel(policy) {
			return true
		}
	}
	return false
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
	hasActivePolicy := false

	for i := range policies {
		policy := &policies[i]
		if strings.TrimSpace(policy.APIType) != reqAPIType(req) {
			continue
		}
		if !routingPolicyIsActive(policy) {
			continue
		}
		hasActivePolicy = true
		if !requestModelKnown && routingPolicyConsumesModel(policy) {
			// Missing request models must not trigger speculative probing for routing.
			// Model-specific rules simply do not participate until the request already
			// carries a usable model.
			continue
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
		if !hasActivePolicy {
			return routingPolicyResolution{}
		}
		if !requestModelKnown {
			// When the request carries no usable model, model-specific active rules are
			// treated as unmatched rather than turning the API type into a fail-closed
			// routing domain.
			return routingPolicyResolution{}
		}
		// Once this API type is governed by routing policy, "no match" is still a
		// policy outcome: selection fails closed instead of widening back to every
		// provider for the API type.
		return routingPolicyResolution{constrained: true}
	}

	selected := policies[bestIndex]
	if targetProviderID := routingPolicyTargetProviderID(&selected); targetProviderID != "" {
		return routingPolicyResolution{
			constrained:      true,
			matched:          true,
			targetProviderID: targetProviderID,
		}
	}
	return routingPolicyResolution{
		constrained: true,
		matched:     true,
		groupIDs:    buildRoutingPolicyGroupSet(selected.Groups),
		vendors:     buildRoutingPolicyVendorSet(selected.Vendors),
	}
}

func routingPolicyIsActive(policy *model.RoutingPolicy) bool {
	return policy != nil && policy.Enabled
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

func routingPolicyTargetProviderID(policy *model.RoutingPolicy) string {
	if policy == nil || policy.TargetProviderID == nil {
		return ""
	}
	return strings.TrimSpace(*policy.TargetProviderID)
}

func routingPolicyRank(policy *model.RoutingPolicy, requestModel string) (rank int, prefixLen int, matched bool) {
	if policy == nil {
		return 0, 0, false
	}

	matchValue := strings.TrimSpace(policy.ModelMatchValue)
	switch policy.ModelMatchType {
	case model.RoutingPolicyModelMatchTypeNone:
		return routingPolicyRankAPIType, 0, matchValue == ""
	case model.RoutingPolicyModelMatchTypeExact:
		if requestModel == "" || matchValue == "" {
			return 0, 0, false
		}
		return routingPolicyRankExact, len(matchValue), requestModel == matchValue
	case model.RoutingPolicyModelMatchTypePrefix:
		if requestModel == "" || matchValue == "" {
			return 0, 0, false
		}
		return routingPolicyRankPrefix, len(matchValue), strings.HasPrefix(requestModel, matchValue)
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
	if r.targetProviderID != "" {
		return strings.TrimSpace(provider.ID) == r.targetProviderID
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

func reqSwitchMode(req *model.SelectRequest) model.SwitchMode {
	if req == nil {
		return model.SwitchModeInitial
	}
	return req.EffectiveSwitchMode()
}

func reqProviderSwitchHistory(req *model.SelectRequest) *model.ProviderSwitchHistory {
	if req == nil {
		return nil
	}
	return req.EffectiveProviderSwitchHistory()
}

func reqProviderContinuityContext(req *model.SelectRequest) *model.ProviderContinuityContext {
	if req == nil {
		return nil
	}
	return req.EffectiveProviderContinuityContext()
}

func reqVisibleContinuitySeedCandidate(req *model.SelectRequest) *model.VisibleContinuitySeedCandidate {
	if req == nil {
		return nil
	}
	return req.EffectiveVisibleContinuitySeedCandidate()
}

func (e *ProviderSelectionEligibility) reqMaxProviderSwitches() int {
	if e == nil || e.req == nil {
		return 0
	}
	return e.req.EffectiveMaxProviderSwitches()
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
