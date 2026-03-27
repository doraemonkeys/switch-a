package admin

import (
	"encoding/json"
	"testing"

	"switch-a/internal/model"
)

func TestValidateExportedProvider_MalformedURL(t *testing.T) {
	p := &ExportedProvider{
		ID:       "p1",
		Name:     "Test",
		APITypes: []ExportedAPIType{{APIType: "claude", BaseURL: "not-a-url"}},
	}

	warnings := validateExportedProvider(p)
	found := false
	for _, w := range warnings {
		if w == "Provider 'p1' has malformed base_url for api_type: claude" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected malformed base_url warning, got: %v", warnings)
	}
}

func TestValidateExportedProvider_ChatGPTDoesNotRequireAPIKey(t *testing.T) {
	credentialData, err := json.Marshal(model.ChatGPTProviderCredential{
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		IDToken:      "id-token",
		AccountID:    "acct_test",
	})
	if err != nil {
		t.Fatalf("marshal credentialData: %v", err)
	}

	p := &ExportedProvider{
		ID:             "gpt",
		Name:           "GPT Provider",
		CredentialType: model.ProviderCredentialTypeChatGPT,
		Credential: &ExportedProviderCredential{
			SecretData:       string(credentialData),
			BindingAccountID: strPtr("acct_test"),
		},
		AuthState: &ExportedProviderAuthState{
			Status: model.ProviderAuthStatusActive,
		},
	}

	warnings := validateExportedProvider(p)
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none", warnings)
	}
}

func TestValidateExportedProvider_WhitespaceKeysWarnAsMissing(t *testing.T) {
	p := &ExportedProvider{
		ID:     "p1",
		Name:   "Test",
		APIKey: "   ",
		APITypes: []ExportedAPIType{{
			APIType: "claude",
			BaseURL: "https://api.example.com",
			APIKey:  "   ",
		}},
	}

	warnings := validateExportedProvider(p)
	found := false
	for _, w := range warnings {
		if w == "Provider 'p1' has no api_key for api_type: claude" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected missing api_key warning, got: %v", warnings)
	}
}

func TestMigrateConfigKey(t *testing.T) {
	tests := []struct {
		name      string
		key       string
		value     string
		wantKey   string
		wantValue string
	}{
		{"legacy true", "sticky_enabled", "true", "sticky_mode", "api_type"},
		{"legacy one", "sticky_enabled", "1", "sticky_mode", "api_type"},
		{"legacy false", "sticky_enabled", "false", "sticky_mode", "off"},
		{"legacy zero", "sticky_enabled", "0", "sticky_mode", "off"},
		{"legacy uppercase", "sticky_enabled", "TRUE", "sticky_mode", "api_type"},
		{"legacy invalid", "sticky_enabled", "maybe", "sticky_enabled", "maybe"},
		{"legacy max retries", "max_retries", "6", "global_max_attempts", "6"},
		{"other key", "sticky_ttl", "300", "sticky_ttl", "300"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotKey, gotValue := migrateConfigKey(tt.key, tt.value)
			if gotKey != tt.wantKey || gotValue != tt.wantValue {
				t.Errorf("migrateConfigKey(%q, %q) = (%q, %q), want (%q, %q)", tt.key, tt.value, gotKey, gotValue, tt.wantKey, tt.wantValue)
			}
		})
	}
}
