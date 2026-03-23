package model

import "time"

// ProviderCredentialType identifies the upstream credential source for a provider.
type ProviderCredentialType string

const (
	// ProviderCredentialTypeAPIKey uses the provider/API-type API key fields.
	ProviderCredentialTypeAPIKey ProviderCredentialType = "api_key"
	// ProviderCredentialTypeChatGPT uses a ChatGPT OAuth login captured locally.
	ProviderCredentialTypeChatGPT ProviderCredentialType = "chatgpt"
)

// IsValidProviderCredentialType reports whether the credential type is supported.
// Empty values remain valid so pre-migration records normalize to the static API-key mode.
func IsValidProviderCredentialType(value ProviderCredentialType) bool {
	switch value {
	case "", ProviderCredentialTypeAPIKey, ProviderCredentialTypeChatGPT:
		return true
	default:
		return false
	}
}

// NormalizeProviderCredentialType applies the domain default for legacy rows.
func NormalizeProviderCredentialType(value ProviderCredentialType) ProviderCredentialType {
	if value == "" {
		return ProviderCredentialTypeAPIKey
	}
	return value
}

// ProviderUsageWindow captures one upstream quota window for admin display.
type ProviderUsageWindow struct {
	UsedPercent   float64    `json:"used_percent"`
	WindowSeconds int64      `json:"window_seconds"`
	ResetAt       *time.Time `json:"reset_at,omitempty"`
}

// ProviderUsageSnapshot stores the latest non-sensitive quota data derived from
// provider-owned credentials. Persisting it alongside the credential keeps the
// admin surface readable even when the upstream usage endpoint is temporarily unavailable.
type ProviderUsageSnapshot struct {
	FetchedAt *time.Time           `json:"fetched_at,omitempty"`
	PlanType  string               `json:"plan_type,omitempty"`
	FiveHour  *ProviderUsageWindow `json:"five_hour,omitempty"`
	OneWeek   *ProviderUsageWindow `json:"one_week,omitempty"`
}

// ProviderAuthProfile is the non-sensitive credential summary returned by the admin API.
type ProviderAuthProfile struct {
	Type        ProviderCredentialType `json:"type"`
	Ready       bool                   `json:"ready"`
	Email       string                 `json:"email,omitempty"`
	AccountID   string                 `json:"account_id,omitempty"`
	PlanType    string                 `json:"plan_type,omitempty"`
	Usage       *ProviderUsageSnapshot `json:"usage,omitempty"`
	ExpiresAt   *time.Time             `json:"expires_at,omitempty"`
	LastRefresh *time.Time             `json:"last_refresh,omitempty"`
}

// ChatGPTProviderCredential persists the OAuth tokens needed to proxy Codex requests.
type ChatGPTProviderCredential struct {
	AccessToken   string                 `json:"access_token"`
	RefreshToken  string                 `json:"refresh_token"`
	IDToken       string                 `json:"id_token"`
	OAuthIssuer   string                 `json:"oauth_issuer,omitempty"`
	OAuthClientID string                 `json:"oauth_client_id,omitempty"`
	AccountID     string                 `json:"account_id"`
	Email         string                 `json:"email,omitempty"`
	PlanType      string                 `json:"plan_type,omitempty"`
	Usage         *ProviderUsageSnapshot `json:"usage,omitempty"`
	LastRefresh   time.Time              `json:"last_refresh"`
	ExpiresAt     time.Time              `json:"expires_at"`
}

// Ready reports whether the credential contains the minimum fields needed to proxy requests.
func (c *ChatGPTProviderCredential) Ready() bool {
	return c != nil &&
		c.AccessToken != "" &&
		c.RefreshToken != "" &&
		c.AccountID != ""
}
