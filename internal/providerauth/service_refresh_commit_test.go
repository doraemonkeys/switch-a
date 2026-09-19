package providerauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/model"
	storepkg "github.com/doraemonkeys/switch-a/internal/store"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

var errRefreshCommitStorage = errors.New("injected temporary credential storage failure")

type refreshCommitFaultStore struct {
	*storepkg.SQLiteStore
	writes          atomic.Int64
	failures        atomic.Int64
	commitThenError atomic.Bool
	beforeWrite     func(context.Context, int64)
}

func (s *refreshCommitFaultStore) UpdateCredentialSessionCAS(
	ctx context.Context, sessionID string, version int64, secret string,
	subject credentialsession.Subject, state credentialsession.AuthState,
) (int64, error) {
	attempt := s.writes.Add(1)
	if s.beforeWrite != nil {
		s.beforeWrite(ctx, attempt)
	}
	if s.failures.Add(-1) >= 0 {
		return 0, errRefreshCommitStorage
	}
	next, err := s.SQLiteStore.UpdateCredentialSessionCAS(ctx, sessionID, version, secret, subject, state)
	if err == nil && s.commitThenError.Swap(false) {
		return 0, errRefreshCommitStorage
	}
	return next, err
}

type refreshCommitFixture struct {
	service  *Service
	storage  *refreshCommitFaultStore
	snapshot credentialsession.Snapshot
	logs     *observer.ObservedLogs
	now      time.Time

	remoteMu      sync.Mutex
	remoteRefresh string
	remoteAccess  string
	exchanges     []string
	usageRequests int
	afterExchange func()
}

