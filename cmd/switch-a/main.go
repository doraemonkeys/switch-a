// Package main is the entry point for switch-a.
package main

import (
	"context"
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
	log := logger.New(logger.DefaultConfig())
	defer func() { _ = log.Sync() }()

	log.Info("starting switch-a", zap.String("port", cfg.Port))

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
	stopHealthCleanup := healthMgr.StartCleanupLoop(5*time.Minute, 10*time.Minute)
	defer stopHealthCleanup()

	// Initialize sticky cache for session affinity
	stickyCache := selector.NewMemoryStickyCache(clock)
	// Start cleanup loop to prevent memory growth from expired entries
	stopCleanup := stickyCache.StartCleanupLoop(5 * time.Minute)
	defer stopCleanup()

	// Start log cleanup loop to prevent request_logs table from growing indefinitely
	stopLogCleanup := startLogCleanupLoop(st, log)
	defer stopLogCleanup()

	// Initialize concurrency limiter for per-provider request limits
	limiter := selector.NewConcurrencyLimiter()

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

	// Create HTTP server with full component stack
	srv := server.New(server.Config{
		Port:        cfg.Port,
		AdminToken:  cfg.AdminToken,
		Logger:      log,
		Store:       st,
		Health:      healthMgr,
		Selector:    sel,
		Concurrency: limiter,
	})

	// Start server in goroutine
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Start()
	}()

	// Wait for interrupt signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errCh:
		return err
	case sig := <-sigCh:
		log.Info("received signal, shutting down", zap.String("signal", sig.String()))
	}

	// Graceful shutdown
	shutdownCtx, cancel := context.WithTimeout(context.Background(), ShutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown error: %w", err)
	}

	log.Info("switch-a stopped")
	return nil
}
