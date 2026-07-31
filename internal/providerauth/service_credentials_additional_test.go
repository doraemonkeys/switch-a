package providerauth

import (
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"
)

func TestShouldPreferChatGPTCredentialAndPruneRecentRefreshes(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 30, 9, 0, 0, 0, time.UTC)
	current := &model.ChatGPTProviderCredential{
		AccessToken:  "current-access",
		RefreshToken: "current-refresh",
		AccountID:    "acct-1",
		LastRefresh:  now,
		ExpiresAt:    now.Add(30 * time.Minute),
	}

	if shouldPreferChatGPTCredential(nil, current) {
		t.Fatal("nil candidate should not be preferred")
	}
	if shouldPreferChatGPTCredential(&model.ChatGPTProviderCredential{AccessToken: "missing-refresh"}, current) {
		t.Fatal("not-ready candidate should not be preferred")
	}
	if !shouldPreferChatGPTCredential(current, nil) {
		t.Fatal("ready candidate should replace a missing current credential")
	}
	if shouldPreferChatGPTCredential(current, &model.ChatGPTProviderCredential{AccountID: "acct-2"}) {
		t.Fatal("ready candidate should not replace an incomplete credential for another account")
	}
	if shouldPreferChatGPTCredential(&model.ChatGPTProviderCredential{
		AccessToken:  "other-account-access",
		RefreshToken: "other-account-refresh",
		AccountID:    "acct-2",
		LastRefresh:  now.Add(time.Minute),
		ExpiresAt:    now.Add(time.Hour),
	}, current) {
		t.Fatal("newer credential for another account should not be preferred")
	}
	if !shouldPreferChatGPTCredential(&model.ChatGPTProviderCredential{
		AccessToken:  "newer-access",
		RefreshToken: "newer-refresh",
		AccountID:    "acct-1",
		LastRefresh:  now.Add(time.Minute),
		ExpiresAt:    now.Add(20 * time.Minute),
	}, current) {
		t.Fatal("newer refresh timestamp should be preferred")
	}
	if !shouldPreferChatGPTCredential(&model.ChatGPTProviderCredential{
		AccessToken:  "same-refresh-newer-expiry",
		RefreshToken: "different-refresh",
		AccountID:    "acct-1",
		LastRefresh:  now,
		ExpiresAt:    now.Add(time.Hour),
	}, current) {
		t.Fatal("equal refresh timestamp with later expiry should be preferred")
	}
	if !shouldPreferChatGPTCredential(&model.ChatGPTProviderCredential{
		AccessToken:  "rotated-expiry",
		RefreshToken: current.RefreshToken,
		AccountID:    "acct-1",
		LastRefresh:  now.Add(-time.Minute),
		ExpiresAt:    now.Add(2 * time.Hour),
	}, current) {
		t.Fatal("same refresh token with later expiry should be preferred")
	}
	if shouldPreferChatGPTCredential(&model.ChatGPTProviderCredential{
		AccessToken:  "older-access",
		RefreshToken: "older-refresh",
		AccountID:    "acct-1",
		LastRefresh:  now.Add(-time.Minute),
		ExpiresAt:    now.Add(20 * time.Minute),
	}, current) {
		t.Fatal("older credential without a stronger signal should not be preferred")
	}

	service := NewService(Config{Clock: fixedClock{now: now}})
	service.recentChatGPTRefreshes["expired"] = recentChatGPTRefresh{
		credential: current,
		expiresAt:  now.Add(-time.Second),
	}
	service.recentChatGPTRefreshes["fresh"] = recentChatGPTRefresh{
		credential: current,
		expiresAt:  now.Add(time.Second),
	}

	service.pruneRecentChatGPTRefreshesLocked(now)
	if _, ok := service.recentChatGPTRefreshes["expired"]; ok {
		t.Fatal("expired refresh entry was not pruned")
	}
	if _, ok := service.recentChatGPTRefreshes["fresh"]; !ok {
		t.Fatal("fresh refresh entry was pruned unexpectedly")
	}
}

func TestReuseRecentChatGPTRefresh_DoesNotCrossAccountsWhenProviderIDIsReused(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	service := NewService(Config{Clock: fixedClock{now: now}})
	service.storeRecentChatGPTRefresh("reused-provider", &model.ChatGPTProviderCredential{
		AccessToken:  "deleted-account-access",
		RefreshToken: "deleted-account-refresh",
		AccountID:    "deleted-account",
		LastRefresh:  now,
		ExpiresAt:    now.Add(2 * time.Hour),
	})
	recreated := &model.ChatGPTProviderCredential{
		AccessToken:  "recreated-account-access",
		RefreshToken: "recreated-account-refresh",
		AccountID:    "recreated-account",
		LastRefresh:  now.Add(-time.Hour),
		ExpiresAt:    now.Add(time.Hour),
	}

	got := service.reuseRecentChatGPTRefresh("reused-provider", recreated)
	if got.AccountID != recreated.AccountID || got.RefreshToken != recreated.RefreshToken {
		t.Fatalf("reused credential = %#v, want recreated provider account", got)
	}
}

