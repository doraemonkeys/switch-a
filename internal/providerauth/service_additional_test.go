package providerauth

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"switch-a/internal/model"
)

func makeCredentialForServiceTests(now time.Time, expiresAt time.Time, issuer string, audience any) model.ChatGPTProviderCredential {
	return model.ChatGPTProviderCredential{
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		IDToken: makeTestJWT(&testing.T{}, map[string]any{
			"iss": issuer,
			"aud": audience,
			"exp": expiresAt.Unix(),
			"https://api.openai.com/auth": map[string]any{
				"chatgpt_account_id": "acct_test",
				"chatgpt_plan_type":  "plus",
			},
		}),
		AccountID:   "acct_test",
		Email:       "user@example.com",
		PlanType:    "plus",
		LastRefresh: now.Add(-time.Minute),
		ExpiresAt:   expiresAt,
	}
}

func TestLoopbackHelpers(t *testing.T) {
	if got := LoopbackCallbackAddress(); got != "http://localhost:1455/auth/callback" {
		t.Fatalf("LoopbackCallbackAddress() = %q, want %q", got, "http://localhost:1455/auth/callback")
	}
	if got := LoopbackCallbackPort(); got != 1455 {
		t.Fatalf("LoopbackCallbackPort() = %d, want %d", got, 1455)
	}
	if got := ChatGPTCodexBaseURL(); got != chatGPTCodexBaseURL {
		t.Fatalf("ChatGPTCodexBaseURL() = %q, want %q", got, chatGPTCodexBaseURL)
	}
	specs := loopbackListenerSpecs()
	if len(specs) != 2 {
		t.Fatalf("len(loopbackListenerSpecs()) = %d, want 2", len(specs))
	}
	if specs[0].network != "tcp4" || loopbackListenerAddr(specs[0]) != "127.0.0.1:1455" {
		t.Fatalf("first loopback listener = %#v, want tcp4 127.0.0.1:1455", specs[0])
	}
	if specs[1].network != "tcp6" || loopbackListenerAddr(specs[1]) != "[::1]:1455" {
		t.Fatalf("second loopback listener = %#v, want tcp6 [::1]:1455", specs[1])
	}
}

func TestBuildProviderAuthView_StaticAndFallbackPaths(t *testing.T) {
	if got := BuildProviderAuthView(nil); got != nil {
		t.Fatalf("BuildProviderAuthView(nil) = %#v, want nil", got)
	}

	t.Run("static provider reports active auth", func(t *testing.T) {
		profile := BuildProviderAuthView(&model.Provider{
			CredentialType: model.ProviderCredentialTypeAPIKey,
			APITypes: []model.ProviderAPIType{
				{APIType: "responses", APIKey: "api-type-key"},
			},
		})
		if profile == nil {
			t.Fatal("BuildProviderAuthView returned nil")
		}
		if profile.Type != model.ProviderCredentialTypeAPIKey {
			t.Fatalf("Type = %q, want %q", profile.Type, model.ProviderCredentialTypeAPIKey)
		}
		if profile.Status != ProviderAuthStatusActive {
			t.Fatalf("Status = %q, want %q", profile.Status, ProviderAuthStatusActive)
		}
	})

	t.Run("api-key provider without a usable key reports not connected", func(t *testing.T) {
		profile := BuildProviderAuthView(&model.Provider{
			ID:             "provider-missing-key",
			CredentialType: model.ProviderCredentialTypeAPIKey,
			AuthState: &model.ProviderAuthState{
				Status: model.ProviderAuthStatusActive,
			},
			APITypes: []model.ProviderAPIType{
				{APIType: "responses", APIKey: "   "},
			},
		})
		if profile == nil {
			t.Fatal("BuildProviderAuthView returned nil")
		}
		if profile.Status != ProviderAuthStatusNotConnected {
			t.Fatalf("Status = %q, want %q", profile.Status, ProviderAuthStatusNotConnected)
		}
		if profile.Reason != ProviderAuthReasonMissingAPIKey {
			t.Fatalf("Reason = %q, want %q", profile.Reason, ProviderAuthReasonMissingAPIKey)
		}
	})

	t.Run("invalid chatgpt payload keeps type without reporting active auth", func(t *testing.T) {
		profile := BuildProviderAuthView(&model.Provider{
			CredentialType: model.ProviderCredentialTypeChatGPT,
			Credential:     &model.ProviderCredential{SecretData: "{"},
		})
		if profile == nil {
			t.Fatal("BuildProviderAuthView returned nil")
		}
		if profile.Type != model.ProviderCredentialTypeChatGPT {
			t.Fatalf("Type = %q, want %q", profile.Type, model.ProviderCredentialTypeChatGPT)
		}
		if profile.Status != ProviderAuthStatusNotConnected {
			t.Fatalf("Status = %q, want %q", profile.Status, ProviderAuthStatusNotConnected)
		}
		if profile.Reason != ProviderAuthReasonCredentialInvalid {
			t.Fatalf("Reason = %q, want %q", profile.Reason, ProviderAuthReasonCredentialInvalid)
		}
	})
}

func TestBuildProviderAuthView_UsesUsagePlanFallback(t *testing.T) {
	now := time.Date(2026, time.March, 22, 12, 0, 0, 0, time.UTC)
	profile := buildChatGPTAuthView(newStoredChatGPTCredential(&model.ChatGPTProviderCredential{
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		IDToken:      "id-token",
		AccountID:    "acct_test",
		Email:        "user@example.com",
		Usage: &model.ProviderUsageSnapshot{
			PlanType: "team",
		},
		LastRefresh: now.Add(-time.Minute),
		ExpiresAt:   now.Add(time.Hour),
	}, ProviderAuthStatusActive, "", ""))

	if profile.Status != ProviderAuthStatusActive {
		t.Fatalf("Status = %q, want %q", profile.Status, ProviderAuthStatusActive)
	}
	if profile.PlanType != "team" {
		t.Fatalf("PlanType = %q, want %q", profile.PlanType, "team")
	}
	if profile.ExpiresAt == nil || !profile.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("ExpiresAt = %#v, want %s", profile.ExpiresAt, now.Add(time.Hour))
	}
	if profile.LastRefreshAt == nil || !profile.LastRefreshAt.Equal(now.Add(-time.Minute)) {
		t.Fatalf("LastRefreshAt = %#v, want %s", profile.LastRefreshAt, now.Add(-time.Minute))
	}

	if got := buildChatGPTAuthView(nil); got == nil || got.Type != model.ProviderCredentialTypeChatGPT {
		t.Fatalf("buildChatGPTAuthView(nil) = %#v, want empty chatgpt profile", got)
	}
}

