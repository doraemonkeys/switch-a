package admin

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/errorrule"
	"github.com/doraemonkeys/switch-a/internal/model"

	"go.uber.org/zap"
)

// ConfigExportVersion is the current version of the config export format.
const ConfigExportVersion = "5.0"

type CredentialSessionTransferMode string

const (
	CredentialSessionTransferStaticSecret   CredentialSessionTransferMode = "static_secret"
	CredentialSessionTransferReauthenticate CredentialSessionTransferMode = "reauthenticate"
	configRestoreReauthenticationReason                                   = "config_restore_requires_verified_reauthentication"
)

// ExportedConfig represents the full exported configuration.
type ExportedConfig struct {
	Version            string                      `json:"version"`
	ExportedAt         time.Time                   `json:"exported_at"`
	Providers          []ExportedProvider          `json:"providers"`
	CredentialSessions []ExportedCredentialSession `json:"credential_sessions"`
	Groups             []ExportedGroup             `json:"groups"`
	RoutingPolicies    []ExportedRoutingPolicy     `json:"routing_policies"`
	Settings           map[string]string           `json:"settings"`
	InternalErrorRules []ExportedInternalErrorRule `json:"internal_error_rules"`
}

// ExportedProvider represents a provider in the export format.
// This is a flattened version without health state or timestamps.
type ExportedProvider struct {
	ID               string                         `json:"id"`
	Name             string                         `json:"name"`
	APITypes         []ExportedAPIType              `json:"api_types"`
	AuthMode         string                         `json:"auth_mode"`
	UsageLimitPolicy model.ProviderUsageLimitPolicy `json:"usage_limit_policy,omitempty"`
	GroupID          *string                        `json:"group_id,omitempty"`
	Weight           int                            `json:"weight"`
	Priority         int                            `json:"priority"`
	Concurrency      int                            `json:"concurrency"`
	MaxRetries       int                            `json:"max_retries"`
	Backoff          ExportedBackoff                `json:"backoff,omitzero"`
	Vendor           string                         `json:"vendor,omitempty"`
	FailoverScope    string                         `json:"failover_scope,omitempty"`
	AcceptFailover   string                         `json:"accept_failover,omitempty"`
	Enabled          bool                           `json:"enabled"`
}

type ExportedCredentialSession struct {
	ID           string                        `json:"id"`
	Kind         credentialsession.Kind        `json:"kind"`
	TransferMode CredentialSessionTransferMode `json:"transfer_mode"`
	SecretData   string                        `json:"secret_data,omitempty"`
	Version      int64                         `json:"version"`
	Subject      credentialsession.Subject     `json:"subject"`
	AuthState    credentialsession.AuthState   `json:"auth_state"`
}

