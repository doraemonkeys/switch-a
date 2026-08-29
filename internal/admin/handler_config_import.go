package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/errorrule"
	errorrulesqlite "github.com/doraemonkeys/switch-a/internal/errorrule/sqlite"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/store"

	"go.uber.org/zap"
)

// ImportConfig handles POST /admin/api/config/import.
func (h *Handler) ImportConfig(w http.ResponseWriter, r *http.Request) {
	limitRequestBody(w, r)

	// Check for dry_run query parameter
	dryRun := r.URL.Query().Get("dry_run") == "true"

	var req ImportConfigRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Invalid request body")
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Request body must contain exactly one JSON value")
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
	if settingErrors := validateImportSettings(req.Settings); len(settingErrors) > 0 {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Import validation failed: "+strings.Join(settingErrors, "; "))
		return
	}

	ctx := r.Context()
	snapshot, err := h.loadConfigImportSnapshot(ctx)
	if err != nil {
		h.logger.Error("failed to load config import snapshot", zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to import config")
		return
	}

	staged := stageConfigImport(
		&req,
		snapshot.providers,
		snapshot.credentialSessions,
		snapshot.groups,
		snapshot.routingPolicies,
		snapshot.settings,
		snapshot.rules,
	)
	ruleRepository := snapshot.ruleRepository
	ruleRevision := snapshot.ruleRevision
	if staged.ruleError != nil {
		if errors.Is(staged.ruleError, errorrulesqlite.ErrImportIDCollision) ||
			errors.Is(staged.ruleError, errorrulesqlite.ErrRuleCapacity) {
			writeError(w, http.StatusConflict, ErrCodeConflict, staged.ruleError.Error())
			return
		}
		writeError(w, http.StatusBadRequest, ErrCodeValidation, staged.ruleError.Error())
		return
	}

	// If dry_run, return preview
	if dryRun {
		h.writeConfigImportPreview(w, staged, ruleRevision)
		return
	}

	if len(staged.warnings) > 0 {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Import validation failed: "+strings.Join(staged.warnings, "; "))
		return
	}
	if staged.bundle.RuleImport.Mode != errorrulesqlite.ImportModePreserve && ruleRepository != nil {
		rawIfMatch := r.Header.Get("If-Match")
		if rawIfMatch == "" {
			writeError(w, http.StatusPreconditionRequired, ErrCodePreconditionRequired, "If-Match is required for rule-changing config import")
			return
		}
		expected, err := parseInternalErrorRuleETag(rawIfMatch)
		if err != nil {
			writeError(w, http.StatusBadRequest, ErrCodeValidation, err.Error())
			return
		}
		staged.bundle.ExpectedRuleRevision = &expected
	}

	err = h.applyValidatedConfigImport(ctx, staged.changes, &staged.bundle)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrRoutingPolicyConflict),
			errors.Is(err, store.ErrRoutingPolicyReferenceConflict):
			writeError(w, http.StatusConflict, ErrCodeConflict, err.Error())
			return
		case errors.Is(err, errorrulesqlite.ErrRevisionMismatch):
			current := ruleRevision
			var mismatch *errorrulesqlite.RevisionMismatchError
			if errors.As(err, &mismatch) {
				current = mismatch.Current
			}
			writeErrorWithDetails(w, http.StatusPreconditionFailed, ErrCodeRevisionMismatch, err.Error(), map[string]string{"current_revision": current.String()})
			return
		case errors.Is(err, errorrulesqlite.ErrImportIDCollision),
			errors.Is(err, errorrulesqlite.ErrRuleCapacity):
			writeError(w, http.StatusConflict, ErrCodeConflict, err.Error())
			return
		}
		h.logger.Error("failed to apply import changes", zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to import config: "+err.Error())
		return
	}

	if ruleRepository != nil {
		ruleRevision, _ = ruleRepository.ListRules()
	}
	resp := newConfigImportResult(staged.changes, ruleRevision)
	w.Header().Set("ETag", resp.RuleSetETag)
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) writeConfigImportPreview(
	w http.ResponseWriter,
	staged stagedConfigImport,
	ruleRevision errorrule.Revision,
) {
	if staged.previewRejectsWarning && len(staged.warnings) > 0 {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Import validation failed: "+strings.Join(staged.warnings, "; "))
		return
	}
	resp := ImportPreviewResponse{
		DryRun:          true,
		Changes:         staged.changes,
		Warnings:        append([]string{}, staged.warnings...),
		RuleSetRevision: ruleRevision.String(),
		RuleSetETag:     formatInternalErrorRuleETag(ruleRevision),
	}
	w.Header().Set("ETag", resp.RuleSetETag)
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) applyValidatedConfigImport(
	ctx context.Context,
	changes ImportChanges,
	bundle *store.ConfigImportBundle,
) error {
	// Imports share the direct-update serialization boundary so their durable
	// result cannot be interleaved with another admin mutation.
	h.configMutationMu.Lock()
	defer h.configMutationMu.Unlock()
	return h.applyConfigImportAtLifecycleBoundary(ctx, changes, bundle)
}

