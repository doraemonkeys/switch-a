package providerauth

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/model"
)

type profileCredentialStore struct {
	session       *credentialsession.Session
	stateWrites   int
	casWrites     int
	stateWriteErr error
}

func (s *profileCredentialStore) GetCredentialSession(context.Context, string) (*credentialsession.Session, error) {
	return s.session.Clone(), nil
}

func (*profileCredentialStore) WithCredentialSessionMutations(
	ctx context.Context,
	_ []string,
) (context.Context, func(), error) {
	return ctx, func() {}, nil
}

func (s *profileCredentialStore) UpdateCredentialSessionCAS(
	_ context.Context,
	_ string,
	_ int64,
	secret string,
	subject credentialsession.Subject,
	state credentialsession.AuthState,
) (int64, error) {
	s.casWrites++
	s.session.SecretData = secret
	_ = s.session.SetSubject(subject)
	s.session.AuthState = state.Clone()
	s.session.Version++
	return s.session.Version, nil
}

func (s *profileCredentialStore) UpdateCredentialSessionAuthState(
	_ context.Context,
	_ string,
	state credentialsession.AuthState,
) error {
	if s.stateWriteErr != nil {
		return s.stateWriteErr
	}
	s.stateWrites++
	s.session.AuthState = state.Clone()
	return nil
}

func profileSnapshot(t *testing.T, now time.Time) credentialsession.Snapshot {
	t.Helper()
	snapshot, err := chatGPTCredentialSessionSnapshot(&model.ChatGPTProviderCredential{
		AccessToken: "access", RefreshToken: "refresh", AccountID: "acct",
		OAuthIssuer: defaultOAuthIssuer, OAuthClientID: defaultOAuthClientID,
		ExpiresAt: now.Add(time.Hour), LastRefresh: now.Add(-time.Minute),
	}, "session")
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestRefreshCredentialSessionUsagePersistsSessionAuthState(t *testing.T) {
	now := time.Now().UTC()
	snapshot := profileSnapshot(t, now)
	store := &profileCredentialStore{session: sessionFromAppliedSnapshot(t, snapshot)}
	service := NewService(Config{
		Clock: fixedClock{now: now}, CredentialStore: store,
		HTTPClient: stubHTTPDoer{do: func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(strings.NewReader(`{
					"plan_type":"pro",
					"rate_limit":{"primary_window":{"used_percent":17,"limit_window_seconds":18000,"reset_at":1990000000}}
				}`)),
			}, nil
		}},
	})
	applicable, err := service.RefreshCredentialSessionUsage(context.Background(), snapshot)
	if err != nil || !applicable {
		t.Fatalf("RefreshCredentialSessionUsage = (%t, %v)", applicable, err)
	}
	if store.stateWrites != 1 || store.session.AuthState.UsageSnapshot == nil ||
		store.session.AuthState.UsageSnapshot.FetchedAt == nil {
		t.Fatalf("persisted state = %#v, writes=%d", store.session.AuthState, store.stateWrites)
	}

	static := credentialsession.Snapshot{Kind: credentialsession.KindAPIKey}
	if applicable, err := service.RefreshCredentialSessionUsage(context.Background(), static); err != nil || applicable {
		t.Fatalf("static usage refresh = (%t, %v)", applicable, err)
	}
	missing := snapshot
	missing.SessionID = ""
	if applicable, err := service.RefreshCredentialSessionUsage(context.Background(), missing); err == nil || !applicable {
		t.Fatalf("missing-session usage refresh = (%t, %v)", applicable, err)
	}
}

func TestRefreshCredentialSessionUsageMarksTerminalAuthFailure(t *testing.T) {
	now := time.Now().UTC()
	snapshot := profileSnapshot(t, now)
	store := &profileCredentialStore{session: sessionFromAppliedSnapshot(t, snapshot)}
	service := NewService(Config{
		Clock: fixedClock{now: now}, CredentialStore: store,
		HTTPClient: stubHTTPDoer{do: func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusUnauthorized,
				Body:       io.NopCloser(strings.NewReader(`{"error":{"code":"token_invalidated","message":"invalid token"}}`)),
			}, nil
		}},
	})
	applicable, err := service.RefreshCredentialSessionUsage(context.Background(), snapshot)
	if !applicable {
		t.Fatal("ChatGPT session was not applicable")
	}
	var stateErr *ProviderAuthStateError
	if !errors.As(err, &stateErr) || stateErr.SessionID != "session" {
		t.Fatalf("error = %#v, want session auth-state error", err)
	}
	if store.session.AuthState.Status != credentialsession.AuthStatusReauthRequired ||
		store.session.AuthState.StatusReason != ProviderAuthReasonTokenInvalidated {
		t.Fatalf("auth state = %#v", store.session.AuthState)
	}
}

