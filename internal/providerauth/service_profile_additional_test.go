package providerauth

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"
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

func TestRefreshChatGPTUsageSnapshot_MarksTokenInvalidationAsReauthRequired(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 22, 1, 50, 46, 0, time.UTC)
	credentialStore := &recordingCredentialStore{}
	service := NewService(Config{
		Clock:           fixedClock{now: now},
		CredentialStore: credentialStore,
		HTTPClient: stubHTTPDoer{
			do: func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusUnauthorized,
					Body: io.NopCloser(strings.NewReader(`{
						"error": {
							"message": "Your authentication token has been invalidated. Please try signing in again.",
							"code": "token_invalidated"
						}
					}`)),
				}, nil
			},
		},
	})
	provider := &model.Provider{
		ID:             "chatgpt-invalidated",
		CredentialType: model.ProviderCredentialTypeChatGPT,
		AuthState: &model.ProviderAuthState{
			Status: model.ProviderAuthStatusActive,
		},
	}
	mustApplyLegacyChatGPTCredential(t, provider, mustEncodeChatGPTCredential(t, model.ChatGPTProviderCredential{
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		AccountID:    "acct-invalidated",
	}))

	err := service.refreshChatGPTUsageSnapshot(context.Background(), provider)
	var authErr *ProviderAuthStateError
	if !errors.As(err, &authErr) {
		t.Fatalf("error = %T, want ProviderAuthStateError", err)
	}
	if authErr.Status != ProviderAuthStatusReauthRequired || authErr.Reason != ProviderAuthReasonTokenInvalidated {
		t.Fatalf("auth error = (%q, %q), want (%q, %q)", authErr.Status, authErr.Reason, ProviderAuthStatusReauthRequired, ProviderAuthReasonTokenInvalidated)
	}
	if provider.AuthState == nil || provider.AuthState.Status != model.ProviderAuthStatusReauthRequired {
		t.Fatalf("provider auth state = %#v, want reauth_required", provider.AuthState)
	}
	if provider.AuthState.StatusReason != ProviderAuthReasonTokenInvalidated {
		t.Fatalf("StatusReason = %q, want %q", provider.AuthState.StatusReason, ProviderAuthReasonTokenInvalidated)
	}
	if !strings.Contains(provider.AuthState.LastError, ProviderAuthReasonTokenInvalidated) {
		t.Fatalf("LastError = %q, want token invalidation detail", provider.AuthState.LastError)
	}
	if credentialStore.authStateCalls != 1 || credentialStore.authStateID != provider.ID {
		t.Fatalf("persisted auth state = (%d calls, %q), want (1, %q)", credentialStore.authStateCalls, credentialStore.authStateID, provider.ID)
	}
	if credentialStore.authState == nil || credentialStore.authState.LastTransitionAt == nil || !credentialStore.authState.LastTransitionAt.Equal(now) {
		t.Fatalf("persisted transition = %#v, want %s", credentialStore.authState, now)
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
