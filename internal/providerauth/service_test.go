package providerauth

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"switch-a/internal/model"
)

type fixedClock struct {
	now time.Time
}

func (c fixedClock) Now() time.Time {
	return c.now
}

func (fixedClock) NewTicker(d time.Duration) *time.Ticker {
	return time.NewTicker(d)
}

type stubHTTPDoer struct {
	do func(req *http.Request) (*http.Response, error)
}

func (s stubHTTPDoer) Do(req *http.Request) (*http.Response, error) {
	return s.do(req)
}

type recordingCredentialStore struct {
	id             string
	credentialType model.ProviderCredentialType
	credentialData string
	authStateID    string
	authState      *model.ProviderAuthState
	calls          int
	authStateCalls int
}

func (s *recordingCredentialStore) UpdateProviderCredential(_ context.Context, id string, credentialType model.ProviderCredentialType, credentialData string) error {
	s.id = id
	s.credentialType = credentialType
	s.credentialData = credentialData
	s.calls++
	return nil
}

func (s *recordingCredentialStore) UpdateProviderAuthState(_ context.Context, providerID string, authState *model.ProviderAuthState) error {
	s.authStateID = providerID
	s.authState = authState.Clone()
	s.authStateCalls++
	return nil
}

func TestGetChatGPTLoginStatus_Pending(t *testing.T) {
	now := time.Date(2026, time.March, 22, 12, 0, 0, 0, time.UTC)
	service := NewService(Config{Clock: fixedClock{now: now}})

	service.mu.Lock()
	service.storePendingLoginLocked(pendingLogin{
		loginID:      "login-pending",
		state:        "state-pending",
		codeVerifier: "verifier",
		expiresAt:    now.Add(time.Minute),
	})
	service.mu.Unlock()

	status, err := service.GetChatGPTLoginStatus("login-pending")
	if err != nil {
		t.Fatalf("GetChatGPTLoginStatus returned error: %v", err)
	}
	if status.Status != ChatGPTLoginStatusPending {
		t.Fatalf("status = %q, want %q", status.Status, ChatGPTLoginStatusPending)
	}
	if status.Auth != nil {
		t.Fatal("pending status should not expose an auth view")
	}
}

func TestGetChatGPTLoginStatus_Completed(t *testing.T) {
	now := time.Date(2026, time.March, 22, 12, 0, 0, 0, time.UTC)
	service := NewService(Config{Clock: fixedClock{now: now}})

	service.mu.Lock()
	service.completed["login-completed"] = completedLogin{
		loginID: "login-completed",
		credential: model.ChatGPTProviderCredential{
			AccessToken:  "access-token",
			RefreshToken: "refresh-token",
			IDToken:      "id-token",
			AccountID:    "acct_test",
			Email:        "user@example.com",
			LastRefresh:  now.Add(-time.Minute),
			ExpiresAt:    now.Add(time.Hour),
		},
		expiresAt: now.Add(time.Minute),
	}
	service.mu.Unlock()

	status, err := service.GetChatGPTLoginStatus("login-completed")
	if err != nil {
		t.Fatalf("GetChatGPTLoginStatus returned error: %v", err)
	}
	if status.Status != ChatGPTLoginStatusCompleted {
		t.Fatalf("status = %q, want %q", status.Status, ChatGPTLoginStatusCompleted)
	}
	if status.Auth == nil {
		t.Fatal("completed status should expose an auth view")
	}
	if status.Auth.Status != ProviderAuthStatusActive {
		t.Fatalf("auth status = %q, want %q", status.Auth.Status, ProviderAuthStatusActive)
	}
	if status.Auth.Email != "user@example.com" {
		t.Fatalf("email = %q, want %q", status.Auth.Email, "user@example.com")
	}
	if status.Auth.AccountID != "acct_test" {
		t.Fatalf("account_id = %q, want %q", status.Auth.AccountID, "acct_test")
	}
}