func TestBuildProviderAuthView_FallsBackForNonChatGPTAndErrors(t *testing.T) {
	provider := &model.Provider{
		CredentialType: model.ProviderCredentialTypeAPIKey,
		APIKey:         "api-key",
	}
	if view := BuildProviderAuthView(provider); view == nil || view.Status != ProviderAuthStatusActive {
		t.Fatalf("BuildProviderAuthView = %#v, want active static profile", view)
	}

	chatGPTProvider := &model.Provider{
		ID:             "provider-gpt",
		CredentialType: model.ProviderCredentialTypeChatGPT,
		Credential:     &model.ProviderCredential{SecretData: "{"},
	}
	view := BuildProviderAuthView(chatGPTProvider)
	if view == nil {
		t.Fatal("BuildProviderAuthView = nil, want fallback profile")
	}
	if view.Type != model.ProviderCredentialTypeChatGPT {
		t.Fatalf("Type = %q, want %q", view.Type, model.ProviderCredentialTypeChatGPT)
	}
}

func TestNormalizeProviderForPersistence_AppliesCredentialTypeInvariants(t *testing.T) {
	provider := &model.Provider{
		ID:             "provider-gpt",
		CredentialType: model.ProviderCredentialTypeChatGPT,
		APIKey:         "should-clear",
		AuthMode:       authModeXAPIKey,
		APITypes: []model.ProviderAPIType{
			{APIType: "responses", BaseURL: "https://example.com"},
		},
	}
	NormalizeProviderForPersistence(provider)

	if provider.APIKey != "" {
		t.Fatalf("APIKey = %q, want empty string", provider.APIKey)
	}
	if provider.AuthMode != authModeBearer {
		t.Fatalf("AuthMode = %q, want %q", provider.AuthMode, authModeBearer)
	}
	if len(provider.APITypes) != 1 {
		t.Fatalf("len(APITypes) = %d, want 1", len(provider.APITypes))
	}
	if provider.APITypes[0].APIType != codexAPIType {
		t.Fatalf("APIType = %q, want %q", provider.APITypes[0].APIType, codexAPIType)
	}
	if provider.APITypes[0].BaseURL != chatGPTCodexBaseURL {
		t.Fatalf("BaseURL = %q, want %q", provider.APITypes[0].BaseURL, chatGPTCodexBaseURL)
	}

	apiKeyProvider := &model.Provider{
		CredentialType: model.ProviderCredentialTypeAPIKey,
		Credential:     &model.ProviderCredential{SecretData: "should-clear"},
	}
	NormalizeProviderForPersistence(apiKeyProvider)
	if apiKeyProvider.Credential != nil {
		t.Fatalf("Credential = %#v, want nil", apiKeyProvider.Credential)
	}
}

func TestStartChatGPTLogin_StoresPendingSessionAndBuildsAuthorizeURL(t *testing.T) {
	now := time.Date(2026, time.March, 22, 12, 0, 0, 0, time.UTC)
	service := NewService(Config{Clock: fixedClock{now: now}})

	service.mu.Lock()
	service.storePendingLoginLocked(pendingLogin{
		loginID:      "expired-login",
		state:        "expired-state",
		codeVerifier: "expired-verifier",
		expiresAt:    now.Add(-time.Second),
	})
	service.completed["expired-completed"] = completedLogin{
		loginID:   "expired-completed",
		expiresAt: now.Add(-time.Second),
	}
	service.mu.Unlock()

	response, err := service.StartChatGPTLogin()
	if err != nil {
		t.Fatalf("StartChatGPTLogin returned error: %v", err)
	}

	parsedURL, err := url.Parse(response.AuthURL)
	if err != nil {
		t.Fatalf("url.Parse returned error: %v", err)
	}
	if parsedURL.Scheme != "https" || parsedURL.Host != "auth.openai.com" || parsedURL.Path != "/oauth/authorize" {
		t.Fatalf("authURL = %s, want auth.openai.com/oauth/authorize", parsedURL.String())
	}

	query := parsedURL.Query()
	if query.Get("response_type") != "code" {
		t.Fatalf("response_type = %q, want %q", query.Get("response_type"), "code")
	}
	if query.Get("client_id") != defaultOAuthClientID {
		t.Fatalf("client_id = %q, want %q", query.Get("client_id"), defaultOAuthClientID)
	}
	if query.Get("redirect_uri") != LoopbackCallbackAddress() {
		t.Fatalf("redirect_uri = %q, want %q", query.Get("redirect_uri"), LoopbackCallbackAddress())
	}
	if query.Get("scope") != defaultOAuthScope {
		t.Fatalf("scope = %q, want %q", query.Get("scope"), defaultOAuthScope)
	}
	if query.Get("originator") != defaultOAuthOriginator {
		t.Fatalf("originator = %q, want %q", query.Get("originator"), defaultOAuthOriginator)
	}
	if query.Get("code_challenge") == "" {
		t.Fatal("code_challenge = empty, want PKCE challenge")
	}

	service.mu.Lock()
	pending, ok := service.pendingByLoginID[response.LoginID]
	_, expiredPendingStillTracked := service.pendingByLoginID["expired-login"]
	_, expiredCompletedStillTracked := service.completed["expired-completed"]
	service.mu.Unlock()

	if !ok {
		t.Fatalf("pendingByLoginID[%q] missing", response.LoginID)
	}
	if pending.state == "" || pending.codeVerifier == "" {
		t.Fatalf("pending login = %#v, want state and verifier", pending)
	}
	if !pending.expiresAt.Equal(now.Add(loginSessionTTL)) {
		t.Fatalf("expiresAt = %s, want %s", pending.expiresAt, now.Add(loginSessionTTL))
	}
	if query.Get("state") != pending.state {
		t.Fatalf("state query = %q, want %q", query.Get("state"), pending.state)
	}
	if expiredPendingStillTracked {
		t.Fatal("expired pending login should be pruned before storing a new one")
	}
	if expiredCompletedStillTracked {
		t.Fatal("expired completed login should be pruned before storing a new one")
	}
}

