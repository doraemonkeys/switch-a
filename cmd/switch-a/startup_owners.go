package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/providerauth"
	"github.com/doraemonkeys/switch-a/internal/selector"
	"github.com/doraemonkeys/switch-a/internal/server"

	"go.uber.org/zap"
)

// LogStore defines the minimal interface for log cleanup operations.
type LogStore interface {
	CleanOldLogs(ctx context.Context, beforeDays int) error
	GetConfig(ctx context.Context, key string) (string, error)
}

// startLogCleanupLoop gives the cleanup goroutine an explicit owner whose stop
// boundary completes before storage shutdown.
func startLogCleanupLoop(store LogStore, log *zap.Logger) (stop func()) {
	ticker := time.NewTicker(LogCleanupInterval)
	done := make(chan struct{})
	var wg sync.WaitGroup

	wg.Go(func() {
		cleanOldLogs(store, log)
		for {
			select {
			case <-ticker.C:
				cleanOldLogs(store, log)
			case <-done:
				ticker.Stop()
				return
			}
		}
	})

	return func() {
		close(done)
		wg.Wait()
	}
}

// LogCleanupTimeout bounds each cleanup operation independently of process
// startup and shutdown contexts.
const LogCleanupTimeout = 30 * time.Second

func cleanOldLogs(store LogStore, log *zap.Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), LogCleanupTimeout)
	defer cancel()

	retentionDays := DefaultLogRetentionDays
	if val, err := store.GetConfig(ctx, "log_retention_days"); err == nil && val != "" {
		if days, err := strconv.Atoi(val); err == nil && days > 0 {
			retentionDays = days
		}
	}

	if err := store.CleanOldLogs(ctx, retentionDays); err != nil {
		log.Error("failed to clean old logs", zap.Error(err))
	} else {
		log.Debug("cleaned old request logs", zap.Int("retention_days", retentionDays))
	}
}

type lifecycleStep func() error

type applicationActivationStep struct {
	component string
	start     lifecycleStep
	stop      lifecycleStep
}

type applicationActivation struct {
	startupID string
	recorder  applicationLifecycleRecorder
	started   []applicationActivationStep
	stopOnce  sync.Once
	stopErr   error
}

// activateApplicationComponents makes partial activation transactional: if a
// later owner cannot start, every earlier owner is synchronously stopped before
// the composition error is returned and storage can close.
func activateApplicationComponents(
	startupID string,
	recorder applicationLifecycleRecorder,
	steps []applicationActivationStep,
) (*applicationActivation, error) {
	activation := &applicationActivation{startupID: startupID, recorder: recorder}
	for _, step := range steps {
		if step.component == "" || step.start == nil || step.stop == nil {
			return nil, errors.Join(
				fmt.Errorf("activate application component: component, start, and stop are required"),
				activation.Shutdown(),
			)
		}
		if err := step.start(); err != nil {
			return nil, errors.Join(
				fmt.Errorf("start application component %s: %w", step.component, err),
				activation.Shutdown(),
			)
		}
		activation.started = append(activation.started, step)
		recordApplicationComponent(recorder, startupID, startupPhaseBackgroundOwners, step.component)
	}
	return activation, nil
}

func (activation *applicationActivation) Shutdown() error {
	if activation == nil {
		return nil
	}
	activation.stopOnce.Do(func() {
		var errs []error
		for index := len(activation.started) - 1; index >= 0; index-- {
			step := activation.started[index]
			if err := step.stop(); err != nil {
				errs = append(errs, fmt.Errorf("stop application component %s: %w", step.component, err))
			}
			recordApplicationComponent(activation.recorder, activation.startupID, startupPhaseShutdownBackgrounds, step.component)
		}
		activation.stopErr = errors.Join(errs...)
	})
	return activation.stopErr
}

// completeServerLifecycle always drains request producers before stopping the
// statistics worker. This ordering is preserved even when a listener fails,
// because that failure is a reason to begin cleanup rather than skip it.
func completeServerLifecycle(wait, drainServers, stopStatistics lifecycleStep) error {
	waitErr := wait()
	drainErr := drainServers()
	statisticsErr := stopStatistics()
	return errors.Join(waitErr, drainErr, statisticsErr)
}

// startApplicationStickyCache binds cleanup and final persistence flushing to a
// single lifecycle owner, preserving their required shutdown order.
func startApplicationStickyCache(
	stickyCache *selector.PersistentStickyCache,
	log *zap.Logger,
) func() {
	stopCleanup := stickyCache.StartCleanupLoop(StickyCacheCleanupInterval)
	return func() {
		stopCleanup()
		flushCtx, cancel := context.WithTimeout(context.Background(), StickyPersistenceShutdownTimeout)
		defer cancel()
		if err := stickyCache.Close(flushCtx); err != nil {
			log.Warn("failed to flush sticky cache during shutdown", zap.Error(err))
		}
	}
}

