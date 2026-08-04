package admin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/doraemonkeys/switch-a/internal/errorrule"
	errorrulesqlite "github.com/doraemonkeys/switch-a/internal/errorrule/sqlite"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/providerauth"
)

const (
	legacyStickyEnabledKey     = "sticky_enabled"
	configStickyModeKey        = "sticky_mode"
	legacyGlobalMaxAttemptsKey = "max_retries"
	configGlobalMaxAttemptsKey = "global_max_attempts"
)

const internalErrorRuleETagPrefix = "internal-error-rules/"

func formatInternalErrorRuleETag(revision errorrule.Revision) string {
	return `"` + internalErrorRuleETagPrefix + revision.String() + `"`
}

func parseInternalErrorRuleETag(raw string) (errorrule.Revision, error) {
	if raw == "" {
		return 0, fmt.Errorf("If-Match is required")
	}
	if strings.Contains(raw, ",") || strings.HasPrefix(raw, "W/") || raw == "*" ||
		len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		return 0, fmt.Errorf("If-Match must contain exactly one strong internal-error rule ETag")
	}
	payload := strings.TrimSuffix(strings.TrimPrefix(raw, `"`), `"`)
	if !strings.HasPrefix(payload, internalErrorRuleETagPrefix) {
		return 0, fmt.Errorf("If-Match is not an internal-error rule ETag")
	}
	revisionText := strings.TrimPrefix(payload, internalErrorRuleETagPrefix)
	revision, err := strconv.ParseInt(revisionText, 10, 64)
	if err != nil || revision < 0 || strconv.FormatInt(revision, 10) != revisionText {
		return 0, fmt.Errorf("If-Match contains an invalid rule revision")
	}
	return errorrule.Revision(revision), nil
}

func configRuleRepository(source any) *errorrulesqlite.Repository {
	provider, ok := source.(interface {
		InternalErrorRuleRepository() *errorrulesqlite.Repository
	})
	if !ok {
		return nil
	}
	return provider.InternalErrorRuleRepository()
}

type ConfigImportMode string

const (
	ConfigImportModeFull         ConfigImportMode = "full"
	ConfigImportModeSettingsOnly ConfigImportMode = "settings_only"
	ConfigImportModeSelection    ConfigImportMode = "selection"
)

type ConfigImportScope struct {
	Mode      ConfigImportMode       `json:"mode"`
	Selection *ConfigImportSelection `json:"selection,omitempty"`
}

type ConfigImportSelection struct {
	GroupIDs    []string `json:"group_ids"`
	ProviderIDs []string `json:"provider_ids"`
}

// ImportConfigRequest represents the request body for config import.
type ImportConfigRequest struct {
	Version            string                      `json:"version"`
	ImportScope        *ConfigImportScope          `json:"import_scope,omitempty"`
	Providers          []ExportedProvider          `json:"providers"`
	Groups             []ExportedGroup             `json:"groups"`
	RoutingPolicies    []ExportedRoutingPolicy     `json:"routing_policies"`
	Settings           map[string]string           `json:"settings"`
	InternalErrorRules []ExportedInternalErrorRule `json:"internal_error_rules"`
}

// ImportChanges represents the changes that will be applied during import.
type ImportChanges struct {
	Providers          ChangeCount `json:"providers"`
	Groups             ChangeCount `json:"groups"`
	RoutingPolicies    ChangeCount `json:"routing_policies"`
	Settings           ChangeCount `json:"settings"`
	InternalErrorRules ChangeCount `json:"internal_error_rules"`
}

// ChangeCount represents preview counts for imported records.
type ChangeCount struct {
	Add       int `json:"add"`
	Update    int `json:"update"`
	Delete    int `json:"delete"`
	Unchanged int `json:"unchanged"`
}

// ImportPreviewResponse is the response for dry_run=true.
type ImportPreviewResponse struct {
	DryRun          bool          `json:"dry_run"`
	Changes         ImportChanges `json:"changes"`
	Warnings        []string      `json:"warnings"`
	RuleSetRevision string        `json:"rule_set_revision"`
	RuleSetETag     string        `json:"rule_set_etag"`
}

// ImportResult represents the result of an actual import.
type ImportResult struct {
	Success         bool           `json:"success"`
	Applied         ImportedCounts `json:"applied"`
	RuleSetRevision string         `json:"rule_set_revision"`
	RuleSetETag     string         `json:"rule_set_etag"`
}