func TestApplyStoredChatGPTCredentialValidatesInputsAndHydratesProviderState(t *testing.T) {
	t.Parallel()

	if err := applyStoredChatGPTCredential(nil, &storedChatGPTCredential{}); err == nil || err.Error() != "provider is required" {
		t.Fatalf("applyStoredChatGPTCredential(nil, credential) error = %v, want provider is required", err)
	}

	provider := &model.Provider{ID: "provider-nil-credential"}
	if err := applyStoredChatGPTCredential(provider, nil); err == nil || err.Error() != "chatgpt credential is required" {
		t.Fatalf("applyStoredChatGPTCredential(provider, nil) error = %v, want chatgpt credential is required", err)
	}

	lastRefresh := time.Date(2026, time.March, 30, 8, 45, 0, 0, time.UTC)
	expiresAt := lastRefresh.Add(2 * time.Hour)
	fetchedAt := lastRefresh.Add(5 * time.Minute)
	readyProvider := &model.Provider{
		ID: "provider-ready",
		Credential: &model.ProviderCredential{
			Version: 7,
		},
	}
	readyCredential := &storedChatGPTCredential{
		ChatGPTProviderCredential: model.ChatGPTProviderCredential{
			AccessToken:   "access-token",
			RefreshToken:  "refresh-token",
			IDToken:       "id-token",
			OAuthIssuer:   " https://issuer.example.com ",
			OAuthClientID: " client-id ",
			AccountID:     "acct-ready",
			Email:         " ready@example.com ",
			PlanType:      " team ",
			Usage: &model.ProviderUsageSnapshot{
				FetchedAt: &fetchedAt,
				PlanType:  "enterprise",
			},
			LastRefresh: lastRefresh,
			ExpiresAt:   expiresAt,
		},
		AuthReason: " invalid_grant ",
		LastError:  " should clear ",
	}

	if err := applyStoredChatGPTCredential(readyProvider, readyCredential); err != nil {
		t.Fatalf("applyStoredChatGPTCredential(ready) error = %v, want nil", err)
	}
	if readyProvider.CredentialType != providerCredentialTypeChatGPT {
		t.Fatalf("CredentialType = %q, want %q", readyProvider.CredentialType, providerCredentialTypeChatGPT)
	}
	if readyProvider.Credential == nil {
		t.Fatal("Credential = nil, want persisted secret")
	}
	if readyProvider.Credential.Version != 7 {
		t.Fatalf("Credential.Version = %d, want 7", readyProvider.Credential.Version)
	}
	if readyProvider.Credential.BindingAccountID == nil || *readyProvider.Credential.BindingAccountID != "acct-ready" {
		t.Fatalf("BindingAccountID = %#v, want acct-ready", readyProvider.Credential.BindingAccountID)
	}

	secret, err := decodeChatGPTCredentialSecret(readyProvider.Credential.SecretData)
	if err != nil {
		t.Fatalf("decodeChatGPTCredentialSecret returned error: %v", err)
	}
	if secret.AccessToken != "access-token" || secret.RefreshToken != "refresh-token" || secret.IDToken != "id-token" {
		t.Fatalf("secret = %#v, want access/refresh/id tokens preserved", secret)
	}
	if secret.OAuthIssuer != "https://issuer.example.com" || secret.OAuthClientID != "client-id" {
		t.Fatalf("secret oauth fields = (%q, %q), want trimmed issuer/client id", secret.OAuthIssuer, secret.OAuthClientID)
	}

	if readyProvider.AuthState == nil {
		t.Fatal("AuthState = nil, want hydrated auth snapshot")
	}
	if readyProvider.AuthState.Status != ProviderAuthStatusActive {
		t.Fatalf("AuthState.Status = %q, want %q", readyProvider.AuthState.Status, ProviderAuthStatusActive)
	}
	if readyProvider.AuthState.StatusReason != "" || readyProvider.AuthState.LastError != "" {
		t.Fatalf("AuthState retained active-state failure markers: %#v", readyProvider.AuthState)
	}
	if readyProvider.AuthState.Email != "ready@example.com" || readyProvider.AuthState.AccountID != "acct-ready" {
		t.Fatalf("AuthState identity = (%q, %q), want trimmed ready identity", readyProvider.AuthState.Email, readyProvider.AuthState.AccountID)
	}
	if readyProvider.AuthState.PlanType != "enterprise" {
		t.Fatalf("AuthState.PlanType = %q, want %q", readyProvider.AuthState.PlanType, "enterprise")
	}
	if readyProvider.AuthState.UsageSnapshot == nil || readyProvider.AuthState.UsageSnapshot.PlanType != "enterprise" {
		t.Fatalf("AuthState.UsageSnapshot = %#v, want usage snapshot preserved", readyProvider.AuthState.UsageSnapshot)
	}
	if readyProvider.AuthState.LastRefreshAt == nil || !readyProvider.AuthState.LastRefreshAt.Equal(lastRefresh) {
		t.Fatalf("AuthState.LastRefreshAt = %#v, want %v", readyProvider.AuthState.LastRefreshAt, lastRefresh)
	}
	if readyProvider.AuthState.ExpiresAt == nil || !readyProvider.AuthState.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("AuthState.ExpiresAt = %#v, want %v", readyProvider.AuthState.ExpiresAt, expiresAt)
	}

	incompleteProvider := &model.Provider{ID: "provider-incomplete"}
	incompleteCredential := &storedChatGPTCredential{
		ChatGPTProviderCredential: model.ChatGPTProviderCredential{
			AccessToken: "access-only",
		},
		AuthReason: " login_required ",
		LastError:  " reauthenticate ",
	}

	if err := applyStoredChatGPTCredential(incompleteProvider, incompleteCredential); err != nil {
		t.Fatalf("applyStoredChatGPTCredential(incomplete) error = %v, want nil", err)
	}
	if incompleteProvider.AuthState == nil {
		t.Fatal("AuthState = nil for incomplete credential, want snapshot")
	}
	if incompleteProvider.AuthState.Status != ProviderAuthStatusNotConnected {
		t.Fatalf("AuthState.Status = %q, want %q", incompleteProvider.AuthState.Status, ProviderAuthStatusNotConnected)
	}
	if incompleteProvider.AuthState.StatusReason != ProviderAuthReasonLoginRequired {
		t.Fatalf("AuthState.StatusReason = %q, want %q", incompleteProvider.AuthState.StatusReason, ProviderAuthReasonLoginRequired)
	}
	if incompleteProvider.AuthState.LastError != "reauthenticate" {
		t.Fatalf("AuthState.LastError = %q, want %q", incompleteProvider.AuthState.LastError, "reauthenticate")
	}
}

