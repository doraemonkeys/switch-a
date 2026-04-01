package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"switch-a/internal/model"
	"switch-a/internal/store"

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
	if req.Version != ConfigExportVersion {
		writeError(
			w,
			http.StatusBadRequest,
			ErrCodeValidation,
			fmt.Sprintf(
				"Unsupported config export version %q; expected %q",
				req.Version,
				ConfigExportVersion,
			),
		)
		return
	}

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
	existingRoutingPolicies, err := h.store.ListRoutingPolicies(ctx)
	if err != nil {
		h.logger.Error("failed to list routing policies for import", zap.Error(err))
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
	existingRoutingPolicyMap := make(map[model.RoutingPolicyNaturalKey]*model.RoutingPolicy, len(existingRoutingPolicies))
	for i := range existingRoutingPolicies {
		key := existingRoutingPolicies[i].NaturalKey()
		existingRoutingPolicyMap[key] = &existingRoutingPolicies[i]
	}

	staged := stageConfigImport(
		&req,
		existingProviderMap,
		existingGroupMap,
		existingRoutingPolicyMap,
		existingSettings,
	)

	// If dry_run, return preview
	if dryRun {
		if staged.previewRejectsWarning && len(staged.warnings) > 0 {
			writeError(w, http.StatusBadRequest, ErrCodeValidation, "Import validation failed: "+strings.Join(staged.warnings, "; "))
			return
		}
		resp := ImportPreviewResponse{
			DryRun:   true,
			Changes:  staged.changes,
			Warnings: append([]string{}, staged.warnings...),
		}
		writeJSON(w, http.StatusOK, resp)
		return
	}

	if len(staged.warnings) > 0 {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Import validation failed: "+strings.Join(staged.warnings, "; "))
		return
	}

	err = h.store.ApplyConfigImport(ctx, &staged.bundle)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrRoutingPolicyConflict),
			errors.Is(err, store.ErrRoutingPolicyReferenceConflict),
			errors.Is(err, store.ErrCredentialBindingConflict):
			writeError(w, http.StatusConflict, ErrCodeConflict, err.Error())
			return
		}
		h.logger.Error("failed to apply import changes", zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to import config: "+err.Error())
		return
	}

	resp := ImportResult{
		Success: true,
		Applied: ImportedCounts{
			Providers: AppliedCount{
				Added:   staged.changes.Providers.Add,
				Updated: staged.changes.Providers.Update,
				Deleted: staged.changes.Providers.Delete,
			},
			Groups: AppliedCount{
				Added:   staged.changes.Groups.Add,
				Updated: staged.changes.Groups.Update,
				Deleted: staged.changes.Groups.Delete,
			},
			RoutingPolicies: AppliedCount{
				Added:   staged.changes.RoutingPolicies.Add,
				Updated: staged.changes.RoutingPolicies.Update,
				Deleted: staged.changes.RoutingPolicies.Delete,
			},
			Settings: AppliedCount{
				Added:   staged.changes.Settings.Add,
				Updated: staged.changes.Settings.Update,
				Deleted: staged.changes.Settings.Delete,
			},
		},
	}
	writeJSON(w, http.StatusOK, resp)
}