// ImportedCounts represents the counts of successfully imported items.
type ImportedCounts struct {
	Providers          AppliedCount `json:"providers"`
	Groups             AppliedCount `json:"groups"`
	RoutingPolicies    AppliedCount `json:"routing_policies"`
	Settings           AppliedCount `json:"settings"`
	InternalErrorRules AppliedCount `json:"internal_error_rules"`
}

// AppliedCount represents applied snapshot deltas after import.
type AppliedCount struct {
	Added   int `json:"added"`
	Updated int `json:"updated"`
	Deleted int `json:"deleted"`
}

// buildProviderFromExport builds a model.Provider from an ExportedProvider.
// Returns false if the provider is invalid and should be skipped.
func buildProviderFromExport(p *ExportedProvider, validGroups map[string]bool) (*model.Provider, bool) {
	if strings.TrimSpace(p.ID) == "" || strings.TrimSpace(p.Name) == "" {
		return nil, false
	}
	credentialType := model.NormalizeProviderCredentialType(p.CredentialType)
	if !IsValidProviderCredentialType(credentialType) {
		return nil, false
	}
	if !model.IsValidProviderUsageLimitPolicy(p.UsageLimitPolicy) {
		return nil, false
	}

	authMode, ok := buildProviderAuthModeFromExport(credentialType, p.AuthMode)
	if !ok {
		return nil, false
	}

	apiTypes, ok := buildProviderAPITypesFromExport(p.ID, credentialType, p.APITypes)
	if !ok {
		return nil, false
	}

	provider := &model.Provider{
		ID:               p.ID,
		Name:             p.Name,
		APIKey:           model.NormalizeAPIKey(p.APIKey),
		APITypes:         apiTypes,
		AuthMode:         authMode,
		CredentialType:   credentialType,
		UsageLimitPolicy: p.UsageLimitPolicy,
		GroupID:          buildProviderGroupIDFromExport(p.GroupID, validGroups),
		Weight:           normalizeProviderWeightFromExport(p.Weight),
		Priority:         p.Priority,
		Concurrency:      p.Concurrency,
		MaxRetries:       p.MaxRetries,
		Backoff: model.BackoffPolicy{
			InitialDelay: p.Backoff.InitialDelay,
			MaxDelay:     p.Backoff.MaxDelay,
			Multiplier:   p.Backoff.Multiplier,
			Jitter:       p.Backoff.Jitter,
		},
		Vendor:         p.Vendor,
		FailoverScope:  normalizeProviderScopeFromExport(p.FailoverScope),
		AcceptFailover: normalizeProviderScopeFromExport(p.AcceptFailover),
		Enabled:        p.Enabled,
	}
	provider.Credential = buildProviderCredentialFromExport(p, credentialType)
	provider.AuthState = buildProviderAuthStateFromExport(p, credentialType)
	providerauth.NormalizeProviderForPersistence(provider)
	if !validateImportedProvider(provider, p, credentialType) {
		return nil, false
	}
	return provider, true
}

func buildProviderAuthModeFromExport(
	credentialType model.ProviderCredentialType,
	exportedAuthMode string,
) (string, bool) {
	if exportedAuthMode == "" {
		return DefaultAuthMode, true
	}
	if credentialType != model.ProviderCredentialTypeChatGPT && !IsValidAuthMode(exportedAuthMode) {
		return "", false
	}
	return exportedAuthMode, true
}

func buildProviderAPITypesFromExport(
	providerID string,
	credentialType model.ProviderCredentialType,
	exportedAPITypes []ExportedAPIType,
) ([]model.ProviderAPIType, bool) {
	if credentialType == model.ProviderCredentialTypeChatGPT {
		return nil, true
	}

	apiTypes := make([]model.ProviderAPIType, 0, len(exportedAPITypes))
	for _, at := range exportedAPITypes {
		if !IsValidAPIType(at.APIType) || at.BaseURL == "" {
			continue
		}
		apiTypes = append(apiTypes, model.ProviderAPIType{
			ProviderID: providerID,
			APIType:    at.APIType,
			BaseURL:    at.BaseURL,
			APIKey:     model.NormalizeAPIKey(at.APIKey),
		})
	}

	return apiTypes, len(apiTypes) > 0
}

func buildProviderGroupIDFromExport(groupID *string, validGroups map[string]bool) *string {
	if groupID == nil || *groupID == "" || !validGroups[*groupID] {
		return nil
	}
	return groupID
}

