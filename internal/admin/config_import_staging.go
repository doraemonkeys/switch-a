package admin

import (
	"fmt"
	"maps"
	"reflect"
	"sort"
	"strings"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/errorrule"
	errorrulesqlite "github.com/doraemonkeys/switch-a/internal/errorrule/sqlite"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/store"
)

type stagedConfigImport struct {
	bundle                       store.ConfigImportBundle
	mode                         ConfigImportMode
	changes                      ImportChanges
	warnings                     []string
	reauthenticationRequirements []CredentialReauthenticationRequirement
	previewRejectsWarning        bool
	ruleError                    error
}

func stageConfigImport(
	req *ImportConfigRequest,
	existingProviders map[string]*model.Provider,
	existingCredentialSessions map[string]credentialsession.Snapshot,
	existingGroups map[string]*model.Group,
	existingRoutingPolicies map[model.RoutingPolicyNaturalKey]*model.RoutingPolicy,
	existingSettings map[string]string,
	existingRules []errorrule.Rule,
) stagedConfigImport {
	resolved, scopeWarnings := resolveImportConfigRequest(req)
	staged := stagedConfigImport{
		warnings:              append([]string{}, scopeWarnings...),
		mode:                  resolved.Scope.Mode,
		previewRejectsWarning: !resolved.CanStage || resolved.Scope.Mode != ConfigImportModeFull,
	}
	if !resolved.CanStage {
		return staged
	}

	finalGroups := stageImportedGroups(&staged, resolved.Groups, existingGroups)
	validGroups := buildValidGroupsMap(resolved.Groups, existingGroups)
	finalProviders := stageImportedProviders(&staged, resolved.Providers, existingProviders, validGroups)
	declaredCredentialSessions := stageImportedCredentialSessions(
		&staged,
		resolved.CredentialSessions,
		existingCredentialSessions,
	)
	validationRequest := &ImportConfigRequest{
		Providers:          resolved.Providers,
		CredentialSessions: resolved.CredentialSessions,
		Groups:             resolved.Groups,
		Settings:           resolved.Settings,
	}
	staged.warnings = append(
		staged.warnings,
		validateImportRequest(
			validationRequest,
			existingGroups,
			buildScopedMissingProviderGroupRefs(req, resolved.Scope),
			declaredCredentialSessions,
		)...,
	)
	stageImportedRoutingPolicies(
		&staged,
		resolved,
		existingRoutingPolicies,
		finalGroups,
		finalProviders,
	)
	if req.CodexState != nil && resolved.Scope.Mode != ConfigImportModeSettingsOnly {
		staged.bundle.CodexState = req.CodexState
		if resolved.Scope.Mode == ConfigImportModeSelection {
			providerIDs, sessionIDs := []string{}, []string{}
			for _, provider := range resolved.Providers {
				providerIDs = append(providerIDs, provider.ID)
				for _, api := range provider.APITypes {
					sessionIDs = append(sessionIDs, api.CredentialSessionID)
				}
			}
			staged.bundle.CodexState = req.CodexState.Select(providerIDs, sessionIDs)
		}
		staged.changes.CodexState.Update = 1
	}
	stageImportedSettings(&staged, resolved.Settings, existingSettings)
	stageImportedInternalErrorRules(&staged, resolved, existingRules, finalProviders)
	return staged
}

func stageImportedCredentialSessions(
	staged *stagedConfigImport,
	exported []ExportedCredentialSession,
	existing map[string]credentialsession.Snapshot,
) map[string]struct{} {
	declared := make(map[string]struct{}, len(exported))
	seen := make(map[string]struct{}, len(exported))
	for index := range exported {
		item := exported[index]
		id := strings.TrimSpace(item.ID)
		if _, duplicate := seen[id]; duplicate {
			staged.warnings = append(staged.warnings, "Duplicate credential session ID in import: "+id)
			continue
		}
		seen[id] = struct{}{}

		if item.Kind == credentialsession.KindChatGPT {
			if err := stageChatGPTReauthenticationDescriptor(staged, item, existing); err != nil {
				staged.warnings = append(staged.warnings, err.Error())
				continue
			}
			declared[id] = struct{}{}
			continue
		}

		session, err := buildStaticCredentialSessionFromExport(item)
		if err != nil {
			staged.warnings = append(staged.warnings, fmt.Sprintf("Invalid credential session %q: %v", item.ID, err))
			continue
		}
		declared[session.ID] = struct{}{}
		current, found := existing[session.ID]
		differs := found && !credentialSessionImportEqual(*session, current)
		if recordStagedUpsert(&staged.changes.CredentialSessions, found, differs) {
			staged.bundle.CredentialSessions = append(staged.bundle.CredentialSessions, *session)
		}
	}
	return declared
}

