// Package main is the entry point for switch-a.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"switch-a/internal"
	"switch-a/internal/config"
	"switch-a/internal/logger"
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
	st, err := store.NewSQLiteStore(cfg.DBPath, internal.RealClock{})
	if err != nil {
		return fmt.Errorf("failed to initialize store: %w", err)
	}
	defer func() { _ = st.Close() }()

	// Initialize default configuration
	ctx := context.Background()
	if err := st.InitDefaultConfig(ctx); err != nil {
		return fmt.Errorf("failed to initialize default config: %w", err)
	}

	// Create HTTP server
	srv := server.New(server.Config{
		Port:       cfg.Port,
		AdminToken: cfg.AdminToken,
		Logger:     log,
		Store:      st,
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
