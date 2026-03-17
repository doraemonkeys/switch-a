package admin

import (
	"reflect"
	"sort"
	"strings"
	"time"

	"switch-a/internal/model"
)

const (
	legacyStickyEnabledKey     = "sticky_enabled"
	configStickyModeKey        = "sticky_mode"
	legacyGlobalMaxAttemptsKey = "max_retries"
	configGlobalMaxAttemptsKey = "global_max_attempts"
)

// ConfigExportVersion is the current version of the config export format.
const ConfigExportVersion = "1.0"

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
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	APIKey         string            `json:"api_key"`
	APITypes       []ExportedAPIType `json:"api_types"`
	AuthMode       string            `json:"auth_mode"`
	GroupID        *string           `json:"group_id,omitempty"`
	Weight         int               `json:"weight"`
	Priority       int               `json:"priority"`
	Concurrency    int               `json:"concurrency"`
	MaxRetries     int               `json:"max_retries"`
	Backoff        ExportedBackoff   `json:"backoff,omitempty"`
	Vendor         string            `json:"vendor,omitempty"`
	FailoverScope  string            `json:"failover_scope,omitempty"`
	AcceptFailover string            `json:"accept_failover,omitempty"`
	Enabled        bool              `json:"enabled"`
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
	// Validate API types
	var validAPITypes []ExportedAPIType
	for _, at := range p.APITypes {
		if IsValidAPIType(at.APIType) && at.BaseURL != "" {
			validAPITypes = append(validAPITypes, at)
		}
	}
	if len(validAPITypes) == 0 {
		return nil, false
	}

	// Validate auth mode
	authMode := p.AuthMode
	if authMode == "" {
		authMode = DefaultAuthMode
	} else if !IsValidAuthMode(authMode) {
		return nil, false
	}

	// Validate group reference
	var groupID *string
	if p.GroupID != nil && *p.GroupID != "" && validGroups[*p.GroupID] {
		groupID = p.GroupID
	}

	weight := p.Weight
	if weight <= 0 {
		weight = DefaultWeight
	}

	apiTypes := make([]model.ProviderAPIType, len(validAPITypes))
	for i, at := range validAPITypes {
		apiTypes[i] = model.ProviderAPIType{
			ProviderID: p.ID,
			APIType:    at.APIType,
			BaseURL:    at.BaseURL,
			APIKey:     model.NormalizeAPIKey(at.APIKey),
		}
	}

	// Validate failover scopes (use defaults if invalid)
	failoverScope := model.Scope(p.FailoverScope)
	if !model.IsValidScope(failoverScope) {
		failoverScope = model.ScopeAny
	}
	acceptFailover := model.Scope(p.AcceptFailover)
	if !model.IsValidScope(acceptFailover) {
		acceptFailover = model.ScopeAny
	}

	provider := &model.Provider{
		ID:          p.ID,
		Name:        p.Name,
		APIKey:      model.NormalizeAPIKey(p.APIKey),
		APITypes:    apiTypes,
		AuthMode:    authMode,
		GroupID:     groupID,
		Weight:      weight,
		Priority:    p.Priority,
		Concurrency: p.Concurrency,
		MaxRetries:  p.MaxRetries,
		Backoff: model.BackoffPolicy{
			InitialDelay: p.Backoff.InitialDelay,
			MaxDelay:     p.Backoff.MaxDelay,
			Multiplier:   p.Backoff.Multiplier,
			Jitter:       p.Backoff.Jitter,
		},
		Vendor:         p.Vendor,
		FailoverScope:  failoverScope,
		AcceptFailover: acceptFailover,
		Enabled:        p.Enabled,
	}
	if errMsg := validateProviderConfiguration(provider); errMsg != "" {
		return nil, false
	}
	return provider, true
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
	apiTypes := make([]ExportedAPIType, len(p.APITypes))
	for i, at := range p.APITypes {
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
	if p.GroupID != nil && *p.GroupID != "" {
		value := *p.GroupID
		groupID = &value
	}

	return ExportedProvider{
		ID:          p.ID,
		Name:        p.Name,
		APIKey:      model.NormalizeAPIKey(p.APIKey),
		APITypes:    apiTypes,
		AuthMode:    p.AuthMode,
		GroupID:     groupID,
		Weight:      p.Weight,
		Priority:    p.Priority,
		Concurrency: p.Concurrency,
		MaxRetries:  p.MaxRetries,
		Backoff: ExportedBackoff{
			InitialDelay: p.Backoff.InitialDelay,
			MaxDelay:     p.Backoff.MaxDelay,
			Multiplier:   p.Backoff.Multiplier,
			Jitter:       p.Backoff.Jitter,
		},
		Vendor:         p.Vendor,
		FailoverScope:  string(p.FailoverScope),
		AcceptFailover: string(p.AcceptFailover),
		Enabled:        p.Enabled,
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
