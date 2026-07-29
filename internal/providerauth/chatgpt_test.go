package providerauth

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"
)

func makeTestJWT(t *testing.T, claims map[string]any) string {
	t.Helper()

	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}

	return "header." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}

func mustEncodeChatGPTCredential(t *testing.T, credential model.ChatGPTProviderCredential) string {
	t.Helper()

	raw, err := encodeChatGPTCredential(credential)
	if err != nil {
		t.Fatalf("encodeChatGPTCredential returned error: %v", err)
	}
	return raw
}

func TestEncodeDecodeChatGPTCredential_RoundTrip(t *testing.T) {
	now := time.Date(2026, time.March, 22, 12, 0, 0, 0, time.UTC)
	resetAt := now.Add(5 * time.Hour)
	raw, err := encodeChatGPTCredential(model.ChatGPTProviderCredential{
		AccessToken:   "access-token",
		RefreshToken:  "refresh-token",
		IDToken:       "id-token",
		OAuthIssuer:   "https://issuer.example.com/",
		OAuthClientID: "client-id",
		AccountID:     "acct_test",
		Email:         "user@example.com",
		PlanType:      "team",
		Usage: &model.ProviderUsageSnapshot{
			FetchedAt: &now,
			PlanType:  "team",
			FiveHour: &model.ProviderUsageWindow{
				UsedPercent:   12.5,
				WindowSeconds: 5 * 60 * 60,
				ResetAt:       &resetAt,
			},
		},
		LastRefresh: now.Add(-time.Minute),
		ExpiresAt:   now.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("encodeChatGPTCredential returned error: %v", err)
	}

	credential, err := decodeChatGPTCredential(raw)
	if err != nil {
		t.Fatalf("decodeChatGPTCredential returned error: %v", err)
	}
	if credential.AccessToken != "access-token" {
		t.Fatalf("AccessToken = %q, want %q", credential.AccessToken, "access-token")
	}
	if credential.PlanType != "team" {
		t.Fatalf("PlanType = %q, want %q", credential.PlanType, "team")
	}
	if credential.OAuthIssuer != "https://issuer.example.com/" {
		t.Fatalf("OAuthIssuer = %q, want %q", credential.OAuthIssuer, "https://issuer.example.com/")
	}
	if credential.OAuthClientID != "client-id" {
		t.Fatalf("OAuthClientID = %q, want %q", credential.OAuthClientID, "client-id")
	}
	if credential.Usage == nil || credential.Usage.FiveHour == nil {
		t.Fatal("Usage = nil, want five-hour snapshot")
	}
	if credential.Usage.FiveHour.UsedPercent != 12.5 {
		t.Fatalf("UsedPercent = %v, want %v", credential.Usage.FiveHour.UsedPercent, 12.5)
	}
}

func TestDecodeChatGPTCredential_Errors(t *testing.T) {
	testCases := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "missing payload",
			raw:  "   ",
			want: "missing chatgpt credential payload",
		},
		{
			name: "invalid json",
			raw:  "{",
			want: "decode chatgpt credential payload",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := decodeChatGPTCredential(tc.raw)
			if err == nil {
				t.Fatal("decodeChatGPTCredential returned nil error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tc.want)
			}
		})
	}
}

func TestNewChatGPTCredentialFromTokens_UsesJWTClaims(t *testing.T) {
	expiry := time.Date(2026, time.March, 23, 12, 0, 0, 0, time.UTC)
	idToken := makeTestJWT(t, map[string]any{
		"iss":   "https://issuer.example.com/",
		"aud":   []any{"", "client-id"},
		"email": "user@example.com",
		"exp":   expiry.Unix(),
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "acct_test",
			"chatgpt_plan_type":  " plus ",
		},
	})

	before := time.Now().UTC()
	credential, err := newChatGPTCredentialFromTokens("access-token", "refresh-token", idToken)
	after := time.Now().UTC()
	if err != nil {
		t.Fatalf("newChatGPTCredentialFromTokens returned error: %v", err)
	}
	if credential.AccountID != "acct_test" {
		t.Fatalf("AccountID = %q, want %q", credential.AccountID, "acct_test")
	}
	if credential.Email != "user@example.com" {
		t.Fatalf("Email = %q, want %q", credential.Email, "user@example.com")
	}
	if credential.OAuthIssuer != "https://issuer.example.com/" {
		t.Fatalf("OAuthIssuer = %q, want %q", credential.OAuthIssuer, "https://issuer.example.com/")
	}
	if credential.OAuthClientID != "client-id" {
		t.Fatalf("OAuthClientID = %q, want %q", credential.OAuthClientID, "client-id")
	}
	if credential.PlanType != "plus" {
		t.Fatalf("PlanType = %q, want %q", credential.PlanType, "plus")
	}
	if !credential.ExpiresAt.Equal(expiry) {
		t.Fatalf("ExpiresAt = %s, want %s", credential.ExpiresAt, expiry)
	}
	if credential.LastRefresh.Before(before) || credential.LastRefresh.After(after) {
		t.Fatalf("LastRefresh = %s, want between %s and %s", credential.LastRefresh, before, after)
	}
}

func TestNewChatGPTCredentialFromTokens_InvalidJWT(t *testing.T) {
	_, err := newChatGPTCredentialFromTokens("access-token", "refresh-token", "bad-token")
	if err == nil {
		t.Fatal("newChatGPTCredentialFromTokens returned nil error")
	}
	if !strings.Contains(err.Error(), "decode chatgpt id_token") {
		t.Fatalf("error = %q, want jwt decode failure", err.Error())
	}
}

