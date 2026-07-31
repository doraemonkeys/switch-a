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

func mustEncodeStoredChatGPTCredentialForLifecycleTest(
	t *testing.T,
	credential model.ChatGPTProviderCredential,
	status ProviderAuthStatus,
	reason string,
	lastError string,
) string {
	t.Helper()

	raw, err := encodeStoredChatGPTCredential(storedChatGPTCredential{
		ChatGPTProviderCredential: credential,
		AuthStatus:                status,
		AuthReason:                reason,
		LastError:                 lastError,
	})
	if err != nil {
		t.Fatalf("encodeStoredChatGPTCredential returned error: %v", err)
	}
	return raw
}

func TestRefreshProviderCredentials_MarksReauthRequiredOnTerminalRefreshFailure(t *testing.T) {
	now := time.Date(2026, time.March, 22, 12, 0, 0, 0, time.UTC)
	idToken := makeTestJWT(t, map[string]any{
		"iss": defaultOAuthIssuer,
		"aud": defaultOAuthClientID,
		"exp": now.Add(30 * time.Second).Unix(),
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "acct_test",
			"chatgpt_plan_type":  "plus",
		},
	})

	store := &recordingCredentialStore{}
	service := NewService(Config{
		Clock:           fixedClock{now: now},
		CredentialStore: store,
		HTTPClient: stubHTTPDoer{
			do: func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusBadRequest,
					Body:       io.NopCloser(strings.NewReader(`{"error":"invalid_grant"}`)),
				}, nil
			},
		},
	})

	provider := &model.Provider{
		ID:             "provider-gpt",
		CredentialType: model.ProviderCredentialTypeChatGPT,
	}
	mustApplyStoredLegacyChatGPTCredential(
		t,
		provider,
		mustEncodeStoredChatGPTCredentialForLifecycleTest(
			t,
			model.ChatGPTProviderCredential{
				AccessToken:   "access-token",
				RefreshToken:  "refresh-token",
				IDToken:       idToken,
				OAuthIssuer:   defaultOAuthIssuer,
				OAuthClientID: defaultOAuthClientID,
				AccountID:     "acct_test",
				Email:         "user@example.com",
				PlanType:      "plus",
				LastRefresh:   now.Add(-time.Hour),
				ExpiresAt:     now.Add(30 * time.Second),
			},
			ProviderAuthStatusActive,
			"",
			"",
		),
	)

	refreshed, err := service.RefreshProviderCredentials(context.Background(), provider)
	if !refreshed {
		t.Fatal("RefreshProviderCredentials returned false, want true")
	}

	var stateErr *ProviderAuthStateError
	if !errors.As(err, &stateErr) {
		t.Fatalf("error = %v, want ProviderAuthStateError", err)
	}
	if stateErr.Status != ProviderAuthStatusReauthRequired {
		t.Fatalf("status = %q, want %q", stateErr.Status, ProviderAuthStatusReauthRequired)
	}
	if stateErr.Reason != ProviderAuthReasonInvalidGrant {
		t.Fatalf("reason = %q, want %q", stateErr.Reason, ProviderAuthReasonInvalidGrant)
	}

	if store.calls != 0 {
		t.Fatalf("UpdateProviderCredential calls = %d, want 0", store.calls)
	}
	if store.authStateCalls != 1 {
		t.Fatalf("UpdateProviderAuthState calls = %d, want 1", store.authStateCalls)
	}

	view := BuildProviderAuthView(provider)
	if view == nil {
		t.Fatal("BuildProviderAuthView returned nil")
	}
	if view.Status != ProviderAuthStatusReauthRequired {
		t.Fatalf("view.Status = %q, want %q", view.Status, ProviderAuthStatusReauthRequired)
	}
	if view.Reason != ProviderAuthReasonInvalidGrant {
		t.Fatalf("view.Reason = %q, want %q", view.Reason, ProviderAuthReasonInvalidGrant)
	}
	if !strings.Contains(view.LastError, ProviderAuthReasonInvalidGrant) {
		t.Fatalf("view.LastError = %q, want invalid_grant detail", view.LastError)
	}
}

