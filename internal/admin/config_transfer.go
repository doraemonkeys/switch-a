package admin

import (
	"reflect"
	"sort"
	"strings"
	"time"

	"switch-a/internal/model"
	"switch-a/internal/providerauth"
)

const (
	legacyStickyEnabledKey     = "sticky_enabled"
	configStickyModeKey        = "sticky_mode"
	legacyGlobalMaxAttemptsKey = "max_retries"
	configGlobalMaxAttemptsKey = "global_max_attempts"
)

// ConfigExportVersion is the current version of the config export format.
const ConfigExportVersion = "2.0"

// ExportedConfig represents the full exported configuration.
type ExportedConfig struct {
	Version    string             `json:"version"`
	ExportedAt time.Time          `json:"exported_at"`
	Providers  []ExportedProvider `json:"providers"`
	Groups     []ExportedGroup    `json:"groups"`
	Settings   map[string]string  `json:"settings"`
}

// ExportedProvider represents a provider in the export format.
// This is a flattened version without health state or timestamps.
type ExportedProvider struct {
	ID             string                       `json:"id"`
	Name           string                       `json:"name"`
	APIKey         string                       `json:"api_key"`
	APITypes       []ExportedAPIType            `json:"api_types"`
	AuthMode       string                       `json:"auth_mode"`
	CredentialType model.ProviderCredentialType `json:"credential_type,omitempty"`
	Credential     *ExportedProviderCredential  `json:"credential,omitempty"`
	AuthState      *ExportedProviderAuthState   `json:"auth_state,omitempty"`
	GroupID        *string                      `json:"group_id,omitempty"`
	Weight         int                          `json:"weight"`
	Priority       int                          `json:"priority"`
	Concurrency    int                          `json:"concurrency"`
	MaxRetries     int                          `json:"max_retries"`
	Backoff        ExportedBackoff              `json:"backoff,omitempty"`
	Vendor         string                       `json:"vendor,omitempty"`
	FailoverScope  string                       `json:"failover_scope,omitempty"`
	AcceptFailover string                       `json:"accept_failover,omitempty"`
	Enabled        bool                         `json:"enabled"`
}

// ExportedProviderCredential mirrors provider_credentials without repeating provider_id.
type ExportedProviderCredential struct {
	SecretData       string  `json:"secret_data,omitempty"`
	BindingAccountID *string `json:"binding_account_id,omitempty"`
	Version          int64   `json:"version,omitempty"`
}

// ExportedProviderAuthState mirrors provider_auth_states without repeating provider_id.
type ExportedProviderAuthState struct {
	Status               model.ProviderAuthStatus     `json:"status,omitempty"`
	StatusReason         string                       `json:"status_reason,omitempty"`
	LastError            string                       `json:"last_error,omitempty"`
	LastTransitionAt     *time.Time                   `json:"last_transition_at,omitempty"`
	Email                string                       `json:"email,omitempty"`
	AccountID            string                       `json:"account_id,omitempty"`
	PlanType             string                       `json:"plan_type,omitempty"`
	ExpiresAt            *time.Time                   `json:"expires_at,omitempty"`
	LastRefreshAt        *time.Time                   `json:"last_refresh_at,omitempty"`
	UsageSnapshot        *model.ProviderUsageSnapshot `json:"usage_snapshot,omitempty"`
	RefreshFailCount     int                          `json:"refresh_fail_count,omitempty"`
	LastRefreshFailureAt *time.Time                   `json:"last_refresh_failure_at,omitempty"`
}

// ExportedBackoff represents backoff settings in the export format.
// Uses raw numeric types instead of model.Duration to keep the export format
// independent of internal serialization details.
type ExportedBackoff struct {
	InitialDelay model.Duration `json:"initial_delay,omitempty"`
	MaxDelay     model.Duration `json:"max_delay,omitempty"`
	Multiplier   float64        `json:"multiplier,omitempty"`
	Jitter       bool           `json:"jitter,omitempty"`
}

