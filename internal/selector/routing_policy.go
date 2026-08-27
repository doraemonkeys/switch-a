package selector

import (
	"context"
	"strings"

	"github.com/doraemonkeys/switch-a/internal/model"
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

type routingPolicyResolution struct {
	constrained      bool
	matched          bool
	targetProviderID string
	groupIDs         map[string]struct{}
	vendors          map[string]struct{}
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

	for i := range policies {
		policy := &policies[i]
		if strings.TrimSpace(policy.APIType) != reqAPIType(req) {
			continue
		}
		if !routingPolicyIsActive(policy) {
			continue
		}
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
		// Rules are selective overrides, not declarations that the whole API type is
		// closed. A model-scoped rule therefore cannot constrain a different model;
		// callers retain normal provider selection unless an API-wide fallback rule
		// or another model-specific rule actually matches.
		return routingPolicyResolution{}
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
