package store

import (
	"path/filepath"
	"testing"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/store/migrationtest"
)

func TestNewSQLiteStorePreservesLegacyUsageLimitPolicySemantics(t *testing.T) {
	t.Parallel()

	databasePath := filepath.Join(t.TempDir(), "legacy-usage-limit-policy.db")
	if err := migrationtest.CreateLegacyCredentialDatabase(databasePath); err != nil {
		t.Fatalf("create legacy database: %v", err)
	}

	storage, err := NewSQLiteStore(databasePath, internal.RealClock{}, nil)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := storage.Close(); closeErr != nil {
			t.Errorf("close migrated store: %v", closeErr)
		}
	})

	if storage.db.Migrator().HasColumn("providers", "credential_type") {
		t.Fatal("credential_type remains after credential-session migration")
	}

	chatGPTProviderIDs := []string{
		migrationtest.ChatGPTProviderID,
		migrationtest.DuplicateAccountOwnerID,
		migrationtest.DuplicateAccountRepairID,
	}
	var chatGPTProviders []model.Provider
	if err := storage.db.Where("id IN ?", chatGPTProviderIDs).Order("id ASC").Find(&chatGPTProviders).Error; err != nil {
		t.Fatalf("load migrated ChatGPT providers: %v", err)
	}
	if len(chatGPTProviders) != len(chatGPTProviderIDs) {
		t.Fatalf("migrated ChatGPT provider count = %d, want %d", len(chatGPTProviders), len(chatGPTProviderIDs))
	}
	for _, provider := range chatGPTProviders {
		if provider.UsageLimitPolicy != model.ProviderUsageLimitPolicySuspend {
			t.Errorf("provider %q stored usage-limit policy = %q, want %q", provider.ID, provider.UsageLimitPolicy, model.ProviderUsageLimitPolicySuspend)
		}
		if provider.UsageLimitPolicyOrDefault() != model.ProviderUsageLimitPolicySuspend {
			t.Errorf("provider %q effective usage-limit policy = %q, want %q", provider.ID, provider.UsageLimitPolicyOrDefault(), model.ProviderUsageLimitPolicySuspend)
		}
	}

	var staticProvider model.Provider
	if err := storage.db.First(&staticProvider, "id = ?", migrationtest.StaticProviderID).Error; err != nil {
		t.Fatalf("load migrated static provider: %v", err)
	}
	if staticProvider.UsageLimitPolicy != "" {
		t.Fatalf("static provider stored usage-limit policy = %q, want inherited route-target default", staticProvider.UsageLimitPolicy)
	}
	if staticProvider.UsageLimitPolicyOrDefault() != model.ProviderUsageLimitPolicySwitchProvider {
		t.Fatalf("static provider effective usage-limit policy = %q, want %q", staticProvider.UsageLimitPolicyOrDefault(), model.ProviderUsageLimitPolicySwitchProvider)
	}
}