func TestSessionPersistenceAndStateWriteErrors(t *testing.T) {
	now := time.Now().UTC()
	snapshot := profileSnapshot(t, now)
	store := &profileCredentialStore{session: sessionFromAppliedSnapshot(t, snapshot)}
	service := NewService(Config{CredentialStore: store})
	refreshed := &model.ChatGPTProviderCredential{
		AccessToken: "new-access", RefreshToken: "new-refresh", AccountID: "acct",
		OAuthIssuer: defaultOAuthIssuer, OAuthClientID: defaultOAuthClientID,
		ExpiresAt: now.Add(2 * time.Hour), LastRefresh: now,
	}
	if err := service.persistChatGPTCredentialSession(context.Background(), &snapshot, refreshed); err != nil {
		t.Fatalf("persistChatGPTCredentialSession error = %v", err)
	}
	if store.casWrites != 1 || store.session.Version != 2 {
		t.Fatalf("CAS writes/version = (%d, %d)", store.casWrites, store.session.Version)
	}

	store.stateWriteErr = errors.New("write failed")
	if err := service.persistCredentialSessionAuthState(context.Background(), "session", snapshot.AuthState); err == nil ||
		!strings.Contains(err.Error(), "persist auth state") {
		t.Fatalf("state write error = %v", err)
	}
	if err := NewService(Config{}).persistCredentialSessionAuthState(context.Background(), "session", snapshot.AuthState); err == nil {
		t.Fatal("missing credential store error = nil")
	}
}

func TestDirectRefreshPersistsCredentialSessionGeneration(t *testing.T) {
	now := time.Now().UTC()
	snapshot := profileSnapshot(t, now)
	store := &profileCredentialStore{session: sessionFromAppliedSnapshot(t, snapshot)}
	service := NewService(Config{
		Clock: fixedClock{now: now}, CredentialStore: store,
		HTTPClient: stubHTTPDoer{do: func(request *http.Request) (*http.Response, error) {
			if request.URL.String() != defaultOAuthIssuer+"/oauth/token" {
				t.Fatalf("refresh URL = %q", request.URL.String())
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(strings.NewReader(
					`{"access_token":"new-access","refresh_token":"new-refresh"}`,
				)),
			}, nil
		}},
	})
	current, err := decodeChatGPTCredentialSession(&snapshot)
	if err != nil {
		t.Fatal(err)
	}
	refreshed, err := service.refreshAndPersistChatGPTCredentialDirect(
		context.Background(), "provider", &snapshot, current,
	)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.AccessToken != "new-access" || refreshed.RefreshToken != "new-refresh" ||
		refreshed.AccountID != "acct" || store.casWrites != 1 {
		t.Fatalf("refreshed credential = %#v, CAS writes=%d", refreshed, store.casWrites)
	}
	if recent := service.reuseRecentChatGPTRefresh("session", current); recent.AccessToken != "new-access" {
		t.Fatalf("recent refreshed generation = %#v", recent)
	}
}

func TestRefreshFailureUpdatesCredentialSessionLifecycle(t *testing.T) {
	now := time.Now().UTC()
	snapshot := profileSnapshot(t, now)
	store := &profileCredentialStore{session: sessionFromAppliedSnapshot(t, snapshot)}
	service := NewService(Config{Clock: fixedClock{now: now}, CredentialStore: store})

	transient := errors.New("temporary outage")
	if err := service.persistChatGPTRefreshFailure(
		context.Background(), "provider", &snapshot, nil, transient,
	); !errors.Is(err, transient) {
		t.Fatalf("transient refresh failure = %v", err)
	}
	if store.session.AuthState.Status != credentialsession.AuthStatusActive ||
		store.session.AuthState.RefreshFailCount != 1 {
		t.Fatalf("transient auth state = %#v", store.session.AuthState)
	}

	terminal := errors.New("oauth invalid_grant")
	err := service.persistChatGPTRefreshFailure(
		context.Background(), "provider", &snapshot, nil, terminal,
	)
	var stateErr *ProviderAuthStateError
	if !errors.As(err, &stateErr) || stateErr.SessionID != "session" ||
		stateErr.Reason != ProviderAuthReasonInvalidGrant {
		t.Fatalf("terminal refresh failure = %#v", err)
	}
	if store.session.AuthState.Status != credentialsession.AuthStatusReauthRequired {
		t.Fatalf("terminal auth state = %#v", store.session.AuthState)
	}

	if err := service.persistChatGPTRefreshFailure(
		context.Background(), "provider", nil, nil, transient,
	); !errors.Is(err, transient) {
		t.Fatalf("failure without snapshot = %v", err)
	}
}
