package main

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	adminerrorruleapi "github.com/doraemonkeys/switch-a/internal/admin/errorruleapi"
	"github.com/doraemonkeys/switch-a/internal/errorrule"
	errorrulesqlite "github.com/doraemonkeys/switch-a/internal/errorrule/sqlite"
	"github.com/doraemonkeys/switch-a/internal/errorrule/statistics"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis"
	"github.com/doraemonkeys/switch-a/internal/store"
	"github.com/glebarez/sqlite"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

const expectedResponseProbeProcessBudget = 64 << 20

func TestNewInternalErrorRuntimeBuildsSingletonGraph(t *testing.T) {
	sqlStore, err := openApplicationStore(filepath.Join(t.TempDir(), "runtime.db"), internal.RealClock{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlStore.Close() })
	cachedStore := store.NewCachedStore(store.CachedStoreConfig{Store: sqlStore})
	repository := cachedStore.InternalErrorRuleRepository()

	runtime, err := newInternalErrorRuntime(repository, cachedStore, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	if runtime.ruleRepository != repository {
		t.Fatal("runtime did not retain the loaded repository as its rule-set provider")
	}
	if runtime.processBudget == nil || runtime.processBudget.Limit() != expectedResponseProbeProcessBudget {
		t.Fatalf("process budget = %v, want %d bytes", runtime.processBudget, expectedResponseProbeProcessBudget)
	}
	if runtime.processBudget.Limit() != responseanalysis.ResponseProbeMemoryBudget {
		t.Fatalf("process budget = %d, contract = %d", runtime.processBudget.Limit(), responseanalysis.ResponseProbeMemoryBudget)
	}
	if _, ok := runtime.scheduler.(*responseanalysis.RealScheduler); !ok {
		t.Fatalf("scheduler type = %T, want *responseanalysis.RealScheduler", runtime.scheduler)
	}
	if runtime.responseAnalyzer == nil || runtime.ruleStatistics == nil || runtime.adminHandler == nil {
		t.Fatalf("runtime graph is incomplete: %#v", runtime)
	}
	if err := repository.BindStatsGenerationRetirer(runtime.ruleStatistics.Retire); !errors.Is(err, errorrulesqlite.ErrStatsRetirerBound) {
		t.Fatalf("second stats retirement binding error = %v, want %v", err, errorrulesqlite.ErrStatsRetirerBound)
	}
}

func TestNewInternalErrorRuntimeRejectsIncompleteOrReusedGraph(t *testing.T) {
	sqlStore, err := openApplicationStore(filepath.Join(t.TempDir(), "invalid-graph.db"), internal.RealClock{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlStore.Close() })
	cachedStore := store.NewCachedStore(store.CachedStoreConfig{Store: sqlStore})
	repository := cachedStore.InternalErrorRuleRepository()

	for _, test := range []struct {
		name       string
		repository *errorrulesqlite.Repository
		providers  adminerrorruleapi.ProviderCatalog
		logger     *zap.Logger
		message    string
	}{
		{name: "repository", providers: cachedStore, logger: zap.NewNop(), message: "repository is required"},
		{name: "providers", repository: repository, logger: zap.NewNop(), message: "provider catalog is required"},
		{name: "logger", repository: repository, providers: cachedStore, message: "logger is required"},
		{name: "compiled snapshot", repository: &errorrulesqlite.Repository{}, providers: cachedStore, logger: zap.NewNop(), message: "no compiled snapshot"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := newInternalErrorRuntime(test.repository, test.providers, test.logger)
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("construction error = %v, want message containing %q", err, test.message)
			}
		})
	}

	if _, err := newInternalErrorRuntime(repository, cachedStore, zap.NewNop()); err != nil {
		t.Fatal(err)
	}
	if _, err := newInternalErrorRuntime(repository, cachedStore, zap.NewNop()); !errors.Is(err, errorrulesqlite.ErrStatsRetirerBound) {
		t.Fatalf("reused graph error = %v, want %v", err, errorrulesqlite.ErrStatsRetirerBound)
	}
}

