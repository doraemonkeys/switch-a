package admin

import (
	"fmt"
	"sort"
	"strings"
)

var supportedConfigImportModes = []ConfigImportMode{
	ConfigImportModeFull,
	ConfigImportModeSettingsOnly,
	ConfigImportModeSelection,
}

type resolvedConfigImport struct {
	Scope              resolvedConfigImportScope
	Providers          []ExportedProvider
	CredentialSessions []ExportedCredentialSession
	Groups             []ExportedGroup
	RoutingPolicies    []ExportedRoutingPolicy
	Settings           map[string]string
	InternalErrorRules []ExportedInternalErrorRule
	RuleProviderIDs    []string
	CanStage           bool
}

type resolvedConfigImportScope struct {
	Mode        ConfigImportMode
	GroupIDs    []string
	ProviderIDs []string
}

func resolveImportConfigRequest(req *ImportConfigRequest) (resolvedConfigImport, []string) {
	scope, warnings, canStage := normalizeConfigImportScope(req)
	resolved := resolvedConfigImport{
		Scope:    scope,
		CanStage: canStage,
	}
	if req == nil {
		return resolved, warnings
	}

	switch {
	case !canStage:
		return resolved, warnings
	case scope.Mode == ConfigImportModeFull:
		resolved.Providers = req.Providers
		resolved.CredentialSessions = req.CredentialSessions
		resolved.Groups = req.Groups
		resolved.RoutingPolicies = req.RoutingPolicies
		resolved.Settings = req.Settings
		resolved.InternalErrorRules = req.InternalErrorRules
		return resolved, warnings
	case scope.Mode == ConfigImportModeSettingsOnly:
		resolved.Settings = req.Settings
		return resolved, warnings
	default:
		return resolveSelectedImportConfigRequest(req, resolved, warnings)
	}
}

func normalizeConfigImportScope(
	req *ImportConfigRequest,
) (resolvedConfigImportScope, []string, bool) {
	scope := resolvedConfigImportScope{}
	if req == nil || req.ImportScope == nil {
		return scope, []string{missingConfigImportModeWarning()}, false
	}

	mode := ConfigImportMode(strings.TrimSpace(string(req.ImportScope.Mode)))
	if mode == "" {
		return scope, []string{missingConfigImportModeWarning()}, false
	}
	scope.Mode = mode

	switch mode {
	case ConfigImportModeFull, ConfigImportModeSettingsOnly:
		if req.ImportScope.Selection == nil {
			return scope, nil, true
		}
		return scope, []string{
			fmt.Sprintf(
				"Import scope mode %q does not allow selection",
				mode,
			),
		}, false
	case ConfigImportModeSelection:
		if req.ImportScope.Selection == nil {
			return scope, []string{`Import scope mode "selection" requires selection`}, false
		}
		scope.GroupIDs = normalizeConfigImportScopeIDs(req.ImportScope.Selection.GroupIDs)
		scope.ProviderIDs = normalizeConfigImportScopeIDs(req.ImportScope.Selection.ProviderIDs)
		if len(scope.GroupIDs) == 0 && len(scope.ProviderIDs) == 0 {
			return scope, []string{
				`Import scope mode "selection" requires at least one group_id or provider_id`,
			}, false
		}
		return scope, nil, true
	default:
		return scope, []string{unsupportedConfigImportModeWarning()}, false
	}
}

func missingConfigImportModeWarning() string {
	modeNames := make([]string, 0, len(supportedConfigImportModes))
	for _, mode := range supportedConfigImportModes {
		modeNames = append(modeNames, fmt.Sprintf("%q", mode))
	}
	return "Import scope mode is required and must be one of " + strings.Join(modeNames, ", ")
}

func unsupportedConfigImportModeWarning() string {
	modeNames := make([]string, 0, len(supportedConfigImportModes))
	for _, mode := range supportedConfigImportModes {
		modeNames = append(modeNames, fmt.Sprintf("%q", mode))
	}
	return "Import scope mode must be one of " + strings.Join(modeNames, ", ")
}

