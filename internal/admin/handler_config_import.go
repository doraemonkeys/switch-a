package admin

import (
	"context"
	"encoding/json"
	"net/http"

	"switch-a/internal/model"

	"go.uber.org/zap"
)

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
	warnings = append(warnings, validateImportSettings(req.Settings)...)

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
		if !model.HasAPIKey(p.APIKey) && !model.HasAPIKey(at.APIKey) {
			warnings = append(warnings, "Provider '"+p.ID+"' has no api_key for api_type: "+at.APIType)
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
		group, ok := buildGroupFromExport(&g)
		if ok {
			groupIDs[group.ID] = true
		}
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
	validGroups := buildValidGroupsMap(req.Groups, existingGroups)
	comparableExistingSettings := normalizeSupportedSettings(existingSettings)

	// Calculate provider changes
	for _, p := range req.Providers {
		if p.ID == "" {
			continue
		}
		if existing, exists := existingProviders[p.ID]; exists {
			if providerImportDiffers(&p, existing, validGroups) {
				changes.Providers.Update++
			}
		} else if canImportProvider(&p, validGroups) {
			changes.Providers.Add++
		}
	}

	// Calculate group changes
	for _, g := range req.Groups {
		if g.ID == "" {
			continue
		}
		if existing, exists := existingGroups[g.ID]; exists {
			if groupImportDiffers(&g, existing) {
				changes.Groups.Update++
			}
		} else if canImportGroup(&g) {
			changes.Groups.Add++
		}
	}

	// Calculate settings changes
	for key, value := range normalizeSupportedSettings(req.Settings) {
		if existingValue, exists := comparableExistingSettings[key]; exists {
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
		if added > 0 || updated > 0 {
			group, ok := buildGroupFromExport(&g)
			if ok {
				existingGroups[group.ID] = group
			}
		}
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
		if added > 0 || updated > 0 {
			provider, ok := buildProviderFromExport(&p, validGroups)
			if ok {
				existingProviders[provider.ID] = provider
			}
		}
	}

	// Import settings - distinguish add/update
	comparableExistingSettings := normalizeSupportedSettings(existingSettings)
	settingsToUpdate := normalizeSupportedSettings(req.Settings)
	changedSettings := make(map[string]string, len(settingsToUpdate))
	for key, value := range settingsToUpdate {
		if existingValue, exists := comparableExistingSettings[key]; exists {
			if existingValue == value {
				continue
			}
			applied.Settings.Updated++
		} else {
			applied.Settings.Added++
		}
		changedSettings[key] = value
	}
	if len(changedSettings) > 0 {
		if err := h.store.SetConfigs(ctx, changedSettings); err != nil {
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
	group, ok := buildGroupFromExport(g)
	if !ok {
		return 0, 0, nil
	}

	if _, exists := existingGroups[g.ID]; exists {
		if !groupImportDiffers(g, existingGroups[g.ID]) {
			return 0, 0, nil
		}
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
	if p.ID == "" || p.Name == "" {
		return 0, 0, nil
	}

	provider, ok := buildProviderFromExport(p, validGroups)
	if !ok {
		return 0, 0, nil
	}

	if existing, exists := existingProviders[p.ID]; exists {
		if !providerImportDiffers(p, existing, validGroups) {
			return 0, 0, nil
		}
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
