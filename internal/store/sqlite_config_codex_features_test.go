package store

import (
	"context"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/codex/startup"
)

func TestCodexFeatureDefaultsArePersistedFalse(t *testing.T) {
	configStore := setupTestStore(t)
	ctx := context.Background()
	if err := configStore.InitDefaultConfig(ctx); err != nil {
		t.Fatalf("InitDefaultConfig() error = %v", err)
	}
	defaults := GetDefaultConfigs()
	for _, key := range codexstartup.Keys() {
		if got := defaults[key]; got != "false" {
			t.Errorf("GetDefaultConfigs()[%q] = %q, want false", key, got)
		}
		got, err := configStore.GetConfig(ctx, key)
		if err != nil {
			t.Fatalf("GetConfig(%q) error = %v", key, err)
		}
		if got != "false" {
			t.Errorf("GetConfig(%q) = %q, want false", key, got)
		}
	}
}