func TestApplyAndFinalizeChatGPTLogin_ConsumesSessionOnlyAfterFinalize(t *testing.T) {
	now := time.Date(2026, time.March, 22, 12, 0, 0, 0, time.UTC)
	credential := model.ChatGPTProviderCredential{
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		IDToken:      "id-token",
		AccountID:    "acct_test",
	}
	service := NewService(Config{Clock: fixedClock{now: now}})

	service.mu.Lock()
	service.completed["login-1"] = completedLogin{
		loginID:    "login-1",
		credential: credential,
		expiresAt:  now.Add(time.Minute),
	}
	service.mu.Unlock()

	provider := &model.Provider{ID: "provider-gpt"}
	if err := service.ApplyChatGPTLogin(provider, "login-1"); err != nil {
		t.Fatalf("ApplyChatGPTLogin returned error: %v", err)
	}
	applied, err := DecodeProviderChatGPTCredential(provider)
	if err != nil {
		t.Fatalf("decodeProviderChatGPTCredential returned error: %v", err)
	}
	if applied.AccessToken != credential.AccessToken {
		t.Fatalf("AccessToken = %q, want %q", applied.AccessToken, credential.AccessToken)
	}

	service.mu.Lock()
	_, stillTracked := service.completed["login-1"]
	service.mu.Unlock()
	if !stillTracked {
		t.Fatal("completed login should remain available until finalize")
	}

	if err := service.FinalizeChatGPTLogin("login-1"); err != nil {
		t.Fatalf("FinalizeChatGPTLogin returned error: %v", err)
	}

	service.mu.Lock()
	_, stillTracked = service.completed["login-1"]
	service.mu.Unlock()
	if stillTracked {
		t.Fatal("completed login should be deleted after finalize")
	}

	err = service.FinalizeChatGPTLogin("login-1")
	if err == nil || !strings.Contains(err.Error(), "not found or expired") {
		t.Fatalf("error = %v, want not found or expired", err)
	}
}

func TestApplyProviderCredentials_StaticModesAndErrors(t *testing.T) {
	service := NewService(Config{})

	t.Run("chatgpt rejects non-codex api type", func(t *testing.T) {
		provider := &model.Provider{
			ID:             "provider-gpt",
			CredentialType: model.ProviderCredentialTypeChatGPT,
		}
		mustApplyLegacyChatGPTCredential(t, provider, mustEncodeChatGPTCredential(t, model.ChatGPTProviderCredential{
			AccessToken:  "access-token",
			RefreshToken: "refresh-token",
			IDToken:      "id-token",
			AccountID:    "acct_test",
			ExpiresAt:    time.Now().UTC().Add(time.Hour),
		}))
		err := service.ApplyProviderCredentials(context.Background(), make(http.Header), provider, "responses", authModeBearer, nil)
		if err == nil || !strings.Contains(err.Error(), "only supports api_type") {
			t.Fatalf("error = %v, want api_type validation", err)
		}
	})

	t.Run("static bearer auth", func(t *testing.T) {
		headers := make(http.Header)
		err := service.ApplyProviderCredentials(context.Background(), headers, &model.Provider{
			CredentialType: model.ProviderCredentialTypeAPIKey,
			APIKey:         "api-key",
		}, "responses", authModeBearer, nil)
		if err != nil {
			t.Fatalf("ApplyProviderCredentials returned error: %v", err)
		}
		if got := headers.Get("Authorization"); got != "Bearer api-key" {
			t.Fatalf("Authorization = %q, want %q", got, "Bearer api-key")
		}
	})

	t.Run("auto mode mirrors caller x-api-key", func(t *testing.T) {
		headers := make(http.Header)
		originalReq := httptest.NewRequest(http.MethodPost, "/responses", nil)
		originalReq.Header.Set("X-Api-Key", "incoming")

		err := service.ApplyProviderCredentials(context.Background(), headers, &model.Provider{
			CredentialType: model.ProviderCredentialTypeAPIKey,
			APIKey:         "api-key",
		}, "responses", authModeAuto, originalReq)
		if err != nil {
			t.Fatalf("ApplyProviderCredentials returned error: %v", err)
		}
		if got := headers.Get("x-api-key"); got != "api-key" {
			t.Fatalf("x-api-key = %q, want %q", got, "api-key")
		}
	})
}

func TestRefreshProviderCredentials_StaticProviderReturnsFalse(t *testing.T) {
	service := NewService(Config{})

	refreshed, err := service.RefreshProviderCredentials(context.Background(), &model.Provider{
		CredentialType: model.ProviderCredentialTypeAPIKey,
	})
	if err != nil {
		t.Fatalf("RefreshProviderCredentials returned error: %v", err)
	}
	if refreshed {
		t.Fatal("RefreshProviderCredentials returned true for static provider")
	}
}

func TestBuildProviderAuthView_NotConnectedWithoutChatGPTLogin(t *testing.T) {
	view := BuildProviderAuthView(&model.Provider{
		ID:             "provider-gpt",
		CredentialType: model.ProviderCredentialTypeChatGPT,
	})
	if view == nil {
		t.Fatal("BuildProviderAuthView returned nil")
	}
	if view.Status != ProviderAuthStatusNotConnected {
		t.Fatalf("Status = %q, want %q", view.Status, ProviderAuthStatusNotConnected)
	}
	if view.Reason != ProviderAuthReasonLoginRequired {
		t.Fatalf("Reason = %q, want %q", view.Reason, ProviderAuthReasonLoginRequired)
	}
}

