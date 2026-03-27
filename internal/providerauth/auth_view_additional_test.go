package providerauth

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"switch-a/internal/model"
)

func TestProviderAuthStateError_Error(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name string
		err  *ProviderAuthStateError
		want string
	}{
		{
			name: "reauth with reason",
			err: &ProviderAuthStateError{
				ProviderID: "chatgpt",
				Status:     ProviderAuthStatusReauthRequired,
				Reason:     ProviderAuthReasonInvalidGrant,
			},
			want: `provider "chatgpt" requires reauthentication (invalid_grant)`,
		},
		{
			name: "not connected",
			err: &ProviderAuthStateError{
				ProviderID: "chatgpt",
				Status:     ProviderAuthStatusNotConnected,
			},
			want: `provider "chatgpt" is not connected`,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.err.Error(); got != tc.want {
				t.Fatalf("Error() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestUnsupportedProviderAuthActionError_Error(t *testing.T) {
	t.Parallel()

	err := (&UnsupportedProviderAuthActionError{
		ProviderID:     "provider-1",
		Action:         "refresh-usage",
		CredentialType: model.ProviderCredentialTypeAPIKey,
	}).Error()
	if !strings.Contains(err, "refresh-usage is not supported") {
		t.Fatalf("Error() = %q, want action context", err)
	}
	if !strings.Contains(err, `provider "provider-1"`) {
		t.Fatalf("Error() = %q, want provider id", err)
	}
}

func TestStaticProviderCredentialReady(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		provider *model.Provider
		want     bool
	}{
		{
			name: "provider api key",
			provider: &model.Provider{
				APIKey: " provider-key ",
			},
			want: true,
		},
		{
			name: "api type override key",
			provider: &model.Provider{
				APITypes: []model.ProviderAPIType{
					{APIType: "codex", APIKey: " type-key "},
				},
			},
			want: true,
		},
		{
			name: "no key",
			provider: &model.Provider{
				APIKey: "   ",
				APITypes: []model.ProviderAPIType{
					{APIType: "codex", APIKey: "\t"},
				},
			},
			want: false,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := staticProviderCredentialReady(tc.provider); got != tc.want {
				t.Fatalf("staticProviderCredentialReady() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestHasCompleteChatGPTCredential(t *testing.T) {
	t.Parallel()

	readyCredential, err := model.EncodeChatGPTProviderCredential(&model.ChatGPTProviderCredential{
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		AccountID:    "acct-ready",
	})
	if err != nil {
		t.Fatalf("EncodeChatGPTProviderCredential returned error: %v", err)
	}

	testCases := []struct {
		name     string
		provider *model.Provider
		want     bool
	}{
		{
			name: "ready",
			provider: &model.Provider{
				Credential: &model.ProviderCredential{SecretData: readyCredential},
			},
			want: true,
		},
		{
			name: "invalid payload",
			provider: &model.Provider{
				Credential: &model.ProviderCredential{SecretData: `{`},
			},
			want: false,
		},
		{
			name: "missing refresh token",
			provider: &model.Provider{
				Credential: &model.ProviderCredential{SecretData: `{"access_token":"access","account_id":"acct"}`},
			},
			want: false,
		},
		{
			name:     "nil provider",
			provider: nil,
			want:     false,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := HasCompleteChatGPTCredential(tc.provider); got != tc.want {
				t.Fatalf("HasCompleteChatGPTCredential() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBuildProviderAuthView_ChatGPTFallbacks(t *testing.T) {
	t.Parallel()

	t.Run("api key without effective key is not connected", func(t *testing.T) {
		t.Parallel()

		view := BuildProviderAuthView(&model.Provider{
			ID:             "api-key-missing",
			CredentialType: model.ProviderCredentialTypeAPIKey,
			APIKey:         " ",
			APITypes: []model.ProviderAPIType{
				{APIType: "openai", APIKey: "\t"},
			},
			AuthState: &model.ProviderAuthState{
				Status: model.ProviderAuthStatusActive,
			},
		})
		if view == nil {
			t.Fatal("BuildProviderAuthView() = nil, want view")
		}
		if view.Status != ProviderAuthStatusNotConnected {
			t.Fatalf("Status = %q, want %q", view.Status, ProviderAuthStatusNotConnected)
		}
		if view.Reason != ProviderAuthReasonMissingAPIKey {
			t.Fatalf("Reason = %q, want %q", view.Reason, ProviderAuthReasonMissingAPIKey)
		}
	})

	t.Run("invalid credential marks not connected", func(t *testing.T) {
		t.Parallel()

		view := BuildProviderAuthView(&model.Provider{
			ID:             "chatgpt-invalid",
			CredentialType: model.ProviderCredentialTypeChatGPT,
			Credential: &model.ProviderCredential{
				SecretData: `{`,
			},
			AuthState: &model.ProviderAuthState{
				Status: model.ProviderAuthStatusActive,
			},
		})
		if view == nil {
			t.Fatal("BuildProviderAuthView() = nil, want view")
		}
		if view.Status != ProviderAuthStatusNotConnected {
			t.Fatalf("Status = %q, want %q", view.Status, ProviderAuthStatusNotConnected)
		}
		if view.Reason != ProviderAuthReasonCredentialInvalid {
			t.Fatalf("Reason = %q, want %q", view.Reason, ProviderAuthReasonCredentialInvalid)
		}
		if view.LastError == "" {
			t.Fatal("LastError = empty, want decode error")
		}
	})

	t.Run("missing credential requires login", func(t *testing.T) {
		t.Parallel()

		view := BuildProviderAuthView(&model.Provider{
			ID:             "chatgpt-missing",
			CredentialType: model.ProviderCredentialTypeChatGPT,
			AuthState: &model.ProviderAuthState{
				Status: model.ProviderAuthStatusActive,
			},
		})
		if view == nil {
			t.Fatal("BuildProviderAuthView() = nil, want view")
		}
		if view.Status != ProviderAuthStatusNotConnected {
			t.Fatalf("Status = %q, want %q", view.Status, ProviderAuthStatusNotConnected)
		}
		if view.Reason != ProviderAuthReasonLoginRequired {
			t.Fatalf("Reason = %q, want %q", view.Reason, ProviderAuthReasonLoginRequired)
		}
	})

	t.Run("stored auth state reason wins", func(t *testing.T) {
		t.Parallel()

		expiry := time.Now().UTC().Add(10 * time.Minute)
		view := BuildProviderAuthView(&model.Provider{
			ID:             "chatgpt-reauth",
			CredentialType: model.ProviderCredentialTypeChatGPT,
			AuthState: &model.ProviderAuthState{
				Status:       model.ProviderAuthStatusReauthRequired,
				StatusReason: ProviderAuthReasonInvalidGrant,
				LastError:    "refresh_token_reused",
				ExpiresAt:    &expiry,
			},
		})
		if view == nil {
			t.Fatal("BuildProviderAuthView() = nil, want view")
		}
		if view.Reason != ProviderAuthReasonInvalidGrant {
			t.Fatalf("Reason = %q, want %q", view.Reason, ProviderAuthReasonInvalidGrant)
		}
		if view.LastError != "refresh_token_reused" {
			t.Fatalf("LastError = %q, want refresh_token_reused", view.LastError)
		}
		if view.ExpiresAt == nil || !view.ExpiresAt.Equal(expiry) {
			t.Fatalf("ExpiresAt = %v, want %v", view.ExpiresAt, expiry)
		}
	})
}

func TestStoredChatGPTAuthHelpers(t *testing.T) {
	t.Parallel()

	t.Run("nil stored credential defaults to login required", func(t *testing.T) {
		t.Parallel()

		if got := normalizeStoredChatGPTAuthStatus(nil); got != ProviderAuthStatusNotConnected {
			t.Fatalf("normalizeStoredChatGPTAuthStatus(nil) = %q, want %q", got, ProviderAuthStatusNotConnected)
		}
		if got := reasonForStoredChatGPTAuthStatus(nil, ProviderAuthStatusNotConnected); got != ProviderAuthReasonLoginRequired {
			t.Fatalf("reasonForStoredChatGPTAuthStatus(nil, not_connected) = %q, want %q", got, ProviderAuthReasonLoginRequired)
		}
		view := buildChatGPTAuthView(nil)
		if view.Status != ProviderAuthStatusNotConnected || view.Reason != ProviderAuthReasonLoginRequired {
			t.Fatalf("buildChatGPTAuthView(nil) = %+v, want not_connected/login_required", view)
		}
	})

	t.Run("invalid stored status falls back to ready credential", func(t *testing.T) {
		t.Parallel()

		stored := &storedChatGPTCredential{
			ChatGPTProviderCredential: model.ChatGPTProviderCredential{
				AccessToken:  "access-token",
				RefreshToken: "refresh-token",
				AccountID:    "acct-ready",
				Email:        "user@example.com",
			},
			AuthStatus: ProviderAuthStatus("bogus"),
			LastError:  "ignored",
		}
		if got := normalizeStoredChatGPTAuthStatus(stored); got != ProviderAuthStatusActive {
			t.Fatalf("normalizeStoredChatGPTAuthStatus() = %q, want %q", got, ProviderAuthStatusActive)
		}
		view := buildChatGPTAuthView(stored)
		if view.Status != ProviderAuthStatusActive {
			t.Fatalf("Status = %q, want %q", view.Status, ProviderAuthStatusActive)
		}
		if view.Email != "user@example.com" {
			t.Fatalf("Email = %q, want user@example.com", view.Email)
		}
	})

	t.Run("explicit stored reason wins", func(t *testing.T) {
		t.Parallel()

		stored := &storedChatGPTCredential{
			AuthStatus: ProviderAuthStatusReauthRequired,
			AuthReason: ProviderAuthReasonRefreshTokenReused,
		}
		if got := reasonForStoredChatGPTAuthStatus(stored, ProviderAuthStatusReauthRequired); got != ProviderAuthReasonRefreshTokenReused {
			t.Fatalf("reasonForStoredChatGPTAuthStatus() = %q, want %q", got, ProviderAuthReasonRefreshTokenReused)
		}
	})

	t.Run("status fallback reasons follow lifecycle", func(t *testing.T) {
		t.Parallel()

		stored := &storedChatGPTCredential{}
		if got := reasonForStoredChatGPTAuthStatus(stored, ProviderAuthStatusActive); got != "" {
			t.Fatalf("reasonForStoredChatGPTAuthStatus(active) = %q, want empty", got)
		}
		if got := reasonForStoredChatGPTAuthStatus(stored, ProviderAuthStatusReauthRequired); got != ProviderAuthReasonCredentialInvalid {
			t.Fatalf("reasonForStoredChatGPTAuthStatus(reauth_required) = %q, want %q", got, ProviderAuthReasonCredentialInvalid)
		}
		if got := reasonForStoredChatGPTAuthStatus(stored, ProviderAuthStatusNotConnected); got != ProviderAuthReasonLoginRequired {
			t.Fatalf("reasonForStoredChatGPTAuthStatus(not_connected) = %q, want %q", got, ProviderAuthReasonLoginRequired)
		}
	})
}

func TestRefreshProviderUsage_FailurePaths(t *testing.T) {
	t.Parallel()

	t.Run("usage fetch error is returned", func(t *testing.T) {
		t.Parallel()

		expected := errors.New("usage endpoint down")
		service := NewService(Config{
			HTTPClient: stubHTTPDoer{
				do: func(*http.Request) (*http.Response, error) {
					return nil, expected
				},
			},
		})
		provider := &model.Provider{
			ID:             "chatgpt-fetch-error",
			CredentialType: model.ProviderCredentialTypeChatGPT,
			AuthState: &model.ProviderAuthState{
				Status: model.ProviderAuthStatusActive,
			},
		}
		mustApplyLegacyChatGPTCredential(t, provider, mustEncodeChatGPTCredential(t, model.ChatGPTProviderCredential{
			AccessToken:  "access-token",
			RefreshToken: "refresh-token",
			AccountID:    "acct-fetch-error",
		}))
		provider.AuthState = &model.ProviderAuthState{Status: model.ProviderAuthStatusActive}

		refreshed, err := service.RefreshProviderUsage(context.Background(), provider)
		if !refreshed {
			t.Fatal("RefreshProviderUsage returned false, want true for ChatGPT provider")
		}
		if err == nil || !strings.Contains(err.Error(), expected.Error()) {
			t.Fatalf("RefreshProviderUsage error = %v, want message containing %q", err, expected.Error())
		}
	})

	t.Run("persist error is returned after successful fetch", func(t *testing.T) {
		t.Parallel()

		expected := errors.New("persist failed")
		service := NewService(Config{
			CredentialStore: failingAuthStateStore{err: expected},
			HTTPClient: stubHTTPDoer{
				do: func(*http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusOK,
						Body: io.NopCloser(strings.NewReader(`{
							"plan_type":"team",
							"rate_limit":{
								"primary_window":{"used_percent":12,"limit_window_seconds":18000,"reset_at":1770000000}
							}
						}`)),
					}, nil
				},
			},
		})
		provider := &model.Provider{
			ID:             "chatgpt-persist-error",
			CredentialType: model.ProviderCredentialTypeChatGPT,
			AuthState: &model.ProviderAuthState{
				Status: model.ProviderAuthStatusActive,
			},
		}
		mustApplyLegacyChatGPTCredential(t, provider, mustEncodeChatGPTCredential(t, model.ChatGPTProviderCredential{
			AccessToken:  "access-token",
			RefreshToken: "refresh-token",
			AccountID:    "acct-persist-error",
		}))
		provider.AuthState = &model.ProviderAuthState{Status: model.ProviderAuthStatusActive}

		refreshed, err := service.RefreshProviderUsage(context.Background(), provider)
		if !refreshed {
			t.Fatal("RefreshProviderUsage returned false, want true for ChatGPT provider")
		}
		if !errors.Is(err, expected) {
			t.Fatalf("RefreshProviderUsage error = %v, want wrapped %v", err, expected)
		}
	})
}
