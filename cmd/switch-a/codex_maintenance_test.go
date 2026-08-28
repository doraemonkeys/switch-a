package main

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/codex/continuity"
	"github.com/doraemonkeys/switch-a/internal/codex/cookie"
	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
	codexmaintenance "github.com/doraemonkeys/switch-a/internal/codex/maintenance"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/store"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

type maintenanceCompositionClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *maintenanceCompositionClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (*maintenanceCompositionClock) NewTicker(duration time.Duration) *time.Ticker {
	return time.NewTicker(duration)
}

func (c *maintenanceCompositionClock) advance(delta time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(delta)
	c.mu.Unlock()
}

func TestApplicationCodexLifecycleStartsImmediateSweepAndStops(t *testing.T) {
	security := testApplicationCodexSecurity(t, 9)
	persistence, err := store.NewSQLiteStore(
		filepath.Join(t.TempDir(), "maintenance.db"), internal.RealClock{}, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer persistence.Close()
	core, observed := observer.New(zapcore.DebugLevel)
	log := zap.New(core)
	if err := persistence.FinalizeStaticCredentialSubjects(context.Background(), security.keyring); err != nil {
		t.Fatal(err)
	}
	runtime, err := newApplicationCodexLifecycle(
		context.Background(), persistence, security, internal.RealClock{}, log,
	)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.HTTP == nil || runtime.WebSocket == nil || runtime.maintenance == nil || runtime.maintenance.owner != nil {
		t.Fatalf("Codex lifecycle = %+v", runtime)
	}
	if err := startApplicationCodexLifecycle(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for observed.FilterMessage("codex.maintenance_sweep").Len() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	entries := observed.FilterMessage("codex.maintenance_sweep").All()
	if len(entries) != 1 || entries[0].ContextMap()["trigger"] != string(codexmaintenance.TriggerInitial) {
		t.Fatalf("maintenance logs = %+v", entries)
	}
	stopApplicationCodexLifecycle(runtime, log)
	stopApplicationCodexLifecycle(runtime, log)
	stopApplicationCodexLifecycle(nil, log)
}

func TestApplicationCodexMaintenanceCompositionBoundaries(t *testing.T) {
	persistence, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "boundaries.db"), internal.RealClock{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer persistence.Close()
	valid := func(storage *store.SQLiteStore, continuity *codexcontinuity.Service, cookies *providercookie.Service, clock internal.Clock, log *zap.Logger) error {
		_, err := newApplicationCodexMaintenance(storage, continuity, cookies, clock, log)
		return err
	}
	for _, err := range []error{
		valid(nil, nil, nil, internal.RealClock{}, zap.NewNop()),
		valid(persistence, nil, nil, internal.RealClock{}, zap.NewNop()),
		valid(persistence, &codexcontinuity.Service{}, nil, internal.RealClock{}, zap.NewNop()),
		valid(persistence, nil, &providercookie.Service{}, internal.RealClock{}, zap.NewNop()),
		valid(persistence, &codexcontinuity.Service{}, &providercookie.Service{}, nil, zap.NewNop()),
		valid(persistence, &codexcontinuity.Service{}, &providercookie.Service{}, internal.RealClock{}, nil),
	} {
		if err == nil {
			t.Fatal("maintenance composition accepted an invalid dependency set")
		}
	}
	if err := (*applicationCodexMaintenance)(nil).Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := (*applicationCodexMaintenance)(nil).Start(context.Background()); err == nil {
		t.Fatal("nil maintenance started")
	}
}

func TestCodexMaintenanceObserverLogsCountsAndFailures(t *testing.T) {
	core, observed := observer.New(zapcore.DebugLevel)
	log := zap.New(core)
	observe := codexMaintenanceLogObserver(log)
	observe.ObserveMaintenance(codexmaintenance.Event{
		SweepID: "success", Trigger: codexmaintenance.TriggerInitial, Duration: time.Second, ReachableAuthorities: 2,
		Continuity: codexcontinuity.CleanupResult{Expired: 3, Tombstoned: 4, Deleted: 5},
		Cookies:    providercookie.CleanupResult{ExpiredBindings: 6, ExpiredCookies: 7, OrphanAuthorities: 8, EmptyAuthorities: 9},
	})
	continuityErr := errors.New("continuity failed")
	observe.ObserveMaintenance(codexmaintenance.Event{
		SweepID: "failed", Trigger: codexmaintenance.TriggerPeriodic,
		CookieSkipReason: codexmaintenance.CookieCatalogFailed, ContinuityError: continuityErr,
	})
	entries := observed.All()
	if len(entries) != 2 || entries[0].Level != zapcore.InfoLevel || entries[1].Level != zapcore.WarnLevel {
		t.Fatalf("maintenance log entries = %+v", entries)
	}
	fields := entries[0].ContextMap()
	if fields["sweep_id"] != "success" || fields["continuity_deleted"] != int64(5) || fields["cookie_orphan_authorities"] != int64(8) {
		t.Fatalf("success fields = %+v", fields)
	}
	failureFields := entries[1].ContextMap()
	if failureFields["cookie_skip_reason"] != string(codexmaintenance.CookieCatalogFailed) || failureFields["continuity_error"] != continuityErr.Error() {
		t.Fatalf("failure fields = %+v", failureFields)
	}
	if defaultCodexMaintenanceSweepInterval != time.Hour {
		t.Fatalf("maintenance interval = %v", defaultCodexMaintenanceSweepInterval)
	}
}

func TestApplicationCodexMaintenanceCatalogDrivesOrphanGraceAndReachableRecovery(t *testing.T) {
	ctx := context.Background()
	clock := &maintenanceCompositionClock{now: time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)}
	security := testApplicationCodexSecurity(t, 10)
	persistence, err := store.NewSQLiteStore(
		filepath.Join(t.TempDir(), "orphan-grace.db"), clock, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer persistence.Close()
	if err := persistence.FinalizeStaticCredentialSubjects(ctx, security.keyring); err != nil {
		t.Fatal(err)
	}
	createMaintenanceCompositionSession(t, persistence, clock.Now(), "session-a", "account-a")
	provider := maintenanceCompositionProvider("route-a", "session-a")
	if err := persistence.CreateProvider(ctx, &provider); err != nil {
		t.Fatal(err)
	}
	digester, _, cookies, err := newApplicationCodexServices(
		ctx, persistence, security, bytes.NewReader(bytes.Repeat([]byte{11}, 256)),
		providercookie.HostCanonicalizerFunc(codexidentity.CanonicalizeCookieHost), clock, zap.NewNop(),
	)
	if err != nil {
		t.Fatal(err)
	}
	clientScope, err := digester.ClientScope([]byte("client-secret"))
	if err != nil {
		t.Fatal(err)
	}
	access, err := cookies.ResolveJar(ctx, "issue", "", []codexidentity.ClientScope{clientScope})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := persistence.LoadCodexMaintenanceCatalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	reachable, err := snapshot.ReachableCookieAuthorities()
	if err != nil || len(reachable) != 1 {
		t.Fatalf("initial reachable = %+v, %v", reachable, err)
	}
	request, err := cookies.BeginRequest("seed-cookie", access)
	if err != nil {
		t.Fatal(err)
	}
	responseURL, _ := url.Parse("https://cookie.example/v1/responses")
	if _, err := request.ApplyResponse(reachable[0], responseURL, []string{"sid=value; Max-Age=7776000; Path=/"}); err != nil {
		t.Fatal(err)
	}
	if _, err := request.Commit(ctx, reachable[0]); err != nil {
		t.Fatal(err)
	}

	if err := persistence.DeleteProvider(ctx, provider.ID); err != nil {
		t.Fatal(err)
	}
	clock.advance(time.Hour)
	if result := cleanupFromCatalog(t, ctx, persistence, cookies, "mark-unreachable"); result.OrphanAuthorities != 0 {
		t.Fatalf("first unreachable cleanup = %+v", result)
	}
	replacement := maintenanceCompositionProvider("route-b", "session-a")
	if err := persistence.CreateProvider(ctx, &replacement); err != nil {
		t.Fatal(err)
	}
	clock.advance(time.Hour)
	if result := cleanupFromCatalog(t, ctx, persistence, cookies, "restore-reachable"); result.OrphanAuthorities != 0 {
		t.Fatalf("reachable recovery cleanup = %+v", result)
	}
	if err := persistence.DeleteProvider(ctx, replacement.ID); err != nil {
		t.Fatal(err)
	}
	clock.advance(time.Hour)
	_ = cleanupFromCatalog(t, ctx, persistence, cookies, "remark-unreachable")
	clock.advance(providercookie.DefaultOrphanAuthorityGrace)
	if result := cleanupFromCatalog(t, ctx, persistence, cookies, "delete-orphan"); result.OrphanAuthorities != 1 {
		t.Fatalf("grace-boundary cleanup = %+v", result)
	}
}

func maintenanceCompositionProvider(id, sessionID string) model.Provider {
	return model.Provider{
		ID: id, Name: id, Vendor: "openai", Enabled: true,
		APITypes: []model.ProviderAPIType{{APIType: "codex", BaseURL: "https://cookie.example/v1"}},
		CredentialSessions: []credentialsession.RouteSnapshot{{
			APIType: "codex", VendorScope: "openai", Credential: credentialsession.Snapshot{SessionID: sessionID},
		}},
	}
}

func createMaintenanceCompositionSession(t *testing.T, persistence *store.SQLiteStore, now time.Time, id, account string) {
	t.Helper()
	subject, _ := credentialsession.AccountSubject(account)
	session := &credentialsession.Session{
		ID: id, Kind: credentialsession.KindChatGPT, SecretData: `{"access_token":"test"}`, Version: 1,
		AuthState: credentialsession.AuthState{Status: credentialsession.AuthStatusActive, AccountID: account}, CreatedAt: now, UpdatedAt: now,
	}
	if err := session.SetSubject(subject); err != nil {
		t.Fatal(err)
	}
	if _, err := persistence.CreateCredentialSession(context.Background(), session); err != nil {
		t.Fatal(err)
	}
}

func cleanupFromCatalog(
	t *testing.T,
	ctx context.Context,
	persistence *store.SQLiteStore,
	cookies *providercookie.Service,
	operationID providercookie.OperationID,
) providercookie.CleanupResult {
	t.Helper()
	snapshot, err := persistence.LoadCodexMaintenanceCatalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	reachable, err := snapshot.ReachableCookieAuthorities()
	if err != nil {
		t.Fatal(err)
	}
	result, err := cookies.Cleanup(ctx, operationID, reachable)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