func TestRefreshProviderCredentials_MarksReauthRequiredOnTerminalRefreshFailure_WithAuthStateRow(t *testing.T) {
	now := time.Date(2026, time.March, 22, 12, 0, 0, 0, time.UTC)
	idToken := makeTestJWT(t, map[string]any{
		"iss": "https://issuer.example.com/",
		"aud": "client-refresh",
		"exp": now.Add(30 * time.Second).Unix(),
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "acct_test",
			"chatgpt_plan_type":  "plus",
		},
	})
	credentialData, err := encodeStoredChatGPTCredential(storedChatGPTCredential{
		ChatGPTProviderCredential: model.ChatGPTProviderCredential{
			AccessToken:   "access-token",
			RefreshToken:  "refresh-token",
			IDToken:       idToken,
			OAuthIssuer:   "https://issuer.example.com/",
			OAuthClientID: "client-refresh",
			AccountID:     "acct_test",
			Email:         "user@example.com",
			LastRefresh:   now.Add(-time.Hour),
			ExpiresAt:     now.Add(30 * time.Second),
		},
		AuthStatus: ProviderAuthStatusActive,
	})
	if err != nil {
		t.Fatalf("encodeStoredChatGPTCredential returned error: %v", err)
	}

	store := &recordingCredentialStore{}
	service := NewService(Config{
		Clock:           fixedClock{now: now},
		CredentialStore: store,
		HTTPClient: stubHTTPDoer{
			do: func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusBadRequest,
					Body:       io.NopCloser(strings.NewReader(`refresh_token_reused`)),
				}, nil
			},
		},
	})

	provider := &model.Provider{
		ID:             "provider-gpt",
		CredentialType: model.ProviderCredentialTypeChatGPT,
		AuthState: &model.ProviderAuthState{
			Status: model.ProviderAuthStatusActive,
		},
	}
	mustApplyStoredLegacyChatGPTCredential(t, provider, credentialData)

	refreshed, err := service.RefreshProviderCredentials(context.Background(), provider)
	if !refreshed {
		t.Fatal("RefreshProviderCredentials returned false, want true")
	}
	var stateErr *ProviderAuthStateError
	if !errors.As(err, &stateErr) {
		t.Fatalf("error = %v, want ProviderAuthStateError", err)
	}
	if stateErr.Status != ProviderAuthStatusReauthRequired {
		t.Fatalf("Status = %q, want %q", stateErr.Status, ProviderAuthStatusReauthRequired)
	}
	if stateErr.Reason != ProviderAuthReasonRefreshTokenReused {
		t.Fatalf("Reason = %q, want %q", stateErr.Reason, ProviderAuthReasonRefreshTokenReused)
	}
	if store.calls != 0 {
		t.Fatalf("UpdateProviderCredential calls = %d, want 0", store.calls)
	}
	if store.authStateCalls != 1 {
		t.Fatalf("UpdateProviderAuthState calls = %d, want 1", store.authStateCalls)
	}
	if provider.AuthState == nil {
		t.Fatal("AuthState = nil, want persisted reauth_required snapshot")
	}
	if provider.AuthState.Status != model.ProviderAuthStatusReauthRequired {
		t.Fatalf("AuthState.Status = %q, want %q", provider.AuthState.Status, model.ProviderAuthStatusReauthRequired)
	}
	if provider.AuthState.StatusReason != ProviderAuthReasonRefreshTokenReused {
		t.Fatalf("AuthState.StatusReason = %q, want %q", provider.AuthState.StatusReason, ProviderAuthReasonRefreshTokenReused)
	}
	if !strings.Contains(provider.AuthState.LastError, "refresh_token_reused") {
		t.Fatalf("AuthState.LastError = %q, want refresh_token_reused marker", provider.AuthState.LastError)
	}
}

func TestRefreshProviderCredentials_DoesNotReviveReauthRequiredProvider_WithExplicitAuthState(t *testing.T) {
	now := time.Date(2026, time.March, 22, 12, 0, 0, 0, time.UTC)
	idToken := makeTestJWT(t, map[string]any{
		"iss": "https://issuer.example.com/",
		"aud": "client-refresh",
		"exp": now.Add(30 * time.Second).Unix(),
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "acct_test",
		},
	})
	credentialData, err := encodeStoredChatGPTCredential(storedChatGPTCredential{
		ChatGPTProviderCredential: model.ChatGPTProviderCredential{
			AccessToken:   "access-token",
			RefreshToken:  "refresh-token",
			IDToken:       idToken,
			OAuthIssuer:   "https://issuer.example.com/",
			OAuthClientID: "client-refresh",
			AccountID:     "acct_test",
			LastRefresh:   now.Add(-time.Hour),
			ExpiresAt:     now.Add(30 * time.Second),
		},
		AuthStatus: ProviderAuthStatusReauthRequired,
		AuthReason: ProviderAuthReasonInvalidGrant,
		LastError:  "invalid_grant",
	})
	if err != nil {
		t.Fatalf("encodeStoredChatGPTCredential returned error: %v", err)
	}

	service := NewService(Config{
		Clock: fixedClock{now: now},
		HTTPClient: stubHTTPDoer{
			do: func(req *http.Request) (*http.Response, error) {
				t.Fatalf("unexpected refresh request: %s", req.URL.String())
				return nil, nil
			},
		},
	})

	provider := &model.Provider{
		ID:             "provider-gpt",
		CredentialType: model.ProviderCredentialTypeChatGPT,
	}
	mustApplyStoredLegacyChatGPTCredential(t, provider, credentialData)

	refreshed, err := service.RefreshProviderCredentials(context.Background(), provider)
	if !refreshed {
		t.Fatal("RefreshProviderCredentials returned false, want true")
	}
	var stateErr *ProviderAuthStateError
	if !errors.As(err, &stateErr) {
		t.Fatalf("error = %v, want ProviderAuthStateError", err)
	}
	if stateErr.Status != ProviderAuthStatusReauthRequired {
		t.Fatalf("Status = %q, want %q", stateErr.Status, ProviderAuthStatusReauthRequired)
	}
}

