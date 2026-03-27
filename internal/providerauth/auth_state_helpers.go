package providerauth

import (
	"fmt"
	"strings"
	"time"

	"switch-a/internal/model"
)

func providerAuthStateSnapshot(provider *model.Provider) *model.ProviderAuthState {
	if provider == nil {
		return nil
	}
	credentialType := model.NormalizeProviderCredentialType(provider.CredentialType)
	if provider.AuthState != nil {
		return model.NormalizeProviderAuthStateRecord(provider.ID, credentialType, provider.AuthState)
	}
	if credentialType == providerCredentialTypeChatGPT {
		if stored, err := decodeStoredChatGPTCredential(chatGPTCredentialData(provider)); err == nil && stored != nil {
			status := normalizeStoredChatGPTAuthStatus(stored)
			return buildChatGPTAuthState(
				provider.ID,
				nil,
				&stored.ChatGPTProviderCredential,
				status,
				reasonForStoredChatGPTAuthStatus(stored, status),
				stored.LastError,
				cloneProviderUsageSnapshot(chatGPTUsageSnapshot(&stored.ChatGPTProviderCredential)),
				time.Time{},
			)
		}
	}
	return model.ProviderAuthStateFromCredential(provider.ID, credentialType, provider.Credential)
}

func decodeProviderChatGPTCredential(provider *model.Provider) (*model.ChatGPTProviderCredential, error) {
	if provider == nil {
		return nil, fmt.Errorf("provider is required")
	}
	raw := strings.TrimSpace(chatGPTCredentialData(provider))
	if raw == "" {
		return nil, nil
	}
	secret, err := decodeChatGPTCredentialSecret(raw)
	if err != nil {
		return nil, err
	}
	credential := &model.ChatGPTProviderCredential{
		AccessToken:   secret.AccessToken,
		RefreshToken:  secret.RefreshToken,
		IDToken:       secret.IDToken,
		OAuthIssuer:   strings.TrimSpace(secret.OAuthIssuer),
		OAuthClientID: strings.TrimSpace(secret.OAuthClientID),
	}

	authState := providerAuthStateSnapshot(provider)
	if authState != nil {
		credential.Email = strings.TrimSpace(authState.Email)
		credential.AccountID = strings.TrimSpace(authState.AccountID)
		credential.PlanType = firstNonEmptyString(strings.TrimSpace(authState.PlanType), usagePlanType(authState.UsageSnapshot))
		credential.Usage = cloneProviderUsageSnapshot(authState.UsageSnapshot)
		if authState.ExpiresAt != nil {
			credential.ExpiresAt = authState.ExpiresAt.UTC()
		}
		if authState.LastRefreshAt != nil {
			credential.LastRefresh = authState.LastRefreshAt.UTC()
		}
	}
	if credential.AccountID == "" && provider.Credential != nil && provider.Credential.BindingAccountID != nil {
		credential.AccountID = strings.TrimSpace(*provider.Credential.BindingAccountID)
	}

	// Legacy rows still carry non-sensitive summary fields in the old blob. Keep
	// reading them as fallback until Phase 6 removes the shadow column entirely.
	legacy, legacyErr := model.DecodeChatGPTProviderCredential(raw)
	if legacyErr != nil || legacy == nil {
		return credential, nil
	}

	if credential.AccountID == "" {
		credential.AccountID = strings.TrimSpace(legacy.AccountID)
	}
	if credential.Email == "" {
		credential.Email = strings.TrimSpace(legacy.Email)
	}
	if credential.PlanType == "" {
		credential.PlanType = firstNonEmptyString(strings.TrimSpace(legacy.PlanType), usagePlanType(legacy.Usage))
	}
	if credential.Usage == nil {
		credential.Usage = cloneProviderUsageSnapshot(legacy.Usage)
	}
	if credential.ExpiresAt.IsZero() {
		credential.ExpiresAt = legacy.ExpiresAt.UTC()
	}
	if credential.LastRefresh.IsZero() {
		credential.LastRefresh = legacy.LastRefresh.UTC()
	}
	if credential.OAuthIssuer == "" {
		credential.OAuthIssuer = strings.TrimSpace(legacy.OAuthIssuer)
	}
	if credential.OAuthClientID == "" {
		credential.OAuthClientID = strings.TrimSpace(legacy.OAuthClientID)
	}

	return credential, nil
}