func normalizeProviderWeightFromExport(weight int) int {
	if weight <= 0 {
		return DefaultWeight
	}
	return weight
}

func normalizeProviderScopeFromExport(exportedScope string) model.Scope {
	scope := model.Scope(exportedScope)
	if !model.IsValidScope(scope) {
		return model.ScopeAny
	}
	return scope
}

func validateImportedProvider(
	provider *model.Provider,
	exported *ExportedProvider,
	credentialType model.ProviderCredentialType,
) bool {
	if credentialType == model.ProviderCredentialTypeChatGPT {
		return validateImportedChatGPTProvider(provider, exported)
	}
	if errMsg := validateProviderAPITypeConfiguration(provider); errMsg != "" {
		return false
	}
	for _, at := range provider.APITypes {
		if !model.HasAPIKey(provider.APIKey) && !model.HasAPIKey(at.APIKey) {
			return false
		}
	}
	return true
}

func validateImportedChatGPTProvider(provider *model.Provider, exported *ExportedProvider) bool {
	if provider.AuthState == nil {
		return false
	}
	// Blank exported statuses are a legacy/manual-import shape. Preserve that
	// intent before normalization rewrites the empty value to not_connected.
	requiresReadyCredential := provider.AuthState.Status == model.ProviderAuthStatusActive ||
		chatGPTCredentialMustBeReady(exported)
	if !requiresReadyCredential {
		return true
	}
	credential, err := providerauth.DecodeProviderChatGPTCredential(provider)
	if err != nil || credential == nil {
		return false
	}
	return credential.Ready()
}

// buildGroupFromExport applies the same normalization that Create/Update paths use
// so preview and apply both compare against the persisted domain shape.
func buildGroupFromExport(g *ExportedGroup) (*model.Group, bool) {
	if g.ID == "" || g.Name == "" {
		return nil, false
	}

	strategy := g.Strategy
	if strategy == "" {
		strategy = DefaultStrategy
	} else if !IsValidStrategy(strategy) {
		return nil, false
	}

	if g.Priority == ReservedGroupPriority {
		return nil, false
	}

	weight := g.Weight
	if weight <= 0 {
		weight = DefaultWeight
	}

	return &model.Group{
		ID:       g.ID,
		Name:     g.Name,
		Strategy: strategy,
		Priority: g.Priority,
		Weight:   weight,
		Enabled:  g.Enabled,
	}, true
}

func canImportProvider(p *ExportedProvider, validGroups map[string]bool) bool {
	_, ok := buildProviderFromExport(p, validGroups)
	return ok
}

func providerImportDiffers(imported *ExportedProvider, existing *model.Provider, validGroups map[string]bool) bool {
	importedCanonical, ok := canonicalProviderImportExportJSON(imported, validGroups)
	if !ok {
		return false
	}
	existingExport := buildExportedProvider(existing)
	existingCanonical, ok := canonicalProviderImportExportJSON(&existingExport, validGroups)
	if !ok {
		return true
	}
	return !bytes.Equal(importedCanonical, existingCanonical)
}

func canonicalProviderImportExportJSON(
	exported *ExportedProvider,
	validGroups map[string]bool,
) ([]byte, bool) {
	provider, ok := buildProviderFromExport(exported, validGroups)
	if !ok {
		return nil, false
	}
	payload, err := json.Marshal(buildExportedProvider(provider))
	if err != nil {
		return nil, false
	}
	return payload, true
}

func buildProviderCredentialFromExport(
	p *ExportedProvider,
	credentialType model.ProviderCredentialType,
) *model.ProviderCredential {
	if credentialType != model.ProviderCredentialTypeChatGPT || p.Credential == nil {
		return nil
	}
	record := &model.ProviderCredential{
		ProviderID:       p.ID,
		SecretData:       p.Credential.SecretData,
		BindingAccountID: p.Credential.BindingAccountID,
		Version:          p.Credential.Version,
	}
	if strings.TrimSpace(record.SecretData) == "" {
		return nil
	}
	return model.NormalizeProviderCredentialRecord(p.ID, record)
}