func TestChatGPTClaimHelpers(t *testing.T) {
	t.Run("extract account id errors", func(t *testing.T) {
		_, err := extractChatGPTAccountID(map[string]any{})
		if err == nil || !strings.Contains(err.Error(), "missing auth claims") {
			t.Fatalf("error = %v, want missing auth claims", err)
		}

		_, err = extractChatGPTAccountID(map[string]any{
			"https://api.openai.com/auth": map[string]any{},
		})
		if err == nil || !strings.Contains(err.Error(), "missing chatgpt_account_id") {
			t.Fatalf("error = %v, want missing account id", err)
		}
	})

	t.Run("extract plan type trims whitespace", func(t *testing.T) {
		planType := extractChatGPTPlanType(map[string]any{
			"https://api.openai.com/auth": map[string]any{
				"chatgpt_plan_type": " team ",
			},
		})
		if planType != "team" {
			t.Fatalf("planType = %q, want %q", planType, "team")
		}
		if got := extractChatGPTPlanType(map[string]any{}); got != "" {
			t.Fatalf("planType = %q, want empty string", got)
		}
	})

	t.Run("decode jwt payload errors", func(t *testing.T) {
		testCases := []struct {
			name  string
			token string
			want  string
		}{
			{name: "invalid format", token: "token", want: "invalid jwt format"},
			{name: "invalid base64", token: "header.bad!.sig", want: "decode jwt payload"},
			{name: "invalid json", token: "header." + base64.RawURLEncoding.EncodeToString([]byte("{")) + ".sig", want: "parse jwt payload"},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				_, err := decodeJWTPayload(tc.token)
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("error = %v, want substring %q", err, tc.want)
				}
			})
		}
	})

	t.Run("read string claim", func(t *testing.T) {
		if got := readStringClaim(map[string]any{"email": "user@example.com"}, "email"); got != "user@example.com" {
			t.Fatalf("readStringClaim = %q, want %q", got, "user@example.com")
		}
		if got := readStringClaim(map[string]any{"email": 42}, "email"); got != "" {
			t.Fatalf("readStringClaim = %q, want empty string", got)
		}
	})
}

func TestExtractJWTExpiry_ParsesSupportedFormats(t *testing.T) {
	expected := time.Unix(1770000000, 0).UTC()

	testCases := []struct {
		name   string
		claims map[string]any
		want   time.Time
	}{
		{
			name:   "float64",
			claims: map[string]any{"exp": float64(1770000000)},
			want:   expected,
		},
		{
			name:   "json number",
			claims: map[string]any{"exp": json.Number("1770000000")},
			want:   expected,
		},
		{
			name:   "string",
			claims: map[string]any{"exp": "1770000000"},
			want:   expected,
		},
		{
			name:   "unsupported",
			claims: map[string]any{"exp": true},
			want:   time.Time{},
		},
		{
			name:   "bad string",
			claims: map[string]any{"exp": "nope"},
			want:   time.Time{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractJWTExpiry(tc.claims); !got.Equal(tc.want) {
				t.Fatalf("extractJWTExpiry() = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestExtractClientIDFromClaims_SelectsFirstNonEmptyString(t *testing.T) {
	testCases := []struct {
		name   string
		claims map[string]any
		want   string
	}{
		{
			name:   "string audience",
			claims: map[string]any{"aud": "client-id"},
			want:   "client-id",
		},
		{
			name:   "array audience",
			claims: map[string]any{"aud": []any{"", 12, "client-id"}},
			want:   "client-id",
		},
		{
			name:   "missing audience",
			claims: map[string]any{},
			want:   "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractClientIDFromClaims(tc.claims); got != tc.want {
				t.Fatalf("extractClientIDFromClaims() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestReadHTTPError_IncludesResponseBodyWhenPresent(t *testing.T) {
	response := &http.Response{
		Status: "400 Bad Request",
		Body:   io.NopCloser(strings.NewReader("denied")),
	}
	err := readHTTPError(response, "exchange authorization code")
	if err == nil {
		t.Fatal("readHTTPError returned nil error")
	}
	if !strings.Contains(err.Error(), "exchange authorization code failed with status 400 Bad Request: denied") {
		t.Fatalf("error = %q, want formatted status and body", err.Error())
	}

	response = &http.Response{
		Status: "500 Internal Server Error",
		Body:   io.NopCloser(strings.NewReader("   ")),
	}
	err = readHTTPError(response, "refresh chatgpt token")
	if err == nil {
		t.Fatal("readHTTPError returned nil error")
	}
	if !strings.Contains(err.Error(), "refresh chatgpt token failed with status 500 Internal Server Error") {
		t.Fatalf("error = %q, want formatted status", err.Error())
	}
}

func TestRenderCallbackPage_RendersMessageEnvelope(t *testing.T) {
	recorder := httptest.NewRecorder()

	renderCallbackPage(recorder, callbackPage{
		Status:  "error",
		Message: "Access denied",
		LoginID: "login-123",
	})

	response := recorder.Result()
	if got := response.Header.Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want %q", got, "text/html; charset=utf-8")
	}

	body := recorder.Body.String()
	for _, want := range []string{
		"Switch-A GPT Login Failed",
		"Access denied",
		"switch-a:chatgpt-login",
		`"loginId":"login-123"`,
		`"status":"error"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q", want)
		}
	}
}
