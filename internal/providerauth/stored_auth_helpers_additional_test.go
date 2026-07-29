package providerauth

import (
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"
)

func TestStoredChatGPTCredentialSerializationHelpers(t *testing.T) {
	t.Parallel()

	if stored := newStoredChatGPTCredential(nil, ProviderAuthStatusActive, "", ""); stored != nil {
		t.Fatalf("newStoredChatGPTCredential(nil) = %#v, want nil", stored)
	}

	source := &model.ChatGPTProviderCredential{
		AccessToken:   "access-token",
		RefreshToken:  "refresh-token",
		IDToken:       "id-token",
		OAuthIssuer:   " https://issuer.example.com ",
		OAuthClientID: " client-id ",
		AccountID:     "acct-1",
		Email:         "user@example.com",
		PlanType:      "team",
		LastRefresh:   time.Date(2026, time.March, 30, 11, 0, 0, 0, time.UTC),
		ExpiresAt:     time.Date(2026, time.March, 30, 13, 0, 0, 0, time.UTC),
	}

	stored := newStoredChatGPTCredential(
		source,
		ProviderAuthStatusReauthRequired,
		" invalid_grant ",
		" refresh failed ",
	)
	if stored == nil {
		t.Fatal("newStoredChatGPTCredential(source) = nil, want stored credential")
	}
	if stored.AuthStatus != ProviderAuthStatusReauthRequired {
		t.Fatalf("AuthStatus = %q, want %q", stored.AuthStatus, ProviderAuthStatusReauthRequired)
	}
	if stored.AuthReason != ProviderAuthReasonInvalidGrant {
		t.Fatalf("AuthReason = %q, want %q", stored.AuthReason, ProviderAuthReasonInvalidGrant)
	}
	if stored.LastError != "refresh failed" {
		t.Fatalf("LastError = %q, want %q", stored.LastError, "refresh failed")
	}

	source.AccessToken = "mutated"
	if stored.AccessToken != "access-token" {
		t.Fatalf("stored credential reused caller pointer, AccessToken = %q", stored.AccessToken)
	}

	rawStored, err := encodeStoredChatGPTCredential(*stored)
	if err != nil {
		t.Fatalf("encodeStoredChatGPTCredential returned error: %v", err)
	}
	decodedStored, err := decodeStoredChatGPTCredential(rawStored)
	if err != nil {
		t.Fatalf("decodeStoredChatGPTCredential returned error: %v", err)
	}
	if decodedStored.AuthStatus != ProviderAuthStatusReauthRequired || decodedStored.AuthReason != ProviderAuthReasonInvalidGrant {
		t.Fatalf("decoded stored credential = %#v, want persisted auth status and reason", decodedStored)
	}

	rawSecret, err := encodeChatGPTCredentialSecret(&stored.ChatGPTProviderCredential)
	if err != nil {
		t.Fatalf("encodeChatGPTCredentialSecret returned error: %v", err)
	}
	decodedSecret, err := decodeChatGPTCredentialSecret(rawSecret)
	if err != nil {
		t.Fatalf("decodeChatGPTCredentialSecret returned error: %v", err)
	}
	if decodedSecret.AccessToken != "access-token" || decodedSecret.RefreshToken != "refresh-token" || decodedSecret.IDToken != "id-token" {
		t.Fatalf("decoded secret = %#v, want access/refresh/id tokens preserved", decodedSecret)
	}
	if decodedSecret.OAuthIssuer != "https://issuer.example.com" || decodedSecret.OAuthClientID != "client-id" {
		t.Fatalf("decoded oauth fields = (%q, %q), want trimmed issuer/client", decodedSecret.OAuthIssuer, decodedSecret.OAuthClientID)
	}

	if rawSecret, err := encodeChatGPTCredentialSecret(nil); err != nil || rawSecret != "" {
		t.Fatalf("encodeChatGPTCredentialSecret(nil) = (%q, %v), want (\"\", nil)", rawSecret, err)
	}
	if _, err := decodeChatGPTCredentialSecret(" "); err == nil {
		t.Fatal("decodeChatGPTCredentialSecret(blank) returned nil error")
	}
}

func TestStoredChatGPTAuthStatusReasonFallbacks(t *testing.T) {
	t.Parallel()

	if got := normalizeStoredChatGPTAuthStatus(nil); got != ProviderAuthStatusNotConnected {
		t.Fatalf("normalizeStoredChatGPTAuthStatus(nil) = %q, want %q", got, ProviderAuthStatusNotConnected)
	}

	readyStored := &storedChatGPTCredential{
		ChatGPTProviderCredential: model.ChatGPTProviderCredential{
			AccessToken:  "access",
			RefreshToken: "refresh",
			AccountID:    "acct-1",
		},
	}
	if got := normalizeStoredChatGPTAuthStatus(readyStored); got != ProviderAuthStatusActive {
		t.Fatalf("normalizeStoredChatGPTAuthStatus(ready) = %q, want %q", got, ProviderAuthStatusActive)
	}

	explicitStatus := &storedChatGPTCredential{AuthStatus: ProviderAuthStatusReauthRequired}
	if got := normalizeStoredChatGPTAuthStatus(explicitStatus); got != ProviderAuthStatusReauthRequired {
		t.Fatalf("normalizeStoredChatGPTAuthStatus(explicit) = %q, want %q", got, ProviderAuthStatusReauthRequired)
	}

	if got := reasonForStoredChatGPTAuthStatus(nil, ProviderAuthStatusNotConnected); got != ProviderAuthReasonLoginRequired {
		t.Fatalf("reasonForStoredChatGPTAuthStatus(nil, not_connected) = %q, want %q", got, ProviderAuthReasonLoginRequired)
	}
	if got := reasonForStoredChatGPTAuthStatus(nil, ProviderAuthStatusActive); got != "" {
		t.Fatalf("reasonForStoredChatGPTAuthStatus(nil, active) = %q, want empty string", got)
	}

	if got := reasonForStoredChatGPTAuthStatus(&storedChatGPTCredential{
		AuthReason: " refresh_token_reused ",
	}, ProviderAuthStatusReauthRequired); got != ProviderAuthReasonRefreshTokenReused {
		t.Fatalf("reasonForStoredChatGPTAuthStatus(explicit reason) = %q, want %q", got, ProviderAuthReasonRefreshTokenReused)
	}
	if got := reasonForStoredChatGPTAuthStatus(&storedChatGPTCredential{}, ProviderAuthStatusReauthRequired); got != ProviderAuthReasonCredentialInvalid {
		t.Fatalf("reasonForStoredChatGPTAuthStatus(reauth_required) = %q, want %q", got, ProviderAuthReasonCredentialInvalid)
	}
	if got := reasonForStoredChatGPTAuthStatus(&storedChatGPTCredential{}, ProviderAuthStatusNotConnected); got != ProviderAuthReasonLoginRequired {
		t.Fatalf("reasonForStoredChatGPTAuthStatus(not_connected) = %q, want %q", got, ProviderAuthReasonLoginRequired)
	}
	if got := reasonForStoredChatGPTAuthStatus(&storedChatGPTCredential{}, ProviderAuthStatusActive); got != "" {
		t.Fatalf("reasonForStoredChatGPTAuthStatus(active) = %q, want empty string", got)
	}
	if got := reasonForStoredChatGPTAuthStatus(&storedChatGPTCredential{}, ProviderAuthStatus("unknown")); got != "" {
		t.Fatalf("reasonForStoredChatGPTAuthStatus(unknown) = %q, want empty string", got)
	}
}
