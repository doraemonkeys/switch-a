package admin

import (
	"context"
	"fmt"
	"strings"

	"switch-a/internal/model"
)

type routingPolicyCatalog struct {
	groupsByID       map[string]*model.Group
	providersByID    map[string]*model.Provider
	vendorsByAPIType map[string]map[string]struct{}
}

type routingPolicySpec struct {
	APIType             string
	ModelMatchType      *model.RoutingPolicyModelMatchType
	ModelMatchValue     *string
	Enabled             *bool
	TargetProviderID    *string
	TargetProviderIDSet bool
	AllowedGroupIDs     []string
	AllowedVendors      []string
}

type normalizedRoutingPolicySpec struct {
	apiType          string
	modelMatchType   model.RoutingPolicyModelMatchType
	modelMatchValue  string
	enabled          bool
	targetProviderID *string
	allowedGroupIDs  []string
	allowedVendors   []string
}

func newRoutingPolicyCatalog(groups []model.Group, providers []model.Provider) routingPolicyCatalog {
	catalog := routingPolicyCatalog{
		groupsByID:       make(map[string]*model.Group, len(groups)),
		providersByID:    make(map[string]*model.Provider, len(providers)),
		vendorsByAPIType: make(map[string]map[string]struct{}),
	}
	for i := range groups {
		catalog.groupsByID[groups[i].ID] = &groups[i]
	}
	for i := range providers {
		catalog.providersByID[providers[i].ID] = &providers[i]
		for _, apiType := range providers[i].APITypes {
			vendor := strings.TrimSpace(providers[i].Vendor)
			if vendor == "" {
				continue
			}
			if catalog.vendorsByAPIType[apiType.APIType] == nil {
				catalog.vendorsByAPIType[apiType.APIType] = make(map[string]struct{})
			}
			catalog.vendorsByAPIType[apiType.APIType][vendor] = struct{}{}
		}
	}
	return catalog
}

func newRoutingPolicyCatalogFromMaps(
	groups map[string]*model.Group,
	providers map[string]*model.Provider,
) routingPolicyCatalog {
	groupSlice := make([]model.Group, 0, len(groups))
	for _, group := range groups {
		if group == nil {
			continue
		}
		groupSlice = append(groupSlice, *group)
	}
	providerSlice := make([]model.Provider, 0, len(providers))
	for _, provider := range providers {
		if provider == nil {
			continue
		}
		providerSlice = append(providerSlice, *provider)
	}
	return newRoutingPolicyCatalog(groupSlice, providerSlice)
}

func (h *Handler) loadRoutingPolicyCatalog(ctx context.Context) (routingPolicyCatalog, error) {
	groups, err := h.store.ListGroups(ctx)
	if err != nil {
		return routingPolicyCatalog{}, fmt.Errorf("list groups for routing policy catalog: %w", err)
	}
	providers, err := h.store.ListProviders(ctx)
	if err != nil {
		return routingPolicyCatalog{}, fmt.Errorf("list providers for routing policy catalog: %w", err)
	}
	return newRoutingPolicyCatalog(groups, providers), nil
}

func buildRoutingPolicyFromCatalog(
	spec routingPolicySpec,
	catalog routingPolicyCatalog,
	current *model.RoutingPolicy,
) (*model.RoutingPolicy, error) {
	normalized, err := normalizeRoutingPolicySpec(spec, current)
	if err != nil {
		return nil, err
	}
	if normalized.targetProviderID != nil {
		return buildExactProviderRoutingPolicy(normalized, catalog)
	}
	return buildFilterRoutingPolicy(normalized, catalog, current)
}

