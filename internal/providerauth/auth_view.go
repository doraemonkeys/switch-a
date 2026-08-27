package providerauth

import (
	"fmt"
	"strings"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/model"
)

// ProviderAuthStatus is retained as the admin vocabulary while the lifecycle
// itself is owned exclusively by CredentialSession.
type ProviderAuthStatus = credentialsession.AuthStatus

const (
	ProviderAuthStatusNotConnected   = credentialsession.AuthStatusNotConnected
	ProviderAuthStatusActive         = credentialsession.AuthStatusActive
	ProviderAuthStatusReauthRequired = credentialsession.AuthStatusReauthRequired
)

const (
	ProviderAuthReasonMissingAPIKey       = "missing_api_key"
	ProviderAuthReasonLoginRequired       = "login_required"
	ProviderAuthReasonCredentialInvalid   = "credential_invalid"
	ProviderAuthReasonInvalidGrant        = "invalid_grant"
	ProviderAuthReasonRefreshTokenReused  = "refresh_token_reused"
	ProviderAuthReasonInteractionRequired = "interaction_required"
	ProviderAuthReasonTokenInvalidated    = "token_invalidated"
)

// ProviderAuthView is the explicit admin-facing summary of one credential
// session. Its historical name describes the UI surface, not an ownership link.
type ProviderAuthView struct {
	Type          credentialsession.Kind       `json:"type"`
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

// ProviderAuthStateError reports that a credential session is blocked by its
// persisted authentication lifecycle. ProviderID is optional routing context;
// SessionID is the mutation and identity boundary.
type ProviderAuthStateError struct {
	ProviderID string
	SessionID  string
	Status     ProviderAuthStatus
	Reason     string
	LastError  string
}

func (e *ProviderAuthStateError) Error() string {
	credential := fmt.Sprintf("credential session %q", e.SessionID)
	if e.SessionID == "" {
		credential = "credential session"
	}
	switch e.Status {
	case ProviderAuthStatusReauthRequired:
		if e.Reason != "" {
			return fmt.Sprintf("%s requires reauthentication (%s)", credential, e.Reason)
		}
		return fmt.Sprintf("%s requires reauthentication", credential)
	default:
		return fmt.Sprintf("%s is not connected", credential)
	}
}

// BuildCredentialSessionAuthView maps the immutable session snapshot used by
// routing. It never rehydrates or interprets a Provider-owned projection.
func BuildCredentialSessionAuthView(snapshot *credentialsession.Snapshot) *ProviderAuthView {
	if snapshot == nil {
		return nil
	}
	state := credentialsession.NormalizeAuthState(snapshot.Kind, snapshot.AuthState.Clone())
	switch snapshot.Kind {
	case credentialsession.KindAPIKey:
		if strings.TrimSpace(snapshot.SecretData) == "" {
			state.Status = credentialsession.AuthStatusNotConnected
			state.StatusReason = ProviderAuthReasonMissingAPIKey
		}
	case credentialsession.KindChatGPT:
		credential, err := decodeChatGPTCredentialSession(snapshot)
		if err != nil {
			state.Status = credentialsession.AuthStatusNotConnected
			state.StatusReason = ProviderAuthReasonCredentialInvalid
			state.LastError = err.Error()
		} else if credential == nil || !credential.Ready() {
			state.Status = credentialsession.AuthStatusNotConnected
			state.StatusReason = ProviderAuthReasonLoginRequired
		}
	}
	return buildCredentialSessionAuthView(snapshot.Kind, state)
}

func chatGPTCredentialAuthView(credential *model.ChatGPTProviderCredential) *ProviderAuthView {
	snapshot, err := chatGPTCredentialSessionSnapshot(credential, "")
	if err != nil {
		return buildCredentialSessionAuthView(credentialsession.KindChatGPT, credentialsession.AuthState{
			Status: credentialsession.AuthStatusNotConnected, StatusReason: ProviderAuthReasonCredentialInvalid,
			LastError: err.Error(),
		})
	}
	return BuildCredentialSessionAuthView(&snapshot)
}

func buildCredentialSessionAuthView(kind credentialsession.Kind, state credentialsession.AuthState) *ProviderAuthView {
	state = credentialsession.NormalizeAuthState(kind, state)
	return &ProviderAuthView{
		Type: kind, Status: state.Status, Reason: credentialSessionAuthReason(kind, state),
		Email: strings.TrimSpace(state.Email), AccountID: strings.TrimSpace(state.AccountID),
		PlanType: strings.TrimSpace(state.PlanType), Usage: providerUsageSnapshot(state.UsageSnapshot),
		ExpiresAt: cloneAuthViewTime(state.ExpiresAt), LastRefreshAt: cloneAuthViewTime(state.LastRefreshAt),
		LastError: strings.TrimSpace(state.LastError),
	}
}

func credentialSessionAuthReason(kind credentialsession.Kind, state credentialsession.AuthState) string {
	if state.Status == credentialsession.AuthStatusActive {
		return ""
	}
	if reason := strings.TrimSpace(state.StatusReason); reason != "" {
		return reason
	}
	if kind == credentialsession.KindChatGPT {
		return ProviderAuthReasonLoginRequired
	}
	return ""
}

func cloneAuthViewTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	clone := value.UTC()
	return &clone
}
