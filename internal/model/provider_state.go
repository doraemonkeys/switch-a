package model

import (
	"encoding/json"
	"strings"
	"time"
)

// ProviderAuthStatus models credential lifecycle without conflating it with
// temporary health/circuit-breaker state.
type ProviderAuthStatus string

const (
	ProviderAuthStatusNotConnected   ProviderAuthStatus = "not_connected"
	ProviderAuthStatusActive         ProviderAuthStatus = "active"
	ProviderAuthStatusReauthRequired ProviderAuthStatus = "reauth_required"
)

// IsValidProviderAuthStatus reports whether the auth lifecycle state is known.
func IsValidProviderAuthStatus(value ProviderAuthStatus) bool {
	switch value {
	case ProviderAuthStatusNotConnected, ProviderAuthStatusActive, ProviderAuthStatusReauthRequired:
		return true
	default:
		return false
	}
}

// DefaultProviderAuthStatus keeps static API-key providers routable by default
// while preserving "not connected" for login-backed providers without a session.
func DefaultProviderAuthStatus(credentialType ProviderCredentialType) ProviderAuthStatus {
	if NormalizeProviderCredentialType(credentialType) == ProviderCredentialTypeChatGPT {
		return ProviderAuthStatusNotConnected
	}
	return ProviderAuthStatusActive
}

// ProviderCredential stores only refresh-capable secret material so summary
// refreshes can update non-sensitive state without rewriting this blob.
type ProviderCredential struct {
	ProviderID       string    `gorm:"primaryKey" json:"-"`
	Provider         *Provider `gorm:"foreignKey:ProviderID;constraint:OnDelete:CASCADE" json:"-"`
	SecretData       string    `gorm:"type:text;default:''" json:"secret_data,omitempty"`
	BindingAccountID *string   `gorm:"type:text;uniqueIndex" json:"binding_account_id,omitempty"`
	Version          int64     `gorm:"not null;default:1" json:"version,omitempty"`
	CreatedAt        time.Time `json:"created_at,omitempty"`
	UpdatedAt        time.Time `json:"updated_at,omitempty"`
}

// Clone returns a detached copy that callers can mutate without sharing state.
func (c *ProviderCredential) Clone() *ProviderCredential {
	if c == nil {
		return nil
	}
	clone := *c
	if c.BindingAccountID != nil {
		accountID := *c.BindingAccountID
		clone.BindingAccountID = &accountID
	}
	return &clone
}

// ProviderAuthState stores lifecycle and human-readable summary fields separately
// from provider secrets so the admin API can stay pure-read.
type ProviderAuthState struct {
	ProviderID           string                 `gorm:"primaryKey" json:"-"`
	Provider             *Provider              `gorm:"foreignKey:ProviderID;constraint:OnDelete:CASCADE" json:"-"`
	Status               ProviderAuthStatus     `gorm:"type:text;not null;default:not_connected;index" json:"status"`
	StatusReason         string                 `gorm:"type:text;default:''" json:"status_reason,omitempty"`
	LastError            string                 `gorm:"type:text;default:''" json:"last_error,omitempty"`
	LastTransitionAt     *time.Time             `json:"last_transition_at,omitempty"`
	Email                string                 `gorm:"type:text;default:''" json:"email,omitempty"`
	AccountID            string                 `gorm:"type:text;default:'';index" json:"account_id,omitempty"`
	PlanType             string                 `gorm:"type:text;default:''" json:"plan_type,omitempty"`
	ExpiresAt            *time.Time             `json:"expires_at,omitempty"`
	LastRefreshAt        *time.Time             `json:"last_refresh_at,omitempty"`
	UsageSnapshot        *ProviderUsageSnapshot `gorm:"serializer:json" json:"usage_snapshot,omitempty"`
	RefreshFailCount     int                    `gorm:"not null;default:0" json:"refresh_fail_count,omitempty"`
	LastRefreshFailureAt *time.Time             `json:"last_refresh_failure_at,omitempty"`
	CreatedAt            time.Time              `json:"created_at,omitempty"`
	UpdatedAt            time.Time              `json:"updated_at,omitempty"`
}

// Clone returns a detached copy that callers can safely normalize.
func (s *ProviderAuthState) Clone() *ProviderAuthState {
	if s == nil {
		return nil
	}
	clone := *s
	clone.LastTransitionAt = cloneTimePtr(s.LastTransitionAt)
	clone.ExpiresAt = cloneTimePtr(s.ExpiresAt)
	clone.LastRefreshAt = cloneTimePtr(s.LastRefreshAt)
	clone.LastRefreshFailureAt = cloneTimePtr(s.LastRefreshFailureAt)
	clone.UsageSnapshot = CloneProviderUsageSnapshot(s.UsageSnapshot)
	return &clone
}

// CloneProviderUsageSnapshot returns a detached copy suitable for persistence or export.
func CloneProviderUsageSnapshot(snapshot *ProviderUsageSnapshot) *ProviderUsageSnapshot {
	if snapshot == nil {
		return nil
	}
	clone := *snapshot
	clone.FetchedAt = cloneTimePtr(snapshot.FetchedAt)
	clone.FiveHour = cloneProviderUsageWindow(snapshot.FiveHour)
	clone.OneWeek = cloneProviderUsageWindow(snapshot.OneWeek)
	return &clone
}

// DecodeChatGPTProviderCredential parses the legacy secret blob. Empty payloads
// return nil so callers can model "not connected" without treating it as corruption.
func DecodeChatGPTProviderCredential(raw string) (*ChatGPTProviderCredential, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var credential ChatGPTProviderCredential
	if err := json.Unmarshal([]byte(raw), &credential); err != nil {
		return nil, err
	}
	return &credential, nil
}