func (runtime *applicationRuntime) activateBackgrounds(
	startupID string,
	recorder applicationLifecycleRecorder,
	log *zap.Logger,
) (*applicationActivation, error) {
	var (
		stopHealthCleanup                func()
		stopStickyCleanup                func()
		stopLogCleanup                   func()
		stopVisibleContinuitySeedCleanup func()
		statsWorker                      *ruleStatsWorker
	)
	return activateApplicationComponents(startupID, recorder, []applicationActivationStep{
		{
			component: "codex_maintenance",
			start:     func() error { return startApplicationCodexLifecycle(context.Background(), runtime.codex) },
			stop: func() error {
				stopApplicationCodexLifecycle(runtime.codex, log)
				return nil
			},
		},
		{
			component: "health_cleanup",
			start: func() error {
				stopHealthCleanup = runtime.health.StartCleanupLoop(HealthCleanupInterval, HealthCleanupMaxAge)
				return nil
			},
			stop: func() error { stopHealthCleanup(); return nil },
		},
		{
			component: "sticky_cache",
			start: func() error {
				stopStickyCleanup = startApplicationStickyCache(runtime.sticky, log)
				return nil
			},
			stop: func() error { stopStickyCleanup(); return nil },
		},
		{
			component: "log_cleanup",
			start: func() error {
				stopLogCleanup = startLogCleanupLoop(runtime.store, log)
				return nil
			},
			stop: func() error { stopLogCleanup(); return nil },
		},
		{
			component: "active_registry",
			start:     func() error { runtime.activeRequests.StartCleanup(); return nil },
			stop:      func() error { runtime.activeRequests.StopCleanup(); return nil },
		},
		{
			component: "visible_continuity_seed",
			start: func() error {
				// Matching the sweep cadence to the heuristic TTL avoids a second
				// retention policy while reclaiming seeds that never receive a lookup.
				stopVisibleContinuitySeedCleanup = runtime.continuitySeeds.StartCleanupLoop(model.VisibleContinuitySeedTTL)
				return nil
			},
			stop: func() error { stopVisibleContinuitySeedCleanup(); return nil },
		},
		{
			component: "rule_statistics",
			start: func() error {
				var startErr error
				statsWorker, startErr = startRuleStatsWorker(runtime.errorRules.ruleStatistics, log)
				return startErr
			},
			stop: func() error { return statsWorker.Shutdown() },
		},
	})
}

func startServers(
	proxySrv *server.Server,
	adminSrv *server.AdminServer,
) chan error {
	errCh := make(chan error, 2)
	go func() {
		errCh <- proxySrv.Start()
	}()
	go func() {
		errCh <- adminSrv.Start()
	}()
	return errCh
}

func printServerURLs(proxyPort string, adminPort string) {
	fmt.Println()
	fmt.Println("=========================================")
	fmt.Printf("  Proxy URL:  http://localhost:%s\n", proxyPort)
	fmt.Printf("  Admin URL:  http://localhost:%s/admin\n", adminPort)
	fmt.Println("=========================================")
	fmt.Println()
}

func waitForShutdown(errCh <-chan error, log *zap.Logger) error {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	select {
	case err := <-errCh:
		return joinServerErrors(err, errCh)
	case sig := <-sigCh:
		log.Info("received signal, shutting down", zap.String("signal", sig.String()))
		return nil
	}
}

func joinServerErrors(first error, errCh <-chan error) error {
	// Drain already-queued startup failures so callers see the full picture when
	// multiple listeners fail at nearly the same time.
	errs := []error{first}
	for {
		select {
		case err := <-errCh:
			errs = append(errs, err)
		default:
			return errors.Join(errs...)
		}
	}
}

func shutdownServers(
	proxySrv *server.Server,
	adminSrv *server.AdminServer,
	authService *providerauth.Service,
) error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), ShutdownTimeout)
	defer cancel()

	var errs []error
	if err := proxySrv.Shutdown(shutdownCtx); err != nil {
		errs = append(errs, fmt.Errorf("proxy server shutdown error: %w", err))
	}
	if err := adminSrv.Shutdown(shutdownCtx); err != nil {
		errs = append(errs, fmt.Errorf("admin server shutdown error: %w", err))
	}
	if err := authService.Shutdown(shutdownCtx); err != nil {
		errs = append(errs, fmt.Errorf("oauth callback server shutdown error: %w", err))
	}
	return errors.Join(errs...)
}