func TestEnsureFreshChatGPTCredential_SkipsRefreshWhenStillValid(t *testing.T) {
	now := time.Date(2026, time.March, 22, 12, 0, 0, 0, time.UTC)
	service := NewService(Config{
		Clock: fixedClock{now: now},
		HTTPClient: stubHTTPDoer{
			do: func(req *http.Request) (*http.Response, error) {
				t.Fatalf("unexpected refresh request: %s", req.URL.String())
				return nil, nil
			},
		},
	})

	idToken := makeTestJWT(t, map[string]any{
		"exp": now.Add(2 * time.Hour).Unix(),
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "acct_test",
		},
	})
	provider := &model.Provider{
		ID:             "provider-gpt",
		CredentialType: model.ProviderCredentialTypeChatGPT,
	}
	mustApplyLegacyChatGPTCredential(t, provider, mustEncodeChatGPTCredential(t, model.ChatGPTProviderCredential{
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		IDToken:      idToken,
		AccountID:    "acct_test",
		ExpiresAt:    now.Add(2 * time.Hour),
	}))

	credential, err := service.ensureFreshChatGPTCredential(context.Background(), provider, false)
	if err != nil {
		t.Fatalf("ensureFreshChatGPTCredential returned error: %v", err)
	}
	if credential.AccessToken != "access-token" {
		t.Fatalf("AccessToken = %q, want %q", credential.AccessToken, "access-token")
	}
}

func TestEnsureFreshChatGPTCredential_RefreshesAndPersists(t *testing.T) {
	now := time.Date(2026, time.March, 22, 12, 0, 0, 0, time.UTC)
	oldIDToken := makeTestJWT(t, map[string]any{
		"iss": "https://issuer.example.com/",
		"aud": []any{"", "client-refresh"},
		"exp": now.Add(30 * time.Second).Unix(),
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "acct_test",
			"chatgpt_plan_type":  "plus",
		},
	})
	newExpiry := now.Add(2 * time.Hour)
	newIDToken := makeTestJWT(t, map[string]any{
		"iss": "https://issuer.example.com/",
		"aud": "client-refresh",
		"exp": newExpiry.Unix(),
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "acct_test",
			"chatgpt_plan_type":  "team",
		},
	})
	store := &recordingCredentialStore{}
	service := NewService(Config{
		Clock:           fixedClock{now: now},
		CredentialStore: store,
		HTTPClient: stubHTTPDoer{
			do: func(req *http.Request) (*http.Response, error) {
				if req.URL.String() != "https://issuer.example.com/oauth/token" {
					t.Fatalf("request URL = %q, want %q", req.URL.String(), "https://issuer.example.com/oauth/token")
				}
				if got := req.Header.Get("Content-Type"); got != "application/x-www-form-urlencoded" {
					t.Fatalf("Content-Type = %q, want form encoding", got)
				}
				if got := req.Header.Get("User-Agent"); got != chatGPTOAuthUserAgent {
					t.Fatalf("User-Agent = %q, want %q", got, chatGPTOAuthUserAgent)
				}

				payload, err := io.ReadAll(req.Body)
				if err != nil {
					t.Fatalf("io.ReadAll returned error: %v", err)
				}
				values, err := url.ParseQuery(string(payload))
				if err != nil {
					t.Fatalf("url.ParseQuery returned error: %v", err)
				}
				if values.Get("grant_type") != "refresh_token" {
					t.Fatalf("grant_type = %q, want %q", values.Get("grant_type"), "refresh_token")
				}
				if values.Get("refresh_token") != "refresh-token" {
					t.Fatalf("refresh_token = %q, want %q", values.Get("refresh_token"), "refresh-token")
				}
				if values.Get("client_id") != "client-refresh" {
					t.Fatalf("client_id = %q, want %q", values.Get("client_id"), "client-refresh")
				}

				return &http.Response{
					StatusCode: http.StatusOK,
					Body: io.NopCloser(strings.NewReader(`{
						"access_token":"new-access-token",
						"id_token":"` + newIDToken + `",
						"refresh_token":""
					}`)),
				}, nil
			},
		},
	})

	provider := &model.Provider{
		ID:             "provider-gpt",
		CredentialType: model.ProviderCredentialTypeChatGPT,
	}
	mustApplyLegacyChatGPTCredential(t, provider, mustEncodeChatGPTCredential(t, model.ChatGPTProviderCredential{
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		IDToken:      oldIDToken,
		AccountID:    "acct_test",
		Email:        "user@example.com",
		PlanType:     "plus",
		LastRefresh:  now.Add(-time.Hour),
		ExpiresAt:    now.Add(30 * time.Second),
	}))

	refreshed, err := service.ensureFreshChatGPTCredential(context.Background(), provider, false)
	if err != nil {
		t.Fatalf("ensureFreshChatGPTCredential returned error: %v", err)
	}
	if refreshed.AccessToken != "new-access-token" {
		t.Fatalf("AccessToken = %q, want %q", refreshed.AccessToken, "new-access-token")
	}
	if refreshed.RefreshToken != "refresh-token" {
		t.Fatalf("RefreshToken = %q, want original fallback refresh token", refreshed.RefreshToken)
	}
	if refreshed.OAuthIssuer != "https://issuer.example.com/" {
		t.Fatalf("OAuthIssuer = %q, want %q", refreshed.OAuthIssuer, "https://issuer.example.com/")
	}
	if refreshed.OAuthClientID != "client-refresh" {
		t.Fatalf("OAuthClientID = %q, want %q", refreshed.OAuthClientID, "client-refresh")
	}
	if refreshed.PlanType != "team" {
		t.Fatalf("PlanType = %q, want %q", refreshed.PlanType, "team")
	}
	if !refreshed.ExpiresAt.Equal(newExpiry) {
		t.Fatalf("ExpiresAt = %s, want %s", refreshed.ExpiresAt, newExpiry)
	}
	if store.calls != 1 {
		t.Fatalf("UpdateProviderCredential calls = %d, want 1", store.calls)
	}
	view := mustBuildProviderAuthView(t, provider)
	if view.PlanType != "team" {
		t.Fatalf("AuthView = %#v, want refreshed plan type", view)
	}
}

