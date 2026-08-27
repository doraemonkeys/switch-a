package admin

import (
	"slices"
	"testing"
)

func TestValidateExportedProvider_MalformedURL(t *testing.T) {
	provider := &ExportedProvider{
		ID: "p1", Name: "Test", AuthMode: "bearer",
		APITypes: []ExportedAPIType{{APIType: "claude", BaseURL: "not-a-url", CredentialSessionID: "session-1"}},
	}
	warnings := validateExportedProvider(provider)
	if !slices.Contains(warnings, "Provider 'p1' has malformed base_url for api_type: claude") {
		t.Fatalf("warnings = %v", warnings)
	}
}

func TestValidateExportedProvider_RequiresSessionReference(t *testing.T) {
	provider := &ExportedProvider{
		ID: "p1", Name: "Test", AuthMode: "bearer",
		APITypes: []ExportedAPIType{{APIType: "claude", BaseURL: "https://api.example.com"}},
	}
	warnings := validateExportedProvider(provider)
	if !slices.Contains(warnings, "Provider 'p1' has no credential_session_id for api_type: claude") {
		t.Fatalf("warnings = %v", warnings)
	}
	if imported, ok := buildProviderFromExport(provider, nil); ok || imported != nil {
		t.Fatalf("buildProviderFromExport() = (%#v, %v), want rejection", imported, ok)
	}
}

func TestBuildProviderFromExport_AcceptsExplicitSessionReference(t *testing.T) {
	provider, ok := buildProviderFromExport(&ExportedProvider{
		ID: "p1", Name: "Test", AuthMode: "bearer", Weight: 1,
		APITypes: []ExportedAPIType{{APIType: "codex", BaseURL: "https://api.example.com", CredentialSessionID: "session-1"}},
	}, nil)
	if !ok {
		t.Fatal("buildProviderFromExport() rejected current contract")
	}
	snapshot, found := provider.CredentialSessionForAPIType("codex")
	if !found || snapshot.SessionID != "session-1" {
		t.Fatalf("credential session = %#v", snapshot)
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
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			gotKey, gotValue := migrateConfigKey(testCase.key, testCase.value)
			if gotKey != testCase.wantKey || gotValue != testCase.wantValue {
				t.Fatalf("migrateConfigKey(%q, %q) = (%q, %q), want (%q, %q)", testCase.key, testCase.value, gotKey, gotValue, testCase.wantKey, testCase.wantValue)
			}
		})
	}
}
