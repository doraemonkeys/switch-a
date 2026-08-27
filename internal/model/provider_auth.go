package model

import (
	"strings"
	"time"
)

// ProviderUsageWindow captures one upstream quota window for admin display.
type ProviderUsageWindow struct {
	UsedPercent   float64    `json:"used_percent"`
	WindowSeconds int64      `json:"window_seconds"`
	ResetAt       *time.Time `json:"reset_at,omitempty"`
}

// ProviderUsageSnapshot is the transport-neutral quota payload embedded in a
// credential session's auth state. Keeping it with the session preserves the
// admin view even when the upstream usage endpoint is temporarily unavailable.
type ProviderUsageSnapshot struct {
	FetchedAt *time.Time           `json:"fetched_at,omitempty"`
	PlanType  string               `json:"plan_type,omitempty"`
	FiveHour  *ProviderUsageWindow `json:"five_hour,omitempty"`
	OneWeek   *ProviderUsageWindow `json:"one_week,omitempty"`
}

// ChatGPTProviderCredential is the in-memory decoded form of one session secret.
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

// ChatGPTProviderSecret is the persisted secret-only half of a ChatGPT
// credential. Keeping this wire contract in the model package prevents auth
// services and transactional stores from silently validating different shapes.
type ChatGPTProviderSecret struct {
	AccessToken   string `json:"access_token"`
	RefreshToken  string `json:"refresh_token"`
	IDToken       string `json:"id_token"`
	OAuthIssuer   string `json:"oauth_issuer,omitempty"`
	OAuthClientID string `json:"oauth_client_id,omitempty"`
}

// Ready reports whether the secret can participate in the normal refresh path;
// account identity intentionally lives in the CredentialSession auth snapshot.
func (s *ChatGPTProviderSecret) Ready() bool {
	return s != nil &&
		strings.TrimSpace(s.AccessToken) != "" &&
		strings.TrimSpace(s.RefreshToken) != ""
}

// Ready reports whether the credential contains the minimum fields needed to proxy requests.
func (c *ChatGPTProviderCredential) Ready() bool {
	return c != nil &&
		strings.TrimSpace(c.AccessToken) != "" &&
		strings.TrimSpace(c.RefreshToken) != "" &&
		strings.TrimSpace(c.AccountID) != ""
}
