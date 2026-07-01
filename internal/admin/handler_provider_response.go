package admin

import (
	"time"

	"switch-a/internal/model"
	"switch-a/internal/providerauth"
)

// ProviderPayload keeps the admin HTTP contract explicit so auth lifecycle changes do
// not depend on the storage model's deprecated auth_profile field.
type ProviderPayload struct {
	ID               string                         `json:"id"`
	Name             string                         `json:"name"`
	APIKey           string                         `json:"api_key"`
	APITypes         []model.ProviderAPIType        `json:"api_types"`
	AuthMode         string                         `json:"auth_mode"`
	CredentialType   model.ProviderCredentialType   `json:"credential_type"`
	UsageLimitPolicy model.ProviderUsageLimitPolicy `json:"usage_limit_policy"`
	// UsageLimitPolicyExplicit lets admin clients distinguish a stored override
	// from a credential-derived effective default.
	UsageLimitPolicyExplicit bool                           `json:"usage_limit_policy_explicit,omitempty"`
	GroupID                  *string                        `json:"group_id"`
	Weight                   int                            `json:"weight"`
	Priority                 int                            `json:"priority"`
	Concurrency              int                            `json:"concurrency"`
	MaxRetries               int                            `json:"max_retries"`
	Backoff                  model.BackoffPolicy            `json:"backoff,omitzero"`
	Vendor                   string                         `json:"vendor"`
	FailoverScope            model.Scope                    `json:"failover_scope"`  // Outbound true-failover policy only.
	AcceptFailover           model.Scope                    `json:"accept_failover"` // Inbound true-failover policy only; pre-visible replacement is unaffected.
	Enabled                  bool                           `json:"enabled"`
	CreatedAt                time.Time                      `json:"created_at"`
	UpdatedAt                time.Time                      `json:"updated_at"`
	Health                   *model.HealthState             `json:"health,omitempty"`
	Auth                     *providerauth.ProviderAuthView `json:"auth,omitempty"`
}

// ProviderResponse wraps a provider payload with optional warnings for write responses.
type ProviderResponse struct {
	ProviderPayload
	Warnings []string `json:"warnings,omitempty"`
}

func (h *Handler) providerAuthView(provider *model.Provider) *providerauth.ProviderAuthView {
	if provider == nil {
		return nil
	}
	if h.auth != nil {
		return h.auth.BuildProviderAuthView(provider)
	}
	return providerauth.BuildProviderAuthView(provider)
}

func (h *Handler) providerPayload(provider *model.Provider) ProviderPayload {
	return ProviderPayload{
		ID:                       provider.ID,
		Name:                     provider.Name,
		APIKey:                   provider.APIKey,
		APITypes:                 provider.APITypes,
		AuthMode:                 provider.AuthMode,
		CredentialType:           model.NormalizeProviderCredentialType(provider.CredentialType),
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
		Auth:                     h.providerAuthView(provider),
	}
}

func (h *Handler) providerPayloads(providers []model.Provider) []ProviderPayload {
	payloads := make([]ProviderPayload, len(providers))
	for i := range providers {
		payloads[i] = h.providerPayload(&providers[i])
	}
	return payloads
}
