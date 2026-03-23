package model

import "testing"

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
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.credential.Ready() {
				t.Fatal("Ready() = true, want false when required proxy fields are missing")
			}
		})
	}
}