func TestEnsureFreshChatGPTCredential_PreservesIdentitySnapshotWhenRefreshOmitsIDToken(t *testing.T) {
	now := time.Date(2026, time.March, 22, 12, 0, 0, 0, time.UTC)
	oldIDToken := makeTestJWT(t, map[string]any{
		"iss": "https://issuer.example.com/",
		"aud": "client-refresh",
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
				if req.URL.String() != "https://issuer.example.com/oauth/token" {
					t.Fatalf("request URL = %q, want %q", req.URL.String(), "https://issuer.example.com/oauth/token")
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Body: io.NopCloser(strings.NewReader(`{
						"access_token":"new-access-token",
						"refresh_token":"new-refresh-token"
					}`)),
				}, nil
			},
		},
	})

	provider := &model.Provider{
		ID:             "provider-gpt",
		CredentialType: model.ProviderCredentialTypeChatGPT,
	}
	mustApplyLegacyChatGPTCredential(t, provider, mustEncodeChatGPTCredential(t, model.ChatGPTProviderCredential{
		AccessToken:   "access-token",
		RefreshToken:  "refresh-token",
		IDToken:       oldIDToken,
		OAuthIssuer:   "https://issuer.example.com/",
		OAuthClientID: "client-refresh",
		AccountID:     "acct_test",
		Email:         "user@example.com",
		PlanType:      "plus",
		LastRefresh:   now.Add(-time.Hour),
		ExpiresAt:     now.Add(30 * time.Second),
	}))

	refreshed, err := service.ensureFreshChatGPTCredential(context.Background(), provider, false)
	if err != nil {
		t.Fatalf("ensureFreshChatGPTCredential returned error: %v", err)
	}
	if refreshed.AccessToken != "new-access-token" {
		t.Fatalf("AccessToken = %q, want %q", refreshed.AccessToken, "new-access-token")
	}
	if refreshed.RefreshToken != "new-refresh-token" {
		t.Fatalf("RefreshToken = %q, want %q", refreshed.RefreshToken, "new-refresh-token")
	}
	if refreshed.IDToken != oldIDToken {
		t.Fatal("IDToken should be preserved when refresh response omits id_token")
	}
	if refreshed.AccountID != "acct_test" || refreshed.Email != "user@example.com" || refreshed.PlanType != "plus" {
		t.Fatalf("refreshed identity = %#v, want preserved account snapshot", refreshed)
	}
	if refreshed.OAuthIssuer != "https://issuer.example.com/" || refreshed.OAuthClientID != "client-refresh" {
		t.Fatalf("refreshed oauth context = %#v, want preserved issuer/client", refreshed)
	}
	if store.calls != 1 {
		t.Fatalf("UpdateProviderCredential calls = %d, want 1", store.calls)
	}
}

func TestRefreshChatGPTCredential_ErrorPaths(t *testing.T) {
	now := time.Date(2026, time.March, 22, 12, 0, 0, 0, time.UTC)

	t.Run("invalid id token", func(t *testing.T) {
		service := NewService(Config{})
		_, err := service.refreshChatGPTCredential(context.Background(), &model.ChatGPTProviderCredential{
			IDToken: "bad-token",
		})
		if err == nil || !strings.Contains(err.Error(), "decode chatgpt id_token for refresh") {
			t.Fatalf("error = %v, want jwt decode failure", err)
		}
	})

	t.Run("missing refresh context", func(t *testing.T) {
		service := NewService(Config{})
		_, err := service.refreshChatGPTCredential(context.Background(), &model.ChatGPTProviderCredential{
			RefreshToken: "refresh-token",
			AccountID:    "acct_test",
		})
		if err == nil || !strings.Contains(err.Error(), "missing oauth refresh context") {
			t.Fatalf("error = %v, want missing refresh context", err)
		}
	})

	t.Run("non-2xx response", func(t *testing.T) {
		service := NewService(Config{
			HTTPClient: stubHTTPDoer{
				do: func(req *http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusUnauthorized,
						Status:     "401 Unauthorized",
						Body:       io.NopCloser(strings.NewReader("expired")),
					}, nil
				},
			},
		})
		_, err := service.refreshChatGPTCredential(context.Background(), &model.ChatGPTProviderCredential{
			IDToken:      makeTestJWT(t, map[string]any{"https://api.openai.com/auth": map[string]any{"chatgpt_account_id": "acct_test"}}),
			RefreshToken: "refresh-token",
		})
		if err == nil || !strings.Contains(err.Error(), "refresh chatgpt token failed with status 401 Unauthorized: expired") {
			t.Fatalf("error = %v, want upstream status failure", err)
		}
	})

	t.Run("invalid refresh response payload", func(t *testing.T) {
		service := NewService(Config{
			HTTPClient: stubHTTPDoer{
				do: func(req *http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(strings.NewReader("{")),
					}, nil
				},
			},
		})
		_, err := service.refreshChatGPTCredential(context.Background(), &model.ChatGPTProviderCredential{
			IDToken:      makeTestJWT(t, map[string]any{"https://api.openai.com/auth": map[string]any{"chatgpt_account_id": "acct_test"}}),
			RefreshToken: "refresh-token",
		})
		if err == nil || !strings.Contains(err.Error(), "decode refreshed chatgpt token response") {
			t.Fatalf("error = %v, want decode failure", err)
		}
	})

	_ = now
}