func TestGetChatGPTLoginStatus_Expired(t *testing.T) {
	now := time.Date(2026, time.March, 22, 12, 0, 0, 0, time.UTC)
	service := NewService(Config{Clock: fixedClock{now: now}})

	service.mu.Lock()
	service.storePendingLoginLocked(pendingLogin{
		loginID:      "login-expired",
		state:        "state-expired",
		codeVerifier: "verifier",
		expiresAt:    now.Add(-time.Second),
	})
	service.completed["login-expired-completed"] = completedLogin{
		loginID: "login-expired-completed",
		credential: model.ChatGPTProviderCredential{
			AccessToken:  "access-token",
			RefreshToken: "refresh-token",
			IDToken:      "id-token",
			AccountID:    "acct_test",
		},
		expiresAt: now.Add(-time.Second),
	}
	service.mu.Unlock()

	pendingStatus, err := service.GetChatGPTLoginStatus("login-expired")
	if err != nil {
		t.Fatalf("GetChatGPTLoginStatus(pending) returned error: %v", err)
	}
	if pendingStatus.Status != ChatGPTLoginStatusExpired {
		t.Fatalf("pending status = %q, want %q", pendingStatus.Status, ChatGPTLoginStatusExpired)
	}

	completedStatus, err := service.GetChatGPTLoginStatus("login-expired-completed")
	if err != nil {
		t.Fatalf("GetChatGPTLoginStatus(completed) returned error: %v", err)
	}
	if completedStatus.Status != ChatGPTLoginStatusExpired {
		t.Fatalf("completed status = %q, want %q", completedStatus.Status, ChatGPTLoginStatusExpired)
	}

	service.mu.Lock()
	_, pendingStillTracked := service.pendingByLoginID["login-expired"]
	_, completedStillTracked := service.completed["login-expired-completed"]
	service.mu.Unlock()
	if pendingStillTracked {
		t.Fatal("expired pending login should be pruned from pendingByLoginID")
	}
	if completedStillTracked {
		t.Fatal("expired completed login should be pruned from completed cache")
	}
}

