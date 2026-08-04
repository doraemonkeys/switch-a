package admin

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/doraemonkeys/switch-a/internal/errorrule"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/providerauth"

	"go.uber.org/zap"
)

// ConfigExportVersion is the current version of the config export format.
const ConfigExportVersion = "4.0"

// ExportedConfig represents the full exported configuration.
type ExportedConfig struct {
	Version            string                      `json:"version"`
	ExportedAt         time.Time                   `json:"exported_at"`
	Providers          []ExportedProvider          `json:"providers"`
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
	APIKey           string                         `json:"api_key"`
	APITypes         []ExportedAPIType              `json:"api_types"`
	AuthMode         string                         `json:"auth_mode"`
	CredentialType   model.ProviderCredentialType   `json:"credential_type,omitempty"`
	UsageLimitPolicy model.ProviderUsageLimitPolicy `json:"usage_limit_policy,omitempty"`
	Credential       *ExportedProviderCredential    `json:"credential,omitempty"`
	AuthState        *ExportedProviderAuthState     `json:"auth_state,omitempty"`
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

	// Convert to export format
	exportedProviders := make([]ExportedProvider, len(providers))
	for i := range providers {
		exportedProviders[i] = buildExportedProvider(&providers[i])
	}

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
		ID:               canonical.ID,
		Name:             canonical.Name,
		APIKey:           model.NormalizeAPIKey(canonical.APIKey),
		APITypes:         apiTypes,
		AuthMode:         canonical.AuthMode,
		CredentialType:   model.NormalizeProviderCredentialType(canonical.CredentialType),
		UsageLimitPolicy: canonical.UsageLimitPolicy,
		Credential:       credential,
		AuthState:        authState,
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
