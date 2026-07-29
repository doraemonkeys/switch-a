package admin

import (
	"testing"

	"github.com/doraemonkeys/switch-a/internal/model"
)

func TestBuildProviderFromExport_InvalidMetadataRejected(t *testing.T) {
	t.Parallel()

	if provider, ok := buildProviderFromExport(&ExportedProvider{
		ID:             "provider-1",
		Name:           "Provider",
		CredentialType: "invalid",
		Enabled:        true,
	}, map[string]bool{}); ok || provider != nil {
		t.Fatalf("buildProviderFromExport(invalid credential type) = (%#v, %v), want (nil, false)", provider, ok)
	}

	if provider, ok := buildProviderFromExport(&ExportedProvider{
		ID:               "provider-1",
		Name:             "Provider",
		CredentialType:   model.ProviderCredentialTypeAPIKey,
		UsageLimitPolicy: "invalid",
		Enabled:          true,
	}, map[string]bool{}); ok || provider != nil {
		t.Fatalf("buildProviderFromExport(invalid usage policy) = (%#v, %v), want (nil, false)", provider, ok)
	}
}

func TestNormalizeProviderScopeFromExport_InvalidScopeFallsBackToAny(t *testing.T) {
	t.Parallel()

	if got := normalizeProviderScopeFromExport("invalid"); got != model.ScopeAny {
		t.Fatalf("normalizeProviderScopeFromExport(invalid) = %q, want %q", got, model.ScopeAny)
	}
	if got := normalizeProviderScopeFromExport(string(model.ScopeVendor)); got != model.ScopeVendor {
		t.Fatalf("normalizeProviderScopeFromExport(valid) = %q, want %q", got, model.ScopeVendor)
	}
}

func TestValidateImportedProvider_RequiresAnyAPIKey(t *testing.T) {
	t.Parallel()

	provider := &model.Provider{
		ID:     "provider-1",
		APIKey: "",
		APITypes: []model.ProviderAPIType{
			{ProviderID: "provider-1", APIType: "codex", APIKey: ""},
		},
	}
	exported := &ExportedProvider{
		ID:             "provider-1",
		CredentialType: model.ProviderCredentialTypeAPIKey,
		APITypes: []ExportedAPIType{
			{APIType: "codex"},
		},
	}
	if validateImportedProvider(provider, exported, model.ProviderCredentialTypeAPIKey) {
		t.Fatal("validateImportedProvider() = true, want false when provider and api_type keys are blank")
	}
}

func TestValidateImportedChatGPTProvider_RejectsMissingAuthState(t *testing.T) {
	t.Parallel()

	if validateImportedChatGPTProvider(&model.Provider{}, &ExportedProvider{}) {
		t.Fatal("validateImportedChatGPTProvider(nil auth state) = true, want false")
	}
}

func TestCanonicalProviderImportExportJSON_InvalidProviderReturnsFalse(t *testing.T) {
	t.Parallel()

	payload, ok := canonicalProviderImportExportJSON(&ExportedProvider{
		ID:             "provider-1",
		Name:           "Provider",
		CredentialType: "invalid",
		Enabled:        true,
	}, map[string]bool{})
	if ok || payload != nil {
		t.Fatalf("canonicalProviderImportExportJSON(invalid) = (%v, %v), want (nil, false)", payload, ok)
	}
}

func TestBuildProviderCredentialFromExport_BlankSecretReturnsNil(t *testing.T) {
	t.Parallel()

	credential := buildProviderCredentialFromExport(&ExportedProvider{
		ID: "provider-1",
		Credential: &ExportedProviderCredential{
			SecretData: "   ",
		},
	}, model.ProviderCredentialTypeChatGPT)
	if credential != nil {
		t.Fatalf("buildProviderCredentialFromExport(blank secret) = %#v, want nil", credential)
	}
}

func TestGroupAndRoutingPolicyImportDiffers_NilAndInvalidInputs(t *testing.T) {
	t.Parallel()

	if groupImportDiffers(&ExportedGroup{}, &model.Group{ID: "group-1"}) {
		t.Fatal("groupImportDiffers(invalid imported group) = true, want false")
	}

	policy := &model.RoutingPolicy{APIType: "codex"}
	if !routingPolicyImportDiffers(nil, policy) {
		t.Fatal("routingPolicyImportDiffers(nil, policy) = false, want true")
	}
	if routingPolicyImportDiffers(nil, nil) {
		t.Fatal("routingPolicyImportDiffers(nil, nil) = true, want false")
	}
}
