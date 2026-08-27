package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/config"
	"github.com/doraemonkeys/switch-a/internal/requestcapture"
	"github.com/doraemonkeys/switch-a/internal/store"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestComposeApplicationRuntimeBuildsAndOwnsOneProcessGraph(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "application.db")
	clock := internal.RealClock{}
	log := zap.NewNop()
	sqlStore, err := openApplicationStore(databasePath, clock, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlStore.Close() })

	security := testApplicationCodexSecurity(t, 31)
	if err := sqlStore.FinalizeStaticCredentialSubjects(context.Background(), security.keyring); err != nil {
		t.Fatal(err)
	}
	analytics, err := newApplicationAnalytics(databasePath, clock, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = analytics.Close() })

	cachedStore := store.NewCachedStore(store.CachedStoreConfig{Store: sqlStore})
	cfg := startupCompositionTestConfig()

	// A failed security preflight must not bind the process-wide error runtime;
	// the same storage graph remains valid for the corrected composition.
	failedRuntime, err := composeApplicationRuntime(cfg, clock, log, sqlStore, cachedStore, nil, analytics)
	if failedRuntime != nil || err == nil || !strings.Contains(err.Error(), "initialize Codex runtime") {
		t.Fatalf("failed composition runtime = %#v, error = %v", failedRuntime, err)
	}

	runtime, err := composeApplicationRuntime(cfg, clock, log, sqlStore, cachedStore, security, analytics)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.captures.Close() })

	if runtime.store != cachedStore ||
		runtime.errorRules.ruleRepository != cachedStore.InternalErrorRuleRepository() ||
		runtime.codex == nil || runtime.codex.HTTP == nil || runtime.codex.WebSocket == nil ||
		runtime.codex.maintenance == nil || runtime.health == nil || runtime.sticky == nil ||
		runtime.activeRequests == nil || runtime.continuitySeeds == nil || runtime.auth == nil ||
		runtime.proxyServer == nil || runtime.adminServer == nil {
		t.Fatalf("composed runtime did not preserve the singleton dependency graph: %#v", runtime)
	}
	if got := runtime.captures.Status().ProcessMemory.CeilingBytes; got != cfg.DebugCaptureMemoryCeilingBytes {
		t.Fatalf("capture process ceiling = %d, want %d", got, cfg.DebugCaptureMemoryCeilingBytes)
	}

	var events []applicationLifecycleEvent
	activation, err := runtime.activateBackgrounds(
		"startup-composition",
		applicationLifecycleRecorderFunc(func(event applicationLifecycleEvent) {
			events = append(events, event)
		}),
		log,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = activation.Shutdown() })
	if err := activation.Shutdown(); err != nil {
		t.Fatal(err)
	}
	if err := activation.Shutdown(); err != nil {
		t.Fatalf("idempotent background shutdown failed: %v", err)
	}

	started := []string{
		"codex_maintenance",
		"health_cleanup",
		"sticky_cache",
		"log_cleanup",
		"active_registry",
		"visible_continuity_seed",
		"rule_statistics",
	}
	wantEvents := make([]applicationLifecycleEvent, 0, len(started)*2)
	for _, component := range started {
		wantEvents = append(wantEvents, applicationLifecycleEvent{
			StartupID: "startup-composition",
			Phase:     startupPhaseBackgroundOwners,
			Component: component,
		})
	}
	for index := len(started) - 1; index >= 0; index-- {
		wantEvents = append(wantEvents, applicationLifecycleEvent{
			StartupID: "startup-composition",
			Phase:     startupPhaseShutdownBackgrounds,
			Component: started[index],
		})
	}
	if !reflect.DeepEqual(events, wantEvents) {
		t.Fatalf("background lifecycle events = %+v, want %+v", events, wantEvents)
	}
}

