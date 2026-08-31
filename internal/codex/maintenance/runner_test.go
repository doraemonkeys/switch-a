package maintenance

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/continuity"
	continuitysqlite "github.com/doraemonkeys/switch-a/internal/codex/continuity/sqlite"
	"github.com/doraemonkeys/switch-a/internal/codex/cookie"
	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/doraemonkeys/switch-a/internal/codex/keyring"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

type fakeMaintenanceClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeMaintenanceClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeMaintenanceClock) advance(delta time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(delta)
	c.mu.Unlock()
}

type fakeMaintenanceTicker struct {
	ticks   chan time.Time
	stopped chan struct{}
	once    sync.Once
}

func (t *fakeMaintenanceTicker) C() <-chan time.Time { return t.ticks }
func (t *fakeMaintenanceTicker) Stop()               { t.once.Do(func() { close(t.stopped) }) }

type fakeTickerFactory struct {
	mu       sync.Mutex
	ticker   Ticker
	interval Interval
}

func (f *fakeTickerFactory) NewTicker(interval Interval) Ticker {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.interval = interval
	return f.ticker
}

func (f *fakeTickerFactory) createdInterval() Interval {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.interval
}

type sequenceIDs struct {
	mu     sync.Mutex
	values []string
}

func (s *sequenceIDs) NewID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.values) == 0 {
		return "sweep-default"
	}
	value := s.values[0]
	s.values = s.values[1:]
	return value
}

type fakeCatalog struct {
	mu        sync.Mutex
	snapshots []CatalogSnapshot
	errors    []error
	calls     int
}

func (c *fakeCatalog) LoadCodexMaintenanceCatalog(context.Context) (CatalogSnapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	index := c.calls
	c.calls++
	if index < len(c.errors) && c.errors[index] != nil {
		return CatalogSnapshot{}, c.errors[index]
	}
	if index < len(c.snapshots) {
		return c.snapshots[index], nil
	}
	return CatalogSnapshot{}, nil
}

type fakeContinuityCleaner struct {
	mu      sync.Mutex
	result  codexcontinuity.CleanupResult
	err     error
	calls   int
	onCall  func()
	blocked <-chan struct{}
}

func (c *fakeContinuityCleaner) Cleanup(ctx context.Context) (codexcontinuity.CleanupResult, error) {
	c.mu.Lock()
	c.calls++
	onCall := c.onCall
	blocked := c.blocked
	result, err := c.result, c.err
	c.mu.Unlock()
	if onCall != nil {
		onCall()
	}
	if blocked != nil {
		select {
		case <-blocked:
		case <-ctx.Done():
			return codexcontinuity.CleanupResult{}, ctx.Err()
		}
	}
	return result, err
}

type fakeCookieCleaner struct {
	mu          sync.Mutex
	result      providercookie.CleanupResult
	err         error
	calls       int
	operationID providercookie.OperationID
	reachable   []codexidentity.CookieAuthority
}

func (c *fakeCookieCleaner) Cleanup(_ context.Context, operationID providercookie.OperationID, reachable []codexidentity.CookieAuthority) (providercookie.CleanupResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	c.operationID = operationID
	c.reachable = append([]codexidentity.CookieAuthority(nil), reachable...)
	return c.result, c.err
}

