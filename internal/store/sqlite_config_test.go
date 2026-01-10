package store

import (
	"context"
	"testing"
)

func TestInitDefaultConfig(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	if err := store.InitDefaultConfig(ctx); err != nil {
		t.Fatalf("InitDefaultConfig failed: %v", err)
	}

	// Verify defaults were set
	value, err := store.GetConfig(ctx, "sticky_ttl")
	if err != nil {
		t.Fatalf("GetConfig failed: %v", err)
	}
	if value != "300" {
		t.Errorf("sticky_ttl = %q, want %q", value, "300")
	}
}

func TestConfig(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	// Set config
	if err := store.SetConfig(ctx, "test_key", "test_value"); err != nil {
		t.Fatalf("SetConfig failed: %v", err)
	}

	// Get config
	value, err := store.GetConfig(ctx, "test_key")
	if err != nil {
		t.Fatalf("GetConfig failed: %v", err)
	}
	if value != "test_value" {
		t.Errorf("value = %q, want %q", value, "test_value")
	}

	// Get non-existent config returns default if available
	value, err = store.GetConfig(ctx, "sticky_ttl")
	if err != nil {
		t.Fatalf("GetConfig for default failed: %v", err)
	}
	if value != "300" {
		t.Errorf("default sticky_ttl = %q, want %q", value, "300")
	}

	// Get all config
	if err := store.SetConfig(ctx, "key2", "value2"); err != nil {
		t.Fatalf("SetConfig key2 failed: %v", err)
	}

	all, err := store.GetAllConfig(ctx)
	if err != nil {
		t.Fatalf("GetAllConfig failed: %v", err)
	}
	if len(all) < 2 {
		t.Errorf("GetAllConfig len = %d, want >= 2", len(all))
	}
}

func TestConfigOperations(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	// Get non-existent config that has no default
	val, err := store.GetConfig(ctx, "nonexistent_key")
	if err != nil {
		t.Fatalf("GetConfig failed: %v", err)
	}
	if val != "" {
		t.Errorf("expected empty string for non-existent key, got %q", val)
	}

	// Set and get
	if err := store.SetConfig(ctx, "test_key", "test_value"); err != nil {
		t.Fatalf("SetConfig failed: %v", err)
	}
	val, err = store.GetConfig(ctx, "test_key")
	if err != nil {
		t.Fatalf("GetConfig failed: %v", err)
	}
	if val != "test_value" {
		t.Errorf("GetConfig = %q, want %q", val, "test_value")
	}

	// Get all config
	all, err := store.GetAllConfig(ctx)
	if err != nil {
		t.Fatalf("GetAllConfig failed: %v", err)
	}
	if all["test_key"] != "test_value" {
		t.Errorf("GetAllConfig missing test_key")
	}
}
