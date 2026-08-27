package providerauth

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/model"
)

func pausedChatGPTSessionForCodexExport(t *testing.T) *credentialsession.Snapshot {
	t.Helper()
	secret, err := model.EncodeChatGPTProviderSecret(&model.ChatGPTProviderSecret{
		AccessToken:  " access-token ",
		RefreshToken: " refresh-token ",
		IDToken:      " id-token ",
	})
	if err != nil {
		t.Fatalf("encode credential secret: %v", err)
	}
	subject, err := credentialsession.AccountSubject("account-123")
	if err != nil {
		t.Fatalf("build credential subject: %v", err)
	}
	lastRefresh := time.Date(2026, time.August, 26, 8, 15, 0, 0, time.FixedZone("SGT", 8*60*60))
	return &credentialsession.Snapshot{
		SessionID: "gpt-session", Kind: credentialsession.KindChatGPT, SecretData: secret,
		Subject: subject,
		AuthState: credentialsession.AuthState{
			Status: credentialsession.AuthStatusActive, LastRefreshAt: &lastRefresh,
		},
	}
}

func TestBuildCodexAuthDocument(t *testing.T) {
	snapshot := pausedChatGPTSessionForCodexExport(t)

	document, err := BuildCodexAuthDocument(snapshot, false)
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

	if snapshot.SecretData == "" {
		t.Fatal("export mutated the persisted credential")
	}
}

func TestBuildCodexAuthDocumentRejectsIneligibleProviders(t *testing.T) {
	active := pausedChatGPTSessionForCodexExport(t)
	reauthRequired := pausedChatGPTSessionForCodexExport(t)
	reauthRequired.AuthState.Status = credentialsession.AuthStatusReauthRequired
	incomplete := pausedChatGPTSessionForCodexExport(t)
	incomplete.SecretData = `{"access_token":"access-token"}`

	testCases := []struct {
		name     string
		snapshot *credentialsession.Snapshot
		enabled  bool
		want     error
	}{
		{name: "missing session", want: ErrCodexAuthExportProviderRequired},
		{name: "static API key session", snapshot: &credentialsession.Snapshot{Kind: credentialsession.KindAPIKey}, want: ErrCodexAuthExportRequiresChatGPT},
		{name: "enabled route", snapshot: active, enabled: true, want: ErrCodexAuthExportRequiresPaused},
		{name: "reauthentication required", snapshot: reauthRequired, want: ErrCodexAuthExportCredentialUnavailable},
		{name: "incomplete credential", snapshot: incomplete, want: ErrCodexAuthExportCredentialUnavailable},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := BuildCodexAuthDocument(testCase.snapshot, testCase.enabled)
			if !errors.Is(err, testCase.want) {
				t.Fatalf("error = %v, want errors.Is(%v)", err, testCase.want)
			}
		})
	}
}