func TestApplyProviderCredentials_PreservesCallerOriginatorForChatGPT(t *testing.T) {
	now := time.Date(2026, time.March, 22, 12, 0, 0, 0, time.UTC)
	service := NewService(Config{Clock: fixedClock{now: now}})

	credentialData, err := encodeChatGPTCredential(model.ChatGPTProviderCredential{
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		IDToken:      "id-token",
		AccountID:    "acct_test",
		LastRefresh:  now.Add(-time.Minute),
		ExpiresAt:    now.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("encodeChatGPTCredential returned error: %v", err)
	}

	provider := &model.Provider{
		ID:             "chatgpt-provider",
		CredentialType: model.ProviderCredentialTypeChatGPT,
	}
	mustApplyLegacyChatGPTCredential(t, provider, credentialData)
	headers := make(http.Header)
	headers.Set("Originator", "custom_cli")

	err = service.ApplyProviderCredentials(context.Background(), headers, provider, "codex", "bearer", nil)
	if err != nil {
		t.Fatalf("ApplyProviderCredentials returned error: %v", err)
	}

	if got := headers.Get("Authorization"); got != "Bearer access-token" {
		t.Fatalf("Authorization = %q, want %q", got, "Bearer access-token")
	}
	if got := headers.Get("ChatGPT-Account-Id"); got != "acct_test" {
		t.Fatalf("ChatGPT-Account-Id = %q, want %q", got, "acct_test")
	}
	if got := headers.Get("Originator"); got != "custom_cli" {
		t.Fatalf("Originator = %q, want %q", got, "custom_cli")
	}
}

func TestApplyProviderCredentials_DefaultsOriginatorForChatGPTWhenMissing(t *testing.T) {
	now := time.Date(2026, time.March, 22, 12, 0, 0, 0, time.UTC)
	service := NewService(Config{Clock: fixedClock{now: now}})

	credentialData, err := encodeChatGPTCredential(model.ChatGPTProviderCredential{
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		IDToken:      "id-token",
		AccountID:    "acct_test",
		LastRefresh:  now.Add(-time.Minute),
		ExpiresAt:    now.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("encodeChatGPTCredential returned error: %v", err)
	}

	provider := &model.Provider{
		ID:             "chatgpt-provider",
		CredentialType: model.ProviderCredentialTypeChatGPT,
	}
	mustApplyLegacyChatGPTCredential(t, provider, credentialData)
	headers := make(http.Header)

	err = service.ApplyProviderCredentials(context.Background(), headers, provider, "codex", "bearer", nil)
	if err != nil {
		t.Fatalf("ApplyProviderCredentials returned error: %v", err)
	}

	if got := headers.Get("Originator"); got != chatGPTCodexOriginator {
		t.Fatalf("Originator = %q, want %q", got, chatGPTCodexOriginator)
	}
}

func TestBuildProviderAuthView_ExposesStoredUsageSnapshot(t *testing.T) {
	now := time.Date(2026, time.March, 22, 12, 0, 0, 0, time.UTC)
	resetFiveHours := now.Add(5 * time.Hour)
	resetOneWeek := now.Add(7 * 24 * time.Hour)
	credentialData, err := encodeChatGPTCredential(model.ChatGPTProviderCredential{
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		IDToken:      "id-token",
		AccountID:    "acct_test",
		Email:        "user@example.com",
		PlanType:     "plus",
		Usage: &model.ProviderUsageSnapshot{
			FetchedAt: &now,
			PlanType:  "plus",
			FiveHour: &model.ProviderUsageWindow{
				UsedPercent:   18,
				WindowSeconds: 5 * 60 * 60,
				ResetAt:       &resetFiveHours,
			},
			OneWeek: &model.ProviderUsageWindow{
				UsedPercent:   42,
				WindowSeconds: 7 * 24 * 60 * 60,
				ResetAt:       &resetOneWeek,
			},
		},
		LastRefresh: now.Add(-time.Minute),
		ExpiresAt:   now.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("encodeChatGPTCredential returned error: %v", err)
	}

	provider := &model.Provider{
		ID:             "provider-gpt",
		CredentialType: model.ProviderCredentialTypeChatGPT,
	}
	mustApplyLegacyChatGPTCredential(t, provider, credentialData)
	view := mustBuildProviderAuthView(t, provider)

	if view == nil {
		t.Fatal("BuildProviderAuthView returned nil")
	}
	if view.Status != ProviderAuthStatusActive {
		t.Fatalf("Status = %q, want %q", view.Status, ProviderAuthStatusActive)
	}
	if view.PlanType != "plus" {
		t.Fatalf("PlanType = %q, want %q", view.PlanType, "plus")
	}
	if view.Usage == nil {
		t.Fatal("Usage = nil, want snapshot")
	}
	if got := view.Usage.FiveHour; got == nil || got.UsedPercent != 18 {
		t.Fatalf("FiveHour = %#v, want used_percent 18", got)
	}
	if got := view.Usage.OneWeek; got == nil || got.UsedPercent != 42 {
		t.Fatalf("OneWeek = %#v, want used_percent 42", got)
	}
}

func TestBuildProviderAuthView_IsPureReadForStaleUsageSnapshot(t *testing.T) {
	now := time.Date(2026, time.March, 22, 12, 0, 0, 0, time.UTC)
	staleFetchedAt := now.Add(-chatGPTUsageSnapshotTTL - time.Minute)
	resetFiveHours := now.Add(3 * time.Hour)
	resetOneWeek := now.Add(6 * 24 * time.Hour)
	store := &recordingCredentialStore{}
	requestCount := 0

	service := NewService(Config{
		Clock:           fixedClock{now: now},
		CredentialStore: store,
		HTTPClient: stubHTTPDoer{
			do: func(req *http.Request) (*http.Response, error) {
				requestCount++
				if got := req.Header.Get("Authorization"); got != "Bearer access-token" {
					t.Fatalf("Authorization = %q, want %q", got, "Bearer access-token")
				}
				if got := req.Header.Get("ChatGPT-Account-Id"); got != "acct_test" {
					t.Fatalf("ChatGPT-Account-Id = %q, want %q", got, "acct_test")
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Body: io.NopCloser(strings.NewReader(`{
						"plan_type": "team",
						"rate_limit": {
							"primary_window": {
								"used_percent": 21,
								"limit_window_seconds": 18000,
								"reset_at": 1770000000
							},
							"secondary_window": {
								"used_percent": 57,
								"limit_window_seconds": 604800,
								"reset_at": 1770500000
							}
						}
					}`)),
				}, nil
			},
		},
	})

	credentialData, err := encodeChatGPTCredential(model.ChatGPTProviderCredential{
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		IDToken:      "id-token",
		AccountID:    "acct_test",
		Email:        "user@example.com",
		PlanType:     "plus",
		Usage: &model.ProviderUsageSnapshot{
			FetchedAt: &staleFetchedAt,
			PlanType:  "plus",
			FiveHour: &model.ProviderUsageWindow{
				UsedPercent:   5,
				WindowSeconds: 5 * 60 * 60,
				ResetAt:       &resetFiveHours,
			},
			OneWeek: &model.ProviderUsageWindow{
				UsedPercent:   9,
				WindowSeconds: 7 * 24 * 60 * 60,
				ResetAt:       &resetOneWeek,
			},
		},
		LastRefresh: now.Add(-time.Minute),
		ExpiresAt:   now.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("encodeChatGPTCredential returned error: %v", err)
	}

	provider := &model.Provider{
		ID:             "provider-gpt",
		CredentialType: model.ProviderCredentialTypeChatGPT,
	}
	mustApplyLegacyChatGPTCredential(t, provider, credentialData)

	view := service.BuildProviderAuthView(provider)

	if requestCount != 0 {
		t.Fatalf("usage request count = %d, want 0", requestCount)
	}
	if view == nil {
		t.Fatal("BuildProviderAuthView = nil, want local snapshot")
	}
	if view.PlanType != "plus" {
		t.Fatalf("PlanType = %q, want %q", view.PlanType, "plus")
	}
	if got := view.Usage; got == nil {
		t.Fatal("Usage = nil, want stored snapshot")
	} else {
		if got.FiveHour == nil || got.FiveHour.UsedPercent != 5 {
			t.Fatalf("FiveHour = %#v, want used_percent 5", got.FiveHour)
		}
		if got.OneWeek == nil || got.OneWeek.UsedPercent != 9 {
			t.Fatalf("OneWeek = %#v, want used_percent 9", got.OneWeek)
		}
	}
	if store.calls != 0 {
		t.Fatalf("UpdateProviderCredential calls = %d, want 0", store.calls)
	}
}

func TestRefreshProviderUsage_RefreshesStaleUsageSnapshot(t *testing.T) {
	now := time.Date(2026, time.March, 22, 12, 0, 0, 0, time.UTC)
	staleFetchedAt := now.Add(-chatGPTUsageSnapshotTTL - time.Minute)
	resetFiveHours := now.Add(3 * time.Hour)
	resetOneWeek := now.Add(6 * 24 * time.Hour)
	store := &recordingCredentialStore{}
	requestCount := 0

	service := NewService(Config{
		Clock:           fixedClock{now: now},
		CredentialStore: store,
		HTTPClient: stubHTTPDoer{
			do: func(req *http.Request) (*http.Response, error) {
				requestCount++
				if got := req.Header.Get("Authorization"); got != "Bearer access-token" {
					t.Fatalf("Authorization = %q, want %q", got, "Bearer access-token")
				}
				if got := req.Header.Get("ChatGPT-Account-Id"); got != "acct_test" {
					t.Fatalf("ChatGPT-Account-Id = %q, want %q", got, "acct_test")
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Body: io.NopCloser(strings.NewReader(`{
						"plan_type": "team",
						"rate_limit": {
							"primary_window": {
								"used_percent": 21,
								"limit_window_seconds": 18000,
								"reset_at": 1770000000
							},
							"secondary_window": {
								"used_percent": 57,
								"limit_window_seconds": 604800,
								"reset_at": 1770500000
							}
						}
					}`)),
				}, nil
			},
		},
	})

	credentialData, err := encodeChatGPTCredential(model.ChatGPTProviderCredential{
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		IDToken:      "id-token",
		AccountID:    "acct_test",
		Email:        "user@example.com",
		PlanType:     "plus",
		Usage: &model.ProviderUsageSnapshot{
			FetchedAt: &staleFetchedAt,
			PlanType:  "plus",
			FiveHour: &model.ProviderUsageWindow{
				UsedPercent:   5,
				WindowSeconds: 5 * 60 * 60,
				ResetAt:       &resetFiveHours,
			},
			OneWeek: &model.ProviderUsageWindow{
				UsedPercent:   9,
				WindowSeconds: 7 * 24 * 60 * 60,
				ResetAt:       &resetOneWeek,
			},
		},
		LastRefresh: now.Add(-time.Minute),
		ExpiresAt:   now.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("encodeChatGPTCredential returned error: %v", err)
	}

	provider := &model.Provider{
		ID:             "provider-gpt",
		CredentialType: model.ProviderCredentialTypeChatGPT,
	}
	mustApplyLegacyChatGPTCredential(t, provider, credentialData)

	refreshed, err := service.RefreshProviderUsage(context.Background(), provider)
	if err != nil {
		t.Fatalf("RefreshProviderUsage returned error: %v", err)
	}
	if !refreshed {
		t.Fatal("RefreshProviderUsage returned false, want true")
	}
	if requestCount != 1 {
		t.Fatalf("usage request count = %d, want 1", requestCount)
	}
	view := mustBuildProviderAuthView(t, provider)
	if view.PlanType != "team" {
		t.Fatalf("AuthView = %#v, want refreshed plan type", view)
	}
	if got := view.Usage; got == nil {
		t.Fatal("Usage = nil, want refreshed snapshot")
	} else {
		if got.FiveHour == nil || got.FiveHour.UsedPercent != 21 {
			t.Fatalf("FiveHour = %#v, want used_percent 21", got.FiveHour)
		}
		if got.OneWeek == nil || got.OneWeek.UsedPercent != 57 {
			t.Fatalf("OneWeek = %#v, want used_percent 57", got.OneWeek)
		}
	}
	if store.calls != 0 {
		t.Fatalf("UpdateProviderCredential calls = %d, want 0", store.calls)
	}
	if store.authStateCalls != 1 {
		t.Fatalf("UpdateProviderAuthState calls = %d, want 1", store.authStateCalls)
	}
	if store.authStateID != "provider-gpt" {
		t.Fatalf("persisted auth state id = %q, want %q", store.authStateID, "provider-gpt")
	}
}
