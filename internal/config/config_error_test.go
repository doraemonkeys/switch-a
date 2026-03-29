package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoad_ParseErrorFromDefaultSearchPath(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, ConfigFileName+"."+ConfigFileType)
	if err := os.WriteFile(configPath, []byte("admin_token: [\n"), 0o644); err != nil {
		t.Fatalf("failed to create malformed config: %v", err)
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("chdir to temp dir: %v", err)
	}
	t.Cleanup(func() {
		if chdirErr := os.Chdir(wd); chdirErr != nil {
			t.Errorf("restore working directory: %v", chdirErr)
		}
	})

	_, err = Load()
	if err == nil {
		t.Fatal("Load() succeeded with malformed default config, want parse error")
	}
	if !strings.Contains(err.Error(), "failed to parse config file") {
		t.Fatalf("Load() error = %q, want parse error", err)
	}
}

func TestLoadWithPath_UnmarshalError(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	configContent := "admin_token: test-token\nlog_max_size_mb:\n  value: 1\n"
	if err := os.WriteFile(configPath, []byte(configContent), 0o644); err != nil {
		t.Fatalf("failed to create config file: %v", err)
	}

	_, err := LoadWithPath(configPath)
	if err == nil {
		t.Fatal("LoadWithPath() succeeded with incompatible config shape, want unmarshal error")
	}
	if !strings.Contains(err.Error(), "failed to unmarshal config") {
		t.Fatalf("LoadWithPath() error = %q, want unmarshal error", err)
	}
}