func normalizeRoutingPolicySpec(
	spec routingPolicySpec,
	current *model.RoutingPolicy,
) (normalizedRoutingPolicySpec, error) {
	apiType := strings.TrimSpace(spec.APIType)
	if apiType == "" {
		return normalizedRoutingPolicySpec{}, invalidRoutingPolicy("API type is required")
	}
	if !IsValidAPIType(apiType) {
		return normalizedRoutingPolicySpec{}, invalidRoutingPolicy("Invalid API type")
	}

	modelMatchType, modelMatchValue, err := normalizeRoutingPolicyMatch(spec)
	if err != nil {
		return normalizedRoutingPolicySpec{}, err
	}

	return normalizedRoutingPolicySpec{
		apiType:          apiType,
		modelMatchType:   modelMatchType,
		modelMatchValue:  modelMatchValue,
		enabled:          resolveRoutingPolicyEnabled(spec, current),
		targetProviderID: resolveRoutingPolicyTargetProviderID(spec, current),
		allowedGroupIDs:  normalizeRoutingPolicyStrings(spec.AllowedGroupIDs),
		allowedVendors:   normalizeRoutingPolicyStrings(spec.AllowedVendors),
	}, nil
}

func normalizeRoutingPolicyMatch(
	spec routingPolicySpec,
) (model.RoutingPolicyModelMatchType, string, error) {
	modelMatchType := model.RoutingPolicyModelMatchTypeNone
	if spec.ModelMatchType != nil {
		modelMatchType = model.RoutingPolicyModelMatchType(strings.TrimSpace(string(*spec.ModelMatchType)))
	}
	if !model.IsValidRoutingPolicyModelMatchType(modelMatchType) {
		return model.RoutingPolicyModelMatchTypeNone, "", invalidRoutingPolicy("Invalid model match type")
	}

	modelMatchValue := ""
	if spec.ModelMatchValue != nil {
		modelMatchValue = strings.TrimSpace(*spec.ModelMatchValue)
	}
	switch {
	case modelMatchType == model.RoutingPolicyModelMatchTypeNone && modelMatchValue != "":
		return model.RoutingPolicyModelMatchTypeNone, "", invalidRoutingPolicy("Model match value requires a model match type")
	case modelMatchType != model.RoutingPolicyModelMatchTypeNone && modelMatchValue == "":
		return model.RoutingPolicyModelMatchTypeNone, "", invalidRoutingPolicy("Model match value is required when model match type is set")
	default:
		return modelMatchType, modelMatchValue, nil
	}
}

func resolveRoutingPolicyEnabled(spec routingPolicySpec, current *model.RoutingPolicy) bool {
	enabled := true
	if current != nil {
		enabled = current.Enabled
	}
	if spec.Enabled != nil {
		enabled = *spec.Enabled
	}
	return enabled
}

func resolveRoutingPolicyTargetProviderID(
	spec routingPolicySpec,
	current *model.RoutingPolicy,
) *string {
	targetProviderID := currentTargetProviderID(current)
	if spec.TargetProviderIDSet {
		return normalizeRoutingPolicyTargetProviderID(spec.TargetProviderID)
	}
	if routingPolicySpecSelectsFilterMode(
		normalizeRoutingPolicyStrings(spec.AllowedGroupIDs),
		normalizeRoutingPolicyStrings(spec.AllowedVendors),
	) {
		// Config export omits null target_provider_id, and some update callers switch
		// modes by sending only filter scope. Once filter scope is present, preserving
		// the previous exact target would violate the atomic mode contract.
		return nil
	}
	return targetProviderID
}

func buildExactProviderRoutingPolicy(
	spec normalizedRoutingPolicySpec,
	catalog routingPolicyCatalog,
) (*model.RoutingPolicy, error) {
	if len(spec.allowedGroupIDs) > 0 || len(spec.allowedVendors) > 0 {
		return nil, invalidRoutingPolicy("Target provider cannot be combined with allowed groups or vendors")
	}
	provider, ok := catalog.providersByID[*spec.targetProviderID]
	if !ok {
		return nil, invalidRoutingPolicy("Target provider not found: " + *spec.targetProviderID)
	}
	if _, ok := provider.APITypeConfig(spec.apiType); !ok {
		return nil, invalidRoutingPolicy("Target provider does not support api_type: " + spec.apiType)
	}
	return &model.RoutingPolicy{
		APIType:          spec.apiType,
		ModelMatchType:   spec.modelMatchType,
		ModelMatchValue:  spec.modelMatchValue,
		Enabled:          spec.enabled,
		TargetProviderID: spec.targetProviderID,
	}, nil
}

