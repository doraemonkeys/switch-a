package providerauth

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/model"
)

// BuildCredentialSessionAuthView exposes the pure-read session projection needed
// by admin handlers.
func (s *Service) BuildCredentialSessionAuthView(snapshot credentialsession.Snapshot) *ProviderAuthView {
	return BuildCredentialSessionAuthView(&snapshot)
}

// RefreshCredentialSessionUsage refreshes one shared login regardless of how many
// route targets reference it.
func (s *Service) RefreshCredentialSessionUsage(ctx context.Context, snapshot credentialsession.Snapshot) (bool, error) {
	if snapshot.Kind != credentialsession.KindChatGPT {
		return false, nil
	}
	return true, s.refreshChatGPTUsageSnapshot(ctx, snapshot)
}

func (s *Service) refreshChatGPTUsageSnapshot(ctx context.Context, routeSnapshot credentialsession.Snapshot) error {
	if routeSnapshot.Kind != credentialsession.KindChatGPT || routeSnapshot.SessionID == "" {
		return fmt.Errorf("chatgpt credential session is required")
	}
	ownedCtx, release, err := s.withCredentialSessionMutations(ctx, []string{routeSnapshot.SessionID})
	if err != nil {
		return fmt.Errorf("acquire credential mutation for session %q: %w", routeSnapshot.SessionID, err)
	}
	defer release()

	latest, credential, err := s.reloadChatGPTCredentialSession(ownedCtx, "", routeSnapshot.SessionID)
	if err != nil {
		return err
	}
	ctx = ownedCtx

	snapshot, err := s.fetchChatGPTUsageSnapshot(ctx, credential)
	if err != nil {
		if reason, terminal := classifyChatGPTUsageAuthFailure(err); terminal {
			return s.markChatGPTUsageReauthenticationRequired(
				ctx,
				&latest,
				reason,
				err,
			)
		}
		return err
	}

	updatedState := latest.AuthState.Clone()
	updatedState.Status = credentialsession.AuthStatusActive
	updatedState.StatusReason = ""
	updatedState.LastError = ""
	updatedState.UsageSnapshot = credentialSessionUsageSnapshot(snapshot)
	if err := s.persistCredentialSessionAuthState(ctx, latest.SessionID, updatedState); err != nil {
		return err
	}
	return nil
}

func (s *Service) markChatGPTUsageReauthenticationRequired(
	ctx context.Context,
	snapshot *credentialsession.Snapshot,
	reason string,
	cause error,
) error {
	updatedState := snapshot.AuthState.Clone()
	updatedState.Status = credentialsession.AuthStatusReauthRequired
	updatedState.StatusReason = reason
	updatedState.LastError = cause.Error()
	updatedState.LastTransitionAt = timePointer(s.clock.Now())
	if err := s.persistCredentialSessionAuthState(ctx, snapshot.SessionID, updatedState); err != nil {
		return err
	}
	return &ProviderAuthStateError{
		SessionID: snapshot.SessionID,
		Status:    ProviderAuthStatusReauthRequired,
		Reason:    reason,
		LastError: updatedState.LastError,
	}
}

func (s *Service) persistCredentialSessionAuthState(ctx context.Context, sessionID string, authState credentialsession.AuthState) error {
	store, ok := s.credentialStore.(CredentialStore)
	if !ok {
		return fmt.Errorf("credential session store is unavailable")
	}
	if err := store.UpdateCredentialSessionAuthState(ctx, sessionID, authState); err != nil {
		return fmt.Errorf("persist auth state for credential session %q: %w", sessionID, err)
	}
	return nil
}

func (s *Service) persistChatGPTCredentialSession(
	ctx context.Context,
	snapshot *credentialsession.Snapshot,
	credential *model.ChatGPTProviderCredential,
) error {
	store, ok := s.credentialStore.(CredentialStore)
	if !ok {
		return fmt.Errorf("credential session store is unavailable")
	}
	if snapshot == nil || credential == nil {
		return fmt.Errorf("credential session snapshot and refreshed credential are required")
	}
	if snapshot.Subject.Kind != credentialsession.SubjectAccount || string(snapshot.Subject.Value) != strings.TrimSpace(credential.AccountID) {
		return fmt.Errorf("refreshed credential subject does not match session %q", snapshot.SessionID)
	}
	secretData, err := model.EncodeChatGPTProviderSecret(&model.ChatGPTProviderSecret{
		AccessToken:   credential.AccessToken,
		RefreshToken:  credential.RefreshToken,
		IDToken:       credential.IDToken,
		OAuthIssuer:   credential.OAuthIssuer,
		OAuthClientID: credential.OAuthClientID,
	})
	if err != nil {
		return fmt.Errorf("encode refreshed credential session %q: %w", snapshot.SessionID, err)
	}
	authState := snapshot.AuthState.Clone()
	authState.Status = credentialsession.AuthStatusActive
	authState.StatusReason = ""
	authState.LastError = ""
	authState.Email = credential.Email
	authState.AccountID = credential.AccountID
	authState.PlanType = credential.PlanType
	authState.ExpiresAt = timePointer(credential.ExpiresAt)
	authState.LastRefreshAt = timePointer(credential.LastRefresh)
	authState.UsageSnapshot = credentialSessionUsageSnapshot(credential.Usage)
	authState.RefreshFailCount = 0
	authState.LastRefreshFailureAt = nil
	if _, err := store.UpdateCredentialSessionCAS(ctx, snapshot.SessionID, snapshot.Version, secretData, snapshot.Subject, authState); err != nil {
		return fmt.Errorf("persist refreshed credential session %q: %w", snapshot.SessionID, err)
	}
	return nil
}

