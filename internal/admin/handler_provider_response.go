package admin

import (
	"sort"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/model"
)

// ProviderPayload keeps route configuration and session lifecycle summaries
// explicit without serializing the session-owned secret.
type ProviderPayload struct {
	ID                 string                             `json:"id"`
	Name               string                             `json:"name"`
	APITypes           []ProviderAPITypePayload           `json:"api_types"`
	AuthMode           string                             `json:"auth_mode"`
	CredentialSessions []ProviderCredentialSessionPayload `json:"credential_sessions"`
	UsageLimitPolicy   model.ProviderUsageLimitPolicy     `json:"usage_limit_policy"`
	// UsageLimitPolicyExplicit lets admin clients distinguish a stored override
	// from the route-target-independent effective default.
	UsageLimitPolicyExplicit bool                `json:"usage_limit_policy_explicit,omitempty"`
	GroupID                  *string             `json:"group_id"`
	Weight                   int                 `json:"weight"`
	Priority                 int                 `json:"priority"`
	Concurrency              int                 `json:"concurrency"`
	MaxRetries               int                 `json:"max_retries"`
	Backoff                  model.BackoffPolicy `json:"backoff,omitzero"`
	Vendor                   string              `json:"vendor"`
	FailoverScope            model.Scope         `json:"failover_scope"`  // Outbound true-failover policy only.
	AcceptFailover           model.Scope         `json:"accept_failover"` // Inbound true-failover policy only; pre-visible replacement is unaffected.
	Enabled                  bool                `json:"enabled"`
	CreatedAt                time.Time           `json:"created_at"`
	UpdatedAt                time.Time           `json:"updated_at"`
	Health                   *model.HealthState  `json:"health,omitempty"`
}

type ProviderAPITypePayload struct {
	APIType             string `json:"api_type"`
	BaseURL             string `json:"base_url"`
	CredentialSessionID string `json:"credential_session_id"`
}

type ProviderCredentialSessionPayload struct {
	ID        string                      `json:"id"`
	Vendor    string                      `json:"vendor"`
	Kind      credentialsession.Kind      `json:"kind"`
	Version   int64                       `json:"version"`
	Subject   credentialsession.Subject   `json:"subject"`
	AuthState credentialsession.AuthState `json:"auth_state"`
}

// ProviderResponse wraps a provider payload with optional warnings for write responses.
type ProviderResponse struct {
	ProviderPayload
	Warnings []string `json:"warnings,omitempty"`
}

func (h *Handler) providerPayload(provider *model.Provider) ProviderPayload {
	apiTypes := make([]ProviderAPITypePayload, 0, len(provider.APITypes))
	for _, apiType := range provider.APITypes {
		snapshot, _ := provider.CredentialSessionForAPIType(apiType.APIType)
		sessionID := ""
		if snapshot != nil {
			sessionID = snapshot.SessionID
		}
		apiTypes = append(apiTypes, ProviderAPITypePayload{
			APIType: apiType.APIType, BaseURL: apiType.BaseURL, CredentialSessionID: sessionID,
		})
	}
	sessionsByID := make(map[string]ProviderCredentialSessionPayload)
	for _, route := range provider.CredentialSessions {
		snapshot := route.Credential
		sessionsByID[snapshot.SessionID] = ProviderCredentialSessionPayload{
			ID: snapshot.SessionID, Vendor: snapshot.Vendor, Kind: snapshot.Kind,
			Version: snapshot.Version, Subject: snapshot.Subject.Clone(), AuthState: snapshot.AuthState.Clone(),
		}
	}
	sessions := make([]ProviderCredentialSessionPayload, 0, len(sessionsByID))
	for _, session := range sessionsByID {
		sessions = append(sessions, session)
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].ID < sessions[j].ID })
	return ProviderPayload{
		ID:                       provider.ID,
		Name:                     provider.Name,
		APITypes:                 apiTypes,
		AuthMode:                 provider.AuthMode,
		CredentialSessions:       sessions,
		UsageLimitPolicy:         provider.UsageLimitPolicyOrDefault(),
		UsageLimitPolicyExplicit: provider.UsageLimitPolicy != "",
		GroupID:                  provider.GroupID,
		Weight:                   provider.Weight,
		Priority:                 provider.Priority,
		Concurrency:              provider.Concurrency,
		MaxRetries:               provider.MaxRetries,
		Backoff:                  provider.Backoff,
		Vendor:                   provider.Vendor,
		FailoverScope:            provider.FailoverScope,
		AcceptFailover:           provider.AcceptFailover,
		Enabled:                  provider.Enabled,
		CreatedAt:                provider.CreatedAt,
		UpdatedAt:                provider.UpdatedAt,
		Health:                   provider.Health,
	}
}

func (h *Handler) providerPayloads(providers []model.Provider) []ProviderPayload {
	payloads := make([]ProviderPayload, len(providers))
	for i := range providers {
		payloads[i] = h.providerPayload(&providers[i])
	}
	return payloads
}
