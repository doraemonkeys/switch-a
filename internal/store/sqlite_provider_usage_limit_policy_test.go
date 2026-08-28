package store

import (
	"context"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/model"
)

func TestProviderUsageLimitPolicyPersistsAcrossCreateAndUpdate(t *testing.T) {
	t.Parallel()

	store := setupTestStore(t)
	ctx := context.Background()

	provider := &model.Provider{
		ID:               "policy-provider",
		Name:             "Policy Provider",
		UsageLimitPolicy: model.ProviderUsageLimitPolicySwitchProvider,
		Enabled:          true,
		APITypes: []model.ProviderAPIType{{
			ProviderID: "policy-provider",
			APIType:    "claude",
			BaseURL:    "https://api.example.com",
		}},
	}
	provider = credentialBackedTestProvider(t, store, provider)

	if err := store.CreateProvider(ctx, provider); err != nil {
		t.Fatalf("CreateProvider failed: %v", err)
	}

	created, err := store.GetProvider(ctx, provider.ID)
	if err != nil {
		t.Fatalf("GetProvider after create failed: %v", err)
	}
	if created.UsageLimitPolicy != model.ProviderUsageLimitPolicySwitchProvider {
		t.Fatalf(
			"UsageLimitPolicy after create = %q, want %q",
			created.UsageLimitPolicy,
			model.ProviderUsageLimitPolicySwitchProvider,
		)
	}

	provider.UsageLimitPolicy = model.ProviderUsageLimitPolicySuspend
	if err := store.UpdateProvider(ctx, provider); err != nil {
		t.Fatalf("UpdateProvider failed: %v", err)
	}

	updated, err := store.GetProvider(ctx, provider.ID)
	if err != nil {
		t.Fatalf("GetProvider after update failed: %v", err)
	}
	if updated.UsageLimitPolicy != model.ProviderUsageLimitPolicySuspend {
		t.Fatalf(
			"UsageLimitPolicy after update = %q, want %q",
			updated.UsageLimitPolicy,
			model.ProviderUsageLimitPolicySuspend,
		)
	}
}

func TestProviderUsageLimitPolicyDefaultRemainsStableAcrossSessionRebind(t *testing.T) {
	t.Parallel()

	store := setupTestStore(t)
	ctx := context.Background()

	provider := &model.Provider{
		ID:      "policy-default-provider",
		Name:    "Policy Default Provider",
		Enabled: true,
		APITypes: []model.ProviderAPIType{{
			ProviderID: "policy-default-provider",
			APIType:    "claude",
			BaseURL:    "https://api.example.com",
		}},
	}
	provider = credentialBackedTestProvider(t, store, provider)

	if err := store.CreateProvider(ctx, provider); err != nil {
		t.Fatalf("CreateProvider failed: %v", err)
	}

	created, err := store.GetProvider(ctx, provider.ID)
	if err != nil {
		t.Fatalf("GetProvider after create failed: %v", err)
	}
	if created.UsageLimitPolicy != "" {
		t.Fatalf("stored UsageLimitPolicy after create = %q, want empty inherit-default value", created.UsageLimitPolicy)
	}
	if created.UsageLimitPolicyOrDefault() != model.ProviderUsageLimitPolicySwitchProvider {
		t.Fatalf("effective UsageLimitPolicy after create = %q, want %q", created.UsageLimitPolicyOrDefault(), model.ProviderUsageLimitPolicySwitchProvider)
	}

	chatSession := &credentialsession.Session{
		ID:         "policy-default-chatgpt-session",
		Kind:       credentialsession.KindChatGPT,
		SecretData: `{"access_token":"token"}`,
		Version:    1,
		AuthState: credentialsession.AuthState{
			Status:    credentialsession.AuthStatusActive,
			AccountID: "account-1",
		},
	}
	if err := chatSession.SetSubject(mustAccountSubject(t, "account-1")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateCredentialSession(ctx, chatSession); err != nil {
		t.Fatalf("CreateCredentialSession failed: %v", err)
	}
	created.CredentialSessions[0].Credential.SessionID = chatSession.ID
	if err := store.UpdateProvider(ctx, created); err != nil {
		t.Fatalf("UpdateProvider failed: %v", err)
	}

	updated, err := store.GetProvider(ctx, provider.ID)
	if err != nil {
		t.Fatalf("GetProvider after update failed: %v", err)
	}
	if updated.UsageLimitPolicy != "" {
		t.Fatalf("stored UsageLimitPolicy after session rebind = %q, want empty inherit-default value", updated.UsageLimitPolicy)
	}
	if updated.UsageLimitPolicyOrDefault() != model.ProviderUsageLimitPolicySwitchProvider {
		t.Fatalf("effective UsageLimitPolicy after session rebind = %q, want %q", updated.UsageLimitPolicyOrDefault(), model.ProviderUsageLimitPolicySwitchProvider)
	}
}

func mustAccountSubject(t *testing.T, accountID string) credentialsession.Subject {
	t.Helper()
	subject, err := credentialsession.AccountSubject(accountID)
	if err != nil {
		t.Fatal(err)
	}
	return subject
}