func TestNewApplicationAnalyticsRejectsUnopenableDatabase(t *testing.T) {
	notDirectory := filepath.Join(t.TempDir(), "occupied")
	if err := os.WriteFile(notDirectory, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	runtime, err := newApplicationAnalytics(
		filepath.Join(notDirectory, "analytics.db"),
		internal.RealClock{},
		zap.NewNop(),
	)
	if runtime != nil || err == nil || !strings.Contains(err.Error(), "failed to initialize token analytics repository") {
		t.Fatalf("analytics runtime = %#v, error = %v", runtime, err)
	}
}

func TestLogApplicationStartupEmitsConfigurationAndBuildContext(t *testing.T) {
	core, observed := observer.New(zapcore.DebugLevel)
	cfg := &config.Config{
		ConfigFileUsed: "C:/switch-a/config.yaml",
		Port:           "8181",
		AdminPort:      "9191",
		LogPath:        "C:/switch-a/logs",
		LogLevel:       "debug",
	}

	logApplicationStartup(zap.New(core), cfg)

	loaded := observed.FilterMessage("loaded config file").All()
	if len(loaded) != 1 || loaded[0].ContextMap()["path"] != cfg.ConfigFileUsed {
		t.Fatalf("config load trace = %+v", loaded)
	}
	started := observed.FilterMessage("starting switch-a").All()
	if len(started) != 1 {
		t.Fatalf("startup trace = %+v", started)
	}
	fields := started[0].ContextMap()
	for key, want := range map[string]string{
		"proxy_port": cfg.Port,
		"admin_port": cfg.AdminPort,
		"log_path":   cfg.LogPath,
		"log_level":  cfg.LogLevel,
	} {
		if fields[key] != want {
			t.Fatalf("startup trace field %s = %v, want %q", key, fields[key], want)
		}
	}
	for _, required := range []string{"version", "commit", "built_at"} {
		if _, exists := fields[required]; !exists {
			t.Fatalf("startup trace is missing build field %q: %+v", required, fields)
		}
	}
}

func startupCompositionTestConfig() *config.Config {
	return &config.Config{
		Port:                               "0",
		AdminPort:                          "0",
		DebugCaptureMemoryCeilingBytes:     600 << 20,
		DebugCaptureMaxActiveRecords:       requestcapture.DefaultMaxActiveRecords,
		DebugCaptureMaxActiveTraces:        requestcapture.DefaultMaxActiveTraces,
		DebugCaptureMaxTransitionsPerTrace: requestcapture.DefaultMaxTransitionsPerTrace,
		DebugCaptureMaxPendingExports:      requestcapture.DefaultMaxPendingExports,
		DebugCaptureMaxConcurrentDownloads: requestcapture.DefaultMaxActiveDownloads,
		DebugCaptureDetailPreviewBytes:     requestcapture.DefaultPreviewBytes,
		DebugCaptureDetailEventLimit:       requestcapture.DefaultDetailEventLimit,
		DebugCaptureDownloadTokenTTL:       requestcapture.DefaultDownloadTokenTTL,
		DebugCaptureMaxRecordsPerProvider:  requestcapture.DefaultMaxRecordsPerProvider,
		DebugCaptureChunkBytes:             requestcapture.DefaultChunkBytes,
		DebugCaptureExportLineBytes:        requestcapture.DefaultExportLineBytes,
	}
}

func TestApplicationActivationRejectsIncompleteOwnerAfterSynchronousRollback(t *testing.T) {
	rollbackErr := errors.New("first owner failed to stop")
	var calls []string
	var events []applicationLifecycleEvent
	recorder := applicationLifecycleRecorderFunc(func(event applicationLifecycleEvent) {
		events = append(events, event)
	})

	activation, err := activateApplicationComponents(
		"startup-invalid-owner",
		recorder,
		[]applicationActivationStep{
			{
				component: "first",
				start:     func() error { calls = append(calls, "start:first"); return nil },
				stop:      func() error { calls = append(calls, "stop:first"); return rollbackErr },
			},
			{
				component: "incomplete",
				start:     func() error { calls = append(calls, "start:incomplete"); return nil },
			},
		},
	)
	if activation != nil || err == nil ||
		!strings.Contains(err.Error(), "component, start, and stop are required") ||
		!errors.Is(err, rollbackErr) {
		t.Fatalf("activation = %#v, error = %v", activation, err)
	}
	if want := []string{"start:first", "stop:first"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("activation calls = %v, want %v", calls, want)
	}
	wantEvents := []applicationLifecycleEvent{
		{StartupID: "startup-invalid-owner", Phase: startupPhaseBackgroundOwners, Component: "first"},
		{StartupID: "startup-invalid-owner", Phase: startupPhaseShutdownBackgrounds, Component: "first"},
	}
	if !reflect.DeepEqual(events, wantEvents) {
		t.Fatalf("rollback events = %+v, want %+v", events, wantEvents)
	}
}