func newConfigImportResult(changes ImportChanges, revision errorrule.Revision) ImportResult {
	return ImportResult{
		Success:         true,
		RuleSetRevision: revision.String(),
		RuleSetETag:     formatInternalErrorRuleETag(revision),
		Applied: ImportedCounts{
			Providers: AppliedCount{
				Added:   changes.Providers.Add,
				Updated: changes.Providers.Update,
				Deleted: changes.Providers.Delete,
			},
			CredentialSessions: AppliedCount{
				Added:   changes.CredentialSessions.Add,
				Updated: changes.CredentialSessions.Update,
				Deleted: changes.CredentialSessions.Delete,
			},
			Groups: AppliedCount{
				Added:   changes.Groups.Add,
				Updated: changes.Groups.Update,
				Deleted: changes.Groups.Delete,
			},
			RoutingPolicies: AppliedCount{
				Added:   changes.RoutingPolicies.Add,
				Updated: changes.RoutingPolicies.Update,
				Deleted: changes.RoutingPolicies.Delete,
			},
			Settings: AppliedCount{
				Added:   changes.Settings.Add,
				Updated: changes.Settings.Update,
				Deleted: changes.Settings.Delete,
			},
			InternalErrorRules: AppliedCount{
				Added:   changes.InternalErrorRules.Add,
				Updated: changes.InternalErrorRules.Update,
				Deleted: changes.InternalErrorRules.Delete,
			},
		},
	}
}

type configImportSnapshot struct {
	providers          map[string]*model.Provider
	credentialSessions map[string]credentialsession.Snapshot
	groups             map[string]*model.Group
	routingPolicies    map[model.RoutingPolicyNaturalKey]*model.RoutingPolicy
	settings           map[string]string
	ruleRepository     *errorrulesqlite.Repository
	ruleRevision       errorrule.Revision
	rules              []errorrule.Rule
}

func (h *Handler) loadConfigImportSnapshot(ctx context.Context) (configImportSnapshot, error) {
	providers, err := h.store.ListProviders(ctx)
	if err != nil {
		return configImportSnapshot{}, fmt.Errorf("list providers: %w", err)
	}
	credentialSessions, err := h.store.ListCredentialSessions(ctx)
	if err != nil {
		return configImportSnapshot{}, fmt.Errorf("list credential sessions: %w", err)
	}
	groups, err := h.store.ListGroups(ctx)
	if err != nil {
		return configImportSnapshot{}, fmt.Errorf("list groups: %w", err)
	}
	routingPolicies, err := h.store.ListRoutingPolicies(ctx)
	if err != nil {
		return configImportSnapshot{}, fmt.Errorf("list routing policies: %w", err)
	}
	settings, err := h.store.GetAllConfig(ctx)
	if err != nil {
		return configImportSnapshot{}, fmt.Errorf("get settings: %w", err)
	}

	snapshot := configImportSnapshot{
		providers:          make(map[string]*model.Provider, len(providers)),
		credentialSessions: make(map[string]credentialsession.Snapshot, len(credentialSessions)),
		groups:             make(map[string]*model.Group, len(groups)),
		routingPolicies:    make(map[model.RoutingPolicyNaturalKey]*model.RoutingPolicy, len(routingPolicies)),
		settings:           settings,
		ruleRepository:     configRuleRepository(h.store),
	}
	for i := range providers {
		snapshot.providers[providers[i].ID] = &providers[i]
	}
	for i := range credentialSessions {
		credentialSnapshot, snapshotErr := credentialSessions[i].Snapshot()
		if snapshotErr != nil {
			return configImportSnapshot{}, fmt.Errorf("snapshot credential session %q: %w", credentialSessions[i].ID, snapshotErr)
		}
		snapshot.credentialSessions[credentialSnapshot.SessionID] = credentialSnapshot
	}
	for i := range groups {
		snapshot.groups[groups[i].ID] = &groups[i]
	}
	for i := range routingPolicies {
		snapshot.routingPolicies[routingPolicies[i].NaturalKey()] = &routingPolicies[i]
	}
	if snapshot.ruleRepository != nil {
		snapshot.ruleRevision, snapshot.rules = snapshot.ruleRepository.ListRules()
	}
	return snapshot, nil
}

