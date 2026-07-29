package providerauth

import (
	"strings"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/model"
)

func TestAuthStateHelpers_HandleNilInputs(t *testing.T) {
	if snapshot := providerAuthStateSnapshot(nil); snapshot != nil {
		t.Fatalf("providerAuthStateSnapshot(nil) = %#v, want nil", snapshot)
	}
	if got := chatGPTCredentialData(nil); got != "" {
		t.Fatalf("chatGPTCredentialData(nil) = %q, want empty string", got)
	}

	applyProviderAuthState(nil, &model.ProviderAuthState{ProviderID: "provider-gpt"})

	if err := applyProviderCredential(nil, &model.ChatGPTProviderCredential{}); err == nil {
		t.Fatal("applyProviderCredential(nil, credential) succeeded, want error")
	} else if !strings.Contains(err.Error(), "provider is required") {
		t.Fatalf("applyProviderCredential(nil, credential) error = %q, want provider required", err)
	}
}

func TestApplyProviderCredential_ClearsStoredCredential(t *testing.T) {
	provider := &model.Provider{
		ID:             "provider-gpt",
		CredentialType: model.ProviderCredentialTypeChatGPT,
		Credential: &model.ProviderCredential{
			ProviderID: "provider-gpt",
			SecretData: `{"access_token":"stale"}`,
			Version:    3,
		},
	}

	if err := applyProviderCredential(provider, nil); err != nil {
		t.Fatalf("applyProviderCredential(provider, nil): %v", err)
	}
	if provider.Credential != nil {
		t.Fatalf("provider.Credential = %#v, want nil", provider.Credential)
	}
}

func TestDecodeProviderChatGPTCredential_IgnoresLegacyDecodeErrors(t *testing.T) {
	rawSecretOnly := `{"access_token":"access-token","refresh_token":"refresh-token","id_token":"id-token","expires_at":{"invalid":true}}`
	provider := &model.Provider{
		ID:             "provider-gpt",
		CredentialType: model.ProviderCredentialTypeChatGPT,
		Credential: &model.ProviderCredential{
			ProviderID: "provider-gpt",
			SecretData: rawSecretOnly,
		},
	}

	credential, err := DecodeProviderChatGPTCredential(provider)
	if err != nil {
		t.Fatalf("DecodeProviderChatGPTCredential() error = %v, want nil", err)
	}
	if credential == nil {
		t.Fatal("DecodeProviderChatGPTCredential() = nil, want credential")
	}
	if credential.AccessToken != "access-token" {
		t.Fatalf("AccessToken = %q, want %q", credential.AccessToken, "access-token")
	}
	if credential.RefreshToken != "refresh-token" {
		t.Fatalf("RefreshToken = %q, want %q", credential.RefreshToken, "refresh-token")
	}
	if credential.IDToken != "id-token" {
		t.Fatalf("IDToken = %q, want %q", credential.IDToken, "id-token")
	}
}
