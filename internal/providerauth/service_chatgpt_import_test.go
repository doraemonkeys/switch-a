package providerauth

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

// chatgptAuthJWT builds an unsigned JWT carrying the OpenAI auth block, matching the
// shape of both id_tokens and access_tokens that import needs to decode.
func chatgptAuthJWT(t *testing.T, accountID, email, plan string, exp time.Time) string {
	t.Helper()
	return makeTestJWT(t, map[string]any{
		"iss":   defaultOAuthIssuer,
		"aud":   defaultOAuthClientID,
		"email": email,
		"exp":   float64(exp.Unix()),
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": accountID,
			"chatgpt_plan_type":  plan,
		},
	})
}

func chatgptAccessJWT(t *testing.T, accountID, email, plan string, exp time.Time) string {
	t.Helper()
	return makeTestJWT(t, map[string]any{
		"iss":   defaultOAuthIssuer,
		"aud":   chatGPTAPIAudience,
		"email": email,
		"exp":   float64(exp.Unix()),
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": accountID,
			"chatgpt_plan_type":  plan,
		},
	})
}

func TestParseImportedChatGPTTokens(t *testing.T) {
	cases := []struct {
		name        string
		raw         string
		wantAccess  string
		wantRefresh string
		wantID      string
		wantErr     bool
	}{
		{
			name:        "codex auth.json nested tokens",
			raw:         `{"OPENAI_API_KEY":"sk","tokens":{"id_token":"id","access_token":"acc","refresh_token":"ref"},"last_refresh":"2026-01-01"}`,
			wantAccess:  "acc",
			wantRefresh: "ref",
			wantID:      "id",
		},
		{
			name:        "flat snake_case",
			raw:         `{"access_token":"acc","refresh_token":"ref","id_token":"id"}`,
			wantAccess:  "acc",
			wantRefresh: "ref",
			wantID:      "id",
		},
		{
			name:        "flat camelCase",
			raw:         `{"accessToken":"acc","refreshToken":"ref","idToken":"id"}`,
			wantAccess:  "acc",
			wantRefresh: "ref",
			wantID:      "id",
		},
		{
			name:       "session payload access only",
			raw:        `{"accessToken":"acc","user":{"email":"u@example.com"},"expires":"2026-01-01"}`,
			wantAccess: "acc",
		},
		{
			name:        "nested tokens win over root",
			raw:         `{"access_token":"root","tokens":{"access_token":"nested","refresh_token":"ref"}}`,
			wantAccess:  "nested",
			wantRefresh: "ref",
		},
		{
			name:    "blank input",
			raw:     "   ",
			wantErr: true,
		},
		{
			name:    "invalid json",
			raw:     "{not-json",
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tokens, err := parseImportedChatGPTTokens(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseImportedChatGPTTokens(%q) error = nil, want error", tc.raw)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseImportedChatGPTTokens(%q) error = %v", tc.raw, err)
			}
			if tokens.AccessToken != tc.wantAccess {
				t.Errorf("AccessToken = %q, want %q", tokens.AccessToken, tc.wantAccess)
			}
			if tokens.RefreshToken != tc.wantRefresh {
				t.Errorf("RefreshToken = %q, want %q", tokens.RefreshToken, tc.wantRefresh)
			}
			if tokens.IDToken != tc.wantID {
				t.Errorf("IDToken = %q, want %q", tokens.IDToken, tc.wantID)
			}
		})
	}
}

func TestNewChatGPTCredentialFromImportedTokens(t *testing.T) {
	now := time.Date(2026, time.June, 24, 12, 0, 0, 0, time.UTC)
	exp := now.Add(time.Hour)

	t.Run("uses id_token when present", func(t *testing.T) {
		idToken := chatgptAuthJWT(t, "acct_id", "id@example.com", "pro", exp)
		accessToken := chatgptAccessJWT(t, "acct_id", "access@example.com", "pro", exp)
		credential, err := newChatGPTCredentialFromImportedTokens(accessToken, "ref", idToken, now)
		if err != nil {
			t.Fatalf("error = %v", err)
		}
		if credential.IDToken != idToken {
			t.Errorf("IDToken not preserved from id_token")
		}
		if credential.AccountID != "acct_id" {
			t.Errorf("AccountID = %q, want acct_id", credential.AccountID)
		}
		if credential.Email != "id@example.com" {
			t.Errorf("Email = %q, want id@example.com", credential.Email)
		}
		if !credential.Ready() {
			t.Error("credential is not Ready")
		}
	})

	t.Run("falls back to access_token when id_token missing", func(t *testing.T) {
		accessToken := chatgptAccessJWT(t, "acct_acc", "acc@example.com", "team", exp)
		credential, err := newChatGPTCredentialFromImportedTokens(accessToken, "ref", "", now)
		if err != nil {
			t.Fatalf("error = %v", err)
		}
		if credential.IDToken != "" {
			t.Errorf("IDToken = %q, want empty when no id_token is supplied", credential.IDToken)
		}
		if credential.AccountID != "acct_acc" {
			t.Errorf("AccountID = %q, want acct_acc from access_token", credential.AccountID)
		}
		// Issuer/client id must be captured so refresh works without a stored id_token.
		if credential.OAuthIssuer != defaultOAuthIssuer {
			t.Errorf("OAuthIssuer = %q, want %q", credential.OAuthIssuer, defaultOAuthIssuer)
		}
		if credential.OAuthClientID != defaultOAuthClientID {
			t.Errorf("OAuthClientID = %q, want %q", credential.OAuthClientID, defaultOAuthClientID)
		}
		if !credential.Ready() {
			t.Error("credential is not Ready")
		}
	})

	t.Run("invalid access_token without id_token errors", func(t *testing.T) {
		_, err := newChatGPTCredentialFromImportedTokens("not-a-jwt", "ref", "", now)
		if err == nil || !strings.Contains(err.Error(), "access_token") {
			t.Fatalf("error = %v, want access_token decode failure", err)
		}
	})

	t.Run("rejects access and id token account mismatch", func(t *testing.T) {
		accessToken := chatgptAccessJWT(t, "acct_access", "access@example.com", "team", exp)
		idToken := chatgptAuthJWT(t, "acct_id", "id@example.com", "team", exp)
		_, err := newChatGPTCredentialFromImportedTokens(accessToken, "ref", idToken, now)
		if err == nil || !strings.Contains(err.Error(), "identify different accounts") {
			t.Fatalf("error = %v, want cross-token account mismatch", err)
		}
	})
}