func TestInternalErrorRuntimeShutdownFlushesBeforeRestart(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "restart.db")
	initialStore, err := openApplicationStore(databasePath, internal.RealClock{})
	if err != nil {
		t.Fatal(err)
	}
	initialClosed := false
	t.Cleanup(func() {
		if !initialClosed {
			_ = initialStore.Close()
		}
	})

	created, err := initialStore.InternalErrorRuleRepository().CreateRule(
		context.Background(),
		0,
		c4PassthroughRuleSpec(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(created.Rules) != 1 {
		t.Fatalf("created rules = %d, want 1", len(created.Rules))
	}
	initialCachedStore := store.NewCachedStore(store.CachedStoreConfig{Store: initialStore})
	runtime, err := newInternalErrorRuntime(
		initialCachedStore.InternalErrorRuleRepository(),
		initialCachedStore,
		zap.NewNop(),
	)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := startRuleStatsWorker(runtime.ruleStatistics, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.ruleStatistics.Hit(statistics.HandleFor(created.Rules[0]), time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := worker.Shutdown(); err != nil {
		t.Fatal(err)
	}
	if err := worker.Shutdown(); err != nil {
		t.Fatalf("idempotent worker shutdown failed: %v", err)
	}
	if err := initialStore.Close(); err != nil {
		t.Fatal(err)
	}
	initialClosed = true

	restartedStore, err := openApplicationStore(databasePath, internal.RealClock{})
	if err != nil {
		t.Fatalf("restart failed: %v", err)
	}
	t.Cleanup(func() { _ = restartedStore.Close() })
	restartedCachedStore := store.NewCachedStore(store.CachedStoreConfig{Store: restartedStore})
	restartedRuntime, err := newInternalErrorRuntime(
		restartedCachedStore.InternalErrorRuleRepository(),
		restartedCachedStore,
		zap.NewNop(),
	)
	if err != nil {
		t.Fatalf("rebuild runtime after restart: %v", err)
	}
	revision, persisted, err := restartedRuntime.ruleRepository.ListStatsSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if revision != created.Revision || len(persisted) != 1 || persisted[0].HitCount != 1 {
		t.Fatalf("restarted stats revision=%s stats=%#v", revision.String(), persisted)
	}
}

func TestOpenApplicationStoreRejectsInvalidPersistedRules(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "invalid-rule.db")
	initialStore, err := openApplicationStore(databasePath, internal.RealClock{})
	if err != nil {
		t.Fatal(err)
	}
	initialClosed := false
	t.Cleanup(func() {
		if !initialClosed {
			_ = initialStore.Close()
		}
	})
	if _, err := initialStore.InternalErrorRuleRepository().CreateRule(
		context.Background(),
		0,
		c4PassthroughRuleSpec(),
	); err != nil {
		t.Fatal(err)
	}
	if err := initialStore.Close(); err != nil {
		t.Fatal(err)
	}
	initialClosed = true

	db, err := gorm.Open(sqlite.Open(databasePath), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	databaseClosed := false
	t.Cleanup(func() {
		if !databaseClosed {
			_ = sqlDB.Close()
		}
	})
	if err := db.Exec("UPDATE internal_error_rules SET keywords_json = 'not-json'").Error; err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
	databaseClosed = true

	invalidStore, err := openApplicationStore(databasePath, internal.RealClock{})
	if invalidStore != nil {
		_ = invalidStore.Close()
		t.Fatal("invalid persisted rules unexpectedly produced an application store")
	}
	if err == nil || !strings.Contains(err.Error(), "load internal-error rule set") {
		t.Fatalf("startup error = %v, want compiled rule-set load failure", err)
	}
}

func TestCompleteServerLifecyclePreservesDrainAndFlushOrder(t *testing.T) {
	waitErr := errors.New("listener failed")
	drainErr := errors.New("server drain failed")
	flushErr := errors.New("final flush failed")
	var order []string

	err := completeServerLifecycle(
		func() error {
			order = append(order, "wait")
			return waitErr
		},
		func() error {
			order = append(order, "drain")
			return drainErr
		},
		func() error {
			order = append(order, "flush")
			return flushErr
		},
	)
	if want := []string{"wait", "drain", "flush"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("lifecycle order = %v, want %v", order, want)
	}
	for _, target := range []error{waitErr, drainErr, flushErr} {
		if !errors.Is(err, target) {
			t.Fatalf("lifecycle error %v does not include %v", err, target)
		}
	}
}

type countingRuleStatsRunner struct {
	started       chan struct{}
	finalizations atomic.Int32
	result        error
}

func (r *countingRuleStatsRunner) Run(ctx context.Context) error {
	close(r.started)
	<-ctx.Done()
	r.finalizations.Add(1)
	return r.result
}

func TestRuleStatsWorkerOwnsExactlyOneShutdown(t *testing.T) {
	runnerErr := errors.New("flush unavailable")
	runner := &countingRuleStatsRunner{started: make(chan struct{}), result: runnerErr}
	worker, err := startRuleStatsWorker(runner, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	<-runner.started

	const shutdownCallers = 16
	var callers sync.WaitGroup
	errorsSeen := make(chan error, shutdownCallers)
	for range shutdownCallers {
		callers.Go(func() { errorsSeen <- worker.Shutdown() })
	}
	callers.Wait()
	close(errorsSeen)
	for shutdownErr := range errorsSeen {
		if !errors.Is(shutdownErr, runnerErr) {
			t.Fatalf("shutdown error = %v, want %v", shutdownErr, runnerErr)
		}
	}
	if got := runner.finalizations.Load(); got != 1 {
		t.Fatalf("worker finalizations = %d, want 1", got)
	}
}

func TestRuleStatsWorkerRejectsMissingDependencies(t *testing.T) {
	if _, err := startRuleStatsWorker(nil, zap.NewNop()); err == nil {
		t.Fatal("nil statistics runner unexpectedly accepted")
	}
	runner := &countingRuleStatsRunner{started: make(chan struct{})}
	if _, err := startRuleStatsWorker(runner, nil); err == nil {
		t.Fatal("nil statistics logger unexpectedly accepted")
	}
	var worker *ruleStatsWorker
	if err := worker.Shutdown(); err != nil {
		t.Fatalf("nil worker shutdown error = %v", err)
	}
}

func c4PassthroughRuleSpec() errorrule.RuleSpec {
	return errorrule.RuleSpec{
		Name:      "restart rule",
		Enabled:   true,
		Target:    errorrule.NewGlobalTarget(),
		Keywords:  []string{"capacity"},
		MatchMode: errorrule.MatchAny,
		Action:    errorrule.NewPassthroughAction(),
	}
}