func TestSnapshotChatGPTCredentialIdentityUsesCredentialFieldsAndFallbacks(t *testing.T) {
	t.Parallel()

	fallback := snapshotChatGPTCredentialIdentity(nil, " https://issuer.example.com ", " fallback-client ")
	if fallback.OAuthIssuer != "https://issuer.example.com" {
		t.Fatalf("fallback.OAuthIssuer = %q, want %q", fallback.OAuthIssuer, "https://issuer.example.com")
	}
	if fallback.OAuthClientID != "fallback-client" {
		t.Fatalf("fallback.OAuthClientID = %q, want %q", fallback.OAuthClientID, "fallback-client")
	}

	defaulted := snapshotChatGPTCredentialIdentity(nil, " ", " ")
	if defaulted.OAuthIssuer != defaultOAuthIssuer {
		t.Fatalf("defaulted.OAuthIssuer = %q, want %q", defaulted.OAuthIssuer, defaultOAuthIssuer)
	}

	expiresAt := time.Date(2026, time.March, 31, 12, 0, 0, 0, time.UTC)
	snapshot := snapshotChatGPTCredentialIdentity(&model.ChatGPTProviderCredential{
		IDToken:       "id-token",
		OAuthIssuer:   " ",
		OAuthClientID: "",
		AccountID:     "acct-1",
		Email:         "user@example.com",
		PlanType:      "team",
		ExpiresAt:     expiresAt,
	}, " https://issuer.example.com ", " fallback-client ")

	if snapshot.IDToken != "id-token" {
		t.Fatalf("snapshot.IDToken = %q, want %q", snapshot.IDToken, "id-token")
	}
	if snapshot.OAuthIssuer != "https://issuer.example.com" {
		t.Fatalf("snapshot.OAuthIssuer = %q, want %q", snapshot.OAuthIssuer, "https://issuer.example.com")
	}
	if snapshot.OAuthClientID != "fallback-client" {
		t.Fatalf("snapshot.OAuthClientID = %q, want %q", snapshot.OAuthClientID, "fallback-client")
	}
	if snapshot.AccountID != "acct-1" || snapshot.Email != "user@example.com" || snapshot.PlanType != "team" {
		t.Fatalf("snapshot identity = %#v, want credential identity fields copied", snapshot)
	}
	if !snapshot.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("snapshot.ExpiresAt = %v, want %v", snapshot.ExpiresAt, expiresAt)
	}
}
