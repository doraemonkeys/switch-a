package logger

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.LogPath != "./logs/switch-a.log" {
		t.Errorf("LogPath = %q, want %q", cfg.LogPath, "./logs/switch-a.log")
	}
	if cfg.MaxSizeMB != 100 {
		t.Errorf("MaxSizeMB = %d, want %d", cfg.MaxSizeMB, 100)
	}
	if cfg.MaxKeepDays != 7 {
		t.Errorf("MaxKeepDays = %d, want %d", cfg.MaxKeepDays, 7)
	}
	if cfg.IsDev {
		t.Error("IsDev = true, want false")
	}
}

func TestNew_Development(t *testing.T) {
	cfg := Config{IsDev: true}
	logger := New(cfg)
	if logger == nil {
		t.Fatal("expected non-nil logger")
	}
	logger.Info("test message in development mode")
}

func TestNew_Production(t *testing.T) {
	// Create a log file in a temporary location that we manage ourselves
	tmpDir, err := os.MkdirTemp("", "logger_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	logPath := filepath.Join(tmpDir, "test.log")

	cfg := Config{
		LogPath:     logPath,
		MaxSizeMB:   10,
		MaxKeepDays: 1,
		IsDev:       false,
	}
	logger := New(cfg)
	if logger == nil {
		t.Fatal("expected non-nil logger")
	}
	logger.Info("test message in production mode")
	_ = logger.Sync()

	// Verify log file was created
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		t.Errorf("log file was not created at %s", logPath)
	}

	// Cleanup will be done by the OS or manually - we don't delete because
	// the lumberjack logger may still have the file handle open
}
