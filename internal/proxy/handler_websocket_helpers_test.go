package proxy

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"
)

type mockOAuthHTTPClient struct {
	do func(req *http.Request) (*http.Response, error)
}

func (m mockOAuthHTTPClient) Do(req *http.Request) (*http.Response, error) {
	if m.do != nil {
		return m.do(req)
	}
	return nil, nil
}

func testSyntheticJWT(t *testing.T, claims map[string]any) string {
	t.Helper()

	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal jwt payload: %v", err)
	}

	// A non-empty signature keeps fixtures structurally honest now that production
	// parsing rejects the unsecured, empty-signature form of compact JWS.
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","kid":"test"}`))
	signature := base64.RawURLEncoding.EncodeToString([]byte("synthetic-signature"))
	return header + "." + base64.RawURLEncoding.EncodeToString(payload) + "." + signature
}

func testChatGPTCredentialData(t *testing.T, accessToken, refreshToken, accountID string) string {
	t.Helper()

	expiresAt := time.Now().UTC().Add(1 * time.Hour)
	idToken := testSyntheticJWT(t, map[string]any{
		"iss":   "https://auth.openai.com",
		"aud":   "app_EMoamEEZ73f0CkXaXp7hrann",
		"email": "codex@example.com",
		"exp":   expiresAt.Unix(),
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": accountID,
		},
	})

	raw, err := json.Marshal(model.ChatGPTProviderCredential{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		IDToken:      idToken,
		AccountID:    accountID,
		Email:        "codex@example.com",
		LastRefresh:  time.Now().UTC(),
		ExpiresAt:    expiresAt,
	})
	if err != nil {
		t.Fatalf("marshal chatgpt credential: %v", err)
	}

	return string(raw)
}
