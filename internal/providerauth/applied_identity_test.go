package providerauth

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/doraemonkeys/switch-a/internal/model"
)

func TestApplyProviderCredentialsInjectsWithoutOwningHeaderProjection(t *testing.T) {
	t.Parallel()
	finalURL := mustAppliedIdentityURL(t, "https://api.example.test/v1/responses")
	snapshot := staticCredentialSnapshot(t, "static-session", "vendor-a", "current-secret")
	candidate := mustAppliedIdentityCandidate(t, "route-a", "responses", snapshot, finalURL)
	headers := http.Header{
		"Authorization":       {"Bearer client-secret"},
		"X-Api-Key":           {"prior-attempt-secret"},
		"Chatgpt-Account-Id":  {"client-account"},
		"X-Client-Request-Id": {"logical-request"},
	}

	applied, err := NewService(Config{}).ApplyProviderCredentials(
		context.Background(), headers, candidate, authModeXAPIKey, authModeBearer, nil, finalURL,
	)
	if err != nil {
		t.Fatalf("ApplyProviderCredentials() error = %v", err)
	}
	if !applied.Matches(candidate.Authority()) {
		t.Fatalf("applied identity = %v, want candidate authority", applied)
	}
	if got := headers.Get("X-Api-Key"); got != "current-secret" {
		t.Fatalf("X-Api-Key = %q, want current credential", got)
	}
	if got := headers.Get("Authorization"); got != "Bearer client-secret" {
		t.Fatalf("Authorization = %q, want composer-owned value preserved", got)
	}
	if got := headers.Get("ChatGPT-Account-Id"); got != "client-account" {
		t.Fatalf("ChatGPT-Account-Id = %q, want composer-owned value preserved", got)
	}
	if got := headers.Get("X-Client-Request-Id"); got != "logical-request" {
		t.Fatalf("X-Client-Request-Id = %q, want preserved", got)
	}
}

func TestApplyProviderCredentialsRejectsExpectedAuthorityConflictBeforeInjection(t *testing.T) {
	t.Parallel()
	expectedURL := mustAppliedIdentityURL(t, "https://expected.example.test/v1/responses")
	actualURL := mustAppliedIdentityURL(t, "https://other.example.test/v1/responses")
	snapshot := staticCredentialSnapshot(t, "static-session", "vendor-a", "current-secret")
	candidate := mustAppliedIdentityCandidate(t, "route-a", "responses", snapshot, expectedURL)
	headers := http.Header{
		"Authorization":      {"Bearer client-secret"},
		"X-Api-Key":          {"prior-attempt-secret"},
		"ChatGPT-Account-Id": {"client-account"},
	}

	_, err := NewService(Config{}).ApplyProviderCredentials(
		context.Background(), headers, candidate, authModeBearer, authModeBearer, nil, actualURL,
	)
	var mismatch *codexidentity.AppliedIdentityMismatch
	if !errors.As(err, &mismatch) {
		t.Fatalf("ApplyProviderCredentials() error = %v, want AppliedIdentityMismatch", err)
	}
	for name, want := range map[string]string{
		"Authorization": "Bearer client-secret", "X-Api-Key": "prior-attempt-secret",
	} {
		if got := headers.Get(name); got != want {
			t.Fatalf("%s = %q after mismatch, want unchanged %q", name, got, want)
		}
	}
	if got := headers["ChatGPT-Account-Id"]; len(got) != 1 || got[0] != "client-account" {
		t.Fatalf("ChatGPT-Account-Id = %#v after mismatch, want unchanged", got)
	}
}

func TestApplyProviderCredentialsRejectsIncompleteAttemptEvidence(t *testing.T) {
	t.Parallel()
	finalURL := mustAppliedIdentityURL(t, "https://api.example.test/v1/responses")
	snapshot := staticCredentialSnapshot(t, "static-session", "vendor-a", "current-secret")
	candidate := mustAppliedIdentityCandidate(t, "route-a", "responses", snapshot, finalURL)
	service := NewService(Config{})
	if _, err := service.ApplyProviderCredentials(
		context.Background(), nil, candidate, authModeBearer, authModeBearer, nil, finalURL,
	); err == nil {
		t.Fatal("nil upstream headers were accepted")
	}
	headers := http.Header{"Authorization": {"Bearer client"}, "X-Api-Key": {"old-attempt"}}
	if _, err := service.ApplyProviderCredentials(
		context.Background(), headers, codexidentity.CandidateSnapshot{}, authModeBearer, authModeBearer, nil, finalURL,
	); err == nil {
		t.Fatal("empty candidate was accepted")
	}
	if headers.Get("Authorization") != "Bearer client" || headers.Get("X-Api-Key") != "old-attempt" {
		t.Fatalf("credential headers changed during candidate rejection: %#v", headers)
	}
	if _, err := service.ApplyProviderCredentials(
		context.Background(), make(http.Header), candidate, authModeBearer, authModeBearer, nil, nil,
	); err == nil {
		t.Fatal("missing final URL was accepted")
	}
}