func TestRunnerRunsInitialPeriodicAndStopJoinsTicker(t *testing.T) {
	clock := &fakeMaintenanceClock{now: time.Date(2026, 8, 27, 1, 0, 0, 0, time.FixedZone("local", 8*60*60))}
	ticker := &fakeMaintenanceTicker{ticks: make(chan time.Time, 1), stopped: make(chan struct{})}
	factory := &fakeTickerFactory{ticker: ticker}
	interval, _ := NewInterval(15 * time.Minute)
	account, _ := credentialsession.AccountSubject("account-a")
	catalog := &fakeCatalog{snapshots: []CatalogSnapshot{
		NewCatalogSnapshot([]CatalogRoute{{RouteTargetID: "route", Vendor: "openai", FinalURL: "https://example.test/v1", Subject: account}}),
		NewCatalogSnapshot([]CatalogRoute{{RouteTargetID: "route", Vendor: "openai", FinalURL: "wss://example.test/ws", Subject: account}}),
	}}
	continuity := &fakeContinuityCleaner{result: codexcontinuity.CleanupResult{Expired: 1, Tombstoned: 2, Deleted: 3}}
	continuity.onCall = func() { clock.advance(2 * time.Second) }
	cookies := &fakeCookieCleaner{result: providercookie.CleanupResult{ExpiredBindings: 4, ExpiredCookies: 5, OrphanAuthorities: 6, EmptyAuthorities: 7}}
	events := make(chan Event, 2)
	runner, err := NewRunner(Config{
		Interval: interval, Clock: clock, Tickers: factory, IDs: &sequenceIDs{values: []string{"sweep-1", "sweep-2"}},
		Catalog: catalog, Continuity: continuity, Cookies: cookies, Observer: ObserverFunc(func(event Event) { events <- event }),
	})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := runner.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	initial := awaitMaintenanceEvent(t, events)
	if initial.Trigger != TriggerInitial || initial.SweepID != "sweep-1" || initial.Duration != 2*time.Second || initial.ReachableAuthorities != 1 {
		t.Fatalf("initial event = %+v", initial)
	}
	if initial.Continuity.Deleted != 3 || initial.Cookies.OrphanAuthorities != 6 || initial.Failed() {
		t.Fatalf("initial result = %+v", initial)
	}
	if got := factory.createdInterval().Duration(); got != 15*time.Minute {
		t.Fatalf("ticker interval = %v", got)
	}
	ticker.ticks <- clock.Now()
	periodic := awaitMaintenanceEvent(t, events)
	if periodic.Trigger != TriggerPeriodic || periodic.SweepID != "sweep-2" {
		t.Fatalf("periodic event = %+v", periodic)
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := owner.Stop(stopCtx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ticker.stopped:
	case <-time.After(time.Second):
		t.Fatal("ticker was not stopped before owner joined")
	}
	if err := owner.Stop(stopCtx); err != nil {
		t.Fatalf("idempotent Stop() error = %v", err)
	}
}

func TestRunnerSkipsWholeCookieReachabilityOnCatalogFailures(t *testing.T) {
	account, _ := credentialsession.AccountSubject("account-a")
	valid := CatalogRoute{RouteTargetID: "route-a", Vendor: "openai", FinalURL: "https://valid.example", Subject: account}
	tests := []struct {
		name       string
		id         string
		catalog    *fakeCatalog
		wantReason CookieSkipReason
	}{
		{name: "list", id: "sweep-list", catalog: &fakeCatalog{errors: []error{errors.New("list failed")}}, wantReason: CookieCatalogFailed},
		{name: "origin after valid row", id: "sweep-origin", catalog: &fakeCatalog{snapshots: []CatalogSnapshot{NewCatalogSnapshot([]CatalogRoute{valid, {RouteTargetID: "route-b", Vendor: "openai", FinalURL: "://bad", Subject: account}})}}, wantReason: CookieCatalogInvalid},
		{name: "subject after valid row", id: "sweep-subject", catalog: &fakeCatalog{snapshots: []CatalogSnapshot{NewCatalogSnapshot([]CatalogRoute{valid, {RouteTargetID: "route-b", Vendor: "openai", FinalURL: "https://pending.example", Subject: credentialsession.PendingSubject()}})}}, wantReason: CookieCatalogInvalid},
		{name: "operation ID", id: "", catalog: &fakeCatalog{}, wantReason: CookieOperationInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clock := &fakeMaintenanceClock{now: time.Now()}
			continuity := &fakeContinuityCleaner{}
			cookies := &fakeCookieCleaner{}
			events := make(chan Event, 1)
			runner := newTestRunner(t, clock, test.catalog, continuity, cookies, &sequenceIDs{values: []string{test.id}}, events)
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- runner.Run(ctx) }()
			event := awaitMaintenanceEvent(t, events)
			cancel()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if event.CookieSkipReason != test.wantReason || event.CookieError == nil || !event.Failed() {
				t.Fatalf("event = %+v", event)
			}
			if continuity.calls != 1 || cookies.calls != 0 {
				t.Fatalf("cleanup calls = continuity %d, Cookie %d", continuity.calls, cookies.calls)
			}
			if test.wantReason == CookieOperationInvalid && test.catalog.calls != 0 {
				t.Fatalf("catalog calls = %d, want zero", test.catalog.calls)
			}
		})
	}
}

