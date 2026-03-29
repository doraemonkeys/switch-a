package admin

import (
	"fmt"
	"sort"
	"strings"

	"switch-a/internal/model"
	"switch-a/internal/store"
)

type stagedConfigImport struct {
	bundle   store.ConfigImportBundle
	changes  ImportChanges
	warnings []string
}

func stageConfigImport(
	req *ImportConfigRequest,
	existingProviders map[string]*model.Provider,
	existingGroups map[string]*model.Group,
	existingRoutingPolicies map[model.RoutingPolicyNaturalKey]*model.RoutingPolicy,
	existingSettings map[string]string,
) stagedConfigImport {
	staged := stagedConfigImport{
		warnings: validateImportRequest(req, existingGroups),
	}

	finalGroups := stageImportedGroups(&staged, req.Groups, existingGroups)
	validGroups := buildValidGroupsMap(req.Groups, existingGroups)
	finalProviders := stageImportedProviders(&staged, req.Providers, existingProviders, validGroups)
	stageImportedRoutingPolicies(
		&staged,
		req.RoutingPolicies,
		existingRoutingPolicies,
		finalGroups,
		finalProviders,
	)
	stageImportedSettings(&staged, req.Settings, existingSettings)
	return staged
}

func stageImportedGroups(
	staged *stagedConfigImport,
	exportedGroups []ExportedGroup,
	existingGroups map[string]*model.Group,
) map[string]*model.Group {
	finalGroups := cloneGroupMap(existingGroups, len(exportedGroups))
	seenGroupIDs := make(map[string]struct{}, len(exportedGroups))
	for _, exported := range exportedGroups {
		groupID := strings.TrimSpace(exported.ID)
		if duplicateImportID(seenGroupIDs, groupID) {
			staged.warnings = append(staged.warnings, "Duplicate group ID in import: "+groupID)
			continue
		}

		group, ok := buildGroupFromExport(&exported)
		if !ok {
			continue
		}
		if existing, exists := existingGroups[group.ID]; exists {
			if groupImportDiffers(&exported, existing) {
				staged.changes.Groups.Update++
				staged.bundle.Groups = append(staged.bundle.Groups, *group)
			}
		} else {
			staged.changes.Groups.Add++
			staged.bundle.Groups = append(staged.bundle.Groups, *group)
		}
		finalGroups[group.ID] = group
	}
	return finalGroups
}

func stageImportedProviders(
	staged *stagedConfigImport,
	exportedProviders []ExportedProvider,
	existingProviders map[string]*model.Provider,
	validGroups map[string]bool,
) map[string]*model.Provider {
	finalProviders := cloneProviderMap(existingProviders, len(exportedProviders))
	seenProviderIDs := make(map[string]struct{}, len(exportedProviders))
	for _, exported := range exportedProviders {
		providerID := strings.TrimSpace(exported.ID)
		if duplicateImportID(seenProviderIDs, providerID) {
			staged.warnings = append(staged.warnings, "Duplicate provider ID in import: "+providerID)
			continue
		}

		provider, ok := buildProviderFromExport(&exported, validGroups)
		if !ok {
			continue
		}
		if existing, exists := existingProviders[provider.ID]; exists {
			if providerImportDiffers(&exported, existing, validGroups) {
				staged.changes.Providers.Update++
				staged.bundle.Providers = append(staged.bundle.Providers, *provider)
			}
		} else {
			staged.changes.Providers.Add++
			staged.bundle.Providers = append(staged.bundle.Providers, *provider)
		}
		finalProviders[provider.ID] = provider
	}
	return finalProviders
}

