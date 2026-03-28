package store

import (
	"context"
	"testing"

	"switch-a/internal/model"
)

func TestProviderUsageLimitPolicyPersistsAcrossCreateAndUpdate(t *testing.T) {
	t.Parallel()

	store := setupTestStore(t)
	ctx := context.Background()

	provider := &model.Provider{
		ID:               "policy-provider",
		Name:             "Policy Provider",
		APIKey:           "key",
		CredentialType:   model.ProviderCredentialTypeAPIKey,
		UsageLimitPolicy: model.ProviderUsageLimitPolicySwitchProvider,
		Enabled:          true,
		APITypes: []model.ProviderAPIType{{
			ProviderID: "policy-provider",
			APIType:    "claude",
			BaseURL:    "https://api.example.com",
		}},
	}

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

func TestProviderUsageLimitPolicyDefaultRemainsCredentialDerivedAcrossUpdates(t *testing.T) {
	t.Parallel()

	store := setupTestStore(t)
	ctx := context.Background()

	provider := &model.Provider{
		ID:             "policy-default-provider",
		Name:           "Policy Default Provider",
		APIKey:         "key",
		CredentialType: model.ProviderCredentialTypeAPIKey,
		Enabled:        true,
		APITypes: []model.ProviderAPIType{{
			ProviderID: "policy-default-provider",
			APIType:    "claude",
			BaseURL:    "https://api.example.com",
		}},
	}

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

	created.CredentialType = model.ProviderCredentialTypeChatGPT
	if err := store.UpdateProvider(ctx, created); err != nil {
		t.Fatalf("UpdateProvider failed: %v", err)
	}

	updated, err := store.GetProvider(ctx, provider.ID)
	if err != nil {
		t.Fatalf("GetProvider after update failed: %v", err)
	}
	if updated.UsageLimitPolicy != "" {
		t.Fatalf("stored UsageLimitPolicy after credential_type update = %q, want empty inherit-default value", updated.UsageLimitPolicy)
	}
	if updated.UsageLimitPolicyOrDefault() != model.ProviderUsageLimitPolicySuspend {
		t.Fatalf("effective UsageLimitPolicy after credential_type update = %q, want %q", updated.UsageLimitPolicyOrDefault(), model.ProviderUsageLimitPolicySuspend)
	}
}
