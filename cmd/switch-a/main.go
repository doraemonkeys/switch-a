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

	wg.Add(1)
	go func() {
		defer wg.Done()
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
	}()

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
	activeRegistry := proxy.NewActiveRequestRegistry()
	activeRegistry.StartCleanup()
	defer activeRegistry.StopCleanup()

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

	// Create proxy HTTP server (public port)
	proxySrv := server.New(server.Config{
		Port:           cfg.Port,
		Logger:         log,
		Store:          st,
		Health:         healthMgr,
		Selector:       sel,
		ActiveRegistry: activeRegistry,
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
	})

	// Start servers in goroutines
	errCh := make(chan error, 2)
	go func() {
		errCh <- proxySrv.Start()
	}()
	go func() {
		errCh <- adminSrv.Start()
	}()

	// Print user-friendly URLs after servers start
	fmt.Println()
	fmt.Println("=========================================")
	fmt.Printf("  Proxy URL:  http://localhost:%s\n", cfg.Port)
	fmt.Printf("  Admin URL:  http://localhost:%s/admin\n", cfg.AdminPort)
	fmt.Println("=========================================")
	fmt.Println()

	// Wait for interrupt signal or server error
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errCh:
		// Drain the channel to collect all errors if both servers failed simultaneously
		var errs []error
		errs = append(errs, err)
		// Non-blocking drain of remaining errors
		for {
			select {
			case e := <-errCh:
				errs = append(errs, e)
			default:
				// No more errors in channel
				return errors.Join(errs...)
			}
		}
	case sig := <-sigCh:
		log.Info("received signal, shutting down", zap.String("signal", sig.String()))
	}

	// Graceful shutdown of both servers
	shutdownCtx, cancel := context.WithTimeout(context.Background(), ShutdownTimeout)
	defer cancel()

	var shutdownErr error
	if err := proxySrv.Shutdown(shutdownCtx); err != nil {
		shutdownErr = fmt.Errorf("proxy server shutdown error: %w", err)
	}
	if err := adminSrv.Shutdown(shutdownCtx); err != nil {
		if shutdownErr != nil {
			shutdownErr = fmt.Errorf("%w; admin server shutdown error: %w", shutdownErr, err)
		} else {
			shutdownErr = fmt.Errorf("admin server shutdown error: %w", err)
		}
	}
	if shutdownErr != nil {
		return shutdownErr
	}

	log.Info("switch-a stopped")
	return nil
}