func stageImportedRoutingPolicies(
	staged *stagedConfigImport,
	exportedPolicies []ExportedRoutingPolicy,
	existingRoutingPolicies map[model.RoutingPolicyNaturalKey]*model.RoutingPolicy,
	finalGroups map[string]*model.Group,
	finalProviders map[string]*model.Provider,
) {
	routingCatalog := newRoutingPolicyCatalogFromMaps(finalGroups, finalProviders)
	finalRoutingPolicies := make(map[model.RoutingPolicyNaturalKey]*model.RoutingPolicy, len(exportedPolicies))
	importedRoutingPolicies := make(map[model.RoutingPolicyNaturalKey]*model.RoutingPolicy, len(exportedPolicies))
	seenRoutingKeys := make(map[model.RoutingPolicyNaturalKey]struct{}, len(exportedPolicies))
	for _, exported := range exportedPolicies {
		key := model.NewRoutingPolicyNaturalKey(exported.APIType, exported.ModelMatchType, exported.ModelMatchValue)
		if _, exists := seenRoutingKeys[key]; exists {
			staged.warnings = append(staged.warnings, "Duplicate routing policy natural key in import: "+routingPolicyImportLabel(key))
			continue
		}
		seenRoutingKeys[key] = struct{}{}

		current := existingRoutingPolicies[key]
		policy, err := buildImportedRoutingPolicy(exported, routingCatalog, current)
		if err != nil {
			staged.warnings = append(staged.warnings, err.Error())
			if current != nil {
				finalRoutingPolicies[key] = current
			}
			continue
		}

		importedRoutingPolicies[key] = policy
		finalRoutingPolicies[key] = policy
		if current != nil {
			if routingPolicyImportDiffers(policy, current) {
				staged.changes.RoutingPolicies.Update++
			}
			continue
		}
		staged.changes.RoutingPolicies.Add++
	}

	stageDeletedRoutingPolicies(&staged.changes, existingRoutingPolicies, seenRoutingKeys, finalRoutingPolicies)
	staged.bundle.RoutingPolicies = collectImportedRoutingPolicies(importedRoutingPolicies)
	staged.warnings = append(
		staged.warnings,
		validateStagedExactProviderPolicies(finalRoutingPolicies, finalProviders)...,
	)
}

func buildImportedRoutingPolicy(
	exported ExportedRoutingPolicy,
	routingCatalog routingPolicyCatalog,
	current *model.RoutingPolicy,
) (*model.RoutingPolicy, error) {
	return buildRoutingPolicyFromCatalog(routingPolicySpec{
		APIType:             exported.APIType,
		ModelMatchType:      routingPolicyMatchTypePtr(exported.ModelMatchType),
		ModelMatchValue:     routingPolicyMatchValuePtr(exported.ModelMatchValue),
		Enabled:             boolPtr(exported.Enabled),
		TargetProviderID:    exported.TargetProviderID,
		TargetProviderIDSet: true,
		AllowedGroupIDs:     exported.AllowedGroupIDs,
		AllowedVendors:      exported.AllowedVendors,
	}, routingCatalog, current)
}

func stageDeletedRoutingPolicies(
	changes *ImportChanges,
	existingRoutingPolicies map[model.RoutingPolicyNaturalKey]*model.RoutingPolicy,
	seenRoutingKeys map[model.RoutingPolicyNaturalKey]struct{},
	finalRoutingPolicies map[model.RoutingPolicyNaturalKey]*model.RoutingPolicy,
) {
	for key, current := range existingRoutingPolicies {
		if current == nil {
			continue
		}
		if _, exists := seenRoutingKeys[key]; exists {
			if _, stagedCurrent := finalRoutingPolicies[key]; !stagedCurrent {
				finalRoutingPolicies[key] = current
			}
			continue
		}
		changes.RoutingPolicies.Delete++
	}
}

func stageImportedSettings(
	staged *stagedConfigImport,
	importedSettings map[string]string,
	existingSettings map[string]string,
) {
	comparableExistingSettings := normalizeSupportedSettings(existingSettings)
	settingsToUpdate := normalizeSupportedSettings(importedSettings)
	staged.bundle.Settings = make(map[string]string, len(settingsToUpdate))
	for key, value := range settingsToUpdate {
		if existingValue, exists := comparableExistingSettings[key]; exists {
			if existingValue == value {
				continue
			}
			staged.changes.Settings.Update++
		} else {
			staged.changes.Settings.Add++
		}
		staged.bundle.Settings[key] = value
	}
}