func stageChatGPTReauthenticationDescriptor(
	staged *stagedConfigImport,
	item ExportedCredentialSession,
	existing map[string]credentialsession.Snapshot,
) error {
	id := strings.TrimSpace(item.ID)
	placeholder, err := buildChatGPTReauthenticationPlaceholder(item)
	if err != nil {
		return fmt.Errorf(
			"credential session %q cannot import a ChatGPT reauthentication descriptor: %w",
			id,
			err,
		)
	}
	current, found := existing[id]
	if !found {
		staged.changes.CredentialSessions.Add++
		staged.bundle.CredentialSessions = append(staged.bundle.CredentialSessions, *placeholder)
		recordCredentialReauthenticationRequirement(staged, placeholder.ID, placeholder.Name)
		return nil
	}
	if current.Kind != credentialsession.KindChatGPT {
		return fmt.Errorf(
			"credential session %q reauthentication descriptor does not match the existing ChatGPT session",
			id,
		)
	}
	nameDiffers := placeholder.Name != "" && placeholder.Name != current.Name
	if recordStagedUpsert(&staged.changes.CredentialSessions, true, nameDiffers) {
		staged.bundle.CredentialSessions = append(staged.bundle.CredentialSessions, *placeholder)
	}
	if current.AuthState.Status != credentialsession.AuthStatusActive ||
		!current.Subject.Resolved() || !current.HasCredentialMaterial() {
		requirementName := current.Name
		if placeholder.Name != "" {
			requirementName = placeholder.Name
		}
		recordCredentialReauthenticationRequirement(staged, id, requirementName)
	}
	return nil
}

func recordCredentialReauthenticationRequirement(staged *stagedConfigImport, sessionID, name string) {
	staged.reauthenticationRequirements = append(
		staged.reauthenticationRequirements,
		CredentialReauthenticationRequirement{
			CredentialSessionID: sessionID,
			Name:                name,
		},
	)
}

func credentialSessionImportEqual(session credentialsession.Session, current credentialsession.Snapshot) bool {
	return session.ID == current.SessionID &&
		session.Kind == current.Kind &&
		session.SecretData == current.SecretData &&
		session.Version == current.Version &&
		reflect.DeepEqual(session.Subject(), current.Subject) &&
		reflect.DeepEqual(session.AuthState, current.AuthState)
}

func stageImportedInternalErrorRules(
	staged *stagedConfigImport,
	resolved resolvedConfigImport,
	existing []errorrule.Rule,
	providers map[string]*model.Provider,
) {
	request := errorrulesqlite.ImportRequest{Rules: make([]errorrulesqlite.ImportedRule, len(resolved.InternalErrorRules))}
	for index, exported := range resolved.InternalErrorRules {
		request.Rules[index] = errorrulesqlite.ImportedRule{ID: exported.ID, RuleSpec: exported.RuleSpec}
	}
	switch resolved.Scope.Mode {
	case ConfigImportModeFull:
		request.Mode = errorrulesqlite.ImportModeFull
	case ConfigImportModeSelection:
		request.Mode = errorrulesqlite.ImportModeSelection
		request.SelectedProviderIDs = append([]string(nil), resolved.RuleProviderIDs...)
	case ConfigImportModeSettingsOnly:
		request.Mode = errorrulesqlite.ImportModePreserve
	default:
		return
	}
	staged.bundle.RuleImport = request
	candidate, counts, err := errorrulesqlite.BuildImportCandidate(existing, request)
	if err == nil {
		err = validateImportedRuleProviders(candidate, providers)
	}
	if err != nil {
		staged.ruleError = err
		staged.warnings = append(staged.warnings, err.Error())
		return
	}
	staged.changes.InternalErrorRules = ChangeCount{
		Add:       counts.Add,
		Update:    counts.Update,
		Delete:    counts.Delete,
		Unchanged: counts.Unchanged,
	}
}