func buildProviderAuthStateFromExport(
	p *ExportedProvider,
	credentialType model.ProviderCredentialType,
) *model.ProviderAuthState {
	if p.AuthState == nil {
		return model.NormalizeProviderAuthStateRecord(p.ID, credentialType, nil)
	}
	state := &model.ProviderAuthState{
		ProviderID:           p.ID,
		Status:               p.AuthState.Status,
		StatusReason:         p.AuthState.StatusReason,
		LastError:            p.AuthState.LastError,
		LastTransitionAt:     p.AuthState.LastTransitionAt,
		Email:                p.AuthState.Email,
		AccountID:            p.AuthState.AccountID,
		PlanType:             p.AuthState.PlanType,
		ExpiresAt:            p.AuthState.ExpiresAt,
		LastRefreshAt:        p.AuthState.LastRefreshAt,
		UsageSnapshot:        model.CloneProviderUsageSnapshot(p.AuthState.UsageSnapshot),
		RefreshFailCount:     p.AuthState.RefreshFailCount,
		LastRefreshFailureAt: p.AuthState.LastRefreshFailureAt,
	}
	return model.NormalizeProviderAuthStateRecord(p.ID, credentialType, state)
}

func groupImportDiffers(imported *ExportedGroup, existing *model.Group) bool {
	expected, ok := buildGroupFromExport(imported)
	if !ok {
		return false
	}
	return !reflect.DeepEqual(buildExportedGroup(expected), buildExportedGroup(existing))
}

func routingPolicyImportDiffers(imported *model.RoutingPolicy, existing *model.RoutingPolicy) bool {
	if imported == nil || existing == nil {
		return imported != existing
	}
	return !reflect.DeepEqual(buildExportedRoutingPolicy(imported), buildExportedRoutingPolicy(existing))
}

// buildValidGroupsMap builds a map of valid group IDs from request and existing groups.
func buildValidGroupsMap(requestGroups []ExportedGroup, existingGroups map[string]*model.Group) map[string]bool {
	validGroups := make(map[string]bool)
	for _, g := range requestGroups {
		group, ok := buildGroupFromExport(&g)
		if ok {
			validGroups[group.ID] = true
		}
	}
	for id := range existingGroups {
		validGroups[id] = true
	}
	return validGroups
}

// filterValidSettings filters and returns only valid settings.
func filterValidSettings(settings map[string]string) map[string]string {
	result := make(map[string]string)
	for key, value := range settings {
		if !IsValidConfigKey(key) {
			continue
		}
		if err := ValidateConfigValue(key, value); err != nil {
			continue
		}
		result[key] = value
	}
	return result
}

func validateImportSettings(settings map[string]string) []string {
	normalized := normalizeConfigSettings(settings)
	keys := make([]string, 0, len(normalized))
	for key := range normalized {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	warnings := make([]string, 0)
	for _, key := range keys {
		value := normalized[key]
		if !IsValidConfigKey(key) {
			warnings = append(warnings, "Unknown config key will be skipped: "+key)
			continue
		}
		if err := ValidateConfigValue(key, value); err != nil {
			warnings = append(warnings, "Invalid config value will be skipped for "+key+": "+err.Error())
		}
	}

	return warnings
}

// normalizeSupportedSettings returns the subset of settings that can survive
// the current import contract. Export, preview, and apply all share this view
// so round-tripping cannot promise changes that apply would later discard.
func normalizeSupportedSettings(settings map[string]string) map[string]string {
	return filterValidSettings(normalizeConfigSettings(settings))
}

// migrateConfigKey maps legacy config keys/values to current equivalents.
// Covers all values accepted by old bool validation: true/false/1/0.
func migrateConfigKey(key, value string) (string, string) {
	if key == legacyStickyEnabledKey {
		switch strings.ToLower(value) {
		case "false", "0":
			return configStickyModeKey, "off"
		case "true", "1":
			return configStickyModeKey, "api_type"
		}
	}
	if key == legacyGlobalMaxAttemptsKey {
		return configGlobalMaxAttemptsKey, value
	}
	return key, value
}

// normalizeConfigSettings applies key migrations before validation/update.
func normalizeConfigSettings(settings map[string]string) map[string]string {
	normalized := make(map[string]string, len(settings))
	preferredKeys := make(map[string]bool, len(settings))
	for key, value := range settings {
		migratedKey, _ := migrateConfigKey(key, value)
		if key == migratedKey {
			preferredKeys[migratedKey] = true
		}
	}

	for key, value := range settings {
		migratedKey, migratedValue := migrateConfigKey(key, value)
		if key != migratedKey && preferredKeys[migratedKey] {
			// Prefer the current key over its legacy alias so export/import stays
			// deterministic when both forms are present in the same payload.
			continue
		}
		normalized[migratedKey] = migratedValue
	}

	return normalized
}