func decodeChatGPTCredentialSession(snapshot *credentialsession.Snapshot) (*model.ChatGPTProviderCredential, error) {
	if snapshot == nil || snapshot.Kind != credentialsession.KindChatGPT {
		return nil, nil
	}
	if err := snapshot.RequireResolvedSubject(); err != nil {
		return nil, err
	}
	if snapshot.Subject.Kind != credentialsession.SubjectAccount {
		return nil, fmt.Errorf("chatgpt credential session %q does not carry an account subject", snapshot.SessionID)
	}
	if err := snapshot.Subject.Validate(); err != nil {
		return nil, err
	}
	accountID := string(snapshot.Subject.Value)
	if diagnosticAccountID := strings.TrimSpace(snapshot.AuthState.AccountID); diagnosticAccountID != "" && diagnosticAccountID != accountID {
		return nil, fmt.Errorf("chatgpt credential session %q diagnostic auth account does not match its subject", snapshot.SessionID)
	}
	secret, err := model.DecodeChatGPTProviderSecret(snapshot.SecretData)
	if err != nil || secret == nil {
		return nil, err
	}
	credential := &model.ChatGPTProviderCredential{
		AccessToken:   secret.AccessToken,
		RefreshToken:  secret.RefreshToken,
		IDToken:       secret.IDToken,
		OAuthIssuer:   secret.OAuthIssuer,
		OAuthClientID: secret.OAuthClientID,
		AccountID:     accountID,
		Email:         snapshot.AuthState.Email,
		PlanType:      snapshot.AuthState.PlanType,
		Usage:         providerUsageSnapshot(snapshot.AuthState.UsageSnapshot),
	}
	if snapshot.AuthState.ExpiresAt != nil {
		credential.ExpiresAt = *snapshot.AuthState.ExpiresAt
	}
	if snapshot.AuthState.LastRefreshAt != nil {
		credential.LastRefresh = *snapshot.AuthState.LastRefreshAt
	}
	return credential, nil
}

func timePointer(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	copy := value
	return &copy
}

func credentialSessionUsageSnapshot(snapshot *model.ProviderUsageSnapshot) *credentialsession.UsageSnapshot {
	if snapshot == nil {
		return nil
	}
	result := &credentialsession.UsageSnapshot{FetchedAt: snapshot.FetchedAt, PlanType: snapshot.PlanType}
	if snapshot.FiveHour != nil {
		result.FiveHour = &credentialsession.UsageWindow{UsedPercent: snapshot.FiveHour.UsedPercent, WindowSeconds: snapshot.FiveHour.WindowSeconds, ResetAt: snapshot.FiveHour.ResetAt}
	}
	if snapshot.OneWeek != nil {
		result.OneWeek = &credentialsession.UsageWindow{UsedPercent: snapshot.OneWeek.UsedPercent, WindowSeconds: snapshot.OneWeek.WindowSeconds, ResetAt: snapshot.OneWeek.ResetAt}
	}
	return result
}

func providerUsageSnapshot(snapshot *credentialsession.UsageSnapshot) *model.ProviderUsageSnapshot {
	if snapshot == nil {
		return nil
	}
	result := &model.ProviderUsageSnapshot{FetchedAt: snapshot.FetchedAt, PlanType: snapshot.PlanType}
	if snapshot.FiveHour != nil {
		result.FiveHour = &model.ProviderUsageWindow{UsedPercent: snapshot.FiveHour.UsedPercent, WindowSeconds: snapshot.FiveHour.WindowSeconds, ResetAt: snapshot.FiveHour.ResetAt}
	}
	if snapshot.OneWeek != nil {
		result.OneWeek = &model.ProviderUsageWindow{UsedPercent: snapshot.OneWeek.UsedPercent, WindowSeconds: snapshot.OneWeek.WindowSeconds, ResetAt: snapshot.OneWeek.ResetAt}
	}
	return result
}