func TestExchangeAuthorizationCode_UsesSnapshotWhenAvailableAndFallsBackWhenUnavailable(t *testing.T) {
	now := time.Date(2026, time.March, 22, 12, 0, 0, 0, time.UTC)
	idToken := makeTestJWT(t, map[string]any{
		"exp": now.Add(2 * time.Hour).Unix(),
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "acct_test",
			"chatgpt_plan_type":  "plus",
		},
	})

	t.Run("usage unavailable returns base credential", func(t *testing.T) {
		requestCount := 0
		service := NewService(Config{
			Clock: fixedClock{now: now},
			HTTPClient: stubHTTPDoer{
				do: func(req *http.Request) (*http.Response, error) {
					requestCount++
					if requestCount == 1 {
						payload, err := io.ReadAll(req.Body)
						if err != nil {
							t.Fatalf("io.ReadAll returned error: %v", err)
						}
						values, err := url.ParseQuery(string(payload))
						if err != nil {
							t.Fatalf("url.ParseQuery returned error: %v", err)
						}
						if values.Get("code") != "auth-code" {
							t.Fatalf("code = %q, want %q", values.Get("code"), "auth-code")
						}
						if values.Get("code_verifier") != "code-verifier" {
							t.Fatalf("code_verifier = %q, want %q", values.Get("code_verifier"), "code-verifier")
						}
						return &http.Response{
							StatusCode: http.StatusOK,
							Body: io.NopCloser(strings.NewReader(`{
								"access_token":"access-token",
								"refresh_token":"refresh-token",
								"id_token":"` + idToken + `"
							}`)),
						}, nil
					}

					return &http.Response{
						StatusCode: http.StatusBadGateway,
						Body:       io.NopCloser(strings.NewReader("usage unavailable")),
					}, nil
				},
			},
		})

		credential, err := service.exchangeAuthorizationCode(context.Background(), "auth-code", "code-verifier")
		if err != nil {
			t.Fatalf("exchangeAuthorizationCode returned error: %v", err)
		}
		if credential.PlanType != "plus" {
			t.Fatalf("PlanType = %q, want %q", credential.PlanType, "plus")
		}
		if credential.Usage != nil {
			t.Fatalf("Usage = %#v, want nil when usage fetch fails", credential.Usage)
		}
	})

	t.Run("usage success enriches credential", func(t *testing.T) {
		requestCount := 0
		service := NewService(Config{
			Clock: fixedClock{now: now},
			HTTPClient: stubHTTPDoer{
				do: func(req *http.Request) (*http.Response, error) {
					requestCount++
					if requestCount == 1 {
						return &http.Response{
							StatusCode: http.StatusOK,
							Body: io.NopCloser(strings.NewReader(`{
								"access_token":"access-token",
								"refresh_token":"refresh-token",
								"id_token":"` + idToken + `"
							}`)),
						}, nil
					}

					return &http.Response{
						StatusCode: http.StatusOK,
						Body: io.NopCloser(strings.NewReader(`{
							"plan_type":"team",
							"rate_limit":{
								"primary_window":{"used_percent":15,"limit_window_seconds":18000,"reset_at":1770000000},
								"secondary_window":{"used_percent":45,"limit_window_seconds":604800,"reset_at":1770500000}
							}
						}`)),
					}, nil
				},
			},
		})

		credential, err := service.exchangeAuthorizationCode(context.Background(), "auth-code", "code-verifier")
		if err != nil {
			t.Fatalf("exchangeAuthorizationCode returned error: %v", err)
		}
		if credential.PlanType != "team" {
			t.Fatalf("PlanType = %q, want %q", credential.PlanType, "team")
		}
		if credential.Usage == nil || credential.Usage.OneWeek == nil || credential.Usage.OneWeek.UsedPercent != 45 {
			t.Fatalf("Usage = %#v, want hydrated quota snapshot", credential.Usage)
		}
	})
}

