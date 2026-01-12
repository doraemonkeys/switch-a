package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_Success(t *testing.T) {
	t.Setenv("SWITCHA_ADMIN_TOKEN", "test-token")
	t.Setenv("SWITCHA_PORT", "9000")
	t.Setenv("SWITCHA_ADMIN_PORT", "9001")
	t.Setenv("SWITCHA_DB_PATH", "/tmp/test.db")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.AdminToken != "test-token" {
		t.Errorf("AdminToken = %q, want %q", cfg.AdminToken, "test-token")
	}
	if cfg.Port != "9000" {
		t.Errorf("Port = %q, want %q", cfg.Port, "9000")
	}
	if cfg.AdminPort != "9001" {
		t.Errorf("AdminPort = %q, want %q", cfg.AdminPort, "9001")
	}
	if cfg.DBPath != "/tmp/test.db" {
		t.Errorf("DBPath = %q, want %q", cfg.DBPath, "/tmp/test.db")
	}
}

func TestLoad_DefaultValues(t *testing.T) {
	t.Setenv("SWITCHA_ADMIN_TOKEN", "test-token")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Port != DefaultPort {
		t.Errorf("Port = %q, want default %q", cfg.Port, DefaultPort)
	}
	if cfg.AdminPort != DefaultAdminPort {
		t.Errorf("AdminPort = %q, want default %q", cfg.AdminPort, DefaultAdminPort)
	}
	if cfg.DBPath != DefaultDBPath {
		t.Errorf("DBPath = %q, want default %q", cfg.DBPath, DefaultDBPath)
	}
	if cfg.LogPath != DefaultLogPath {
		t.Errorf("LogPath = %q, want default %q", cfg.LogPath, DefaultLogPath)
	}
	if cfg.LogMaxSizeMB != DefaultLogMaxSizeMB {
		t.Errorf("LogMaxSizeMB = %d, want default %d", cfg.LogMaxSizeMB, DefaultLogMaxSizeMB)
	}
	if cfg.LogMaxKeepDays != DefaultLogMaxKeepDays {
		t.Errorf("LogMaxKeepDays = %d, want default %d", cfg.LogMaxKeepDays, DefaultLogMaxKeepDays)
	}
	if cfg.LogLevel != DefaultLogLevel {
		t.Errorf("LogLevel = %q, want default %q", cfg.LogLevel, DefaultLogLevel)
	}
}

func TestLoad_MissingAdminToken(t *testing.T) {
	_, err := Load()
	if err == nil {
		t.Fatal("expected error when SWITCHA_ADMIN_TOKEN is missing")
	}
}

func TestLoadWithPath_ConfigFile(t *testing.T) {
	// Create a temporary config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	configContent := `port: "8080"
admin_port: "8081"
db_path: "/data/app.db"
admin_token: "file-token"
log_path: "/var/log/app.log"
log_max_size_mb: 50
log_max_keep_days: 14
log_level: "debug"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("failed to create config file: %v", err)
	}

	cfg, err := LoadWithPath(configPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Port != "8080" {
		t.Errorf("Port = %q, want %q", cfg.Port, "8080")
	}
	if cfg.AdminPort != "8081" {
		t.Errorf("AdminPort = %q, want %q", cfg.AdminPort, "8081")
	}
	if cfg.DBPath != "/data/app.db" {
		t.Errorf("DBPath = %q, want %q", cfg.DBPath, "/data/app.db")
	}
	if cfg.AdminToken != "file-token" {
		t.Errorf("AdminToken = %q, want %q", cfg.AdminToken, "file-token")
	}
	if cfg.LogPath != "/var/log/app.log" {
		t.Errorf("LogPath = %q, want %q", cfg.LogPath, "/var/log/app.log")
	}
	if cfg.LogMaxSizeMB != 50 {
		t.Errorf("LogMaxSizeMB = %d, want %d", cfg.LogMaxSizeMB, 50)
	}
	if cfg.LogMaxKeepDays != 14 {
		t.Errorf("LogMaxKeepDays = %d, want %d", cfg.LogMaxKeepDays, 14)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, "debug")
	}
}

func TestLoadWithPath_EnvOverridesFile(t *testing.T) {
	// Create a temporary config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	configContent := `port: "8080"
admin_port: "8081"
db_path: "/data/app.db"
admin_token: "file-token"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("failed to create config file: %v", err)
	}

	// Set environment variables to override config file
	t.Setenv("SWITCHA_PORT", "9999")
	t.Setenv("SWITCHA_ADMIN_TOKEN", "env-token")

	cfg, err := LoadWithPath(configPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Environment variables should override config file
	if cfg.Port != "9999" {
		t.Errorf("Port = %q, want %q (env should override file)", cfg.Port, "9999")
	}
	if cfg.AdminToken != "env-token" {
		t.Errorf("AdminToken = %q, want %q (env should override file)", cfg.AdminToken, "env-token")
	}

	// Config file values should be used when env not set
	if cfg.AdminPort != "8081" {
		t.Errorf("AdminPort = %q, want %q", cfg.AdminPort, "8081")
	}
	if cfg.DBPath != "/data/app.db" {
		t.Errorf("DBPath = %q, want %q", cfg.DBPath, "/data/app.db")
	}
}

func TestLoadWithPath_NonExistentFile(t *testing.T) {
	_, err := LoadWithPath("/nonexistent/path/config.yaml")
	if err == nil {
		t.Fatal("expected error for nonexistent config file")
	}
}

func TestLoadWithPath_JSONFormat(t *testing.T) {
	// Create a temporary JSON config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")

	configContent := `{
  "port": "7070",
  "admin_port": "7071",
  "db_path": "/var/data.db",
  "admin_token": "json-token"
}`
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("failed to create config file: %v", err)
	}

	cfg, err := LoadWithPath(configPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Port != "7070" {
		t.Errorf("Port = %q, want %q", cfg.Port, "7070")
	}
	if cfg.AdminToken != "json-token" {
		t.Errorf("AdminToken = %q, want %q", cfg.AdminToken, "json-token")
	}
}

func TestConfigFileUsed(t *testing.T) {
	// Create a temporary config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	configContent := `admin_token: "test"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("failed to create config file: %v", err)
	}

	cfg, err := LoadWithPath(configPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.ConfigFileUsed != configPath {
		t.Errorf("ConfigFileUsed = %q, want %q", cfg.ConfigFileUsed, configPath)
	}
}

func TestConfigFileUsed_NoFile(t *testing.T) {
	// When loading without a config file (env only), ConfigFileUsed should be empty
	t.Setenv("SWITCHA_ADMIN_TOKEN", "test-token")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.ConfigFileUsed != "" {
		t.Errorf("ConfigFileUsed = %q, want empty string when no config file is used", cfg.ConfigFileUsed)
	}
}