func chatGPTCredentialData(provider *model.Provider) string {
	if provider == nil {
		return ""
	}
	if provider.Credential != nil && strings.TrimSpace(provider.Credential.SecretData) != "" {
		return provider.Credential.SecretData
	}
	return ""
}

func buildChatGPTAuthState(
	providerID string,
	current *model.ProviderAuthState,
	credential *model.ChatGPTProviderCredential,
	status ProviderAuthStatus,
	reason string,
	lastError string,
	usage *model.ProviderUsageSnapshot,
	transitionAt time.Time,
) *model.ProviderAuthState {
	state := model.NormalizeProviderAuthStateRecord(providerID, providerCredentialTypeChatGPT, current).Clone()
	previousStatus := state.Status
	previousReason := strings.TrimSpace(state.StatusReason)

	state.Status = status
	state.StatusReason = strings.TrimSpace(reason)
	state.LastError = strings.TrimSpace(lastError)

	if credential != nil {
		state.Email = strings.TrimSpace(credential.Email)
		state.AccountID = strings.TrimSpace(credential.AccountID)
		state.PlanType = firstNonEmptyString(
			usagePlanType(usage),
			strings.TrimSpace(credential.PlanType),
			usagePlanType(state.UsageSnapshot),
		)
		if !credential.ExpiresAt.IsZero() {
			expiresAt := credential.ExpiresAt.UTC()
			state.ExpiresAt = &expiresAt
		}
		if !credential.LastRefresh.IsZero() {
			lastRefresh := credential.LastRefresh.UTC()
			state.LastRefreshAt = &lastRefresh
		}
	}

	if usage != nil {
		state.UsageSnapshot = cloneProviderUsageSnapshot(usage)
		state.PlanType = firstNonEmptyString(usagePlanType(state.UsageSnapshot), strings.TrimSpace(state.PlanType))
	}

	if status == ProviderAuthStatusActive {
		state.StatusReason = ""
		state.LastError = ""
		state.RefreshFailCount = 0
		state.LastRefreshFailureAt = nil
	}

	if transitionAt.IsZero() {
		transitionAt = chatGPTLastRefreshAt(credential)
	}
	if !transitionAt.IsZero() &&
		(previousStatus != state.Status || previousReason != strings.TrimSpace(state.StatusReason)) {
		transition := transitionAt.UTC()
		state.LastTransitionAt = &transition
	}

	return model.NormalizeProviderAuthStateRecord(providerID, providerCredentialTypeChatGPT, state)
}

func applyProviderAuthState(provider *model.Provider, authState *model.ProviderAuthState) {
	if provider == nil {
		return
	}
	provider.AuthState = model.NormalizeProviderAuthStateRecord(
		provider.ID,
		model.NormalizeProviderCredentialType(provider.CredentialType),
		authState,
	)
}

func applyProviderCredential(provider *model.Provider, credential *model.ChatGPTProviderCredential) error {
	if provider == nil {
		return fmt.Errorf("provider is required")
	}
	if credential == nil {
		provider.Credential = nil
		return nil
	}

	secretData, err := encodeChatGPTCredentialSecret(credential)
	if err != nil {
		return err
	}

	record := &model.ProviderCredential{
		ProviderID: provider.ID,
		SecretData: secretData,
	}
	if accountID := strings.TrimSpace(credential.AccountID); accountID != "" {
		record.BindingAccountID = &accountID
	}
	if provider.Credential != nil && provider.Credential.Version > 0 {
		record.Version = provider.Credential.Version
	}
	provider.Credential = model.NormalizeProviderCredentialRecord(provider.ID, record)
	return nil
}

func chatGPTUsageSnapshot(credential *model.ChatGPTProviderCredential) *model.ProviderUsageSnapshot {
	if credential == nil {
		return nil
	}
	return credential.Usage
}

func chatGPTLastRefreshAt(credential *model.ChatGPTProviderCredential) time.Time {
	if credential == nil {
		return time.Time{}
	}
	return credential.LastRefresh
}

func usagePlanType(snapshot *model.ProviderUsageSnapshot) string {
	if snapshot == nil {
		return ""
	}
	return strings.TrimSpace(snapshot.PlanType)
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func cloneTimePtr(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := value.UTC()
	return &cloned
}