func validateImportedRuleProviders(rules []errorrule.Rule, providers map[string]*model.Provider) error {
	for _, rule := range rules {
		providerID, scoped := rule.Target.ProviderID()
		if !scoped {
			continue
		}
		if _, exists := providers[string(providerID)]; !exists {
			return fmt.Errorf("internal-error rule %q references missing provider %q", rule.ID, providerID)
		}
	}
	return nil
}

// Scoped preview must distinguish "selected but unchanged" from "not staged at all",
// otherwise operators cannot verify that the resolved import scope matches their selection.
func recordStagedUpsert(changeCount *ChangeCount, exists bool, differs bool) bool {
	switch {
	case !exists:
		changeCount.Add++
		return true
	case differs:
		changeCount.Update++
		return true
	default:
		changeCount.Unchanged++
		return false
	}
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
		existing, exists := existingGroups[group.ID]
		differs := exists && groupImportDiffers(&exported, existing)
		if recordStagedUpsert(&staged.changes.Groups, exists, differs) {
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
		existing, exists := existingProviders[provider.ID]
		differs := exists && providerImportDiffers(&exported, existing, validGroups)
		if recordStagedUpsert(&staged.changes.Providers, exists, differs) {
			staged.bundle.Providers = append(staged.bundle.Providers, *provider)
		}
		finalProviders[provider.ID] = provider
	}
	return finalProviders
}

func stageImportedRoutingPolicies(
	staged *stagedConfigImport,
	resolved resolvedConfigImport,
	existingRoutingPolicies map[model.RoutingPolicyNaturalKey]*model.RoutingPolicy,
	finalGroups map[string]*model.Group,
	finalProviders map[string]*model.Provider,
) {
	if resolved.Scope.Mode != ConfigImportModeFull {
		staged.bundle.RoutingPolicyMode = store.ConfigImportRoutingPolicyModePreserve
		finalRoutingPolicies := cloneRoutingPolicyMap(existingRoutingPolicies)
		staged.warnings = append(
			staged.warnings,
			validateStagedExactProviderPolicies(finalRoutingPolicies, finalProviders)...,
		)
		return
	}

	staged.bundle.RoutingPolicyMode = store.ConfigImportRoutingPolicyModeReplace
	exportedPolicies := resolved.RoutingPolicies
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
		recordStagedUpsert(
			&staged.changes.RoutingPolicies,
			current != nil,
			current != nil && routingPolicyImportDiffers(policy, current),
		)
	}

	stageDeletedRoutingPolicies(&staged.changes, existingRoutingPolicies, seenRoutingKeys, finalRoutingPolicies)
	staged.bundle.RoutingPolicies = collectStagedRoutingPolicies(importedRoutingPolicies)
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
		existingValue, exists := comparableExistingSettings[key]
		if !recordStagedUpsert(&staged.changes.Settings, exists, exists && existingValue != value) {
			continue
		}
		staged.bundle.Settings[key] = value
	}
}

func cloneGroupMap(groups map[string]*model.Group, extraCapacity int) map[string]*model.Group {
	cloned := make(map[string]*model.Group, len(groups)+extraCapacity)
	maps.Copy(cloned, groups)
	return cloned
}

func cloneProviderMap(providers map[string]*model.Provider, extraCapacity int) map[string]*model.Provider {
	cloned := make(map[string]*model.Provider, len(providers)+extraCapacity)
	maps.Copy(cloned, providers)
	return cloned
}

func cloneRoutingPolicyMap(
	policies map[model.RoutingPolicyNaturalKey]*model.RoutingPolicy,
) map[model.RoutingPolicyNaturalKey]*model.RoutingPolicy {
	cloned := make(map[model.RoutingPolicyNaturalKey]*model.RoutingPolicy, len(policies))
	maps.Copy(cloned, policies)
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

func collectStagedRoutingPolicies(
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