func TestRunnerIsolatesCleanupErrorsAndReportsCookieFailure(t *testing.T) {
	clock := &fakeMaintenanceClock{now: time.Now()}
	continuityErr := errors.New("continuity unavailable")
	cookieErr := errors.New("Cookie unavailable")
	continuity := &fakeContinuityCleaner{err: continuityErr}
	cookies := &fakeCookieCleaner{err: cookieErr}
	events := make(chan Event, 1)
	runner := newTestRunner(t, clock, &fakeCatalog{}, continuity, cookies, &sequenceIDs{values: []string{"sweep-errors"}}, events)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	event := awaitMaintenanceEvent(t, events)
	cancel()
	_ = <-done
	if !errors.Is(event.ContinuityError, continuityErr) || !errors.Is(event.CookieError, cookieErr) || !event.Failed() {
		t.Fatalf("event = %+v", event)
	}
	if cookies.calls != 1 {
		t.Fatalf("Cookie cleanup calls = %d, want 1 despite continuity failure", cookies.calls)
	}
}

func TestOwnerStopCancelsInFlightSweepAndHonorsDeadline(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	continuity := &fakeContinuityCleaner{
		blocked: release,
		onCall:  func() { close(started) },
	}
	events := make(chan Event, 1)
	runner := newTestRunner(t, &fakeMaintenanceClock{now: time.Now()}, &fakeCatalog{}, continuity, &fakeCookieCleaner{}, &sequenceIDs{}, events)
	owner, err := runner.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("maintenance sweep did not start")
	}
	deadline, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if err := owner.Stop(deadline); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Stop(deadline) = %v", err)
	}
	close(release)
	if err := owner.Stop(context.Background()); err != nil {
		t.Fatalf("Stop(join) = %v", err)
	}
	if event := awaitMaintenanceEvent(t, events); !errors.Is(event.ContinuityError, context.Canceled) {
		t.Fatalf("canceled sweep event = %+v", event)
	}
}

const alreadyExpiredStopStressRuns = 100

func TestOwnerStopAlreadyExpiredContextWinsCompletedOwner(t *testing.T) {
	completionErr := errors.New("owner completed")
	tests := []struct {
		name       string
		newContext func() (context.Context, context.CancelFunc)
	}{
		{
			name: "canceled",
			newContext: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx, cancel
			},
		},
		{
			name: "expired deadline",
			newContext: func() (context.Context, context.CancelFunc) {
				return context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for range alreadyExpiredStopStressRuns {
				ctx, cancelContext := test.newContext()
				done := make(chan struct{})
				close(done)
				cancelSignaled := make(chan struct{})
				owner := &Owner{
					cancel: func() { close(cancelSignaled) },
					done:   done,
					err:    completionErr,
				}

				if err := owner.Stop(ctx); err != ctx.Err() {
					t.Fatalf("Stop() = %v, want context error %v", err, ctx.Err())
				}
				select {
				case <-cancelSignaled:
				default:
					t.Fatal("Stop returned before signaling owner cancellation")
				}
				if err := owner.Stop(ctx); err != ctx.Err() {
					t.Fatalf("repeated Stop() = %v, want context error %v", err, ctx.Err())
				}
				if err := owner.Stop(context.Background()); !errors.Is(err, completionErr) {
					t.Fatalf("Stop(join) = %v, want owner completion error %v", err, completionErr)
				}
				cancelContext()
			}
		})
	}
}