func TestRefreshProviderCredentials_DoesNotReviveReauthRequiredProvider(t *testing.T) {
	now := time.Date(2026, time.March, 22, 12, 0, 0, 0, time.UTC)
	idToken := makeTestJWT(t, map[string]any{
		"iss": defaultOAuthIssuer,
		"aud": defaultOAuthClientID,
		"exp": now.Add(30 * time.Second).Unix(),
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "acct_test",
			"chatgpt_plan_type":  "plus",
		},
	})

	store := &recordingCredentialStore{}
	service := NewService(Config{
		Clock:           fixedClock{now: now},
		CredentialStore: store,
		HTTPClient: stubHTTPDoer{
			do: func(req *http.Request) (*http.Response, error) {
				t.Fatalf("unexpected refresh request for reauth-required provider: %s", req.URL.String())
				return nil, nil
			},
		},
	})

	provider := &model.Provider{
		ID:             "provider-gpt",
		CredentialType: model.ProviderCredentialTypeChatGPT,
	}
	mustApplyStoredLegacyChatGPTCredential(
		t,
		provider,
		mustEncodeStoredChatGPTCredentialForLifecycleTest(
			t,
			model.ChatGPTProviderCredential{
				AccessToken:   "access-token",
				RefreshToken:  "refresh-token",
				IDToken:       idToken,
				OAuthIssuer:   defaultOAuthIssuer,
				OAuthClientID: defaultOAuthClientID,
				AccountID:     "acct_test",
				Email:         "user@example.com",
				PlanType:      "plus",
				LastRefresh:   now.Add(-time.Hour),
				ExpiresAt:     now.Add(30 * time.Second),
			},
			ProviderAuthStatusReauthRequired,
			ProviderAuthReasonInvalidGrant,
			"refresh chatgpt token failed with status 400 Bad Request: invalid_grant",
		),
	)

	refreshed, err := service.RefreshProviderCredentials(context.Background(), provider)
	if !refreshed {
		t.Fatal("RefreshProviderCredentials returned false, want true")
	}

	var stateErr *ProviderAuthStateError
	if !errors.As(err, &stateErr) {
		t.Fatalf("error = %v, want ProviderAuthStateError", err)
	}
	if stateErr.Status != ProviderAuthStatusReauthRequired {
		t.Fatalf("status = %q, want %q", stateErr.Status, ProviderAuthStatusReauthRequired)
	}

	if store.calls != 0 {
		t.Fatalf("UpdateProviderCredential calls = %d, want 0", store.calls)
	}
	if store.authStateCalls != 0 {
		t.Fatalf("UpdateProviderAuthState calls = %d, want 0", store.authStateCalls)
	}

	view := BuildProviderAuthView(provider)
	if view == nil {
		t.Fatal("BuildProviderAuthView returned nil")
	}
	if view.Status != ProviderAuthStatusReauthRequired {
		t.Fatalf("view.Status = %q, want %q", view.Status, ProviderAuthStatusReauthRequired)
	}
	if view.Reason != ProviderAuthReasonInvalidGrant {
		t.Fatalf("view.Reason = %q, want %q", view.Reason, ProviderAuthReasonInvalidGrant)
	}
}

func TestApplyChatGPTLogin_ClearsReauthRequiredState(t *testing.T) {
	now := time.Date(2026, time.March, 22, 12, 0, 0, 0, time.UTC)
	service := NewService(Config{Clock: fixedClock{now: now}})

	service.mu.Lock()
	service.completed["login-completed"] = completedLogin{
		loginID: "login-completed",
		credential: model.ChatGPTProviderCredential{
			AccessToken:  "new-access-token",
			RefreshToken: "new-refresh-token",
			IDToken:      "new-id-token",
			AccountID:    "acct_test",
			Email:        "user@example.com",
			PlanType:     "team",
			LastRefresh:  now,
			ExpiresAt:    now.Add(time.Hour),
		},
		expiresAt: now.Add(time.Minute),
	}
	service.mu.Unlock()

	provider := &model.Provider{
		ID:             "provider-gpt",
		CredentialType: model.ProviderCredentialTypeChatGPT,
	}
	mustApplyStoredLegacyChatGPTCredential(
		t,
		provider,
		mustEncodeStoredChatGPTCredentialForLifecycleTest(
			t,
			model.ChatGPTProviderCredential{
				AccessToken:   "access-token",
				RefreshToken:  "refresh-token",
				IDToken:       "id-token",
				AccountID:     "acct_test",
				Email:         "user@example.com",
				PlanType:      "plus",
				LastRefresh:   now.Add(-time.Hour),
				ExpiresAt:     now.Add(30 * time.Second),
				OAuthIssuer:   defaultOAuthIssuer,
				OAuthClientID: defaultOAuthClientID,
			},
			ProviderAuthStatusReauthRequired,
			ProviderAuthReasonInvalidGrant,
			"refresh chatgpt token failed with status 400 Bad Request: invalid_grant",
		),
	)

	if err := service.ApplyChatGPTLogin(provider, "login-completed"); err != nil {
		t.Fatalf("ApplyChatGPTLogin returned error: %v", err)
	}

	view := BuildProviderAuthView(provider)
	if view == nil {
		t.Fatal("BuildProviderAuthView returned nil")
	}
	if view.Status != ProviderAuthStatusActive {
		t.Fatalf("view.Status = %q, want %q", view.Status, ProviderAuthStatusActive)
	}
	if view.Reason != "" {
		t.Fatalf("view.Reason = %q, want empty", view.Reason)
	}
	if view.PlanType != "team" {
		t.Fatalf("view.PlanType = %q, want %q", view.PlanType, "team")
	}
}