func cloneGroupMap(groups map[string]*model.Group, extraCapacity int) map[string]*model.Group {
	cloned := make(map[string]*model.Group, len(groups)+extraCapacity)
	for id, group := range groups {
		cloned[id] = group
	}
	return cloned
}

func cloneProviderMap(providers map[string]*model.Provider, extraCapacity int) map[string]*model.Provider {
	cloned := make(map[string]*model.Provider, len(providers)+extraCapacity)
	for id, provider := range providers {
		cloned[id] = provider
	}
	return cloned
}

func duplicateImportID(seen map[string]struct{}, id string) bool {
	if id == "" {
		return false
	}
	if _, exists := seen[id]; exists {
		return true
	}
	seen[id] = struct{}{}
	return false
}

func validateStagedExactProviderPolicies(
	policies map[model.RoutingPolicyNaturalKey]*model.RoutingPolicy,
	providers map[string]*model.Provider,
) []string {
	if len(policies) == 0 {
		return nil
	}

	keys := make([]model.RoutingPolicyNaturalKey, 0, len(policies))
	for key, policy := range policies {
		if policy == nil || currentTargetProviderID(policy) == nil {
			continue
		}
		keys = append(keys, key)
	}
	sortRoutingPolicyNaturalKeys(keys)

	warnings := make([]string, 0, len(keys))
	for _, key := range keys {
		policy := policies[key]
		targetProviderID := currentTargetProviderID(policy)
		if targetProviderID == nil {
			continue
		}
		provider, exists := providers[*targetProviderID]
		if !exists {
			warnings = append(
				warnings,
				fmt.Sprintf(
					"Routing policy %s targets missing provider %q in the staged catalog",
					routingPolicyImportLabel(key),
					*targetProviderID,
				),
			)
			continue
		}
		if _, ok := provider.APITypeConfig(policy.APIType); ok {
			continue
		}
		warnings = append(
			warnings,
			fmt.Sprintf(
				"Routing policy %s targets provider %q, which does not support api_type %q in the staged catalog",
				routingPolicyImportLabel(key),
				*targetProviderID,
				policy.APIType,
			),
		)
	}

	return warnings
}

func collectImportedRoutingPolicies(
	policies map[model.RoutingPolicyNaturalKey]*model.RoutingPolicy,
) []model.RoutingPolicy {
	if len(policies) == 0 {
		return nil
	}

	keys := make([]model.RoutingPolicyNaturalKey, 0, len(policies))
	for key, policy := range policies {
		if policy == nil {
			continue
		}
		keys = append(keys, key)
	}
	sortRoutingPolicyNaturalKeys(keys)

	result := make([]model.RoutingPolicy, 0, len(keys))
	for _, key := range keys {
		policy := policies[key]
		if policy == nil {
			continue
		}
		result = append(result, *policy)
	}
	return result
}

func sortRoutingPolicyNaturalKeys(keys []model.RoutingPolicyNaturalKey) {
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].APIType != keys[j].APIType {
			return keys[i].APIType < keys[j].APIType
		}
		if keys[i].ModelMatchType != keys[j].ModelMatchType {
			return keys[i].ModelMatchType < keys[j].ModelMatchType
		}
		return keys[i].ModelMatchValue < keys[j].ModelMatchValue
	})
}

func routingPolicyImportLabel(key model.RoutingPolicyNaturalKey) string {
	if key.ModelMatchType == model.RoutingPolicyModelMatchTypeNone {
		return fmt.Sprintf("api_type=%q", key.APIType)
	}
	return fmt.Sprintf(
		"api_type=%q,%s=%q",
		key.APIType,
		key.ModelMatchType,
		key.ModelMatchValue,
	)
}

func routingPolicyMatchTypePtr(value model.RoutingPolicyModelMatchType) *model.RoutingPolicyModelMatchType {
	matchType := value
	return &matchType
}

func routingPolicyMatchValuePtr(value string) *string {
	matchValue := value
	return &matchValue
}

func boolPtr(value bool) *bool {
	boolean := value
	return &boolean
}
