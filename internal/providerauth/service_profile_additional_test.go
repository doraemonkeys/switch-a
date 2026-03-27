package providerauth

import (
	"context"
	"errors"
	"testing"

	"switch-a/internal/model"
)

type failingAuthStateStore struct {
	err error
}

func (s failingAuthStateStore) UpdateProviderCredential(context.Context, string, model.ProviderCredentialType, string) error {
	return nil
}

func (s failingAuthStateStore) UpdateProviderAuthState(context.Context, string, *model.ProviderAuthState) error {
	return s.err
}

func TestRefreshProviderUsage_APIKeyProviderIsNoOp(t *testing.T) {
	t.Parallel()

	service := NewService(Config{})
	refreshed, err := service.RefreshProviderUsage(context.Background(), &model.Provider{
		ID:             "api-provider",
		CredentialType: model.ProviderCredentialTypeAPIKey,
		APIKey:         "api-key",
	})
	if err != nil {
		t.Fatalf("RefreshProviderUsage returned error: %v", err)
	}
	if refreshed {
		t.Fatal("RefreshProviderUsage returned true, want false for API-key provider")
	}
}

func TestRefreshChatGPTUsageSnapshot_RejectsNilProvider(t *testing.T) {
	t.Parallel()

	service := NewService(Config{})
	if err := service.refreshChatGPTUsageSnapshot(context.Background(), nil); err == nil {
		t.Fatal("refreshChatGPTUsageSnapshot(nil) error = nil, want error")
	}
}

func TestRefreshChatGPTUsageSnapshot_RejectsInvalidCredential(t *testing.T) {
	t.Parallel()

	service := NewService(Config{})
	err := service.refreshChatGPTUsageSnapshot(context.Background(), &model.Provider{
		ID:             "chatgpt-invalid",
		CredentialType: model.ProviderCredentialTypeChatGPT,
		Credential: &model.ProviderCredential{
			SecretData: `{`,
		},
	})
	if err == nil {
		t.Fatal("refreshChatGPTUsageSnapshot returned nil error, want auth-state error")
	}

	var authErr *ProviderAuthStateError
	if !errors.As(err, &authErr) {
		t.Fatalf("refreshChatGPTUsageSnapshot error = %T, want ProviderAuthStateError", err)
	}
	if authErr.Status != ProviderAuthStatusNotConnected {
		t.Fatalf("Status = %q, want %q", authErr.Status, ProviderAuthStatusNotConnected)
	}
	if authErr.Reason != ProviderAuthReasonCredentialInvalid {
		t.Fatalf("Reason = %q, want %q", authErr.Reason, ProviderAuthReasonCredentialInvalid)
	}
	if authErr.LastError == "" {
		t.Fatal("LastError = empty, want decode failure")
	}
}

func TestRefreshChatGPTUsageSnapshot_RejectsIncompleteCredential(t *testing.T) {
	t.Parallel()

	service := NewService(Config{})
	err := service.refreshChatGPTUsageSnapshot(context.Background(), &model.Provider{
		ID:             "chatgpt-incomplete",
		CredentialType: model.ProviderCredentialTypeChatGPT,
		Credential: &model.ProviderCredential{
			SecretData: `{"access_token":"access","account_id":"acct"}`,
		},
	})
	if err == nil {
		t.Fatal("refreshChatGPTUsageSnapshot returned nil error, want auth-state error")
	}

	var authErr *ProviderAuthStateError
	if !errors.As(err, &authErr) {
		t.Fatalf("refreshChatGPTUsageSnapshot error = %T, want ProviderAuthStateError", err)
	}
	if authErr.Status != ProviderAuthStatusNotConnected {
		t.Fatalf("Status = %q, want %q", authErr.Status, ProviderAuthStatusNotConnected)
	}
	if authErr.Reason != ProviderAuthReasonLoginRequired {
		t.Fatalf("Reason = %q, want %q", authErr.Reason, ProviderAuthReasonLoginRequired)
	}
}

func TestRefreshChatGPTUsageSnapshot_RejectsInactiveAuthState(t *testing.T) {
	t.Parallel()

	service := NewService(Config{})
	provider := &model.Provider{
		ID:             "chatgpt-reauth",
		CredentialType: model.ProviderCredentialTypeChatGPT,
	}
	mustApplyLegacyChatGPTCredential(t, provider, mustEncodeChatGPTCredential(t, model.ChatGPTProviderCredential{
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		AccountID:    "acct-reauth",
	}))
	provider.AuthState = &model.ProviderAuthState{
		Status:       model.ProviderAuthStatusReauthRequired,
		StatusReason: ProviderAuthReasonInvalidGrant,
		LastError:    "refresh_token_reused",
	}

	err := service.refreshChatGPTUsageSnapshot(context.Background(), provider)
	if err == nil {
		t.Fatal("refreshChatGPTUsageSnapshot returned nil error, want auth-state error")
	}

	var authErr *ProviderAuthStateError
	if !errors.As(err, &authErr) {
		t.Fatalf("refreshChatGPTUsageSnapshot error = %T, want ProviderAuthStateError", err)
	}
	if authErr.Status != ProviderAuthStatusReauthRequired {
		t.Fatalf("Status = %q, want %q", authErr.Status, ProviderAuthStatusReauthRequired)
	}
	if authErr.Reason != ProviderAuthReasonInvalidGrant {
		t.Fatalf("Reason = %q, want %q", authErr.Reason, ProviderAuthReasonInvalidGrant)
	}
	if authErr.LastError != "refresh_token_reused" {
		t.Fatalf("LastError = %q, want refresh_token_reused", authErr.LastError)
	}
}

func TestPersistProviderAuthState_NoStoreOrProviderIDIsNoOp(t *testing.T) {
	t.Parallel()

	service := NewService(Config{})
	if err := service.persistProviderAuthState(context.Background(), "provider-1", &model.ProviderAuthState{
		Status: model.ProviderAuthStatusActive,
	}); err != nil {
		t.Fatalf("persistProviderAuthState without store returned error: %v", err)
	}

	service = NewService(Config{CredentialStore: &recordingCredentialStore{}})
	if err := service.persistProviderAuthState(context.Background(), "", &model.ProviderAuthState{
		Status: model.ProviderAuthStatusActive,
	}); err != nil {
		t.Fatalf("persistProviderAuthState with empty provider id returned error: %v", err)
	}
}

func TestPersistProviderAuthState_WrapsStoreError(t *testing.T) {
	t.Parallel()

	expected := errors.New("boom")
	service := NewService(Config{
		CredentialStore: failingAuthStateStore{err: expected},
	})

	err := service.persistProviderAuthState(context.Background(), "provider-1", &model.ProviderAuthState{
		Status: model.ProviderAuthStatusActive,
	})
	if err == nil {
		t.Fatal("persistProviderAuthState error = nil, want wrapped error")
	}
	if !errors.Is(err, expected) {
		t.Fatalf("persistProviderAuthState error = %v, want wrapped %v", err, expected)
	}
}
