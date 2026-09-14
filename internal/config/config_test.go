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
	t.Setenv(EnvCodexKeyringFile, "/run/secrets/codex-keyring.json")

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
	if cfg.CodexKeyringFile != "/run/secrets/codex-keyring.json" {
		t.Errorf("CodexKeyringFile = %q", cfg.CodexKeyringFile)
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
	if cfg.Host != "" || cfg.AdminHost != "" {
		t.Errorf("listener hosts = %q / %q, want all interfaces", cfg.Host, cfg.AdminHost)
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
	if cfg.CodexKeyringFile != DefaultCodexKeyringFile {
		t.Errorf("CodexKeyringFile = %q, want default %q", cfg.CodexKeyringFile, DefaultCodexKeyringFile)
	}
}

func TestLoadWithPath_ListenerHosts(t *testing.T) {
	tests := []struct {
		name          string
		content       string
		envHost       string
		envAdminHost  string
		wantHost      string
		wantAdminHost string
	}{
		{name: "omitted hosts listen on all interfaces"},
		{name: "empty hosts listen on all interfaces", content: "host: \"\"\nadmin_host: \"\"\n"},
		{
			name:     "independent IPv4 and IPv6 hosts",
			content:  "host: 192.0.2.10\nadmin_host: '::1'\n",
			wantHost: "192.0.2.10", wantAdminHost: "::1",
		},
		{
			name:    "environment only",
			envHost: "::1", envAdminHost: "127.0.0.1",
			wantHost: "::1", wantAdminHost: "127.0.0.1",
		},
		{
			name:    "proxy override preserves admin file setting",
			content: "host: 192.0.2.10\nadmin_host: '::1'\n",
			envHost: "127.0.0.1", wantHost: "127.0.0.1", wantAdminHost: "::1",
		},
		{
			name:         "admin override preserves proxy file setting",
			content:      "host: 192.0.2.10\nadmin_host: '::1'\n",
			envAdminHost: "127.0.0.1", wantHost: "192.0.2.10", wantAdminHost: "127.0.0.1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(EnvHost, tt.envHost)
			t.Setenv(EnvAdminHost, tt.envAdminHost)
			t.Setenv(EnvAdminToken, "test-token")
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(tt.content), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := LoadWithPath(path)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Host != tt.wantHost || cfg.AdminHost != tt.wantAdminHost {
				t.Fatalf("listener hosts = %q / %q, want %q / %q", cfg.Host, cfg.AdminHost, tt.wantHost, tt.wantAdminHost)
			}
		})
	}
}

func TestLoad_KeyringDefaultDoesNotFollowDatabaseOverride(t *testing.T) {
	t.Setenv(EnvAdminToken, "test-token")
	t.Setenv(EnvDBPath, filepath.Join(t.TempDir(), "custom.db"))

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.CodexKeyringFile != DefaultCodexKeyringFile {
		t.Fatalf("CodexKeyringFile = %q, want process-relative default %q", cfg.CodexKeyringFile, DefaultCodexKeyringFile)
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
codex_keyring_file: "/run/secrets/file-keyring.json"
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
	if cfg.CodexKeyringFile != "/run/secrets/file-keyring.json" {
		t.Errorf("CodexKeyringFile = %q", cfg.CodexKeyringFile)
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
	t.Setenv(EnvCodexKeyringFile, "/run/secrets/env-keyring.json")

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
	if cfg.CodexKeyringFile != "/run/secrets/env-keyring.json" {
		t.Errorf("CodexKeyringFile = %q, want environment override", cfg.CodexKeyringFile)
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