func normalizeConfigImportScopeIDs(ids []string) []string {
	if len(ids) == 0 {
		return nil
	}

	normalized := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, rawID := range ids {
		id := strings.TrimSpace(rawID)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		normalized = append(normalized, id)
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func resolveSelectedImportConfigRequest(
	req *ImportConfigRequest,
	resolved resolvedConfigImport,
	warnings []string,
) (resolvedConfigImport, []string) {
	selectedGroupIDs := buildConfigImportScopeIDSet(resolved.Scope.GroupIDs)
	selectedProviderIDs := buildConfigImportScopeIDSet(resolved.Scope.ProviderIDs)
	fileGroupIDs := buildExportedGroupIDSet(req.Groups)
	fileProviderIDs := buildExportedProviderIDSet(req.Providers)
	importGroupIDs := copyConfigImportScopeIDSet(selectedGroupIDs, len(selectedProviderIDs))

	for _, groupID := range resolved.Scope.GroupIDs {
		if _, exists := fileGroupIDs[groupID]; exists {
			continue
		}
		warnings = append(
			warnings,
			fmt.Sprintf("Selected group %q was not found in the import file", groupID),
		)
	}

	missingProviderGroupWarnings := make(map[string]struct{}, len(selectedProviderIDs))
	for _, provider := range req.Providers {
		providerID := strings.TrimSpace(provider.ID)
		if providerID == "" {
			continue
		}
		if _, selected := selectedProviderIDs[providerID]; !selected {
			continue
		}
		groupID := trimmedConfigImportGroupID(provider.GroupID)
		if groupID == "" {
			continue
		}
		// Provider dependencies must bring their parent group along for referential
		// integrity, but they must not widen provider selection to unchecked siblings.
		importGroupIDs[groupID] = struct{}{}
		if _, exists := fileGroupIDs[groupID]; exists {
			continue
		}
		warningKey := configImportProviderGroupRefKey(providerID, groupID)
		if _, warned := missingProviderGroupWarnings[warningKey]; warned {
			continue
		}
		missingProviderGroupWarnings[warningKey] = struct{}{}
		warnings = append(
			warnings,
			fmt.Sprintf(
				"Selected provider %q references group %q, but that group is missing from the import file",
				providerID,
				groupID,
			),
		)
	}

	for _, providerID := range resolved.Scope.ProviderIDs {
		if _, exists := fileProviderIDs[providerID]; exists {
			continue
		}
		warnings = append(
			warnings,
			fmt.Sprintf("Selected provider %q was not found in the import file", providerID),
		)
	}

	resolved.Groups = selectExportedGroupsForImport(req.Groups, importGroupIDs)
	resolved.Providers = selectExportedProvidersForImport(
		req.Providers,
		selectedProviderIDs,
		selectedGroupIDs,
	)
	resolved.CredentialSessions = selectExportedCredentialSessionsForImport(
		req.CredentialSessions,
		resolved.Providers,
	)
	resolved.InternalErrorRules = req.InternalErrorRules
	resolved.RuleProviderIDs = expandedRuleProviderIDs(req.Providers, selectedProviderIDs, selectedGroupIDs)
	return resolved, warnings
}

func selectExportedCredentialSessionsForImport(
	sessions []ExportedCredentialSession,
	providers []ExportedProvider,
) []ExportedCredentialSession {
	referenced := make(map[string]struct{})
	for _, provider := range providers {
		for _, apiType := range provider.APITypes {
			id := strings.TrimSpace(apiType.CredentialSessionID)
			if id != "" {
				referenced[id] = struct{}{}
			}
		}
	}
	selected := make([]ExportedCredentialSession, 0, len(referenced))
	for _, session := range sessions {
		if _, ok := referenced[strings.TrimSpace(session.ID)]; ok {
			selected = append(selected, session)
		}
	}
	return selected
}

func expandedRuleProviderIDs(
	providers []ExportedProvider,
	selectedProviderIDs map[string]struct{},
	selectedGroupIDs map[string]struct{},
) []string {
	expanded := copyConfigImportScopeIDSet(selectedProviderIDs, len(providers))
	for _, provider := range providers {
		if _, selected := selectedGroupIDs[trimmedConfigImportGroupID(provider.GroupID)]; selected {
			expanded[strings.TrimSpace(provider.ID)] = struct{}{}
		}
	}
	result := make([]string, 0, len(expanded))
	for id := range expanded {
		if id != "" {
			result = append(result, id)
		}
	}
	sort.Strings(result)
	return result
}

func buildConfigImportScopeIDSet(ids []string) map[string]struct{} {
	if len(ids) == 0 {
		return nil
	}
	result := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		result[id] = struct{}{}
	}
	return result
}

func copyConfigImportScopeIDSet(
	ids map[string]struct{},
	extraCapacity int,
) map[string]struct{} {
	if len(ids) == 0 && extraCapacity == 0 {
		return nil
	}
	cloned := make(map[string]struct{}, len(ids)+extraCapacity)
	for id := range ids {
		cloned[id] = struct{}{}
	}
	return cloned
}

func buildExportedGroupIDSet(groups []ExportedGroup) map[string]struct{} {
	if len(groups) == 0 {
		return nil
	}
	result := make(map[string]struct{}, len(groups))
	for _, group := range groups {
		groupID := strings.TrimSpace(group.ID)
		if groupID == "" {
			continue
		}
		result[groupID] = struct{}{}
	}
	return result
}

func buildExportedProviderIDSet(providers []ExportedProvider) map[string]struct{} {
	if len(providers) == 0 {
		return nil
	}
	result := make(map[string]struct{}, len(providers))
	for _, provider := range providers {
		providerID := strings.TrimSpace(provider.ID)
		if providerID == "" {
			continue
		}
		result[providerID] = struct{}{}
	}
	return result
}

func selectExportedGroupsForImport(
	groups []ExportedGroup,
	selectedGroupIDs map[string]struct{},
) []ExportedGroup {
	if len(groups) == 0 || len(selectedGroupIDs) == 0 {
		return nil
	}

	selected := make([]ExportedGroup, 0, len(groups))
	for _, group := range groups {
		groupID := strings.TrimSpace(group.ID)
		if _, include := selectedGroupIDs[groupID]; !include {
			continue
		}
		selected = append(selected, group)
	}
	if len(selected) == 0 {
		return nil
	}
	return selected
}

func selectExportedProvidersForImport(
	providers []ExportedProvider,
	selectedProviderIDs map[string]struct{},
	selectedGroupIDs map[string]struct{},
) []ExportedProvider {
	if len(providers) == 0 || (len(selectedProviderIDs) == 0 && len(selectedGroupIDs) == 0) {
		return nil
	}

	selected := make([]ExportedProvider, 0, len(providers))
	for _, provider := range providers {
		providerID := strings.TrimSpace(provider.ID)
		groupID := trimmedConfigImportGroupID(provider.GroupID)
		_, providerSelected := selectedProviderIDs[providerID]
		_, groupSelected := selectedGroupIDs[groupID]
		if !providerSelected && !groupSelected {
			continue
		}
		selected = append(selected, provider)
	}
	if len(selected) == 0 {
		return nil
	}
	return selected
}

func buildScopedMissingProviderGroupRefs(
	req *ImportConfigRequest,
	scope resolvedConfigImportScope,
) map[string]struct{} {
	if req == nil || len(scope.ProviderIDs) == 0 {
		return nil
	}

	selectedProviderIDs := buildConfigImportScopeIDSet(scope.ProviderIDs)
	fileGroupIDs := buildExportedGroupIDSet(req.Groups)
	missingRefs := make(map[string]struct{}, len(selectedProviderIDs))
	for _, provider := range req.Providers {
		providerID := strings.TrimSpace(provider.ID)
		if _, selected := selectedProviderIDs[providerID]; !selected {
			continue
		}
		groupID := trimmedConfigImportGroupID(provider.GroupID)
		if groupID == "" {
			continue
		}
		if _, exists := fileGroupIDs[groupID]; exists {
			continue
		}
		missingRefs[configImportProviderGroupRefKey(providerID, groupID)] = struct{}{}
	}
	if len(missingRefs) == 0 {
		return nil
	}
	return missingRefs
}

func trimmedConfigImportGroupID(groupID *string) string {
	if groupID == nil {
		return ""
	}
	return strings.TrimSpace(*groupID)
}

func configImportProviderGroupRefKey(providerID string, groupID string) string {
	return providerID + "\x00" + groupID
}
