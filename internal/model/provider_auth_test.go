package model

import (
	"testing"
	"time"
)

func TestIsValidProviderCredentialType(t *testing.T) {
	testCases := []struct {
		name  string
		value ProviderCredentialType
		want  bool
	}{
		{
			name:  "empty uses legacy default",
			value: "",
			want:  true,
		},
		{
			name:  "api key is supported",
			value: ProviderCredentialTypeAPIKey,
			want:  true,
		},
		{
			name:  "chatgpt is supported",
			value: ProviderCredentialTypeChatGPT,
			want:  true,
		},
		{
			name:  "unknown type is rejected",
			value: ProviderCredentialType("oauth"),
			want:  false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsValidProviderCredentialType(tc.value); got != tc.want {
				t.Fatalf("IsValidProviderCredentialType(%q) = %t, want %t", tc.value, got, tc.want)
			}
		})
	}
}

func TestNormalizeProviderCredentialType(t *testing.T) {
	testCases := []struct {
		name  string
		value ProviderCredentialType
		want  ProviderCredentialType
	}{
		{
			name:  "empty normalizes to api key",
			value: "",
			want:  ProviderCredentialTypeAPIKey,
		},
		{
			name:  "api key remains unchanged",
			value: ProviderCredentialTypeAPIKey,
			want:  ProviderCredentialTypeAPIKey,
		},
		{
			name:  "chatgpt remains unchanged",
			value: ProviderCredentialTypeChatGPT,
			want:  ProviderCredentialTypeChatGPT,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormalizeProviderCredentialType(tc.value); got != tc.want {
				t.Fatalf("NormalizeProviderCredentialType(%q) = %q, want %q", tc.value, got, tc.want)
			}
		})
	}
}

func TestChatGPTProviderCredentialReady_AllowsMissingIDToken(t *testing.T) {
	credential := &ChatGPTProviderCredential{
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		AccountID:    "acct_test",
	}

	if !credential.Ready() {
		t.Fatal("Ready() = false, want true when access, refresh, and account identifiers are present")
	}
}

func TestChatGPTProviderCredentialReady_RejectsMissingProxyFields(t *testing.T) {
	testCases := []struct {
		name       string
		credential *ChatGPTProviderCredential
	}{
		{
			name:       "nil credential",
			credential: nil,
		},
		{
			name: "missing access token",
			credential: &ChatGPTProviderCredential{
				RefreshToken: "refresh-token",
				AccountID:    "acct_test",
			},
		},
		{
			name: "missing refresh token",
			credential: &ChatGPTProviderCredential{
				AccessToken: "access-token",
				AccountID:   "acct_test",
			},
		},
		{
			name: "missing account id",
			credential: &ChatGPTProviderCredential{
				AccessToken:  "access-token",
				RefreshToken: "refresh-token",
			},
		},
		{
			name: "blank required fields",
			credential: &ChatGPTProviderCredential{
				AccessToken:  "   ",
				RefreshToken: "refresh-token",
				AccountID:    "acct_test",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.credential.Ready() {
				t.Fatal("Ready() = true, want false when required proxy fields are missing")
			}
		})
	}
}

func TestDefaultProviderAuthStatus(t *testing.T) {
	if got := DefaultProviderAuthStatus(ProviderCredentialTypeAPIKey); got != ProviderAuthStatusActive {
		t.Fatalf("DefaultProviderAuthStatus(api_key) = %q, want %q", got, ProviderAuthStatusActive)
	}
	if got := DefaultProviderAuthStatus(ProviderCredentialTypeChatGPT); got != ProviderAuthStatusNotConnected {
		t.Fatalf("DefaultProviderAuthStatus(chatgpt) = %q, want %q", got, ProviderAuthStatusNotConnected)
	}
}

func TestProviderCredentialFromLegacy_PreservesRawSecretAndBinding(t *testing.T) {
	payload, err := EncodeChatGPTProviderCredential(&ChatGPTProviderCredential{
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		AccountID:    "acct_test",
	})
	if err != nil {
		t.Fatalf("EncodeChatGPTProviderCredential() error = %v", err)
	}

	record := ProviderCredentialFromLegacy("provider-gpt", ProviderCredentialTypeChatGPT, payload)
	if record == nil {
		t.Fatal("ProviderCredentialFromLegacy() = nil, want record")
	}
	if record.SecretData != payload {
		t.Fatalf("SecretData = %q, want original payload", record.SecretData)
	}
	if record.BindingAccountID == nil || *record.BindingAccountID != "acct_test" {
		t.Fatalf("BindingAccountID = %v, want acct_test", record.BindingAccountID)
	}
	if record.Version != 1 {
		t.Fatalf("Version = %d, want 1", record.Version)
	}
}

func TestProviderAuthStateFromCredential_ChatGPTReadyHydratesSummary(t *testing.T) {
	now := time.Date(2026, 3, 27, 12, 0, 0, 0, time.UTC)
	payload, err := EncodeChatGPTProviderCredential(&ChatGPTProviderCredential{
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		AccountID:    "acct_test",
		Email:        "user@example.com",
		PlanType:     "team",
		LastRefresh:  now.Add(-time.Minute),
		ExpiresAt:    now.Add(time.Hour),
		Usage: &ProviderUsageSnapshot{
			PlanType: "team",
		},
	})
	if err != nil {
		t.Fatalf("EncodeChatGPTProviderCredential() error = %v", err)
	}

	state := ProviderAuthStateFromCredential(
		"provider-gpt",
		ProviderCredentialTypeChatGPT,
		ProviderCredentialFromLegacy("provider-gpt", ProviderCredentialTypeChatGPT, payload),
	)
	if state == nil {
		t.Fatal("ProviderAuthStateFromCredential() = nil, want state")
	}
	if state.Status != ProviderAuthStatusActive {
		t.Fatalf("Status = %q, want %q", state.Status, ProviderAuthStatusActive)
	}
	if state.AccountID != "acct_test" {
		t.Fatalf("AccountID = %q, want acct_test", state.AccountID)
	}
	if state.Email != "user@example.com" {
		t.Fatalf("Email = %q, want user@example.com", state.Email)
	}
	if state.PlanType != "team" {
		t.Fatalf("PlanType = %q, want team", state.PlanType)
	}
	if state.UsageSnapshot == nil || state.UsageSnapshot.PlanType != "team" {
		t.Fatalf("UsageSnapshot = %+v, want team snapshot", state.UsageSnapshot)
	}
	if state.ExpiresAt == nil || !state.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("ExpiresAt = %v, want %v", state.ExpiresAt, now.Add(time.Hour))
	}
	if state.LastRefreshAt == nil || !state.LastRefreshAt.Equal(now.Add(-time.Minute)) {
		t.Fatalf("LastRefreshAt = %v, want %v", state.LastRefreshAt, now.Add(-time.Minute))
	}
}

func TestProviderAuthStateFromCredential_ChatGPTWithoutCredentialIsNotConnected(t *testing.T) {
	state := ProviderAuthStateFromCredential("provider-gpt", ProviderCredentialTypeChatGPT, nil)
	if state == nil {
		t.Fatal("ProviderAuthStateFromCredential() = nil, want state")
	}
	if state.Status != ProviderAuthStatusNotConnected {
		t.Fatalf("Status = %q, want %q", state.Status, ProviderAuthStatusNotConnected)
	}
}