func buildFilterRoutingPolicy(
	spec normalizedRoutingPolicySpec,
	catalog routingPolicyCatalog,
	current *model.RoutingPolicy,
) (*model.RoutingPolicy, error) {
	if len(spec.allowedGroupIDs) == 0 && len(spec.allowedVendors) == 0 {
		return nil, invalidRoutingPolicy("At least one allowed group or vendor is required")
	}
	if err := validateRoutingPolicyGroups(spec.allowedGroupIDs, catalog); err != nil {
		return nil, err
	}
	if err := validateRoutingPolicyVendors(spec, catalog, current); err != nil {
		return nil, err
	}
	return newFilterRoutingPolicy(spec), nil
}

func validateRoutingPolicyGroups(
	allowedGroupIDs []string,
	catalog routingPolicyCatalog,
) error {
	for _, groupID := range allowedGroupIDs {
		if _, ok := catalog.groupsByID[groupID]; ok {
			continue
		}
		return invalidRoutingPolicy("Group not found: " + groupID)
	}
	return nil
}

func validateRoutingPolicyVendors(
	spec normalizedRoutingPolicySpec,
	catalog routingPolicyCatalog,
	current *model.RoutingPolicy,
) error {
	if routingPolicyVendorSetPreserved(current, spec.apiType, spec.allowedVendors, spec.targetProviderID) {
		return nil
	}
	vendorsForAPIType := catalog.vendorsByAPIType[spec.apiType]
	for _, vendor := range spec.allowedVendors {
		if _, ok := vendorsForAPIType[vendor]; ok {
			continue
		}
		return invalidRoutingPolicy("Vendor not available for api_type " + spec.apiType + ": " + vendor)
	}
	return nil
}

func newFilterRoutingPolicy(spec normalizedRoutingPolicySpec) *model.RoutingPolicy {
	policy := &model.RoutingPolicy{
		APIType:         spec.apiType,
		ModelMatchType:  spec.modelMatchType,
		ModelMatchValue: spec.modelMatchValue,
		Enabled:         spec.enabled,
		Groups:          make([]model.RoutingPolicyGroup, 0, len(spec.allowedGroupIDs)),
		Vendors:         make([]model.RoutingPolicyVendor, 0, len(spec.allowedVendors)),
	}
	for _, groupID := range spec.allowedGroupIDs {
		policy.Groups = append(policy.Groups, model.RoutingPolicyGroup{GroupID: groupID})
	}
	for _, vendor := range spec.allowedVendors {
		policy.Vendors = append(policy.Vendors, model.RoutingPolicyVendor{Vendor: vendor})
	}
	return policy
}

func currentTargetProviderID(current *model.RoutingPolicy) *string {
	if current == nil {
		return nil
	}
	return normalizeRoutingPolicyTargetProviderID(current.TargetProviderID)
}

func normalizeRoutingPolicyTargetProviderID(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func routingPolicySpecSelectsFilterMode(
	allowedGroupIDs []string,
	allowedVendors []string,
) bool {
	return len(allowedGroupIDs) > 0 || len(allowedVendors) > 0
}

func routingPolicyVendorSetPreserved(
	current *model.RoutingPolicy,
	apiType string,
	requested []string,
	targetProviderID *string,
) bool {
	if current == nil || currentTargetProviderID(current) != nil || targetProviderID != nil {
		return false
	}
	if strings.TrimSpace(current.APIType) != apiType {
		return false
	}
	existing := make([]string, 0, len(current.Vendors))
	for _, vendor := range current.Vendors {
		existing = append(existing, vendor.Vendor)
	}
	existing = normalizeRoutingPolicyStrings(existing)
	if len(existing) != len(requested) {
		return false
	}
	for i := range existing {
		if existing[i] != requested[i] {
			return false
		}
	}
	return true
}
