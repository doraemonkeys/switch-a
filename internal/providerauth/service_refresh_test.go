package providerauth

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"switch-a/internal/model"
)

type concurrentCredentialStore struct {
	mu             sync.Mutex
	id             string
	credentialType model.ProviderCredentialType
	credentialData string
	authState      *model.ProviderAuthState
	calls          int
	authStateCalls int
}

func (s *concurrentCredentialStore) UpdateProviderCredential(
	_ context.Context,
	id string,
	credentialType model.ProviderCredentialType,
	credentialData string,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.id = id
	s.credentialType = credentialType
	s.credentialData = credentialData
	s.calls++
	return nil
}

func (s *concurrentCredentialStore) UpdateProviderAuthState(
	_ context.Context,
	_ string,
	authState *model.ProviderAuthState,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.authState = authState.Clone()
	s.authStateCalls++
	return nil
}

func (s *concurrentCredentialStore) snapshot() (int, string, model.ProviderCredentialType, string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.calls, s.id, s.credentialType, s.credentialData
}

func TestApplyProviderCredentials_DeduplicatesConcurrentChatGPTRefresh(t *testing.T) {
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
	newIDToken := makeTestJWT(t, map[string]any{
		"iss": "https://issuer.example.com/",
		"aud": "client-refresh",
		"exp": now.Add(2 * time.Hour).Unix(),
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "acct_test",
			"chatgpt_plan_type":  "team",
		},
	})
	oldCredentialData, err := encodeChatGPTCredential(model.ChatGPTProviderCredential{
		AccessToken:   "old-access-token",
		RefreshToken:  "old-refresh-token",
		IDToken:       oldIDToken,
		OAuthIssuer:   "https://issuer.example.com/",
		OAuthClientID: "client-refresh",
		AccountID:     "acct_test",
		PlanType:      "plus",
		LastRefresh:   now.Add(-time.Hour),
		ExpiresAt:     now.Add(30 * time.Second),
	})
	if err != nil {
		t.Fatalf("encodeChatGPTCredential returned error: %v", err)
	}

	store := &concurrentCredentialStore{}
	var refreshCalls atomic.Int32
	unexpectedSecondRefresh := make(chan struct{}, 1)
	secondErrCh := make(chan error, 1)
	secondHeaders := make(http.Header)
	secondRequest := httptest.NewRequest(http.MethodPost, "/responses", nil)

	var service *Service
	service = NewService(Config{
		Clock:           fixedClock{now: now},
		CredentialStore: store,
		HTTPClient: stubHTTPDoer{
			do: func(req *http.Request) (*http.Response, error) {
				if got := refreshCalls.Add(1); got == 1 {
					go func() {
						provider := &model.Provider{
							ID:             "provider-gpt",
							CredentialType: model.ProviderCredentialTypeChatGPT,
						}
						mustApplyLegacyChatGPTCredential(t, provider, oldCredentialData)
						secondErrCh <- service.ApplyProviderCredentials(
							context.Background(),
							secondHeaders,
							provider,
							codexAPIType,
							authModeAuto,
							secondRequest,
						)
					}()

					select {
					case <-unexpectedSecondRefresh:
					case <-time.After(200 * time.Millisecond):
					}

					return &http.Response{
						StatusCode: http.StatusOK,
						Body: io.NopCloser(strings.NewReader(`{
							"access_token":"new-access-token",
							"refresh_token":"new-refresh-token",
							"id_token":"` + newIDToken + `"
						}`)),
					}, nil
				}

				unexpectedSecondRefresh <- struct{}{}
				return &http.Response{
					StatusCode: http.StatusInternalServerError,
					Body:       io.NopCloser(strings.NewReader("duplicate refresh")),
				}, nil
			},
		},
	})

	firstHeaders := make(http.Header)
	firstProvider := &model.Provider{
		ID:             "provider-gpt",
		CredentialType: model.ProviderCredentialTypeChatGPT,
	}
	mustApplyLegacyChatGPTCredential(t, firstProvider, oldCredentialData)
	firstRequest := httptest.NewRequest(http.MethodPost, "/responses", nil)

	if err := service.ApplyProviderCredentials(
		context.Background(),
		firstHeaders,
		firstProvider,
		codexAPIType,
		authModeAuto,
		firstRequest,
	); err != nil {
		t.Fatalf("first ApplyProviderCredentials returned error: %v", err)
	}

	if err := <-secondErrCh; err != nil {
		t.Fatalf("second ApplyProviderCredentials returned error: %v", err)
	}

	select {
	case <-unexpectedSecondRefresh:
		t.Fatal("second request attempted a duplicate refresh instead of joining the in-flight refresh")
	default:
	}

	if got := refreshCalls.Load(); got != 1 {
		t.Fatalf("refresh call count = %d, want 1", got)
	}

	for name, headers := range map[string]http.Header{
		"first":  firstHeaders,
		"second": secondHeaders,
	} {
		if got := headers.Get("Authorization"); got != "Bearer new-access-token" {
			t.Fatalf("%s Authorization = %q, want %q", name, got, "Bearer new-access-token")
		}
		if got := headers.Get("ChatGPT-Account-Id"); got != "acct_test" {
			t.Fatalf("%s ChatGPT-Account-Id = %q, want %q", name, got, "acct_test")
		}
	}

	calls, id, credentialType, credentialData := store.snapshot()
	if calls != 1 {
		t.Fatalf("UpdateProviderCredential calls = %d, want 1", calls)
	}
	if id != "provider-gpt" {
		t.Fatalf("persisted id = %q, want %q", id, "provider-gpt")
	}
	if credentialType != model.ProviderCredentialTypeChatGPT {
		t.Fatalf("persisted credential type = %q, want %q", credentialType, model.ProviderCredentialTypeChatGPT)
	}

	refreshed, err := decodeChatGPTCredentialSecret(credentialData)
	if err != nil {
		t.Fatalf("decodeChatGPTCredentialSecret returned error: %v", err)
	}
	if refreshed.RefreshToken != "new-refresh-token" {
		t.Fatalf("persisted RefreshToken = %q, want %q", refreshed.RefreshToken, "new-refresh-token")
	}
}