func (h *Handler) applyConfigImportAtLifecycleBoundary(
	ctx context.Context,
	changes ImportChanges,
	bundle *store.ConfigImportBundle,
) error {
	apply := func() error { return h.store.ApplyConfigImport(ctx, bundle) }
	if !hasChanges(changes.Providers) && !hasChanges(changes.CredentialSessions) && !hasChanges(changes.Groups) &&
		!hasChanges(changes.RoutingPolicies) {
		return apply()
	}
	// Retiring every current generation under the mutation lock also covers a
	// provider created concurrently with import staging; an ID union computed
	// before this boundary would leave that generation authorized by stale policy.
	return h.mutateAllProviderGenerations(apply)
}

func hasChanges(count ChangeCount) bool {
	return count.Add > 0 || count.Update > 0 || count.Delete > 0
}

// validateImportRequest validates the import request and returns warnings.
func validateImportRequest(
	req *ImportConfigRequest,
	existingGroups map[string]*model.Group,
	suppressedProviderGroupRefs map[string]struct{},
	declaredCredentialSessions map[string]struct{},
) []string {
	estimatedWarnings := len(req.Providers)*2 + len(req.CredentialSessions) + len(req.Groups) + len(req.Settings)
	warnings := make([]string, 0, estimatedWarnings)

	// Validate providers
	for _, p := range req.Providers {
		warnings = append(warnings, validateExportedProvider(&p)...)
	}
	warnings = append(warnings, validateCredentialSessionReferences(req, declaredCredentialSessions)...)

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

	if !model.IsValidProviderUsageLimitPolicy(p.UsageLimitPolicy) {
		warnings = append(warnings, "Provider '"+p.ID+"' has invalid usage_limit_policy: "+string(p.UsageLimitPolicy))
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
		if strings.TrimSpace(at.CredentialSessionID) == "" {
			warnings = append(warnings, "Provider '"+p.ID+"' has no credential_session_id for api_type: "+at.APIType)
		}
	}
	if p.AuthMode != "" && !IsValidAuthMode(p.AuthMode) {
		warnings = append(warnings, "Provider '"+p.ID+"' has invalid auth_mode: "+p.AuthMode)
	}

	return warnings
}

func validateCredentialSessionReferences(
	req *ImportConfigRequest,
	declared map[string]struct{},
) []string {
	warnings := make([]string, 0)
	for _, provider := range req.Providers {
		for _, apiType := range provider.APITypes {
			id := strings.TrimSpace(apiType.CredentialSessionID)
			if id == "" {
				continue
			}
			if _, ok := declared[id]; !ok {
				warnings = append(warnings, "Provider '"+provider.ID+"' references missing credential session '"+id+"'")
			}
		}
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
func validateProviderGroupRefs(
	req *ImportConfigRequest,
	existingGroups map[string]*model.Group,
	suppressedProviderGroupRefs map[string]struct{},
) []string {
	warnings := make([]string, 0, len(req.Providers))

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
		credentialSessionsFromProviders(existingProviders),
		existingGroups,
		nil,
		existingSettings,
		nil,
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
		credentialSessionsFromProviders(existingProviders),
		existingGroups,
		existingRoutingPolicyMap,
		existingSettings,
		nil,
	)
	if len(staged.warnings) > 0 {
		return ImportedCounts{}, fmt.Errorf(
			"import validation failed: %s",
			strings.Join(staged.warnings, "; "),
		)
	}
	if err := h.applyConfigImportAtLifecycleBoundary(ctx, staged.changes, &staged.bundle); err != nil {
		return ImportedCounts{}, err
	}

	return ImportedCounts{
		Providers: AppliedCount{
			Added:   staged.changes.Providers.Add,
			Updated: staged.changes.Providers.Update,
			Deleted: staged.changes.Providers.Delete,
		},
		CredentialSessions: AppliedCount{
			Added:   staged.changes.CredentialSessions.Add,
			Updated: staged.changes.CredentialSessions.Update,
			Deleted: staged.changes.CredentialSessions.Delete,
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

func credentialSessionsFromProviders(providers map[string]*model.Provider) map[string]credentialsession.Snapshot {
	sessions := make(map[string]credentialsession.Snapshot)
	for _, provider := range providers {
		for _, route := range provider.CredentialSessions {
			sessions[route.Credential.SessionID] = route.Credential
		}
	}
	return sessions
}
