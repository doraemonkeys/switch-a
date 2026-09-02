package store

import (
	"context"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/defaults"
)

func TestInitDefaultConfigDeletesOnlyObsoleteRuntimeRows(t *testing.T) {
	configStore := setupTestStore(t)
	ctx := context.Background()

	for _, key := range obsoleteRuntimeConfigKeys {
		if err := configStore.SetConfig(ctx, key, "true"); err != nil {
			t.Fatalf("SetConfig(%q) error = %v", key, err)
		}
	}
	const unrelatedKey = "operator_owned_setting"
	if err := configStore.SetConfig(ctx, unrelatedKey, "preserve-me"); err != nil {
		t.Fatalf("SetConfig(%q) error = %v", unrelatedKey, err)
	}

	for run := 1; run <= 2; run++ {
		if err := configStore.InitDefaultConfig(ctx); err != nil {
			t.Fatalf("InitDefaultConfig() run %d error = %v", run, err)
		}
		stored, err := configStore.GetAllConfig(ctx)
		if err != nil {
			t.Fatalf("GetAllConfig() run %d error = %v", run, err)
		}
		for _, key := range obsoleteRuntimeConfigKeys {
			if _, exists := stored[key]; exists {
				t.Errorf("obsolete runtime row %q remains after run %d", key, run)
			}
		}
		if stored[unrelatedKey] != "preserve-me" {
			t.Errorf("unrelated row after run %d = %q", run, stored[unrelatedKey])
		}
		if stored[defaults.ConfigKeyWebSocketProbeClientModel] != DefaultWebSocketProbeClientModel {
			t.Errorf("WebSocket probe setting after run %d = %q", run, stored[defaults.ConfigKeyWebSocketProbeClientModel])
		}
	}
}

func TestObsoleteRuntimeKeysAreAbsentFromFreshDefaults(t *testing.T) {
	configStore := setupTestStore(t)
	ctx := context.Background()
	if err := configStore.InitDefaultConfig(ctx); err != nil {
		t.Fatalf("InitDefaultConfig() error = %v", err)
	}
	configDefaults := GetDefaultConfigs()
	stored, err := configStore.GetAllConfig(ctx)
	if err != nil {
		t.Fatalf("GetAllConfig() error = %v", err)
	}
	for _, key := range obsoleteRuntimeConfigKeys {
		if _, exists := configDefaults[key]; exists {
			t.Errorf("GetDefaultConfigs() contains removed key %q", key)
		}
		if _, exists := stored[key]; exists {
			t.Errorf("fresh database contains removed key %q", key)
		}
		value, err := configStore.GetConfig(ctx, key)
		if err != nil {
			t.Fatalf("GetConfig(%q) error = %v", key, err)
		}
		if value != "" {
			t.Errorf("GetConfig(%q) = %q, want empty", key, value)
		}
	}
}