// ExportedAPIType represents an API type with its base URL in the export format.
type ExportedAPIType struct {
	APIType string `json:"api_type"`
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key,omitempty"`
}

// ExportedGroup represents a group in the export format.
type ExportedGroup struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Strategy string `json:"strategy"`
	Priority int    `json:"priority"`
	Weight   int    `json:"weight"`
	Enabled  bool   `json:"enabled"`
}

// ImportConfigRequest represents the request body for config import.
type ImportConfigRequest struct {
	Version   string             `json:"version"`
	Providers []ExportedProvider `json:"providers"`
	Groups    []ExportedGroup    `json:"groups"`
	Settings  map[string]string  `json:"settings"`
}

// ImportChanges represents the changes that will be applied during import.
type ImportChanges struct {
	Providers ChangeCount `json:"providers"`
	Groups    ChangeCount `json:"groups"`
	Settings  ChangeCount `json:"settings"`
}

// ChangeCount represents add/update/delete counts.
type ChangeCount struct {
	Add    int `json:"add"`
	Update int `json:"update"`
	Delete int `json:"delete"`
}

// ImportPreviewResponse is the response for dry_run=true.
type ImportPreviewResponse struct {
	DryRun   bool          `json:"dry_run"`
	Changes  ImportChanges `json:"changes"`
	Warnings []string      `json:"warnings"`
}

// ImportResult represents the result of an actual import.
type ImportResult struct {
	Success bool           `json:"success"`
	Applied ImportedCounts `json:"applied"`
}

// ImportedCounts represents the counts of successfully imported items.
type ImportedCounts struct {
	Providers AppliedCount `json:"providers"`
	Groups    AppliedCount `json:"groups"`
	Settings  AppliedCount `json:"settings"`
}

// AppliedCount represents added/updated counts for applied changes.
type AppliedCount struct {
	Added   int `json:"added"`
	Updated int `json:"updated"`
}

