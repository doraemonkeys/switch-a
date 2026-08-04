// Package main is the entry point for switch-a.
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

	"github.com/doraemonkeys/switch-a/internal"
	adminerrorruleapi "github.com/doraemonkeys/switch-a/internal/admin/errorruleapi"
	"github.com/doraemonkeys/switch-a/internal/buildinfo"
	"github.com/doraemonkeys/switch-a/internal/config"
	"github.com/doraemonkeys/switch-a/internal/errorrule"
	errorrulesqlite "github.com/doraemonkeys/switch-a/internal/errorrule/sqlite"
	"github.com/doraemonkeys/switch-a/internal/errorrule/statistics"
	"github.com/doraemonkeys/switch-a/internal/health"
	"github.com/doraemonkeys/switch-a/internal/logger"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/providerauth"
	"github.com/doraemonkeys/switch-a/internal/proxy"
	"github.com/doraemonkeys/switch-a/internal/requestcapture"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis"
	"github.com/doraemonkeys/switch-a/internal/selector"
	"github.com/doraemonkeys/switch-a/internal/server"
	"github.com/doraemonkeys/switch-a/internal/store"

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

// LogStore defines the minimal interface for log cleanup operations.
type LogStore interface {
	CleanOldLogs(ctx context.Context, beforeDays int) error
	GetConfig(ctx context.Context, key string) (string, error)
}

