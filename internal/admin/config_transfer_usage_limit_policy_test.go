package admin

import (
	"testing"

	"switch-a/internal/model"
)

func TestBuildProviderFromExport_PreservesInheritedUsageLimitPolicyStorage(t *testing.T) {
	t.Parallel()

	provider, ok := buildProviderFromExport(&ExportedProvider{
		ID:             "relay-provider",
		Name:           "Relay Provider",
		APIKey:         "relay-key",
		APITypes:       []ExportedAPIType{{APIType: "claude", BaseURL: "https://api.example.com"}},
		CredentialType: model.ProviderCredentialTypeAPIKey,
	}, nil)
	if !ok {
		t.Fatal("buildProviderFromExport returned ok=false")
	}
	if provider.UsageLimitPolicy != "" {
		t.Fatalf(
			"UsageLimitPolicy = %q, want empty inherit-default value",
			provider.UsageLimitPolicy,
		)
	}
	if provider.UsageLimitPolicyOrDefault() != model.ProviderUsageLimitPolicySwitchProvider {
		t.Fatalf("effective UsageLimitPolicy = %q, want %q", provider.UsageLimitPolicyOrDefault(), model.ProviderUsageLimitPolicySwitchProvider)
	}
}

func TestBuildExportedProvider_OmitsInheritedUsageLimitPolicy(t *testing.T) {
	t.Parallel()

	exported := buildExportedProvider(&model.Provider{
		ID:             "gpt-provider",
		Name:           "GPT Provider",
		AuthMode:       "bearer",
		CredentialType: model.ProviderCredentialTypeChatGPT,
		APITypes: []model.ProviderAPIType{{
			ProviderID: "gpt-provider",
			APIType:    "codex",
			BaseURL:    "https://chatgpt.com/backend-api/codex",
		}},
	})

	if exported.UsageLimitPolicy != "" {
		t.Fatalf(
			"UsageLimitPolicy = %q, want empty inherit-default value",
			exported.UsageLimitPolicy,
		)
	}
}

func TestBuildExportedProvider_PreservesExplicitUsageLimitPolicy(t *testing.T) {
	t.Parallel()

	exported := buildExportedProvider(&model.Provider{
		ID:               "relay-provider",
		Name:             "Relay Provider",
		AuthMode:         "bearer",
		CredentialType:   model.ProviderCredentialTypeAPIKey,
		UsageLimitPolicy: model.ProviderUsageLimitPolicySuspend,
		APITypes: []model.ProviderAPIType{{
			ProviderID: "relay-provider",
			APIType:    "claude",
			BaseURL:    "https://api.example.com",
		}},
	})

	if exported.UsageLimitPolicy != model.ProviderUsageLimitPolicySuspend {
		t.Fatalf("UsageLimitPolicy = %q, want %q", exported.UsageLimitPolicy, model.ProviderUsageLimitPolicySuspend)
	}
}

func TestValidateExportedProvider_InvalidUsageLimitPolicyWarns(t *testing.T) {
	t.Parallel()

	warnings := validateExportedProvider(&ExportedProvider{
		ID:               "relay-provider",
		Name:             "Relay Provider",
		APIKey:           "relay-key",
		APITypes:         []ExportedAPIType{{APIType: "claude", BaseURL: "https://api.example.com"}},
		UsageLimitPolicy: model.ProviderUsageLimitPolicy("drop"),
	})

	found := false
	for _, warning := range warnings {
		if warning == "Provider 'relay-provider' has invalid usage_limit_policy: drop" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("warnings = %v, want invalid usage_limit_policy warning", warnings)
	}
}
