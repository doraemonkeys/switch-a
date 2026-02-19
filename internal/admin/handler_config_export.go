package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"switch-a/internal/model"

	"go.uber.org/zap"
)

const (
	legacyStickyEnabledKey = "sticky_enabled"
	configStickyModeKey    = "sticky_mode"
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

// ExportConfig handles GET /admin/api/config/export.
func (h *Handler) ExportConfig(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Fetch all providers
	providers, err := h.store.ListProviders(ctx)
	if err != nil {
		h.logger.Error("failed to list providers for export", zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to export config")
		return
	}

	// Fetch all groups
	groups, err := h.store.ListGroups(ctx)
	if err != nil {
		h.logger.Error("failed to list groups for export", zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to export config")
		return
	}

	// Fetch all settings
	settings, err := h.store.GetAllConfig(ctx)
	if err != nil {
		h.logger.Error("failed to get config for export", zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to export config")
		return
	}

	// Convert to export format
	exportedProviders := make([]ExportedProvider, len(providers))
	for i, p := range providers {
		apiTypes := make([]ExportedAPIType, len(p.APITypes))
		for j, at := range p.APITypes {
			apiTypes[j] = ExportedAPIType{
				APIType: at.APIType,
				BaseURL: at.BaseURL,
			}
		}
		exportedProviders[i] = ExportedProvider{
			ID:          p.ID,
			Name:        p.Name,
			APIKey:      p.APIKey,
			APITypes:    apiTypes,
			AuthMode:    p.AuthMode,
			GroupID:     p.GroupID,
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

	exportedGroups := make([]ExportedGroup, len(groups))
	for i, g := range groups {
		exportedGroups[i] = ExportedGroup{
			ID:       g.ID,
			Name:     g.Name,
			Strategy: g.Strategy,
			Priority: g.Priority,
			Weight:   g.Weight,
			Enabled:  g.Enabled,
		}
	}

	export := ExportedConfig{
		Version:    ConfigExportVersion,
		ExportedAt: time.Now().UTC(),
		Providers:  exportedProviders,
		Groups:     exportedGroups,
		Settings:   settings,
	}

	writeJSON(w, http.StatusOK, export)
}

// ImportConfig handles POST /admin/api/config/import.
func (h *Handler) ImportConfig(w http.ResponseWriter, r *http.Request) {
	limitRequestBody(w, r)

	// Check for dry_run query parameter
	dryRun := r.URL.Query().Get("dry_run") == "true"

	var req ImportConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Invalid request body")
		return
	}

	// Validate the import request
	warnings := h.validateImportRequest(&req)

	ctx := r.Context()

	// Get existing data for comparison
	existingProviders, err := h.store.ListProviders(ctx)
	if err != nil {
		h.logger.Error("failed to list providers for import", zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to import config")
		return
	}

	existingGroups, err := h.store.ListGroups(ctx)
	if err != nil {
		h.logger.Error("failed to list groups for import", zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to import config")
		return
	}

	existingSettings, err := h.store.GetAllConfig(ctx)
	if err != nil {
		h.logger.Error("failed to get config for import", zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to import config")
		return
	}

	// Build lookup maps
	existingProviderMap := make(map[string]*model.Provider)
	for i := range existingProviders {
		existingProviderMap[existingProviders[i].ID] = &existingProviders[i]
	}

	existingGroupMap := make(map[string]*model.Group)
	for i := range existingGroups {
		existingGroupMap[existingGroups[i].ID] = &existingGroups[i]
	}

	// Calculate changes
	changes := h.calculateImportChanges(&req, existingProviderMap, existingGroupMap, existingSettings)

	// If dry_run, return preview
	if dryRun {
		resp := ImportPreviewResponse{
			DryRun:   true,
			Changes:  changes,
			Warnings: warnings,
		}
		writeJSON(w, http.StatusOK, resp)
		return
	}

	// Apply changes
	applied, err := h.applyImportChanges(ctx, &req, existingProviderMap, existingGroupMap, existingSettings)
	if err != nil {
		h.logger.Error("failed to apply import changes", zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to import config: "+err.Error())
		return
	}

	resp := ImportResult{
		Success: true,
		Applied: applied,
	}
	writeJSON(w, http.StatusOK, resp)
}

// validateImportRequest validates the import request and returns warnings.
func (h *Handler) validateImportRequest(req *ImportConfigRequest) []string {
	var warnings []string

	// Validate providers
	for _, p := range req.Providers {
		warnings = append(warnings, validateExportedProvider(&p)...)
	}

	// Validate groups
	for _, g := range req.Groups {
		warnings = append(warnings, validateExportedGroup(&g)...)
	}

	// Validate settings
	for key := range normalizeImportSettings(req.Settings) {
		if !IsValidConfigKey(key) {
			warnings = append(warnings, "Unknown config key will be skipped: "+key)
		}
	}

	// Check for provider references to non-existent groups
	warnings = append(warnings, validateProviderGroupRefs(req)...)

	return warnings
}

// validateExportedProvider validates a single provider and returns warnings.
func validateExportedProvider(p *ExportedProvider) []string {
	var warnings []string

	if p.ID == "" {
		warnings = append(warnings, "Provider with empty ID will be skipped")
	}
	if p.Name == "" {
		warnings = append(warnings, "Provider '"+p.ID+"' has empty name")
	}
	for _, at := range p.APITypes {
		if !IsValidAPIType(at.APIType) {
			warnings = append(warnings, "Provider '"+p.ID+"' has invalid api_type: "+at.APIType)
		}
		if at.BaseURL == "" {
			warnings = append(warnings, "Provider '"+p.ID+"' has empty base_url for api_type: "+at.APIType)
		} else if !isValidBaseURL(at.BaseURL) {
			// Match the same validation used in CRUD handlers to prevent imports
			// of providers with malformed URLs that would fail at proxy routing time.
			warnings = append(warnings, "Provider '"+p.ID+"' has malformed base_url for api_type: "+at.APIType)
		}
	}
	if p.AuthMode != "" && !IsValidAuthMode(p.AuthMode) {
		warnings = append(warnings, "Provider '"+p.ID+"' has invalid auth_mode: "+p.AuthMode)
	}

	return warnings
}

// validateExportedGroup validates a single group and returns warnings.
func validateExportedGroup(g *ExportedGroup) []string {
	var warnings []string

	if g.ID == "" {
		warnings = append(warnings, "Group with empty ID will be skipped")
	}
	if g.Name == "" {
		warnings = append(warnings, "Group '"+g.ID+"' has empty name")
	}
	if g.Strategy != "" && !IsValidStrategy(g.Strategy) {
		warnings = append(warnings, "Group '"+g.ID+"' has invalid strategy: "+g.Strategy)
	}
	if g.Priority == ReservedGroupPriority {
		warnings = append(warnings, "Group '"+g.ID+"' uses reserved priority value")
	}

	return warnings
}

// validateProviderGroupRefs checks for provider references to non-existent groups.
func validateProviderGroupRefs(req *ImportConfigRequest) []string {
	var warnings []string

	groupIDs := make(map[string]bool)
	for _, g := range req.Groups {
		groupIDs[g.ID] = true
	}
	for _, p := range req.Providers {
		if p.GroupID != nil && *p.GroupID != "" && !groupIDs[*p.GroupID] {
			warnings = append(warnings, "Provider '"+p.ID+"' references non-existent group '"+*p.GroupID+"'")
		}
	}

	return warnings
}

// calculateImportChanges calculates what will be changed during import.
func (h *Handler) calculateImportChanges(
	req *ImportConfigRequest,
	existingProviders map[string]*model.Provider,
	existingGroups map[string]*model.Group,
	existingSettings map[string]string,
) ImportChanges {
	var changes ImportChanges

	// Calculate provider changes
	for _, p := range req.Providers {
		if p.ID == "" {
			continue
		}
		if _, exists := existingProviders[p.ID]; exists {
			changes.Providers.Update++
		} else {
			changes.Providers.Add++
		}
	}

	// Calculate group changes
	for _, g := range req.Groups {
		if g.ID == "" {
			continue
		}
		if _, exists := existingGroups[g.ID]; exists {
			changes.Groups.Update++
		} else {
			changes.Groups.Add++
		}
	}

	// Calculate settings changes
	for key, value := range normalizeImportSettings(req.Settings) {
		if !IsValidConfigKey(key) {
			continue
		}
		if existingValue, exists := existingSettings[key]; exists {
			if existingValue != value {
				changes.Settings.Update++
			}
		} else {
			changes.Settings.Add++
		}
	}

	return changes
}

// applyImportChanges applies the import changes to the store.
func (h *Handler) applyImportChanges(
	ctx context.Context,
	req *ImportConfigRequest,
	existingProviders map[string]*model.Provider,
	existingGroups map[string]*model.Group,
	existingSettings map[string]string,
) (ImportedCounts, error) {
	var applied ImportedCounts

	// Import groups first (providers may reference them)
	for _, g := range req.Groups {
		added, updated, err := h.importGroup(ctx, &g, existingGroups)
		if err != nil {
			return applied, err
		}
		applied.Groups.Added += added
		applied.Groups.Updated += updated
	}

	// Build group lookup including newly created groups
	validGroups := buildValidGroupsMap(req.Groups, existingGroups)

	// Import providers
	for _, p := range req.Providers {
		added, updated, err := h.importProvider(ctx, &p, existingProviders, validGroups)
		if err != nil {
			return applied, err
		}
		applied.Providers.Added += added
		applied.Providers.Updated += updated
	}

	// Import settings - distinguish add/update
	settingsToUpdate := filterValidSettings(normalizeImportSettings(req.Settings))
	if len(settingsToUpdate) > 0 {
		for key := range settingsToUpdate {
			if _, exists := existingSettings[key]; exists {
				applied.Settings.Updated++
			} else {
				applied.Settings.Added++
			}
		}
		if err := h.store.SetConfigs(ctx, settingsToUpdate); err != nil {
			return applied, err
		}
	}

	return applied, nil
}

// importGroup imports a single group and returns (added, updated, error).
func (h *Handler) importGroup(
	ctx context.Context,
	g *ExportedGroup,
	existingGroups map[string]*model.Group,
) (int, int, error) {
	if g.ID == "" || g.Name == "" {
		return 0, 0, nil
	}

	// Validate strategy
	strategy := g.Strategy
	if strategy == "" {
		strategy = DefaultStrategy
	} else if !IsValidStrategy(strategy) {
		return 0, 0, nil
	}

	// Skip reserved priority
	if g.Priority == ReservedGroupPriority {
		return 0, 0, nil
	}

	weight := g.Weight
	if weight <= 0 {
		weight = DefaultWeight
	}

	group := &model.Group{
		ID:       g.ID,
		Name:     g.Name,
		Strategy: strategy,
		Priority: g.Priority,
		Weight:   weight,
		Enabled:  g.Enabled,
	}

	if _, exists := existingGroups[g.ID]; exists {
		if err := h.store.UpdateGroup(ctx, group); err != nil {
			return 0, 0, err
		}
		return 0, 1, nil
	}
	if err := h.store.CreateGroup(ctx, group); err != nil {
		return 0, 0, err
	}
	return 1, 0, nil
}

// importProvider imports a single provider and returns (added, updated, error).
func (h *Handler) importProvider(
	ctx context.Context,
	p *ExportedProvider,
	existingProviders map[string]*model.Provider,
	validGroups map[string]bool,
) (int, int, error) {
	if p.ID == "" || p.Name == "" || p.APIKey == "" {
		return 0, 0, nil
	}

	provider, ok := buildProviderFromExport(p, validGroups)
	if !ok {
		return 0, 0, nil
	}

	if existing, exists := existingProviders[p.ID]; exists {
		// Preserve timestamps by not setting them (GORM will handle UpdatedAt)
		provider.CreatedAt = existing.CreatedAt
		if err := h.store.UpdateProvider(ctx, provider); err != nil {
			return 0, 0, err
		}
		return 0, 1, nil
	}
	if err := h.store.CreateProvider(ctx, provider); err != nil {
		return 0, 0, err
	}
	return 1, 0, nil
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

	return &model.Provider{
		ID:          p.ID,
		Name:        p.Name,
		APIKey:      p.APIKey,
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
	}, true
}

// buildValidGroupsMap builds a map of valid group IDs from request and existing groups.
func buildValidGroupsMap(requestGroups []ExportedGroup, existingGroups map[string]*model.Group) map[string]bool {
	validGroups := make(map[string]bool)
	for _, g := range requestGroups {
		if g.ID != "" {
			validGroups[g.ID] = true
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

// migrateImportKey maps legacy config keys/values to current equivalents.
// Covers all values accepted by old bool validation: true/false/1/0.
func migrateImportKey(key, value string) (string, string) {
	if key == legacyStickyEnabledKey {
		switch strings.ToLower(value) {
		case "false", "0":
			return configStickyModeKey, "off"
		case "true", "1":
			return configStickyModeKey, "api_type"
		}
	}
	return key, value
}

// normalizeImportSettings applies key migrations before validation/update.
func normalizeImportSettings(settings map[string]string) map[string]string {
	normalized := make(map[string]string, len(settings))
	_, hasStickyMode := settings[configStickyModeKey]

	for key, value := range settings {
		migratedKey, migratedValue := migrateImportKey(key, value)
		if hasStickyMode && key == legacyStickyEnabledKey && migratedKey == configStickyModeKey {
			// Prefer explicit sticky_mode over migrated sticky_enabled to keep
			// import behavior deterministic when both keys are present.
			continue
		}
		normalized[migratedKey] = migratedValue
	}

	return normalized
}
