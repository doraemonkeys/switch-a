package migration

import (
	"testing"

	"github.com/doraemonkeys/switch-a/internal/model"
)

func TestMigrateProviderUsageLimitPolicyStorage_BackfillsDerivedDefaults(t *testing.T) {
	t.Parallel()

	db := setupProviderStateMigrationDB(t)
	providers := []model.Provider{
		{
			ID:               "relay-default",
			Name:             "Relay Default",
			APIKey:           "key",
			CredentialType:   model.ProviderCredentialTypeAPIKey,
			UsageLimitPolicy: model.ProviderUsageLimitPolicySwitchProvider,
			APITypes:         []model.ProviderAPIType{{ProviderID: "relay-default", APIType: "claude", BaseURL: "https://api.example.com"}},
			Enabled:          true,
		},
		{
			ID:               "gpt-default",
			Name:             "GPT Default",
			CredentialType:   model.ProviderCredentialTypeChatGPT,
			UsageLimitPolicy: model.ProviderUsageLimitPolicySuspend,
			APITypes:         []model.ProviderAPIType{{ProviderID: "gpt-default", APIType: "codex", BaseURL: "https://chatgpt.com/backend-api/codex"}},
			Enabled:          true,
		},
		{
			ID:               "gpt-explicit",
			Name:             "GPT Explicit",
			CredentialType:   model.ProviderCredentialTypeChatGPT,
			UsageLimitPolicy: model.ProviderUsageLimitPolicySwitchProvider,
			APITypes:         []model.ProviderAPIType{{ProviderID: "gpt-explicit", APIType: "codex", BaseURL: "https://chatgpt.com/backend-api/codex"}},
			Enabled:          true,
		},
	}
	for i := range providers {
		if err := db.Omit("Credential", "AuthState").Create(&providers[i]).Error; err != nil {
			t.Fatalf("create provider %q: %v", providers[i].ID, err)
		}
	}

	if err := MigrateProviderUsageLimitPolicyStorage(db); err != nil {
		t.Fatalf("MigrateProviderUsageLimitPolicyStorage: %v", err)
	}

	var relayDefault model.Provider
	if err := db.First(&relayDefault, "id = ?", "relay-default").Error; err != nil {
		t.Fatalf("read relay-default: %v", err)
	}
	if relayDefault.UsageLimitPolicy != "" {
		t.Fatalf("relay-default UsageLimitPolicy = %q, want empty inherit-default value", relayDefault.UsageLimitPolicy)
	}

	var gptDefault model.Provider
	if err := db.First(&gptDefault, "id = ?", "gpt-default").Error; err != nil {
		t.Fatalf("read gpt-default: %v", err)
	}
	if gptDefault.UsageLimitPolicy != "" {
		t.Fatalf("gpt-default UsageLimitPolicy = %q, want empty inherit-default value", gptDefault.UsageLimitPolicy)
	}

	var gptExplicit model.Provider
	if err := db.First(&gptExplicit, "id = ?", "gpt-explicit").Error; err != nil {
		t.Fatalf("read gpt-explicit: %v", err)
	}
	if gptExplicit.UsageLimitPolicy != model.ProviderUsageLimitPolicySwitchProvider {
		t.Fatalf("gpt-explicit UsageLimitPolicy = %q, want %q", gptExplicit.UsageLimitPolicy, model.ProviderUsageLimitPolicySwitchProvider)
	}
}