func TestApplyProviderCredentialsRejectsChatGPTOutsideCodexAPI(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.August, 27, 1, 0, 0, 0, time.UTC)
	finalURL := mustAppliedIdentityURL(t, "https://chatgpt.com/backend-api/codex/responses")
	snapshot := chatGPTCredentialSnapshot(t, "login-session", "acct-live", "access-live", now.Add(time.Hour))
	candidate := mustAppliedIdentityCandidate(t, "route-chatgpt", "responses", snapshot, finalURL)
	headers := http.Header{"Authorization": {"Bearer client"}}
	if _, err := NewService(Config{Clock: fixedClock{now: now}}).ApplyProviderCredentials(
		context.Background(), headers, candidate, authModeBearer, authModeBearer, nil, finalURL,
	); err == nil {
		t.Fatal("ChatGPT credential was accepted outside Codex API type")
	}
	if headers.Get("Authorization") != "Bearer client" {
		t.Fatalf("Authorization changed during API-type rejection: %#v", headers)
	}
}

func TestApplyProviderCredentialsUsesActualChatGPTAccount(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.August, 27, 1, 0, 0, 0, time.UTC)
	finalURL := mustAppliedIdentityURL(t, "wss://chatgpt.com/backend-api/codex/responses")
	snapshot := chatGPTCredentialSnapshot(t, "login-session", "acct-live", "access-live", now.Add(time.Hour))
	candidate := mustAppliedIdentityCandidate(t, "route-chatgpt", codexAPIType, snapshot, finalURL)
	headers := http.Header{
		"X-Api-Key":          {"client-key"},
		"ChatGPT-Account-Id": {"forged-account"},
	}

	applied, err := NewService(Config{Clock: fixedClock{now: now}}).ApplyProviderCredentials(
		context.Background(), headers, candidate, authModeBearer, authModeBearer, nil, finalURL,
	)
	if err != nil {
		t.Fatalf("ApplyProviderCredentials() error = %v", err)
	}
	if !applied.Matches(candidate.Authority()) {
		t.Fatal("applied ChatGPT identity did not match the selected authority")
	}
	if got := headers.Get("Authorization"); got != "Bearer access-live" {
		t.Fatalf("Authorization = %q, want actual access token", got)
	}
	if got := headers.Get("ChatGPT-Account-Id"); got != "acct-live" {
		t.Fatalf("ChatGPT-Account-Id = %q, want actual account", got)
	}
	if got := headers.Get("X-Api-Key"); got != "client-key" {
		t.Fatalf("X-Api-Key = %q, want composer-owned value preserved", got)
	}
}

func TestApplyProviderCredentialsRefreshesOnlyAfterIdentityPreflight(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.August, 27, 1, 0, 0, 0, time.UTC)
	finalURL := mustAppliedIdentityURL(t, "https://chatgpt.com/backend-api/codex/responses")
	snapshot := chatGPTCredentialSnapshot(t, "login-session", "acct-live", "access-old", now.Add(time.Minute))
	candidate := mustAppliedIdentityCandidate(t, "route-chatgpt", codexAPIType, snapshot, finalURL)
	store := &appliedIdentityCredentialStore{session: sessionFromAppliedSnapshot(t, snapshot)}
	service := NewService(Config{
		Clock: fixedClock{now: now}, CredentialStore: store,
		HTTPClient: stubHTTPDoer{do: func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(
				`{"access_token":"access-new","refresh_token":"refresh-new"}`,
			))}, nil
		}},
	})
	headers := make(http.Header)

	applied, err := service.ApplyProviderCredentials(
		context.Background(), headers, candidate, authModeBearer, authModeBearer, nil, finalURL,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !applied.Matches(candidate.Authority()) || headers.Get("Authorization") != "Bearer access-new" ||
		headers.Get("ChatGPT-Account-Id") != "acct-live" || store.casCalls != 1 {
		t.Fatalf("applied=%v headers=%#v CAS calls=%d", applied, headers, store.casCalls)
	}
}

