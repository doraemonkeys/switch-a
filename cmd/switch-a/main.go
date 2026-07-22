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

	"switch-a/internal"
	"switch-a/internal/config"
	"switch-a/internal/health"
	"switch-a/internal/logger"
	"switch-a/internal/model"
	"switch-a/internal/providerauth"
	"switch-a/internal/proxy"
	"switch-a/internal/selector"
	"switch-a/internal/server"
	"switch-a/internal/store"

	"go.uber.org/zap"
)

func main() {
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

	log.Info("starting switch-a",
		zap.String("proxy_port", cfg.Port),
		zap.String("admin_port", cfg.AdminPort),
		zap.String("log_path", cfg.LogPath),
		zap.String("log_level", cfg.LogLevel),
	)

	// Initialize store
	sqlStore, err := store.NewSQLiteStore(cfg.DBPath, internal.RealClock{})
	if err != nil {
		return fmt.Errorf("failed to initialize store: %w", err)
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

	// Initialize clock for time-based operations
	clock := internal.RealClock{}

	// Initialize health manager for circuit breaker and availability tracking
	healthMgr := health.NewManager(health.Config{
		Store:  st,
		Clock:  clock,
		Logger: log,
	})
	// Start cleanup loop to prevent memory growth from old failure records
	stopHealthCleanup := healthMgr.StartCleanupLoop(HealthCleanupInterval, HealthCleanupMaxAge)
	defer stopHealthCleanup()

	// Initialize sticky cache for session affinity
	stickyCache := selector.NewMemoryStickyCache(clock)
	// Start cleanup loop to prevent memory growth from expired entries
	stopCleanup := stickyCache.StartCleanupLoop(StickyCacheCleanupInterval)
	defer stopCleanup()

	// Start log cleanup loop to prevent request_logs table from growing indefinitely
	stopLogCleanup := startLogCleanupLoop(st, log)
	defer stopLogCleanup()

	// Initialize concurrency limiter for per-provider request limits
	limiter := selector.NewConcurrencyLimiter()

	// Initialize active request registry for live request monitoring
	activeRegistry := proxy.NewActiveRequestRegistryWithHook(func(req proxy.ActiveRequest, reason proxy.ActiveRequestRemovalReason) {
		if reason == proxy.ActiveRequestRemovalReasonStale {
			return
		}
		limiter.Release(req.ProviderID)
	})
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
	})

	// Create admin HTTP server (separate port for security)
	adminSrv := server.NewAdmin(server.AdminConfig{
		Port:          cfg.AdminPort,
		AdminToken:    cfg.AdminToken,
		Logger:        log,
		Store:         st,
		Health:        healthMgr,
		Selector:      sel,
		Concurrency:   limiter,
		ActiveReqList: activeRegistry,
		Auth:          authService,
	})

	errCh := startServers(proxySrv, adminSrv)
	printServerURLs(cfg.Port, cfg.AdminPort)
	if err := waitForShutdown(errCh, log); err != nil {
		return err
	}
	if err := shutdownServers(proxySrv, adminSrv, authService); err != nil {
		return err
	}

	log.Info("switch-a stopped")
	return nil
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
