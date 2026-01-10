package store

import (
	"context"
	"testing"
	"time"

	"switch-a/internal/model"
)

func TestHealthState(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	// Create provider first
	provider := &model.Provider{
		ID:      "p1",
		Name:    "Test Provider",
		BaseURL: "https://api.example.com",
		APIKey:  "key",
		Enabled: true,
	}
	if err := store.CreateProvider(ctx, provider); err != nil {
		t.Fatalf("CreateProvider failed: %v", err)
	}

	// Get default health state
	state, err := store.GetHealthState(ctx, "p1")
	if err != nil {
		t.Fatalf("GetHealthState failed: %v", err)
	}
	if !state.Available {
		t.Error("expected Available = true by default")
	}

	// Update health state
	now := time.Now()
	state.Available = false
	state.FailCount = 3
	state.LastFailure = &now
	state.LastError = "connection timeout"

	if err := store.UpdateHealthState(ctx, state); err != nil {
		t.Fatalf("UpdateHealthState failed: %v", err)
	}

	// Verify update
	got, err := store.GetHealthState(ctx, "p1")
	if err != nil {
		t.Fatalf("GetHealthState after update failed: %v", err)
	}
	if got.Available {
		t.Error("expected Available = false")
	}
	if got.FailCount != 3 {
		t.Errorf("FailCount = %d, want 3", got.FailCount)
	}

	// List health states
	states, err := store.ListHealthStates(ctx)
	if err != nil {
		t.Fatalf("ListHealthStates failed: %v", err)
	}
	if len(states) != 1 {
		t.Errorf("ListHealthStates len = %d, want 1", len(states))
	}
}
