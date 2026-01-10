package config

import (
	"os"
	"testing"
)

func TestLoad_Success(t *testing.T) {
	os.Setenv("SWITCHA_ADMIN_TOKEN", "test-token")
	os.Setenv("SWITCHA_PORT", "9000")
	os.Setenv("SWITCHA_DB_PATH", "/tmp/test.db")
	defer func() {
		os.Unsetenv("SWITCHA_ADMIN_TOKEN")
		os.Unsetenv("SWITCHA_PORT")
		os.Unsetenv("SWITCHA_DB_PATH")
	}()

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
	os.Setenv("SWITCHA_ADMIN_TOKEN", "test-token")
	os.Unsetenv("SWITCHA_PORT")
	os.Unsetenv("SWITCHA_DB_PATH")
	defer os.Unsetenv("SWITCHA_ADMIN_TOKEN")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Port != "8080" {
		t.Errorf("Port = %q, want default %q", cfg.Port, "8080")
	}
	if cfg.DBPath != "./data.db" {
		t.Errorf("DBPath = %q, want default %q", cfg.DBPath, "./data.db")
	}
}

func TestLoad_MissingAdminToken(t *testing.T) {
	os.Unsetenv("SWITCHA_ADMIN_TOKEN")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error when SWITCHA_ADMIN_TOKEN is missing")
	}
}

func TestGetEnvOrDefault(t *testing.T) {
	os.Setenv("TEST_KEY", "custom-value")
	defer os.Unsetenv("TEST_KEY")

	if v := getEnvOrDefault("TEST_KEY", "default"); v != "custom-value" {
		t.Errorf("got %q, want %q", v, "custom-value")
	}

	if v := getEnvOrDefault("NONEXISTENT_KEY", "default"); v != "default" {
		t.Errorf("got %q, want %q", v, "default")
	}
}