func TestRunnerConfigurationBoundaries(t *testing.T) {
	if _, err := NewInterval(0); err == nil {
		t.Fatal("NewInterval accepted zero")
	}
	interval, _ := NewInterval(time.Millisecond)
	valid := Config{
		Interval: interval, Clock: &fakeMaintenanceClock{now: time.Now()}, Catalog: &fakeCatalog{},
		Continuity: &fakeContinuityCleaner{}, Cookies: &fakeCookieCleaner{},
	}
	for _, mutate := range []func(*Config){
		func(config *Config) { config.Interval = Interval{} },
		func(config *Config) { config.Clock = nil },
		func(config *Config) { config.Catalog = nil },
		func(config *Config) { config.Continuity = nil },
		func(config *Config) { config.Cookies = nil },
		func(config *Config) { var typed *fakeCatalog; config.Catalog = typed },
	} {
		config := valid
		mutate(&config)
		if _, err := NewRunner(config); err == nil {
			t.Fatalf("NewRunner accepted invalid config %+v", config)
		}
	}
	runner, err := NewRunner(valid)
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Run(nil); err == nil {
		t.Fatal("Run accepted nil context")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runner.Run(canceled); err != nil {
		t.Fatalf("Run(canceled) error = %v", err)
	}
	if _, err := runner.Start(nil); err == nil {
		t.Fatal("Start accepted nil context")
	}
	if _, err := (*Runner)(nil).Start(context.Background()); err == nil {
		t.Fatal("nil Runner started")
	}
	if err := (*Owner)(nil).Stop(context.Background()); err != nil {
		t.Fatalf("nil Owner Stop() = %v", err)
	}
	owner := &Owner{cancel: func() {}, done: make(chan struct{})}
	if err := owner.Stop(nil); err == nil {
		t.Fatal("Stop accepted nil context")
	}
}

func TestRunnerRejectsNilTicker(t *testing.T) {
	interval, _ := NewInterval(time.Minute)
	runner, err := NewRunner(Config{
		Interval: interval, Clock: &fakeMaintenanceClock{now: time.Now()}, Tickers: &fakeTickerFactory{},
		Catalog: &fakeCatalog{}, Continuity: &fakeContinuityCleaner{}, Cookies: &fakeCookieCleaner{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Run(context.Background()); err == nil {
		t.Fatal("Run accepted nil ticker")
	}
}

func TestPeriodicSweepRetiresQuietContinuityLegacyKeyReference(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "legacy-retirement.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDatabase, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDatabase.Close()
	ctx := context.Background()
	if err := continuitysqlite.Migrate(ctx, database); err != nil {
		t.Fatal(err)
	}
	repository, err := continuitysqlite.Open(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	keyMaterial := func(value byte) string {
		return base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{value}, 32))
	}
	document := `{"schema_version":1,"hmac":{"current":"h1","keys":{"h1":"` + keyMaterial(1) +
		`"}},"aead":{"current":"a1","keys":{"a1":"` + keyMaterial(2) + `"}}}`
	keys, err := codexkeyring.Parse([]byte(document), bytes.NewReader(bytes.Repeat([]byte{3}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	digester, err := codexidentity.NewDigester(keys)
	if err != nil {
		t.Fatal(err)
	}
	clock := &fakeMaintenanceClock{now: time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)}
	limits := codexcontinuity.Limits{PendingTTL: time.Minute, CommittedIdleTTL: time.Minute, TombstoneTTL: time.Minute, MaxBindings: 10}
	policy, err := codexcontinuity.NewPolicy(map[codexcontinuity.Kind]codexcontinuity.Limits{
		codexcontinuity.KindThreadID: limits, codexcontinuity.KindSessionID: limits,
		codexcontinuity.KindConversationID: limits, codexcontinuity.KindWindowID: limits,
		codexcontinuity.KindTurnState: limits, codexcontinuity.KindTurnMetadata: limits,
		codexcontinuity.KindResponseReference: limits,
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := codexcontinuity.NewService(codexcontinuity.Config{Store: repository, Digester: &digester, Policy: policy, Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	client, err := digester.ClientScope([]byte("client-secret"))
	if err != nil {
		t.Fatal(err)
	}
	subject, _ := codexidentity.NewAccountCredentialSubject("account-a")
	origin, _ := codexidentity.ParseOrigin("https://example.test")
	authority, _ := codexidentity.NewUpstreamAuthority("openai", origin, subject)
	protocol, _ := codexidentity.NewProtocolScope(authority, "codex")
	if _, err := service.Claim(ctx, codexcontinuity.ClaimRequest{
		Evidence: codexcontinuity.Evidence{Kind: codexcontinuity.KindThreadID, DigestInput: []byte("thread-a")},
		Scope: codexcontinuity.Scope{
			CurrentClientScope: client, ClientScopeCandidates: []codexidentity.ClientScope{client}, ProtocolScope: protocol,
		},
		OperationID: "claim-legacy",
	}); err != nil {
		t.Fatal(err)
	}
	if versions, err := repository.RequiredHMACVersions(ctx); err != nil || len(versions) != 1 || versions[0] != "h1" {
		t.Fatalf("required versions before cleanup = %v, %v", versions, err)
	}

	clock.advance(90 * time.Second)
	ticker := &fakeMaintenanceTicker{ticks: make(chan time.Time, 1), stopped: make(chan struct{})}
	events := make(chan Event, 2)
	interval, _ := NewInterval(time.Minute)
	runner, err := NewRunner(Config{
		Interval: interval, Clock: clock, Tickers: &fakeTickerFactory{ticker: ticker}, IDs: &sequenceIDs{values: []string{"legacy-1", "legacy-2"}},
		Catalog: &fakeCatalog{}, Continuity: service, Cookies: &fakeCookieCleaner{}, Observer: ObserverFunc(func(event Event) { events <- event }),
	})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := runner.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	initial := awaitMaintenanceEvent(t, events)
	if initial.Continuity.Tombstoned != 1 || initial.Continuity.Deleted != 0 {
		t.Fatalf("initial cleanup = %+v", initial.Continuity)
	}
	clock.advance(time.Minute)
	ticker.ticks <- clock.Now()
	periodic := awaitMaintenanceEvent(t, events)
	if periodic.Continuity.Deleted != 1 {
		t.Fatalf("periodic cleanup = %+v", periodic.Continuity)
	}
	if err := owner.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if versions, err := repository.RequiredHMACVersions(ctx); err != nil || len(versions) != 0 {
		t.Fatalf("required versions after cleanup = %v, %v", versions, err)
	}
}

func newTestRunner(t *testing.T, clock Clock, catalog Catalog, continuity ContinuityCleaner, cookies CookieCleaner, ids IDSource, events chan<- Event) *Runner {
	t.Helper()
	interval, _ := NewInterval(time.Hour)
	ticker := &fakeMaintenanceTicker{ticks: make(chan time.Time), stopped: make(chan struct{})}
	runner, err := NewRunner(Config{
		Interval: interval, Clock: clock, Tickers: &fakeTickerFactory{ticker: ticker}, IDs: ids,
		Catalog: catalog, Continuity: continuity, Cookies: cookies, Observer: ObserverFunc(func(event Event) { events <- event }),
	})
	if err != nil {
		t.Fatal(err)
	}
	return runner
}

func awaitMaintenanceEvent(t *testing.T, events <-chan Event) Event {
	t.Helper()
	select {
	case event := <-events:
		return event
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for maintenance event")
		return Event{}
	}
}