func TestApplyProviderCredentialsRejectsChatGPTSubjectConflictBeforeInjection(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.August, 27, 1, 0, 0, 0, time.UTC)
	finalURL := mustAppliedIdentityURL(t, "https://chatgpt.com/backend-api/codex/responses")
	snapshot := chatGPTCredentialSnapshot(t, "login-session", "acct-expected", "access-live", now.Add(time.Hour))
	snapshot.AuthState.AccountID = "acct-actual"
	candidate := mustAppliedIdentityCandidate(t, "route-chatgpt", codexAPIType, snapshot, finalURL)
	headers := http.Header{"Authorization": {"Bearer client"}, "ChatGPT-Account-Id": {"forged"}}
	doCalls := 0

	_, err := NewService(Config{
		Clock: fixedClock{now: now},
		HTTPClient: stubHTTPDoer{do: func(*http.Request) (*http.Response, error) {
			doCalls++
			return nil, errors.New("unexpected refresh request")
		}},
	}).ApplyProviderCredentials(
		context.Background(), headers, candidate, authModeBearer, authModeBearer, nil, finalURL,
	)
	var stateError *ProviderAuthStateError
	if !errors.As(err, &stateError) || stateError.Reason != ProviderAuthReasonCredentialInvalid {
		t.Fatalf("ApplyProviderCredentials() error = %v, want invalid credential state", err)
	}
	if doCalls != 0 {
		t.Fatalf("refresh requests = %d before diagnostic mismatch rejection, want zero", doCalls)
	}
	if headers.Get("Authorization") != "Bearer client" || headers["ChatGPT-Account-Id"][0] != "forged" {
		t.Fatalf("headers changed before subject validation: %#v", headers)
	}
}

func TestApplyProviderCredentialsRejectsOriginConflictBeforeRefreshIO(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.August, 27, 1, 0, 0, 0, time.UTC)
	expectedURL := mustAppliedIdentityURL(t, "https://chatgpt.com/backend-api/codex/responses")
	actualURL := mustAppliedIdentityURL(t, "https://unexpected.example.test/backend-api/codex/responses")
	snapshot := chatGPTCredentialSnapshot(t, "login-session", "acct-live", "access-old", now.Add(time.Minute))
	candidate := mustAppliedIdentityCandidate(t, "route-chatgpt", codexAPIType, snapshot, expectedURL)
	doCalls := 0
	service := NewService(Config{
		Clock: fixedClock{now: now},
		HTTPClient: stubHTTPDoer{do: func(*http.Request) (*http.Response, error) {
			doCalls++
			return nil, errors.New("unexpected refresh request")
		}},
	})
	headers := http.Header{"Authorization": {"Bearer client"}, "ChatGPT-Account-Id": {"forged"}}

	_, err := service.ApplyProviderCredentials(
		context.Background(), headers, candidate, authModeBearer, authModeBearer, nil, actualURL,
	)
	var mismatch *codexidentity.AppliedIdentityMismatch
	if !errors.As(err, &mismatch) {
		t.Fatalf("ApplyProviderCredentials() error = %v, want AppliedIdentityMismatch", err)
	}
	if doCalls != 0 {
		t.Fatalf("refresh requests = %d before AppliedIdentity validation, want zero", doCalls)
	}
	if headers.Get("Authorization") != "Bearer client" || headers["ChatGPT-Account-Id"][0] != "forged" {
		t.Fatalf("headers changed during preflight rejection: %#v", headers)
	}
}

func TestRefreshCredentialSessionRejectsChangedSubjectBeforeCAS(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.August, 27, 1, 0, 0, 0, time.UTC)
	snapshot := chatGPTCredentialSnapshot(t, "login-session", "acct-original", "access-old", now.Add(time.Minute))
	store := &appliedIdentityCredentialStore{session: sessionFromAppliedSnapshot(t, snapshot)}
	changedIDToken := makeTestJWT(t, map[string]any{
		"iss":                         defaultOAuthIssuer,
		"aud":                         defaultOAuthClientID,
		"exp":                         now.Add(time.Hour).Unix(),
		"https://api.openai.com/auth": map[string]any{"chatgpt_account_id": "acct-changed"},
	})
	service := NewService(Config{
		Clock: fixedClock{now: now}, CredentialStore: store,
		HTTPClient: stubHTTPDoer{do: func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(
				`{"access_token":"access-new","refresh_token":"refresh-new","id_token":"` + changedIDToken + `"}`,
			))}, nil
		}},
	})

	refreshed, err := service.RefreshCredentialSession(context.Background(), snapshot)
	if !refreshed {
		t.Fatal("RefreshCredentialSession() refreshed = false, want attempted refresh")
	}
	if err == nil || !strings.Contains(err.Error(), "different account") {
		t.Fatalf("RefreshCredentialSession() error = %v, want changed-account rejection", err)
	}
	if store.casCalls != 0 {
		t.Fatalf("UpdateCredentialSessionCAS calls = %d, want zero", store.casCalls)
	}
}