// validateImportRequest validates the import request and returns warnings.
func validateImportRequest(
	req *ImportConfigRequest,
	existingGroups map[string]*model.Group,
	suppressedProviderGroupRefs map[string]struct{},
) []string {
	estimatedWarnings := len(req.Providers)*2 + len(req.Groups) + len(req.Settings)
	warnings := make([]string, 0, estimatedWarnings)

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
	warnings = append(
		warnings,
		validateProviderGroupRefs(req, existingGroups, suppressedProviderGroupRefs)...,
	)

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
	if p.ID == "" || p.Name == "" {
		return warnings
	}

	credentialType := model.NormalizeProviderCredentialType(p.CredentialType)
	if !IsValidProviderCredentialType(credentialType) {
		return append(warnings, "Provider '"+p.ID+"' has invalid credential_type: "+string(p.CredentialType))
	}
	if !model.IsValidProviderUsageLimitPolicy(p.UsageLimitPolicy) {
		warnings = append(warnings, "Provider '"+p.ID+"' has invalid usage_limit_policy: "+string(p.UsageLimitPolicy))
	}
	if p.AuthState != nil && p.AuthState.Status != "" && !model.IsValidProviderAuthStatus(p.AuthState.Status) {
		warnings = append(warnings, "Provider '"+p.ID+"' has invalid auth_state.status: "+string(p.AuthState.Status))
	}
	if credentialType == model.ProviderCredentialTypeChatGPT {
		if chatGPTCredentialMustBeReady(p) && !exportedChatGPTProviderReady(p) {
			warnings = append(warnings, "Provider '"+p.ID+"' has incomplete or invalid GPT login")
		}
		return warnings
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

// chatGPTCredentialMustBeReady preserves legacy/manual import intent encoded in
// the raw export payload before auth-state normalization rewrites blank values.
func chatGPTCredentialMustBeReady(p *ExportedProvider) bool {
	if p == nil || p.AuthState == nil {
		return false
	}
	return p.AuthState.Status == "" || p.AuthState.Status == model.ProviderAuthStatusActive
}

func exportedChatGPTProviderReady(provider *ExportedProvider) bool {
	if provider == nil {
		return false
	}
	return canImportProvider(provider, nil)
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
func validateProviderGroupRefs(
	req *ImportConfigRequest,
	existingGroups map[string]*model.Group,
	suppressedProviderGroupRefs map[string]struct{},
) []string {
	var warnings []string

	groupIDs := make(map[string]bool)
	for _, g := range req.Groups {
		group, ok := buildGroupFromExport(&g)
		if ok {
			groupIDs[group.ID] = true
		}
	}
	for id := range existingGroups {
		groupIDs[id] = true
	}
	for _, p := range req.Providers {
		groupID := trimmedConfigImportGroupID(p.GroupID)
		if groupID == "" || groupIDs[groupID] {
			continue
		}
		if _, suppressed := suppressedProviderGroupRefs[configImportProviderGroupRefKey(p.ID, groupID)]; suppressed {
			continue
		}
		warnings = append(warnings, "Provider '"+p.ID+"' references non-existent group '"+groupID+"'")
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
	staged := stageConfigImport(
		req,
		existingProviders,
		existingGroups,
		nil,
		existingSettings,
	)
	return staged.changes
}

// applyImportChanges applies the import changes to the store.
func (h *Handler) applyImportChanges(
	ctx context.Context,
	req *ImportConfigRequest,
	existingProviders map[string]*model.Provider,
	existingGroups map[string]*model.Group,
	existingSettings map[string]string,
) (ImportedCounts, error) {
	existingRoutingPolicies, err := h.store.ListRoutingPolicies(ctx)
	if err != nil {
		return ImportedCounts{}, err
	}
	existingRoutingPolicyMap := make(map[model.RoutingPolicyNaturalKey]*model.RoutingPolicy, len(existingRoutingPolicies))
	for i := range existingRoutingPolicies {
		key := existingRoutingPolicies[i].NaturalKey()
		existingRoutingPolicyMap[key] = &existingRoutingPolicies[i]
	}

	staged := stageConfigImport(
		req,
		existingProviders,
		existingGroups,
		existingRoutingPolicyMap,
		existingSettings,
	)
	if len(staged.warnings) > 0 {
		return ImportedCounts{}, fmt.Errorf(
			"import validation failed: %s",
			strings.Join(staged.warnings, "; "),
		)
	}
	if err := h.store.ApplyConfigImport(ctx, &staged.bundle); err != nil {
		return ImportedCounts{}, err
	}

	return ImportedCounts{
		Providers: AppliedCount{
			Added:   staged.changes.Providers.Add,
			Updated: staged.changes.Providers.Update,
			Deleted: staged.changes.Providers.Delete,
		},
		Groups: AppliedCount{
			Added:   staged.changes.Groups.Add,
			Updated: staged.changes.Groups.Update,
			Deleted: staged.changes.Groups.Delete,
		},
		RoutingPolicies: AppliedCount{
			Added:   staged.changes.RoutingPolicies.Add,
			Updated: staged.changes.RoutingPolicies.Update,
			Deleted: staged.changes.RoutingPolicies.Delete,
		},
		Settings: AppliedCount{
			Added:   staged.changes.Settings.Add,
			Updated: staged.changes.Settings.Update,
			Deleted: staged.changes.Settings.Delete,
		},
	}, nil
}