func TestHandleChatGPTOAuthCallback_Variants(t *testing.T) {
	now := time.Date(2026, time.March, 22, 12, 0, 0, 0, time.UTC)

	t.Run("method not allowed", func(t *testing.T) {
		service := NewService(Config{})
		recorder := httptest.NewRecorder()

		service.handleChatGPTOAuthCallback(recorder, httptest.NewRequest(http.MethodPost, LoopbackCallbackAddress(), nil))
		if recorder.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status = %d, want %d", recorder.Code, http.StatusMethodNotAllowed)
		}
	})

	t.Run("oauth error response", func(t *testing.T) {
		service := NewService(Config{})
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, LoopbackCallbackAddress()+"?error=access_denied&error_description=Denied", nil)

		service.handleChatGPTOAuthCallback(recorder, request)
		if !strings.Contains(recorder.Body.String(), "Denied") {
			t.Fatalf("body = %q, want oauth error description", recorder.Body.String())
		}
	})

	t.Run("missing state or code", func(t *testing.T) {
		service := NewService(Config{})
		recorder := httptest.NewRecorder()

		service.handleChatGPTOAuthCallback(recorder, httptest.NewRequest(http.MethodGet, LoopbackCallbackAddress(), nil))
		if !strings.Contains(recorder.Body.String(), "OAuth callback missing state or code.") {
			t.Fatalf("body = %q, want missing state/code message", recorder.Body.String())
		}
	})

	t.Run("unknown session", func(t *testing.T) {
		service := NewService(Config{Clock: fixedClock{now: now}})
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, LoopbackCallbackAddress()+"?state=unknown&code=auth-code", nil)

		service.handleChatGPTOAuthCallback(recorder, request)
		if !strings.Contains(recorder.Body.String(), "Login session expired or was not recognized.") {
			t.Fatalf("body = %q, want missing session message", recorder.Body.String())
		}
	})

	t.Run("exchange failure", func(t *testing.T) {
		service := NewService(Config{
			Clock: fixedClock{now: now},
			HTTPClient: stubHTTPDoer{
				do: func(req *http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusUnauthorized,
						Status:     "401 Unauthorized",
						Body:       io.NopCloser(strings.NewReader("denied")),
					}, nil
				},
			},
		})
		service.mu.Lock()
		service.storePendingLoginLocked(pendingLogin{
			loginID:      "login-1",
			state:        "state-1",
			codeVerifier: "verifier-1",
			expiresAt:    now.Add(time.Minute),
		})
		service.mu.Unlock()

		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, LoopbackCallbackAddress()+"?state=state-1&code=auth-code", nil)
		service.handleChatGPTOAuthCallback(recorder, request)
		if !strings.Contains(recorder.Body.String(), "exchange authorization code failed with status 401 Unauthorized: denied") {
			t.Fatalf("body = %q, want exchange failure", recorder.Body.String())
		}
	})

	t.Run("status stays pending while callback finalizes the login", func(t *testing.T) {
		exchangeStarted := make(chan struct{})
		releaseExchange := make(chan struct{})
		service := NewService(Config{
			Clock: fixedClock{now: now},
			HTTPClient: stubHTTPDoer{
				do: func(req *http.Request) (*http.Response, error) {
					close(exchangeStarted)
					<-releaseExchange
					return &http.Response{
						StatusCode: http.StatusUnauthorized,
						Status:     "401 Unauthorized",
						Body:       io.NopCloser(strings.NewReader("denied")),
					}, nil
				},
			},
		})
		service.mu.Lock()
		service.storePendingLoginLocked(pendingLogin{
			loginID:      "login-pending",
			state:        "state-pending",
			codeVerifier: "verifier-pending",
			expiresAt:    now.Add(time.Minute),
		})
		service.mu.Unlock()

		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, LoopbackCallbackAddress()+"?state=state-pending&code=auth-code", nil)
		callbackDone := make(chan struct{})
		go func() {
			defer close(callbackDone)
			service.handleChatGPTOAuthCallback(recorder, request)
		}()

		<-exchangeStarted

		status, err := service.GetChatGPTLoginStatus("login-pending")
		if err != nil {
			t.Fatalf("GetChatGPTLoginStatus() error = %v", err)
		}
		if status.Status != ChatGPTLoginStatusPending {
			t.Fatalf("status during callback = %q, want %q", status.Status, ChatGPTLoginStatusPending)
		}

		close(releaseExchange)
		<-callbackDone
	})

	t.Run("successful callback stores completed login", func(t *testing.T) {
		idToken := makeTestJWT(t, map[string]any{
			"exp": now.Add(2 * time.Hour).Unix(),
			"https://api.openai.com/auth": map[string]any{
				"chatgpt_account_id": "acct_test",
				"chatgpt_plan_type":  "team",
			},
		})
		service := NewService(Config{
			Clock: fixedClock{now: now},
			HTTPClient: stubHTTPDoer{
				do: func(req *http.Request) (*http.Response, error) {
					if strings.Contains(req.URL.Path, "/oauth/token") {
						return &http.Response{
							StatusCode: http.StatusOK,
							Body: io.NopCloser(strings.NewReader(`{
								"access_token":"access-token",
								"refresh_token":"refresh-token",
								"id_token":"` + idToken + `"
							}`)),
						}, nil
					}
					return &http.Response{
						StatusCode: http.StatusBadGateway,
						Body:       io.NopCloser(strings.NewReader("usage unavailable")),
					}, nil
				},
			},
		})
		service.mu.Lock()
		service.storePendingLoginLocked(pendingLogin{
			loginID:      "login-2",
			state:        "state-2",
			codeVerifier: "verifier-2",
			expiresAt:    now.Add(time.Minute),
		})
		service.mu.Unlock()

		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, LoopbackCallbackAddress()+"?state=state-2&code=auth-code", nil)
		service.handleChatGPTOAuthCallback(recorder, request)

		if !strings.Contains(recorder.Body.String(), `"loginId":"login-2"`) {
			t.Fatalf("body = %q, want callback payload with login id", recorder.Body.String())
		}
		if !strings.Contains(recorder.Body.String(), "GPT account connected. You can close this window.") {
			t.Fatalf("body = %q, want success message", recorder.Body.String())
		}

		service.mu.Lock()
		_, pendingStillTracked := service.pendingByState["state-2"]
		completed, completedTracked := service.completed["login-2"]
		service.mu.Unlock()
		if pendingStillTracked {
			t.Fatal("pending login should be removed after successful callback")
		}
		if !completedTracked {
			t.Fatal("completed login should be stored after successful callback")
		}
		if completed.credential.PlanType != "team" {
			t.Fatalf("completed plan type = %q, want %q", completed.credential.PlanType, "team")
		}
	})
}

func TestHelperFunctions(t *testing.T) {
	if got := firstNonEmpty("", "", "value", "ignored"); got != "value" {
		t.Fatalf("firstNonEmpty() = %q, want %q", got, "value")
	}

	headers := make(http.Header)
	request := httptest.NewRequest(http.MethodPost, "/responses", nil)
	request.Header.Set("Authorization", "Bearer original")
	applyStaticAuthHeader(headers, "provider-key", "", authModeAuto, request)
	if got := headers.Get("Authorization"); got != "Bearer provider-key" {
		t.Fatalf("Authorization = %q, want %q", got, "Bearer provider-key")
	}

	if got := detectAuthMode(request); got != authModeBearer {
		t.Fatalf("detectAuthMode() = %q, want %q", got, authModeBearer)
	}
	request = httptest.NewRequest(http.MethodPost, "/responses", nil)
	request.Header.Set("X-Api-Key", "caller-key")
	if got := detectAuthMode(request); got != authModeXAPIKey {
		t.Fatalf("detectAuthMode() = %q, want %q", got, authModeXAPIKey)
	}
	if got := detectAuthMode(nil); got != authModeBearer {
		t.Fatalf("detectAuthMode(nil) = %q, want %q", got, authModeBearer)
	}
}