// buildProviderFromExport builds a model.Provider from an ExportedProvider.
// Returns false if the provider is invalid and should be skipped.
func buildProviderFromExport(p *ExportedProvider, validGroups map[string]bool) (*model.Provider, bool) {
	credentialType := model.NormalizeProviderCredentialType(p.CredentialType)
	if !IsValidProviderCredentialType(credentialType) {
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
		ID:             p.ID,
		Name:           p.Name,
		APIKey:         model.NormalizeAPIKey(p.APIKey),
		APITypes:       apiTypes,
		AuthMode:       authMode,
		CredentialType: credentialType,
		GroupID:        buildProviderGroupIDFromExport(p.GroupID, validGroups),
		Weight:         normalizeProviderWeightFromExport(p.Weight),
		Priority:       p.Priority,
		Concurrency:    p.Concurrency,
		MaxRetries:     p.MaxRetries,
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
	if provider.AuthState.Status != model.ProviderAuthStatusActive {
		return true
	}
	return exportedChatGPTCredentialReady(exported.Credential)
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

func buildExportedProvider(p *model.Provider) ExportedProvider {
	canonical := *p
	canonical.APITypes = append([]model.ProviderAPIType(nil), p.APITypes...)
	providerauth.NormalizeProviderForPersistence(&canonical)
	credential := exportedProviderCredential(canonical.Credential)
	authState := exportedProviderAuthState(canonical.AuthState)
	if authState == nil {
		authState = exportedProviderAuthState(model.ProviderAuthStateFromCredential(
			canonical.ID,
			canonical.CredentialType,
			canonical.Credential,
		))
	}

	apiTypes := make([]ExportedAPIType, len(canonical.APITypes))
	for i, at := range canonical.APITypes {
		apiTypes[i] = ExportedAPIType{
			APIType: at.APIType,
			BaseURL: at.BaseURL,
			APIKey:  model.NormalizeAPIKey(at.APIKey),
		}
	}
	sort.Slice(apiTypes, func(i, j int) bool {
		return apiTypes[i].APIType < apiTypes[j].APIType
	})

	var groupID *string
	if canonical.GroupID != nil && *canonical.GroupID != "" {
		value := *canonical.GroupID
		groupID = &value
	}

	return ExportedProvider{
		ID:             canonical.ID,
		Name:           canonical.Name,
		APIKey:         model.NormalizeAPIKey(canonical.APIKey),
		APITypes:       apiTypes,
		AuthMode:       canonical.AuthMode,
		CredentialType: model.NormalizeProviderCredentialType(canonical.CredentialType),
		Credential:     credential,
		AuthState:      authState,
		GroupID:        groupID,
		Weight:         canonical.Weight,
		Priority:       canonical.Priority,
		Concurrency:    canonical.Concurrency,
		MaxRetries:     canonical.MaxRetries,
		Backoff: ExportedBackoff{
			InitialDelay: canonical.Backoff.InitialDelay,
			MaxDelay:     canonical.Backoff.MaxDelay,
			Multiplier:   canonical.Backoff.Multiplier,
			Jitter:       canonical.Backoff.Jitter,
		},
		Vendor:         canonical.Vendor,
		FailoverScope:  string(canonical.FailoverScope),
		AcceptFailover: string(canonical.AcceptFailover),
		Enabled:        canonical.Enabled,
	}
}

func buildExportedGroup(g *model.Group) ExportedGroup {
	return ExportedGroup{
		ID:       g.ID,
		Name:     g.Name,
		Strategy: g.Strategy,
		Priority: g.Priority,
		Weight:   g.Weight,
		Enabled:  g.Enabled,
	}
}

func canImportProvider(p *ExportedProvider, validGroups map[string]bool) bool {
	_, ok := buildProviderFromExport(p, validGroups)
	return ok
}

func canImportGroup(g *ExportedGroup) bool {
	_, ok := buildGroupFromExport(g)
	return ok
}

func providerImportDiffers(imported *ExportedProvider, existing *model.Provider, validGroups map[string]bool) bool {
	expected, ok := buildProviderFromExport(imported, validGroups)
	if !ok {
		return false
	}
	return !reflect.DeepEqual(buildExportedProvider(expected), buildExportedProvider(existing))
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

func exportedProviderCredential(credential *model.ProviderCredential) *ExportedProviderCredential {
	if credential == nil {
		return nil
	}
	normalized := model.NormalizeProviderCredentialRecord(credential.ProviderID, credential)
	return &ExportedProviderCredential{
		SecretData:       normalized.SecretData,
		BindingAccountID: normalized.BindingAccountID,
		Version:          normalized.Version,
	}
}

func exportedProviderAuthState(authState *model.ProviderAuthState) *ExportedProviderAuthState {
	if authState == nil {
		return nil
	}
	normalized := authState.Clone()
	return &ExportedProviderAuthState{
		Status:               normalized.Status,
		StatusReason:         normalized.StatusReason,
		LastError:            normalized.LastError,
		LastTransitionAt:     normalized.LastTransitionAt,
		Email:                normalized.Email,
		AccountID:            normalized.AccountID,
		PlanType:             normalized.PlanType,
		ExpiresAt:            normalized.ExpiresAt,
		LastRefreshAt:        normalized.LastRefreshAt,
		UsageSnapshot:        model.CloneProviderUsageSnapshot(normalized.UsageSnapshot),
		RefreshFailCount:     normalized.RefreshFailCount,
		LastRefreshFailureAt: normalized.LastRefreshFailureAt,
	}
}

func groupImportDiffers(imported *ExportedGroup, existing *model.Group) bool {
	expected, ok := buildGroupFromExport(imported)
	if !ok {
		return false
	}
	return !reflect.DeepEqual(buildExportedGroup(expected), buildExportedGroup(existing))
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