func TestApplyProviderCredentials_ReusesRecentChatGPTRefreshForStaleProviderCopy(t *testing.T) {
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
	newIDToken := makeTestJWT(t, map[string]any{
		"iss": "https://issuer.example.com/",
		"aud": "client-refresh",
		"exp": now.Add(2 * time.Hour).Unix(),
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "acct_test",
			"chatgpt_plan_type":  "team",
		},
	})
	oldCredentialData, err := encodeChatGPTCredential(model.ChatGPTProviderCredential{
		AccessToken:   "old-access-token",
		RefreshToken:  "old-refresh-token",
		IDToken:       oldIDToken,
		OAuthIssuer:   "https://issuer.example.com/",
		OAuthClientID: "client-refresh",
		AccountID:     "acct_test",
		PlanType:      "plus",
		LastRefresh:   now.Add(-time.Hour),
		ExpiresAt:     now.Add(30 * time.Second),
	})
	if err != nil {
		t.Fatalf("encodeChatGPTCredential returned error: %v", err)
	}

	store := &concurrentCredentialStore{}
	var refreshCalls atomic.Int32
	service := NewService(Config{
		Clock:           fixedClock{now: now},
		CredentialStore: store,
		HTTPClient: stubHTTPDoer{
			do: func(req *http.Request) (*http.Response, error) {
				refreshCalls.Add(1)
				return &http.Response{
					StatusCode: http.StatusOK,
					Body: io.NopCloser(strings.NewReader(`{
						"access_token":"new-access-token",
						"refresh_token":"new-refresh-token",
						"id_token":"` + newIDToken + `"
					}`)),
				}, nil
			},
		},
	})

	firstProvider := &model.Provider{
		ID:             "provider-gpt",
		CredentialType: model.ProviderCredentialTypeChatGPT,
	}
	mustApplyLegacyChatGPTCredential(t, firstProvider, oldCredentialData)
	firstHeaders := make(http.Header)
	if err := service.ApplyProviderCredentials(
		context.Background(),
		firstHeaders,
		firstProvider,
		codexAPIType,
		authModeAuto,
		httptest.NewRequest(http.MethodPost, "/responses", nil),
	); err != nil {
		t.Fatalf("first ApplyProviderCredentials returned error: %v", err)
	}

	secondProvider := &model.Provider{
		ID:             "provider-gpt",
		CredentialType: model.ProviderCredentialTypeChatGPT,
	}
	mustApplyLegacyChatGPTCredential(t, secondProvider, oldCredentialData)
	secondHeaders := make(http.Header)
	if err := service.ApplyProviderCredentials(
		context.Background(),
		secondHeaders,
		secondProvider,
		codexAPIType,
		authModeAuto,
		httptest.NewRequest(http.MethodPost, "/responses", nil),
	); err != nil {
		t.Fatalf("second ApplyProviderCredentials returned error: %v", err)
	}

	if got := refreshCalls.Load(); got != 1 {
		t.Fatalf("refresh call count = %d, want 1", got)
	}
	if got := secondHeaders.Get("Authorization"); got != "Bearer new-access-token" {
		t.Fatalf("second Authorization = %q, want %q", got, "Bearer new-access-token")
	}

	refreshed, err := DecodeProviderChatGPTCredential(secondProvider)
	if err != nil {
		t.Fatalf("decodeProviderChatGPTCredential returned error: %v", err)
	}
	if refreshed.RefreshToken != "new-refresh-token" {
		t.Fatalf("second provider RefreshToken = %q, want %q", refreshed.RefreshToken, "new-refresh-token")
	}

	calls, _, _, _ := store.snapshot()
	if calls != 1 {
		t.Fatalf("UpdateProviderCredential calls = %d, want 1", calls)
	}
}
