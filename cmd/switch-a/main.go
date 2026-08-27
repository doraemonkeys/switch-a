// Package main is the entry point for switch-a.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/store"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

func main() {
	if isVersionRequest(os.Args[1:]) {
		if err := writeVersion(os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "error: write version: %v\n", err)
			os.Exit(ExitCodeError)
		}
		return
	}

	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(ExitCodeError)
	}
}

func run() error {
	return runApplication(nil)
}

func runApplication(recorder applicationLifecycleRecorder) error {
	startupID := uuid.NewString()
	cfg, resolvedKeyringPath, err := loadApplicationConfiguration(startupID, recorder)
	if err != nil {
		return err
	}

	log := newApplicationLogger(cfg, startupID, recorder)
	defer func() { _ = log.Sync() }()

	clock := internal.RealClock{}
	sqlStore, err := openApplicationStore(cfg.DBPath, clock, func(report store.RequestLogTimestampMigrationReport) {
		logRequestLogTimestampMigration(log, report)
	})
	if err != nil {
		logCodexStartupFailure(log, startupID, startupPhaseDatabase, err)
		return err
	}
	defer func() {
		if closeErr := sqlStore.Close(); closeErr != nil {
			log.Error("failed to close application stores", zap.Error(closeErr))
		}
		recordApplicationComponent(recorder, startupID, startupPhaseShutdownStorage, "writer")
	}()
	recordApplicationLifecycle(recorder, startupID, startupPhaseDatabase)
	if err := sqlStore.InitDefaultConfig(context.Background()); err != nil {
		wrapped := fmt.Errorf("failed to initialize default config: %w", err)
		logCodexStartupFailure(log, startupID, startupPhaseDefaults, wrapped)
		return wrapped
	}
	recordApplicationLifecycle(recorder, startupID, startupPhaseDefaults)

	codexSecurity, err := bootstrapApplicationCodexSecurityWithOS(
		context.Background(), startupID, resolvedKeyringPath, sqlStore, log, recorder,
	)
	if err != nil {
		return err
	}

	analyticsRuntime, err := newApplicationAnalytics(cfg.DBPath, clock, log)
	if err != nil {
		logCodexStartupFailure(log, startupID, startupPhaseComposition, err)
		return err
	}
	defer func() {
		if closeErr := analyticsRuntime.Close(); closeErr != nil {
			log.Error("failed to close analytics store", zap.Error(closeErr))
		}
		recordApplicationComponent(recorder, startupID, startupPhaseShutdownStorage, "analytics")
	}()
	st := store.NewCachedStore(store.CachedStoreConfig{Store: sqlStore})

	runtime, err := composeApplicationRuntime(cfg, clock, log, sqlStore, st, codexSecurity, analyticsRuntime)
	if err != nil {
		logCodexStartupFailure(log, startupID, startupPhaseComposition, err)
		return err
	}
	// The capture manager must outlive both HTTP servers because in-flight proxy
	// recorders and export downloads can retain leases during graceful shutdown.
	defer func() {
		if closeErr := runtime.captures.Close(); closeErr != nil {
			log.Error("failed to close request capture manager", zap.Error(closeErr))
		}
	}()
	recordApplicationLifecycle(recorder, startupID, startupPhaseComposition)

	activation, err := runtime.activateBackgrounds(startupID, recorder, log)
	if err != nil {
		logCodexStartupFailure(log, startupID, startupPhaseBackgroundOwners, err)
		return err
	}
	defer func() {
		if stopErr := activation.Shutdown(); stopErr != nil {
			log.Error("failed to stop application background owners", zap.Error(stopErr))
		}
	}()

	errCh := startServers(runtime.proxyServer, runtime.adminServer)
	recordApplicationLifecycle(recorder, startupID, startupPhaseListeners)
	printServerURLs(cfg.Port, cfg.AdminPort)
	if err := completeServerLifecycle(
		func() error { return waitForShutdown(errCh, log) },
		func() error {
			err := shutdownServers(runtime.proxyServer, runtime.adminServer, runtime.auth)
			recordApplicationLifecycle(recorder, startupID, startupPhaseShutdownListeners)
			return err
		},
		activation.Shutdown,
	); err != nil {
		return err
	}

	log.Info("switch-a stopped")
	return nil
}
