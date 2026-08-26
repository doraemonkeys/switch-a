package providerauth

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"
)

func pausedChatGPTProviderForCodexExport(t *testing.T) *model.Provider {
	t.Helper()
	secret, err := model.EncodeChatGPTProviderSecret(&model.ChatGPTProviderSecret{
		AccessToken:  " access-token ",
		RefreshToken: " refresh-token ",
		IDToken:      " id-token ",
	})
	if err != nil {
		t.Fatalf("encode credential secret: %v", err)
	}
	lastRefresh := time.Date(2026, time.August, 26, 8, 15, 0, 0, time.FixedZone("SGT", 8*60*60))
	return &model.Provider{
		ID:             "gpt-paused",
		Enabled:        false,
		CredentialType: model.ProviderCredentialTypeChatGPT,
		Credential: &model.ProviderCredential{
			ProviderID: "gpt-paused",
			SecretData: secret,
		},
		AuthState: &model.ProviderAuthState{
			ProviderID:    "gpt-paused",
			Status:        model.ProviderAuthStatusActive,
			AccountID:     " account-123 ",
			LastRefreshAt: &lastRefresh,
		},
	}
}

func TestBuildCodexAuthDocument(t *testing.T) {
	provider := pausedChatGPTProviderForCodexExport(t)

	document, err := BuildCodexAuthDocument(provider)
	if err != nil {
		t.Fatalf("BuildCodexAuthDocument() error = %v", err)
	}
	if document.OpenAIAPIKey != nil {
		t.Fatalf("OPENAI_API_KEY = %v, want nil", document.OpenAIAPIKey)
	}
	if document.AuthMode != "chatgpt" {
		t.Fatalf("auth_mode = %q, want chatgpt", document.AuthMode)
	}
	if document.Tokens != (CodexAuthTokens{
		IDToken:      "id-token",
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		AccountID:    "account-123",
	}) {
		t.Fatalf("tokens = %#v", document.Tokens)
	}
	if document.LastRefresh == nil || document.LastRefresh.Format(time.RFC3339) != "2026-08-26T00:15:00Z" {
		t.Fatalf("last_refresh = %v", document.LastRefresh)
	}

	payload, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("marshal auth document: %v", err)
	}
	const want = `{"auth_mode":"chatgpt","OPENAI_API_KEY":null,"tokens":{"id_token":"id-token","access_token":"access-token","refresh_token":"refresh-token","account_id":"account-123"},"last_refresh":"2026-08-26T00:15:00Z"}`
	if string(payload) != want {
		t.Fatalf("payload = %s, want %s", payload, want)
	}

	if provider.Enabled {
		t.Fatal("export mutated the provider lifecycle state")
	}
	if provider.Credential.SecretData == "" {
		t.Fatal("export mutated the persisted credential")
	}
}

func TestBuildCodexAuthDocumentRejectsIneligibleProviders(t *testing.T) {
	active := pausedChatGPTProviderForCodexExport(t)
	active.Enabled = true
	reauthRequired := pausedChatGPTProviderForCodexExport(t)
	reauthRequired.AuthState.Status = model.ProviderAuthStatusReauthRequired
	incomplete := pausedChatGPTProviderForCodexExport(t)
	incomplete.Credential.SecretData = `{"access_token":"access-token"}`

	testCases := []struct {
		name     string
		provider *model.Provider
		want     error
	}{
		{name: "missing provider", want: ErrCodexAuthExportProviderRequired},
		{name: "static API key provider", provider: &model.Provider{CredentialType: model.ProviderCredentialTypeAPIKey}, want: ErrCodexAuthExportRequiresChatGPT},
		{name: "enabled GPT provider", provider: active, want: ErrCodexAuthExportRequiresPaused},
		{name: "reauthentication required", provider: reauthRequired, want: ErrCodexAuthExportCredentialUnavailable},
		{name: "incomplete credential", provider: incomplete, want: ErrCodexAuthExportCredentialUnavailable},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := BuildCodexAuthDocument(testCase.provider)
			if !errors.Is(err, testCase.want) {
				t.Fatalf("error = %v, want errors.Is(%v)", err, testCase.want)
			}
		})
	}
}