// EncodeChatGPTProviderCredential re-serializes a credential for storage/export.
func EncodeChatGPTProviderCredential(credential *ChatGPTProviderCredential) (string, error) {
	if credential == nil {
		return "", nil
	}
	payload, err := json.Marshal(credential)
	if err != nil {
		return "", err
	}
	return string(payload), nil
}

// ProviderCredentialFromLegacy lifts the old provider column into the new
// provider_credentials shape without losing raw secret material.
func ProviderCredentialFromLegacy(providerID string, credentialType ProviderCredentialType, credentialData string) *ProviderCredential {
	if NormalizeProviderCredentialType(credentialType) != ProviderCredentialTypeChatGPT {
		return nil
	}
	if strings.TrimSpace(credentialData) == "" {
		return nil
	}
	record := &ProviderCredential{
		ProviderID: providerID,
		SecretData: credentialData,
		Version:    1,
	}
	if credential, err := DecodeChatGPTProviderCredential(credentialData); err == nil && credential != nil {
		record.BindingAccountID = normalizeOptionalStringPtr(credential.AccountID)
	}
	return NormalizeProviderCredentialRecord(providerID, record)
}

// ProviderAuthStateFromCredential derives an auth-state snapshot from the
// split credential record when the dedicated auth-state row is absent.
func ProviderAuthStateFromCredential(
	providerID string,
	credentialType ProviderCredentialType,
	credential *ProviderCredential,
) *ProviderAuthState {
	state := &ProviderAuthState{
		ProviderID: providerID,
		Status:     DefaultProviderAuthStatus(credentialType),
	}
	if credentialType != ProviderCredentialTypeChatGPT {
		return NormalizeProviderAuthStateRecord(providerID, credentialType, state)
	}

	if credential == nil || strings.TrimSpace(credential.SecretData) == "" {
		return NormalizeProviderAuthStateRecord(providerID, credentialType, state)
	}

	decoded, err := DecodeChatGPTProviderCredential(credential.SecretData)
	if err != nil || decoded == nil {
		return NormalizeProviderAuthStateRecord(providerID, credentialType, state)
	}
	if credential.BindingAccountID != nil {
		state.AccountID = strings.TrimSpace(*credential.BindingAccountID)
	}
	if decoded.AccountID == "" {
		decoded.AccountID = state.AccountID
	}
	if decoded.Ready() {
		state.Status = ProviderAuthStatusActive
	}
	state.Email = decoded.Email
	state.AccountID = firstNonEmptyString(state.AccountID, strings.TrimSpace(decoded.AccountID))
	state.PlanType = firstNonEmptyString(strings.TrimSpace(decoded.PlanType), usagePlanType(decoded.Usage))
	state.UsageSnapshot = CloneProviderUsageSnapshot(decoded.Usage)
	if !decoded.ExpiresAt.IsZero() {
		expiresAt := decoded.ExpiresAt
		state.ExpiresAt = &expiresAt
	}
	if !decoded.LastRefresh.IsZero() {
		lastRefresh := decoded.LastRefresh
		state.LastRefreshAt = &lastRefresh
	}
	return NormalizeProviderAuthStateRecord(providerID, credentialType, state)
}

// NormalizeProviderCredentialRecord keeps nullable binding keys and versioning
// consistent regardless of whether a row came from migration, import, or runtime writes.
func NormalizeProviderCredentialRecord(providerID string, credential *ProviderCredential) *ProviderCredential {
	if credential == nil {
		return nil
	}
	normalized := credential.Clone()
	normalized.ProviderID = providerID
	normalized.BindingAccountID = normalizeOptionalStringPtr(derefStringPtr(normalized.BindingAccountID))
	if normalized.Version <= 0 {
		normalized.Version = 1
	}
	return normalized
}

// NormalizeProviderAuthStateRecord keeps export/import and backfill code aligned
// on defaults so callers do not need to duplicate status fallback logic.
func NormalizeProviderAuthStateRecord(
	providerID string,
	credentialType ProviderCredentialType,
	authState *ProviderAuthState,
) *ProviderAuthState {
	if authState == nil {
		return &ProviderAuthState{
			ProviderID: providerID,
			Status:     DefaultProviderAuthStatus(credentialType),
		}
	}

	normalized := authState.Clone()
	normalized.ProviderID = providerID
	if !IsValidProviderAuthStatus(normalized.Status) {
		normalized.Status = DefaultProviderAuthStatus(credentialType)
	}
	normalized.StatusReason = strings.TrimSpace(normalized.StatusReason)
	normalized.LastError = strings.TrimSpace(normalized.LastError)
	normalized.Email = strings.TrimSpace(normalized.Email)
	normalized.AccountID = strings.TrimSpace(normalized.AccountID)
	normalized.PlanType = firstNonEmptyString(strings.TrimSpace(normalized.PlanType), usagePlanType(normalized.UsageSnapshot))
	if normalized.RefreshFailCount < 0 {
		normalized.RefreshFailCount = 0
	}
	return normalized
}

func cloneProviderUsageWindow(window *ProviderUsageWindow) *ProviderUsageWindow {
	if window == nil {
		return nil
	}
	clone := *window
	clone.ResetAt = cloneTimePtr(window.ResetAt)
	return &clone
}

func cloneTimePtr(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func normalizeOptionalStringPtr(value string) *string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func derefStringPtr(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func usagePlanType(snapshot *ProviderUsageSnapshot) string {
	if snapshot == nil {
		return ""
	}
	return strings.TrimSpace(snapshot.PlanType)
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
