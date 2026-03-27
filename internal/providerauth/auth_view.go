package providerauth

import (
	"fmt"
	"strings"
	"time"

	"switch-a/internal/model"
)

// ProviderAuthStatus describes the persisted authentication lifecycle visible to admins.
type ProviderAuthStatus = model.ProviderAuthStatus

const (
	ProviderAuthStatusNotConnected   = model.ProviderAuthStatusNotConnected
	ProviderAuthStatusActive         = model.ProviderAuthStatusActive
	ProviderAuthStatusReauthRequired = model.ProviderAuthStatusReauthRequired
)

const (
	ProviderAuthReasonMissingAPIKey       = "missing_api_key"
	ProviderAuthReasonLoginRequired       = "login_required"
	ProviderAuthReasonCredentialInvalid   = "credential_invalid"
	ProviderAuthReasonInvalidGrant        = "invalid_grant"
	ProviderAuthReasonRefreshTokenReused  = "refresh_token_reused"
	ProviderAuthReasonInteractionRequired = "interaction_required"
)

// ProviderAuthView is the explicit admin-facing auth summary returned by admin APIs.
type ProviderAuthView struct {
	Type          model.ProviderCredentialType `json:"type"`
	Status        ProviderAuthStatus           `json:"status"`
	Reason        string                       `json:"reason,omitempty"`
	Email         string                       `json:"email,omitempty"`
	AccountID     string                       `json:"account_id,omitempty"`
	PlanType      string                       `json:"plan_type,omitempty"`
	Usage         *model.ProviderUsageSnapshot `json:"usage,omitempty"`
	ExpiresAt     *time.Time                   `json:"expires_at,omitempty"`
	LastRefreshAt *time.Time                   `json:"last_refresh_at,omitempty"`
	LastError     string                       `json:"last_error,omitempty"`
}

// ProviderAuthStateError reports that a provider is blocked by its persisted auth lifecycle.
type ProviderAuthStateError struct {
	ProviderID string
	Status     ProviderAuthStatus
	Reason     string
	LastError  string
}

func (e *ProviderAuthStateError) Error() string {
	switch e.Status {
	case ProviderAuthStatusReauthRequired:
		if e.Reason != "" {
			return fmt.Sprintf("provider %q requires reauthentication (%s)", e.ProviderID, e.Reason)
		}
		return fmt.Sprintf("provider %q requires reauthentication", e.ProviderID)
	default:
		return fmt.Sprintf("provider %q is not connected", e.ProviderID)
	}
}

// UnsupportedProviderAuthActionError reports that an explicit auth action does not apply
// to the provider's credential type.
type UnsupportedProviderAuthActionError struct {
	ProviderID     string
	Action         string
	CredentialType model.ProviderCredentialType
}

func (e *UnsupportedProviderAuthActionError) Error() string {
	return fmt.Sprintf(
		"%s is not supported for provider %q with credential type %q",
		e.Action,
		e.ProviderID,
		e.CredentialType,
	)
}

// BuildProviderAuthView maps the locally persisted provider snapshot to the explicit admin auth view.
func BuildProviderAuthView(provider *model.Provider) *ProviderAuthView {
	if provider == nil {
		return nil
	}

	credentialType := model.NormalizeProviderCredentialType(provider.CredentialType)
	authState := providerAuthStateSnapshot(provider)
	if credentialType == providerCredentialTypeAPIKey {
		authState = apiKeyAuthStateSnapshot(provider, authState)
	}

	if credentialType == providerCredentialTypeChatGPT {
		if credential, err := decodeProviderChatGPTCredential(provider); err != nil {
			authState = buildChatGPTAuthState(
				provider.ID,
				authState,
				nil,
				ProviderAuthStatusNotConnected,
				ProviderAuthReasonCredentialInvalid,
				err.Error(),
				nil,
				time.Time{},
			)
		} else if credential == nil && authState.Status == ProviderAuthStatusActive {
			authState = buildChatGPTAuthState(
				provider.ID,
				authState,
				nil,
				ProviderAuthStatusNotConnected,
				ProviderAuthReasonLoginRequired,
				"",
				nil,
				time.Time{},
			)
		}
	}

	return buildProviderAuthView(credentialType, authState)
}

func apiKeyAuthStateSnapshot(
	provider *model.Provider,
	authState *model.ProviderAuthState,
) *model.ProviderAuthState {
	if staticProviderCredentialReady(provider) {
		return authState
	}
	state := &model.ProviderAuthState{
		Status:       ProviderAuthStatusNotConnected,
		StatusReason: ProviderAuthReasonMissingAPIKey,
	}
	if authState != nil {
		state = authState.Clone()
		state.Status = ProviderAuthStatusNotConnected
		state.StatusReason = ProviderAuthReasonMissingAPIKey
	}
	return model.NormalizeProviderAuthStateRecord(provider.ID, providerCredentialTypeAPIKey, state)
}

// HasCompleteChatGPTCredential gates config writes on persisted secret material without
// conflating that with the current auth lifecycle.
func HasCompleteChatGPTCredential(provider *model.Provider) bool {
	credential, err := decodeProviderChatGPTCredential(provider)
	return err == nil && credential != nil && credential.Ready()
}

func buildChatGPTAuthViewFromCredential(credential *model.ChatGPTProviderCredential) *ProviderAuthView {
	return buildProviderAuthView(
		providerCredentialTypeChatGPT,
		buildChatGPTAuthState(
			"",
			nil,
			credential,
			ProviderAuthStatusActive,
			"",
			"",
			cloneProviderUsageSnapshot(chatGPTUsageSnapshot(credential)),
			chatGPTLastRefreshAt(credential),
		),
	)
}

func buildProviderAuthView(
	credentialType model.ProviderCredentialType,
	authState *model.ProviderAuthState,
) *ProviderAuthView {
	normalized := model.NormalizeProviderAuthStateRecord("", credentialType, authState)
	return &ProviderAuthView{
		Type:          credentialType,
		Status:        normalized.Status,
		Reason:        providerAuthReason(credentialType, normalized),
		Email:         strings.TrimSpace(normalized.Email),
		AccountID:     strings.TrimSpace(normalized.AccountID),
		PlanType:      strings.TrimSpace(normalized.PlanType),
		Usage:         cloneProviderUsageSnapshot(normalized.UsageSnapshot),
		ExpiresAt:     cloneTimePtr(normalized.ExpiresAt),
		LastRefreshAt: cloneTimePtr(normalized.LastRefreshAt),
		LastError:     strings.TrimSpace(normalized.LastError),
	}
}

func providerAuthReason(
	credentialType model.ProviderCredentialType,
	authState *model.ProviderAuthState,
) string {
	if authState == nil || authState.Status == ProviderAuthStatusActive {
		return ""
	}
	if reason := strings.TrimSpace(authState.StatusReason); reason != "" {
		return reason
	}
	if credentialType == providerCredentialTypeChatGPT {
		return ProviderAuthReasonLoginRequired
	}
	return ""
}