// startLogCleanupLoop starts a background goroutine that periodically cleans up
// old request logs to prevent the request_logs table from growing indefinitely.
// Returns a stop function to terminate the cleanup loop.
//
// Note: A fresh context with timeout is created for each cleanup operation rather
// than storing the startup context. This follows Go best practices where contexts
// should be passed through call chains, not stored for later use.
//
// The stop function waits for the cleanup goroutine to fully exit before returning,
// ensuring no database operations occur after the store is closed.
func startLogCleanupLoop(store LogStore, log *zap.Logger) (stop func()) {
	ticker := time.NewTicker(LogCleanupInterval)
	done := make(chan struct{})
	var wg sync.WaitGroup

	wg.Go(func() {
		// Run initial cleanup on startup with fresh context
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
		wg.Wait() // Wait for goroutine to fully exit before returning
	}
}

// LogCleanupTimeout is the maximum time allowed for a single log cleanup operation.
const LogCleanupTimeout = 30 * time.Second

// cleanOldLogs performs the actual log cleanup, reading retention days from config.
// Creates a fresh context with timeout for each cleanup operation.
func cleanOldLogs(store LogStore, log *zap.Logger) {
	// Create a fresh context with timeout for this cleanup operation
	ctx, cancel := context.WithTimeout(context.Background(), LogCleanupTimeout)
	defer cancel()

	// Get retention days from config, default to 7
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

// internalErrorRuntime holds the single process-wide instances shared by the
// request path and the administrative control plane. Keeping this graph here
// prevents either HTTP server from silently constructing a private budget,
// analyzer, repository view, or statistics accumulator.
type internalErrorRuntime struct {
	ruleRepository   *errorrulesqlite.Repository
	processBudget    *responseanalysis.ProcessMemoryBudget
	scheduler        responseanalysis.Scheduler
	responseAnalyzer *responseanalysis.Analyzer
	ruleStatistics   *statistics.Accumulator
	adminHandler     *adminerrorruleapi.Handler
}

func newInternalErrorRuntime(
	repository *errorrulesqlite.Repository,
	providers adminerrorruleapi.ProviderCatalog,
	log *zap.Logger,
) (*internalErrorRuntime, error) {
	if repository == nil {
		return nil, fmt.Errorf("internal-error rule repository is required")
	}
	if providers == nil {
		return nil, fmt.Errorf("internal-error provider catalog is required")
	}
	if log == nil {
		return nil, fmt.Errorf("internal-error runtime logger is required")
	}
	snapshot := repository.CurrentRuleSet()
	if snapshot == nil {
		return nil, fmt.Errorf("internal-error rule repository has no compiled snapshot")
	}

	accumulator, err := statistics.New(repository)
	if err != nil {
		return nil, fmt.Errorf("initialize internal-error rule statistics: %w", err)
	}
	budget, err := responseanalysis.NewDefaultProcessMemoryBudget()
	if err != nil {
		return nil, fmt.Errorf("initialize response-analysis process budget: %w", err)
	}
	// One scheduler instance owns every probe timer so test and production
	// construction cannot diverge into per-handler timing domains.
	scheduler := &responseanalysis.RealScheduler{}
	analyzer, err := responseanalysis.NewAnalyzer(
		responseanalysis.NewRegistry(),
		budget,
		responseanalysis.AnalyzerOptions{Scheduler: scheduler},
	)
	if err != nil {
		return nil, fmt.Errorf("initialize response analyzer: %w", err)
	}
	adminHandler, err := adminerrorruleapi.NewHandler(adminerrorruleapi.Config{
		Rules:        repository,
		Stats:        repository,
		StatsOverlay: accumulator,
		Providers:    providers,
		Analyzer:     adminerrorruleapi.NewRegistryAnalyzer(),
		Logger:       log,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize internal-error admin API: %w", err)
	}
	// Bind only after every fallible dependent is ready. A failed construction
	// must not leave a reusable repository half-bound to discarded state.
	if err := repository.BindStatsGenerationRetirer(accumulator.Retire); err != nil {
		return nil, fmt.Errorf("bind internal-error statistics retirement: %w", err)
	}

	log.Info("initialized internal-error runtime",
		zap.String("rule_set_revision", snapshot.Revision().String()),
		zap.Int("rule_count", len(snapshot.Rules())),
		zap.Int("response_probe_memory_budget_bytes", budget.Limit()),
	)
	return &internalErrorRuntime{
		ruleRepository:   repository,
		processBudget:    budget,
		scheduler:        scheduler,
		responseAnalyzer: analyzer,
		ruleStatistics:   accumulator,
		adminHandler:     adminHandler,
	}, nil
}

type ruleStatsRunner interface {
	Run(context.Context) error
}

// ruleStatsWorker gives cancellation and the final flush one idempotent owner.
// The worker stores only a cancel function; the execution context remains local
// to the goroutine that consumes it.
type ruleStatsWorker struct {
	cancel   context.CancelFunc
	done     <-chan error
	logger   *zap.Logger
	workerID string
	stopOnce sync.Once
	stopErr  error
}

func startRuleStatsWorker(runner ruleStatsRunner, log *zap.Logger) (*ruleStatsWorker, error) {
	if runner == nil {
		return nil, fmt.Errorf("internal-error statistics runner is required")
	}
	if log == nil {
		return nil, fmt.Errorf("internal-error statistics logger is required")
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	worker := &ruleStatsWorker{
		cancel: cancel, done: done, logger: log,
		workerID: errorrule.UUIDGenerator{}.NewID(),
	}
	log.Info("starting internal-error statistics worker",
		zap.String("stats_worker_id", worker.workerID),
		zap.Duration("flush_interval", statistics.StatsFlushInterval),
		zap.Duration("shutdown_timeout", statistics.StatsShutdownTimeout),
	)
	go func() {
		done <- runner.Run(ctx)
		close(done)
	}()
	return worker, nil
}

func (w *ruleStatsWorker) Shutdown() error {
	if w == nil {
		return nil
	}
	w.stopOnce.Do(func() {
		w.logger.Info("stopping internal-error statistics worker",
			zap.String("stats_worker_id", w.workerID),
		)
		w.cancel()
		if err := <-w.done; err != nil {
			w.stopErr = fmt.Errorf("final internal-error statistics flush: %w", err)
			w.logger.Error("internal-error statistics worker stopped with an error",
				zap.String("stats_worker_id", w.workerID),
				zap.Error(err),
			)
			return
		}
		w.logger.Info("internal-error statistics worker stopped",
			zap.String("stats_worker_id", w.workerID),
			zap.String("shutdown_result", "final_flush_completed"),
		)
	})
	return w.stopErr
}

type lifecycleStep func() error

// completeServerLifecycle always drains request producers before stopping the
// statistics worker. This ordering is preserved even when a listener fails,
// because that failure is a reason to begin cleanup rather than skip it.
func completeServerLifecycle(wait, drainServers, stopStatistics lifecycleStep) error {
	waitErr := wait()
	drainErr := drainServers()
	statisticsErr := stopStatistics()
	return errors.Join(waitErr, drainErr, statisticsErr)
}

func openApplicationStore(dbPath string, clock internal.Clock) (*store.SQLiteStore, error) {
	sqlStore, err := store.NewSQLiteStore(dbPath, clock)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize store: %w", err)
	}
	return sqlStore, nil
}

// startApplicationStickyCache owns both sticky background loops so callers
// cannot accidentally close SQLite before the final write-behind flush.
func startApplicationStickyCache(
	persistence selector.StickyPersistence,
	clock internal.Clock,
	log *zap.Logger,
) (*selector.PersistentStickyCache, func()) {
	stickyCache := selector.NewPersistentStickyCache(persistence, clock, log)
	stopCleanup := stickyCache.StartCleanupLoop(StickyCacheCleanupInterval)
	return stickyCache, func() {
		stopCleanup()
		flushCtx, cancel := context.WithTimeout(context.Background(), StickyPersistenceShutdownTimeout)
		defer cancel()
		if err := stickyCache.Close(flushCtx); err != nil {
			log.Warn("failed to flush sticky cache during shutdown", zap.Error(err))
		}
	}
}

func run() error {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// Initialize logger
	log := logger.New(logger.Config{
		LogPath:     cfg.LogPath,
		MaxSizeMB:   cfg.LogMaxSizeMB,
		MaxKeepDays: cfg.LogMaxKeepDays,
		Level:       cfg.LogLevel,
	})
	defer func() { _ = log.Sync() }()

	// Log which config file was loaded (if any)
	if cfg.ConfigFileUsed != "" {
		log.Info("loaded config file", zap.String("path", cfg.ConfigFileUsed))
	}

	build := buildinfo.Current()
	log.Info("starting switch-a",
		zap.String("version", build.Version),
		zap.String("commit", build.Commit),
		zap.String("built_at", build.BuiltAt),
		zap.String("proxy_port", cfg.Port),
		zap.String("admin_port", cfg.AdminPort),
		zap.String("log_path", cfg.LogPath),
		zap.String("log_level", cfg.LogLevel),
	)

	// The store migration loads and compiles persisted rules before any HTTP or
	// background work starts, so an invalid durable rule makes startup atomic.
	clock := internal.RealClock{}
	sqlStore, err := openApplicationStore(cfg.DBPath, clock)
	if err != nil {
		return err
	}

	// Wrap with caching layer to reduce database pressure for config reads.
	// Each proxy request reads 10+ config values; caching prevents database
	// overload under high QPS while allowing config changes to propagate quickly.
	st := store.NewCachedStore(store.CachedStoreConfig{
		Store: sqlStore,
		// Default TTL of 5 seconds balances responsiveness with performance
	})
	defer func() { _ = sqlStore.Close() }()

	// Initialize default configuration
	ctx := context.Background()
	if err := sqlStore.InitDefaultConfig(ctx); err != nil {
		return fmt.Errorf("failed to initialize default config: %w", err)
	}

	errorRuntime, err := newInternalErrorRuntime(st.InternalErrorRuleRepository(), st, log)
	if err != nil {
		return err
	}

	captureManager, err := newCaptureManager(cfg, clock, log)
	if err != nil {
		return err
	}
	// The capture manager must outlive both HTTP servers because in-flight proxy
	// recorders and export downloads can retain leases during graceful shutdown.
	defer func() {
		if closeErr := captureManager.Close(); closeErr != nil {
			log.Error("failed to close request capture manager", zap.Error(closeErr))
		}
	}()

	// Initialize health manager for circuit breaker and availability tracking
	healthMgr := health.NewManager(health.Config{
		Store:  st,
		Clock:  clock,
		Logger: log,
	})
	// Start cleanup loop to prevent memory growth from old failure records
	stopHealthCleanup := healthMgr.StartCleanupLoop(HealthCleanupInterval, HealthCleanupMaxAge)
	defer stopHealthCleanup()

	// Initialize sticky cache for session affinity. The memory layer serves the
	// hot path while SQLite restores bindings after restart and mirrors mutations
	// on a best-effort basis.
	stickyCache, stopStickyCache := startApplicationStickyCache(sqlStore, clock, log)
	defer stopStickyCache()

	// Start log cleanup loop to prevent request_logs table from growing indefinitely
	stopLogCleanup := startLogCleanupLoop(st, log)
	defer stopLogCleanup()

	// Initialize concurrency limiter for per-provider request limits
	limiter := selector.NewConcurrencyLimiter()

	// The registry owns exact generation-bound leases. A provider-ID removal hook
	// could release capacity from a recreated provider rather than this request's
	// original lifecycle generation.
	activeRegistry := proxy.NewActiveRequestRegistry()
	activeRegistry.StartCleanup()
	defer activeRegistry.StopCleanup()

	visibleContinuitySeedStore := proxy.NewVisibleContinuitySeedStore()
	// Matching the sweep cadence to the heuristic TTL avoids a second retention
	// policy while still reclaiming seeds that would otherwise sit until a lookup.
	stopVisibleContinuitySeedCleanup := visibleContinuitySeedStore.StartCleanupLoop(model.VisibleContinuitySeedTTL)
	defer stopVisibleContinuitySeedCleanup()

	// Initialize selector for provider selection with all features:
	// - Health checks
	// - Sticky sessions
	// - Concurrency limits
	// - Group-based strategies (priority, weight, random)
	sel := selector.NewSelector(selector.Config{
		Store:         st,
		HealthChecker: healthMgr,
		StickyCache:   stickyCache,
		Limiter:       limiter,
		Clock:         clock,
		Logger:        log,
	})

	authService := providerauth.NewService(providerauth.Config{
		CredentialStore: st,
		Clock:           clock,
		Logger:          log,
	})

	// Create proxy HTTP server (public port)
	proxySrv := server.New(server.Config{
		Port:                       cfg.Port,
		Logger:                     log,
		Store:                      st,
		Health:                     healthMgr,
		Selector:                   sel,
		ActiveRegistry:             activeRegistry,
		VisibleContinuitySeedStore: visibleContinuitySeedStore,
		Auth:                       authService,
		Capture:                    captureManager,
		RuleSetProvider:            errorRuntime.ruleRepository,
		ResponseAnalyzer:           errorRuntime.responseAnalyzer,
		RuleStatistics:             errorRuntime.ruleStatistics,
	})

	// Create admin HTTP server (separate port for security)
	adminSrv := server.NewAdmin(server.AdminConfig{
		Port:                cfg.AdminPort,
		AdminToken:          cfg.AdminToken,
		Logger:              log,
		Store:               st,
		Health:              healthMgr,
		Selector:            sel,
		ProviderLifecycles:  sel,
		Concurrency:         limiter,
		ActiveReqList:       activeRegistry,
		Auth:                authService,
		ProviderImportStore: st,
		InternalErrorRules:  errorRuntime.adminHandler,
		CaptureSessions:     captureManager,
		CaptureQueries:      captureManager,
		CaptureExports:      captureManager,
	})

	statsWorker, err := startRuleStatsWorker(errorRuntime.ruleStatistics, log)
	if err != nil {
		return err
	}

	errCh := startServers(proxySrv, adminSrv)
	printServerURLs(cfg.Port, cfg.AdminPort)
	if err := completeServerLifecycle(
		func() error { return waitForShutdown(errCh, log) },
		func() error { return shutdownServers(proxySrv, adminSrv, authService) },
		statsWorker.Shutdown,
	); err != nil {
		return err
	}

	log.Info("switch-a stopped")
	return nil
}

func newCaptureManager(
	cfg *config.Config,
	clock requestcapture.Clock,
	log *zap.Logger,
) (*requestcapture.Manager, error) {
	manager, err := requestcapture.NewManager(requestCaptureManagerConfig(cfg, clock, log))
	if err != nil {
		return nil, fmt.Errorf("failed to initialize request capture manager: %w", err)
	}
	return manager, nil
}

func requestCaptureManagerConfig(
	cfg *config.Config,
	clock requestcapture.Clock,
	log *zap.Logger,
) requestcapture.Config {
	return requestcapture.Config{
		ProcessCeilingBytes:       cfg.DebugCaptureMemoryCeilingBytes,
		DefaultSessionQuotaBytes:  requestcapture.DefaultSessionQuotaBytes,
		ChunkBytes:                cfg.DebugCaptureChunkBytes,
		DefaultRecordsPerProvider: requestcapture.DefaultRecordsPerProvider,
		MaxRecordsPerProvider:     cfg.DebugCaptureMaxRecordsPerProvider,
		MaxActiveTraces:           cfg.DebugCaptureMaxActiveTraces,
		MaxActiveRecords:          cfg.DebugCaptureMaxActiveRecords,
		MaxTransitionsPerTrace:    cfg.DebugCaptureMaxTransitionsPerTrace,
		MaxPendingExports:         cfg.DebugCaptureMaxPendingExports,
		MaxActiveDownloads:        cfg.DebugCaptureMaxConcurrentDownloads,
		PreviewBytes:              cfg.DebugCaptureDetailPreviewBytes,
		DetailEventLimit:          cfg.DebugCaptureDetailEventLimit,
		ExportLineBytes:           cfg.DebugCaptureExportLineBytes,
		DownloadTokenTTL:          cfg.DebugCaptureDownloadTokenTTL,
		Clock:                     clock,
		Logger:                    log,
	}
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