func TestRefreshCoordinationIsScopedToCredentialSession(t *testing.T) {
	t.Parallel()
	service := NewService(Config{})
	first, firstLeader := service.beginChatGPTRefresh("session-a")
	second, secondLeader := service.beginChatGPTRefresh("session-b")
	joined, joinedLeader := service.beginChatGPTRefresh("session-a")
	if !firstLeader || !secondLeader || joinedLeader {
		t.Fatalf("leader flags = (%t,%t,%t), want (true,true,false)", firstLeader, secondLeader, joinedLeader)
	}
	if first == second || joined != first {
		t.Fatal("refresh coordination crossed session boundaries or failed to join the same session")
	}
}

func staticCredentialSnapshot(t *testing.T, sessionID, vendor, secret string) credentialsession.Snapshot {
	t.Helper()
	digest := sha256.Sum256([]byte("subject:" + secret))
	subject, err := credentialsession.KeyedDigestSubject("test-hmac", digest[:])
	if err != nil {
		t.Fatalf("KeyedDigestSubject() error = %v", err)
	}
	return credentialsession.Snapshot{
		SessionID: sessionID, Vendor: vendor, Kind: credentialsession.KindAPIKey,
		SecretData: secret, Version: 1, Subject: subject,
		AuthState: credentialsession.AuthState{Status: credentialsession.AuthStatusActive},
	}
}

func chatGPTCredentialSnapshot(t *testing.T, sessionID, accountID, accessToken string, expiresAt time.Time) credentialsession.Snapshot {
	t.Helper()
	secret, err := model.EncodeChatGPTProviderSecret(&model.ChatGPTProviderSecret{
		AccessToken: accessToken, RefreshToken: "refresh-old", OAuthIssuer: defaultOAuthIssuer, OAuthClientID: defaultOAuthClientID,
	})
	if err != nil {
		t.Fatalf("EncodeChatGPTProviderSecret() error = %v", err)
	}
	subject, err := credentialsession.AccountSubject(accountID)
	if err != nil {
		t.Fatalf("AccountSubject() error = %v", err)
	}
	return credentialsession.Snapshot{
		SessionID: sessionID, Vendor: "openai", Kind: credentialsession.KindChatGPT,
		SecretData: secret, Version: 1, Subject: subject,
		AuthState: credentialsession.AuthState{
			Status: credentialsession.AuthStatusActive, AccountID: accountID, ExpiresAt: &expiresAt,
		},
	}
}

func mustAppliedIdentityCandidate(
	t *testing.T,
	routeTargetID string,
	apiType string,
	snapshot credentialsession.Snapshot,
	finalURL *url.URL,
) codexidentity.CandidateSnapshot {
	t.Helper()
	candidate, err := codexidentity.NewAuthorityResolver().Resolve(credentialsession.RouteSnapshot{
		RouteTargetID: routeTargetID, APIType: apiType, Credential: snapshot,
	}, apiType, finalURL)
	if err != nil {
		t.Fatalf("AuthorityResolver.Resolve() error = %v", err)
	}
	return candidate
}

func mustAppliedIdentityURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	return parsed
}

type appliedIdentityCredentialStore struct {
	session  *credentialsession.Session
	casCalls int
}

func (s *appliedIdentityCredentialStore) GetCredentialSession(context.Context, string) (*credentialsession.Session, error) {
	return s.session.Clone(), nil
}

func (*appliedIdentityCredentialStore) ResolveCredentialSession(context.Context, string, string) (credentialsession.RouteSnapshot, error) {
	return credentialsession.RouteSnapshot{}, errors.New("unexpected ResolveCredentialSession call")
}

func (*appliedIdentityCredentialStore) WithCredentialSessionMutations(ctx context.Context, _ []string) (context.Context, func(), error) {
	return ctx, func() {}, nil
}

func (s *appliedIdentityCredentialStore) UpdateCredentialSessionCAS(context.Context, string, int64, string, credentialsession.Subject, credentialsession.AuthState) (int64, error) {
	s.casCalls++
	return 2, nil
}

func (*appliedIdentityCredentialStore) UpdateCredentialSessionAuthState(context.Context, string, credentialsession.AuthState) error {
	return nil
}

func sessionFromAppliedSnapshot(t *testing.T, snapshot credentialsession.Snapshot) *credentialsession.Session {
	t.Helper()
	session := &credentialsession.Session{
		ID: snapshot.SessionID, Vendor: snapshot.Vendor, Kind: snapshot.Kind,
		SecretData: snapshot.SecretData, Version: snapshot.Version, AuthState: snapshot.AuthState,
	}
	if err := session.SetSubject(snapshot.Subject); err != nil {
		t.Fatalf("SetSubject() error = %v", err)
	}
	return session
}
