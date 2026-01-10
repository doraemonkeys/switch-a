package config

import (
	"testing"
)

func TestLoad_Success(t *testing.T) {
	t.Setenv("SWITCHA_ADMIN_TOKEN", "test-token")
	t.Setenv("SWITCHA_PORT", "9000")
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
	if cfg.DBPath != "./data.db" {
		t.Errorf("DBPath = %q, want default %q", cfg.DBPath, "./data.db")
	}
}

func TestLoad_MissingAdminToken(t *testing.T) {
	_, err := Load()
	if err == nil {
		t.Fatal("expected error when SWITCHA_ADMIN_TOKEN is missing")
	}
}

func TestGetEnvOrDefault(t *testing.T) {
	t.Setenv("TEST_KEY", "custom-value")

	if v := getEnvOrDefault("TEST_KEY", "default"); v != "custom-value" {
		t.Errorf("got %q, want %q", v, "custom-value")
	}

	if v := getEnvOrDefault("NONEXISTENT_KEY", "default"); v != "default" {
		t.Errorf("got %q, want %q", v, "default")
	}
}