type credentialSessionLister interface {
	ListCredentialSessions(context.Context) ([]credentialsession.Session, error)
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
	APIType             string `json:"api_type"`
	BaseURL             string `json:"base_url"`
	CredentialSessionID string `json:"credential_session_id"`
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

// ExportedRoutingPolicy captures routing behavior by natural key so config
// transfer never depends on storage-local IDs or timestamps.
type ExportedRoutingPolicy struct {
	APIType          string                            `json:"api_type"`
	ModelMatchType   model.RoutingPolicyModelMatchType `json:"model_match_type,omitempty"`
	ModelMatchValue  string                            `json:"model_match_value,omitempty"`
	Enabled          bool                              `json:"enabled"`
	TargetProviderID *string                           `json:"target_provider_id,omitempty"`
	AllowedGroupIDs  []string                          `json:"allowed_group_ids"`
	AllowedVendors   []string                          `json:"allowed_vendors"`
}

// ExportedInternalErrorRule intentionally omits runtime lifecycle, revision,
// positions, timestamps, and statistics. Array order is the transfer order.
type ExportedInternalErrorRule struct {
	ID errorrule.RuleID `json:"id"`
	errorrule.RuleSpec
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
	routingPolicies, err := h.store.ListRoutingPolicies(ctx)
	if err != nil {
		h.logger.Error("failed to list routing policies for export", zap.Error(err))
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

	sessionRepository, ok := h.store.(credentialSessionLister)
	if !ok {
		h.logger.Error("credential session repository is unavailable for config export")
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to export config")
		return
	}
	sessions, err := sessionRepository.ListCredentialSessions(ctx)
	if err != nil {
		h.logger.Error("failed to list credential sessions for export", zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to export config")
		return
	}

	// Convert to export format
	exportedProviders := make([]ExportedProvider, len(providers))
	for i := range providers {
		exportedProviders[i] = buildExportedProvider(&providers[i])
	}
	exportedSessions := make([]ExportedCredentialSession, len(sessions))
	for index := range sessions {
		exportedSessions[index] = buildExportedCredentialSession(&sessions[index])
	}
	sort.Slice(exportedSessions, func(i, j int) bool { return exportedSessions[i].ID < exportedSessions[j].ID })

	exportedGroups := make([]ExportedGroup, len(groups))
	for i := range groups {
		exportedGroups[i] = buildExportedGroup(&groups[i])
	}
	exportedRoutingPolicies := make([]ExportedRoutingPolicy, len(routingPolicies))
	for i := range routingPolicies {
		exportedRoutingPolicies[i] = buildExportedRoutingPolicy(&routingPolicies[i])
	}
	exportedRules := make([]ExportedInternalErrorRule, 0)
	if repository := configRuleRepository(h.store); repository != nil {
		_, rules := repository.ListRules()
		exportedRules = make([]ExportedInternalErrorRule, len(rules))
		for index, rule := range rules {
			exportedRules[index] = ExportedInternalErrorRule{ID: rule.ID, RuleSpec: rule.RuleSpec}
		}
	}

	export := ExportedConfig{
		Version:            ConfigExportVersion,
		ExportedAt:         time.Now().UTC(),
		Providers:          exportedProviders,
		CredentialSessions: exportedSessions,
		Groups:             exportedGroups,
		RoutingPolicies:    exportedRoutingPolicies,
		InternalErrorRules: exportedRules,
		// Export normalized settings so a current backup can always round-trip
		// through the current import contract, even if the store still contains
		// legacy keys from an older release.
		Settings: normalizeSupportedSettings(settings),
	}

	writeJSON(w, http.StatusOK, export)
}

func buildExportedCredentialSession(session *credentialsession.Session) ExportedCredentialSession {
	exported := ExportedCredentialSession{
		ID:      session.ID,
		Kind:    session.Kind,
		Version: session.Version,
	}
	if session.Kind == credentialsession.KindChatGPT {
		// A config document is bearer-controlled input, so it may describe how to
		// restore a login but can never carry the proof that activates one.
		exported.TransferMode = CredentialSessionTransferReauthenticate
		exported.Subject = credentialsession.PendingSubject()
		exported.AuthState = credentialsession.AuthState{
			Status:       credentialsession.AuthStatusReauthRequired,
			StatusReason: configRestoreReauthenticationReason,
		}
		return exported
	}
	exported.TransferMode = CredentialSessionTransferStaticSecret
	exported.SecretData = session.SecretData
	exported.Subject = session.Subject()
	exported.AuthState = session.AuthState.Clone()
	return exported
}

func buildExportedProvider(p *model.Provider) ExportedProvider {
	canonical := *p
	canonical.APITypes = append([]model.ProviderAPIType(nil), p.APITypes...)

	apiTypes := make([]ExportedAPIType, len(canonical.APITypes))
	for i, at := range canonical.APITypes {
		apiTypes[i] = ExportedAPIType{
			APIType:             at.APIType,
			BaseURL:             at.BaseURL,
			CredentialSessionID: credentialSessionIDForAPIType(&canonical, at.APIType),
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
		ID:               canonical.ID,
		Name:             canonical.Name,
		APITypes:         apiTypes,
		AuthMode:         canonical.AuthMode,
		UsageLimitPolicy: canonical.UsageLimitPolicy,
		GroupID:          groupID,
		Weight:           canonical.Weight,
		Priority:         canonical.Priority,
		Concurrency:      canonical.Concurrency,
		MaxRetries:       canonical.MaxRetries,
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

func credentialSessionIDForAPIType(provider *model.Provider, apiType string) string {
	snapshot, ok := provider.CredentialSessionForAPIType(apiType)
	if !ok {
		return ""
	}
	return snapshot.SessionID
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

func buildExportedRoutingPolicy(policy *model.RoutingPolicy) ExportedRoutingPolicy {
	groupIDs := make([]string, 0, len(policy.Groups))
	for _, group := range policy.Groups {
		groupIDs = append(groupIDs, group.GroupID)
	}
	vendors := make([]string, 0, len(policy.Vendors))
	for _, vendor := range policy.Vendors {
		vendors = append(vendors, vendor.Vendor)
	}
	var targetProviderID *string
	if policy.TargetProviderID != nil {
		trimmed := strings.TrimSpace(*policy.TargetProviderID)
		if trimmed != "" {
			targetProviderID = &trimmed
		}
	}
	return ExportedRoutingPolicy{
		APIType:          strings.TrimSpace(policy.APIType),
		ModelMatchType:   policy.ModelMatchType,
		ModelMatchValue:  strings.TrimSpace(policy.ModelMatchValue),
		Enabled:          policy.Enabled,
		TargetProviderID: targetProviderID,
		AllowedGroupIDs:  normalizeRoutingPolicyStrings(groupIDs),
		AllowedVendors:   normalizeRoutingPolicyStrings(vendors),
	}
}