func TestImportChatGPTLogin(t *testing.T) {
	now := time.Date(2026, time.June, 24, 12, 0, 0, 0, time.UTC)
	idToken := chatgptAuthJWT(t, "acct_import", "import@example.com", "pro", now.Add(time.Hour))
	accessToken := chatgptAccessJWT(t, "acct_import", "import@example.com", "pro", now.Add(time.Hour))

	// Usage fetch is best-effort; a failing stub keeps these tests independent of the
	// upstream usage endpoint while still exercising the best-effort branch.
	newImportService := func() *Service {
		return NewService(Config{
			Clock: fixedClock{now: now},
			HTTPClient: stubHTTPDoer{do: func(*http.Request) (*http.Response, error) {
				return nil, errors.New("usage endpoint unavailable")
			}},
		})
	}

	t.Run("stages a completed session for the save pipeline", func(t *testing.T) {
		service := newImportService()
		raw := `{"tokens":{"id_token":"` + idToken + `","access_token":"` + accessToken + `","refresh_token":"ref"}}`

		status, err := service.ImportChatGPTLogin(context.Background(), raw)
		if err != nil {
			t.Fatalf("ImportChatGPTLogin error = %v", err)
		}
		if status.Status != ChatGPTLoginStatusCompleted {
			t.Fatalf("status = %q, want completed", status.Status)
		}
		if status.LoginID == "" {
			t.Fatal("LoginID is empty")
		}
		if status.Auth == nil {
			t.Fatal("Auth view is nil")
		}
		if status.Auth.AccountID != "acct_import" || status.Auth.Email != "import@example.com" {
			t.Fatalf("auth view = %+v, want account/email from id_token", status.Auth)
		}
		if status.Auth.Status != ProviderAuthStatusActive {
			t.Fatalf("auth status = %q, want active", status.Auth.Status)
		}

		session, err := service.BuildCredentialSessionFromChatGPTLogin(status.LoginID, "imported-session")
		if err != nil {
			t.Fatalf("BuildCredentialSessionFromChatGPTLogin error = %v", err)
		}
		snapshot, err := session.Snapshot()
		if err != nil {
			t.Fatalf("Session.Snapshot error = %v", err)
		}
		credential, err := decodeChatGPTCredentialSession(&snapshot)
		if err != nil || credential == nil || !credential.Ready() {
			t.Fatalf("credential session = %#v, %v; want complete ChatGPT credential", credential, err)
		}
		if err := service.FinalizeChatGPTLogin(status.LoginID); err != nil {
			t.Fatalf("FinalizeChatGPTLogin error = %v", err)
		}
		if err := service.FinalizeChatGPTLogin(status.LoginID); err == nil {
			t.Fatal("second FinalizeChatGPTLogin should fail once the session is consumed")
		}
	})

	t.Run("rejects missing refresh token", func(t *testing.T) {
		service := newImportService()
		raw := `{"access_token":"acc","id_token":"` + idToken + `"}`
		_, err := service.ImportChatGPTLogin(context.Background(), raw)
		if err == nil || !strings.Contains(err.Error(), "refresh token") {
			t.Fatalf("error = %v, want missing refresh token", err)
		}
	})

	t.Run("rejects missing access token", func(t *testing.T) {
		service := newImportService()
		raw := `{"refresh_token":"ref","id_token":"` + idToken + `"}`
		_, err := service.ImportChatGPTLogin(context.Background(), raw)
		if err == nil || !strings.Contains(err.Error(), "access token") {
			t.Fatalf("error = %v, want missing access token", err)
		}
	})

	t.Run("rejects invalid json", func(t *testing.T) {
		service := newImportService()
		if _, err := service.ImportChatGPTLogin(context.Background(), "{bad"); err == nil {
			t.Fatal("expected error for invalid json")
		}
	})
}