func newRefreshCommitFixture(t *testing.T, expiresIn time.Duration) *refreshCommitFixture {
	t.Helper()
	now := time.Date(2026, time.September, 19, 0, 0, 0, 0, time.UTC)
	database, err := storepkg.NewSQLiteStore(filepath.Join(t.TempDir(), "refresh.db"), fixedClock{now: now}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	snapshot := chatGPTCredentialSnapshot(t, "refresh-session", "refresh-account", "access-old", now.Add(expiresIn))
	session, err := database.CreateCredentialSession(context.Background(), sessionFromAppliedSnapshot(t, snapshot))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err = session.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	logCore, logs := observer.New(zap.DebugLevel)
	f := &refreshCommitFixture{
		storage:  &refreshCommitFaultStore{SQLiteStore: database},
		snapshot: snapshot, now: now, logs: logs, remoteRefresh: "refresh-old",
	}
	f.service = NewService(Config{
		CredentialStore: f.storage, Clock: fixedClock{now: now}, Logger: zap.New(logCore),
		HTTPClient: stubHTTPDoer{do: func(request *http.Request) (*http.Response, error) {
			f.remoteMu.Lock()
			defer f.remoteMu.Unlock()
			if request.URL.Path != "/oauth/token" {
				f.usageRequests++
				if request.Header.Get(headerAuthorization) != bearerPrefix+f.remoteAccess {
					return nil, errors.New("usage query sent stale credentials")
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("{}"))}, nil
			}
			if err := request.ParseForm(); err != nil {
				return nil, err
			}
			token := request.Form.Get("refresh_token")
			f.exchanges = append(f.exchanges, token)
			if token != f.remoteRefresh {
				return &http.Response{
					StatusCode: http.StatusBadRequest,
					Body: io.NopCloser(strings.NewReader(
						"{\"error\":{\"code\":\"refresh_token_reused\",\"message\":\"refresh token already used\"}}",
					)),
				}, nil
			}
			f.remoteRefresh = fmt.Sprintf("refresh-generation-%d", len(f.exchanges))
			f.remoteAccess = chatgptAccessJWT(t, "refresh-account", "refresh@example.com", "pro", f.service.clock.Now().Add(time.Hour))
			body := fmt.Sprintf("{\"access_token\":%q,\"refresh_token\":%q}", f.remoteAccess, f.remoteRefresh)
			if f.afterExchange != nil {
				f.afterExchange()
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
		}},
	})
	return f
}

func (f *refreshCommitFixture) apply(t *testing.T, ctx context.Context) error {
	t.Helper()
	return f.applySnapshot(t, ctx, f.snapshot)
}

func (f *refreshCommitFixture) applySnapshot(t *testing.T, ctx context.Context, snapshot credentialsession.Snapshot) error {
	t.Helper()
	finalURL := mustAppliedIdentityURL(t, "https://chatgpt.com/backend-api/codex/responses")
	candidate := mustAppliedIdentityCandidate(t, "route-"+snapshot.SessionID, codexAPIType, "openai", snapshot, finalURL)
	headers := make(http.Header)
	_, err := f.service.ApplyProviderCredentials(ctx, headers, candidate, authModeBearer, authModeBearer, nil, finalURL)
	return err
}

func (f *refreshCommitFixture) persisted(t *testing.T) credentialsession.Snapshot {
	t.Helper()
	session, err := f.storage.GetCredentialSession(context.Background(), f.snapshot.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := session.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func (f *refreshCommitFixture) requireRecovered(t *testing.T, exchanges, writes int) {
	t.Helper()
	persisted := f.persisted(t)
	credential, err := decodeChatGPTCredentialSession(&persisted)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.AuthState.Status != credentialsession.AuthStatusActive ||
		credential.RefreshToken != f.remoteRefresh || credential.AccessToken != f.remoteAccess ||
		len(f.exchanges) != exchanges || f.storage.writes.Load() != int64(writes) {
		t.Fatalf("recovery: auth=%s exchanges=%d writes=%d", persisted.AuthState.Status, len(f.exchanges), f.storage.writes.Load())
	}
	if f.service.pendingChatGPTRefreshCommit(f.snapshot.SessionID) != nil {
		t.Fatal("completed commit still pending")
	}
}

func TestChatGPTRefreshCommitRecovery(t *testing.T) {
	for _, test := range []struct {
		name      string
		expiresIn time.Duration
		force     bool
		failures  int64
	}{
		{name: "healthy", expiresIn: -time.Minute},
		{name: "expired", expiresIn: -time.Minute, failures: 1},
		{name: "proactive", expiresIn: time.Second, failures: 1},
		{name: "forced", expiresIn: time.Hour, force: true, failures: 1},
		{name: "repeated storage failures", expiresIn: -time.Minute, failures: 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newRefreshCommitFixture(t, test.expiresIn)
			f.storage.failures.Store(test.failures)
			for attempt := int64(0); attempt < test.failures; attempt++ {
				var err error
				if test.force {
					_, err = f.service.RefreshCredentialSession(context.Background(), f.snapshot)
				} else {
					err = f.apply(t, context.Background())
				}
				if !errors.Is(err, errRefreshCommitStorage) {
					t.Fatalf("attempt %d: %v, want temporary storage failure", attempt, err)
				}
				if state := f.persisted(t).AuthState; state.Status != credentialsession.AuthStatusActive || state.RefreshFailCount != 0 {
					t.Fatalf("storage error changed authentication state: %#v", state)
				}
				// Recovery must outlive the unrelated stale-snapshot cache.
				f.service.clock = fixedClock{now: f.now.Add(3 * recentRefreshReuseWindow)}
				f.service.InvalidateCredentialSessions([]string{f.snapshot.SessionID})
			}
			// A normal request must settle even a forced refresh whose old access
			// token is still fresh; otherwise its fast path could strand the commit.
			if err := f.apply(t, context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := f.apply(t, context.Background()); err != nil {
				t.Fatal(err)
			}
			f.requireRecovered(t, 1, int(test.failures)+1)
			if test.failures > 0 {
				pendingEvents := f.logs.FilterMessage("chatgpt_refresh.commit_pending").All()
				failedEvents := f.logs.FilterMessage("chatgpt_refresh.commit_failed").All()
				committedEvents := f.logs.FilterMessage("chatgpt_refresh.committed").All()
				if len(pendingEvents) != 1 || len(failedEvents) != int(test.failures) || len(committedEvents) != 1 {
					t.Fatal("missing refresh commit lifecycle diagnostics")
				}
				operationID := pendingEvents[0].ContextMap()["operation_id"]
				if operationID == "" || operationID != failedEvents[0].ContextMap()["operation_id"] ||
					operationID != committedEvents[0].ContextMap()["operation_id"] {
					t.Fatal("commit retry lost its stable operation identifier")
				}
			}
		})
	}
}

func TestChatGPTRefreshCommitForcedRetry(t *testing.T) {
	f := newRefreshCommitFixture(t, time.Hour)
	f.storage.failures.Store(1)
	if _, err := f.service.RefreshCredentialSession(context.Background(), f.snapshot); !errors.Is(err, errRefreshCommitStorage) {
		t.Fatal(err)
	}
	if _, err := f.service.RefreshCredentialSession(context.Background(), f.snapshot); err != nil {
		t.Fatal(err)
	}
	f.requireRecovered(t, 1, 2)
}

func TestChatGPTRefreshCommitSurvivesRequestCancellation(t *testing.T) {
	f := newRefreshCommitFixture(t, -time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.afterExchange = cancel
	if err := f.apply(t, ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("first request: %v, want canceled commit", err)
	}
	f.afterExchange = nil
	if err := f.apply(t, context.Background()); err != nil {
		t.Fatal(err)
	}
	f.requireRecovered(t, 1, 2)
}

func TestChatGPTRefreshCommitReconcilesAmbiguousSave(t *testing.T) {
	f := newRefreshCommitFixture(t, -time.Minute)
	f.storage.commitThenError.Store(true)
	if err := f.apply(t, context.Background()); !errors.Is(err, errRefreshCommitStorage) {
		t.Fatal(err)
	}
	if _, err := f.service.RefreshCredentialSession(context.Background(), f.snapshot); err != nil {
		t.Fatal(err)
	}
	f.requireRecovered(t, 1, 1)
}

func TestChatGPTRefreshCommitPreservesMetadataUpdates(t *testing.T) {
	f := newRefreshCommitFixture(t, -time.Minute)
	f.storage.failures.Store(1)
	if err := f.apply(t, context.Background()); !errors.Is(err, errRefreshCommitStorage) {
		t.Fatal(err)
	}
	if _, err := f.storage.RenameCredentialSessionCAS(context.Background(), f.snapshot.SessionID, f.snapshot.Version, "renamed during recovery"); err != nil {
		t.Fatal(err)
	}
	fetchedAt := f.now.Add(time.Minute)
	if err := f.service.ObserveCredentialSessionUsage(context.Background(), f.snapshot.SessionID, &model.ProviderUsageSnapshot{
		FetchedAt: &fetchedAt, PlanType: "team",
		FiveHour: &model.ProviderUsageWindow{UsedPercent: 42},
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.apply(t, context.Background()); err != nil {
		t.Fatal(err)
	}
	f.requireRecovered(t, 1, 2)
	persisted := f.persisted(t)
	if persisted.Name != "renamed during recovery" || persisted.Version != f.snapshot.Version+3 ||
		persisted.AuthState.PlanType != "team" || persisted.AuthState.UsageSnapshot.FiveHour.UsedPercent != 42 {
		t.Fatalf("metadata was lost: name=%s version=%d state=%#v", persisted.Name, persisted.Version, persisted.AuthState)
	}
}

func TestChatGPTRefreshCommitBeforeRenewingExpiredReplacement(t *testing.T) {
	f := newRefreshCommitFixture(t, -time.Minute)
	f.storage.failures.Store(1)
	if err := f.apply(t, context.Background()); !errors.Is(err, errRefreshCommitStorage) {
		t.Fatal(err)
	}
	firstReplacement := f.remoteRefresh
	f.service.clock = fixedClock{now: f.now.Add(2 * time.Hour)}
	if err := f.apply(t, context.Background()); err != nil {
		t.Fatal(err)
	}
	f.requireRecovered(t, 2, 3)
	if f.exchanges[1] != firstReplacement {
		t.Fatal("expired pending access token renewed with the consumed source refresh token")
	}
}

func TestChatGPTUsageQueryRecoversPendingRefreshCommit(t *testing.T) {
	f := newRefreshCommitFixture(t, -time.Minute)
	f.storage.failures.Store(2)
	if err := f.apply(t, context.Background()); !errors.Is(err, errRefreshCommitStorage) {
		t.Fatal(err)
	}
	if _, err := f.service.RefreshCredentialSessionUsage(context.Background(), f.snapshot); !errors.Is(err, errRefreshCommitStorage) {
		t.Fatal(err)
	}
	if f.usageRequests != 0 {
		t.Fatal("usage endpoint called while a rotated credential was uncommitted")
	}
	if _, err := f.service.RefreshCredentialSessionUsage(context.Background(), f.snapshot); err != nil {
		t.Fatal(err)
	}
	f.requireRecovered(t, 1, 3)
	if f.usageRequests != 1 {
		t.Fatalf("usage requests = %d, want one", f.usageRequests)
	}
}

func TestChatGPTRefreshCommitDoesNotOverwriteReauthentication(t *testing.T) {
	f := newRefreshCommitFixture(t, -time.Minute)
	f.storage.failures.Store(1)
	if err := f.apply(t, context.Background()); !errors.Is(err, errRefreshCommitStorage) {
		t.Fatal(err)
	}
	replacement := chatGPTCredentialSnapshot(t, f.snapshot.SessionID, "refresh-account", "reauthenticated-access", f.now.Add(time.Hour))
	replacement.AuthState.LastRefreshAt = timePointer(f.now.Add(-time.Hour))
	ctx, release, err := f.storage.WithCredentialSessionMutations(context.Background(), []string{f.snapshot.SessionID})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.storage.SQLiteStore.UpdateCredentialSessionCAS(ctx, f.snapshot.SessionID, f.snapshot.Version,
		replacement.SecretData, replacement.Subject, replacement.AuthState)
	release()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.RefreshCredentialSession(context.Background(), f.snapshot); err != nil {
		t.Fatal(err)
	}
	persisted := f.persisted(t)
	if persisted.SecretData != replacement.SecretData || len(f.exchanges) != 1 || f.storage.writes.Load() != 1 {
		t.Fatal("pending refresh overwrote or refreshed the replacement login")
	}
	if f.service.pendingChatGPTRefreshCommit(f.snapshot.SessionID) != nil {
		t.Fatal("superseded commit was retained")
	}
}

func TestChatGPTRefreshCommitDoesNotResurrectDeletedSession(t *testing.T) {
	f := newRefreshCommitFixture(t, -time.Minute)
	f.storage.failures.Store(1)
	if err := f.apply(t, context.Background()); !errors.Is(err, errRefreshCommitStorage) {
		t.Fatal(err)
	}
	if err := f.storage.DeleteCredentialSession(context.Background(), f.snapshot.SessionID); err != nil {
		t.Fatal(err)
	}
	if err := f.apply(t, context.Background()); !errors.Is(err, credentialsession.ErrNotFound) {
		t.Fatalf("request after deletion = %v", err)
	}
	if len(f.exchanges) != 1 || f.storage.writes.Load() != 1 {
		t.Fatal("deleted session was refreshed or rewritten")
	}
}

func TestChatGPTRefreshCommitKeepsFreshRequestPath(t *testing.T) {
	f := newRefreshCommitFixture(t, time.Hour)
	if err := f.apply(t, context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.exchanges) != 0 || f.storage.writes.Load() != 0 {
		t.Fatal("fresh credential triggered refresh work")
	}
}

func TestChatGPTRefreshCommitUsesCredentialMaterialAcrossImports(t *testing.T) {
	f := newRefreshCommitFixture(t, -time.Minute)
	f.storage.failures.Store(1)
	if err := f.apply(t, context.Background()); !errors.Is(err, errRefreshCommitStorage) {
		t.Fatal(err)
	}
	var formatted bytes.Buffer
	if err := json.Indent(&formatted, []byte(f.snapshot.SecretData), "", "  "); err != nil {
		t.Fatal(err)
	}
	ctx, release, err := f.storage.WithCredentialSessionMutations(context.Background(), []string{f.snapshot.SessionID})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.storage.SQLiteStore.UpdateCredentialSessionCAS(ctx, f.snapshot.SessionID, f.snapshot.Version,
		formatted.String(), f.snapshot.Subject, f.snapshot.AuthState)
	f.service.InvalidateCredentialSessions([]string{f.snapshot.SessionID})
	release()
	if err != nil {
		t.Fatal(err)
	}
	if err := f.apply(t, context.Background()); err != nil {
		t.Fatal(err)
	}
	f.requireRecovered(t, 1, 2)
}

func TestChatGPTRefreshCommitRespectsAuthenticationLifecycle(t *testing.T) {
	f := newRefreshCommitFixture(t, -time.Minute)
	f.storage.failures.Store(1)
	if err := f.apply(t, context.Background()); !errors.Is(err, errRefreshCommitStorage) {
		t.Fatal(err)
	}
	ctx, release, err := f.storage.WithCredentialSessionMutations(context.Background(), []string{f.snapshot.SessionID})
	if err != nil {
		t.Fatal(err)
	}
	state := f.snapshot.AuthState.Clone()
	state.Status = credentialsession.AuthStatusReauthRequired
	state.StatusReason = ProviderAuthReasonInteractionRequired
	err = f.storage.UpdateCredentialSessionAuthState(ctx, f.snapshot.SessionID, state)
	release()
	if err != nil {
		t.Fatal(err)
	}
	var authErr *ProviderAuthStateError
	if err := f.apply(t, context.Background()); !errors.As(err, &authErr) || authErr.Status != ProviderAuthStatusReauthRequired {
		t.Fatalf("explicit auth transition was bypassed: %v", err)
	}
	if len(f.exchanges) != 1 || f.storage.writes.Load() != 1 {
		t.Fatal("pending result reactivated a session requiring reauthentication")
	}
}

func TestChatGPTRefreshCommitConcurrentRecovery(t *testing.T) {
	f := newRefreshCommitFixture(t, -time.Minute)
	f.storage.failures.Store(1)
	if err := f.apply(t, context.Background()); !errors.Is(err, errRefreshCommitStorage) {
		t.Fatal(err)
	}
	other := chatGPTCredentialSnapshot(t, "independent-session", "other-account", "other-access", f.now.Add(time.Hour))
	if _, err := f.storage.CreateCredentialSession(context.Background(), sessionFromAppliedSnapshot(t, other)); err != nil {
		t.Fatal(err)
	}
	entered, unblock := make(chan struct{}), make(chan struct{})
	var unblockOnce sync.Once
	unblockCommit := func() { unblockOnce.Do(func() { close(unblock) }) }
	defer unblockCommit()
	f.storage.beforeWrite = func(ctx context.Context, attempt int64) {
		if attempt == 2 {
			close(entered)
			select {
			case <-unblock:
			case <-ctx.Done():
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	const callers = 12
	results := make(chan error, callers)
	go func() { results <- f.apply(t, ctx) }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("commit retry never started")
	}
	for range callers - 1 {
		go func() { results <- f.apply(t, ctx) }()
	}
	// Session coordination must not hold a database transaction or global lock
	// while this one account waits to save.
	if err := f.applySnapshot(t, ctx, other); err != nil {
		t.Fatalf("unrelated credential blocked: %v", err)
	}
	canceled, cancelFollower := context.WithCancel(ctx)
	cancelFollower()
	if err := f.apply(t, canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled follower = %v", err)
	}
	unblockCommit()
	for range callers {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	f.requireRecovered(t, 1, 2)
}
