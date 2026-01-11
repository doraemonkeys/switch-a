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

func TestIncrementSuccessCount(t *testing.T) {
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

	now := time.Now()

	// Increment success count on new provider (no existing health state)
	state, err := store.IncrementSuccessCount(ctx, "p1", now)
	if err != nil {
		t.Fatalf("IncrementSuccessCount failed: %v", err)
	}
	if state.SuccessCount != 1 {
		t.Errorf("SuccessCount = %d, want 1", state.SuccessCount)
	}
	if !state.Available {
		t.Error("expected Available = true")
	}

	// Increment again
	state, err = store.IncrementSuccessCount(ctx, "p1", now.Add(time.Second))
	if err != nil {
		t.Fatalf("IncrementSuccessCount failed: %v", err)
	}
	if state.SuccessCount != 2 {
		t.Errorf("SuccessCount = %d, want 2", state.SuccessCount)
	}
}

func TestIncrementSuccessCount_PreservesManualDisable(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	// Create provider
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

	// Manually disable provider
	manualState := &model.HealthState{
		ProviderID:     "p1",
		Available:      false,
		DisabledReason: "manual: maintenance",
	}
	if err := store.UpdateHealthState(ctx, manualState); err != nil {
		t.Fatalf("UpdateHealthState failed: %v", err)
	}

	// IncrementSuccessCount should NOT set available=true for manually disabled providers
	now := time.Now()
	state, err := store.IncrementSuccessCount(ctx, "p1", now)
	if err != nil {
		t.Fatalf("IncrementSuccessCount failed: %v", err)
	}
	if state.Available {
		t.Error("IncrementSuccessCount should preserve manual disable, expected Available = false")
	}
	if state.SuccessCount != 1 {
		t.Errorf("SuccessCount = %d, want 1", state.SuccessCount)
	}
}

func TestIncrementFailCount(t *testing.T) {
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

	now := time.Now()

	// Increment fail count on new provider
	state, err := store.IncrementFailCount(ctx, "p1", now, "connection timeout")
	if err != nil {
		t.Fatalf("IncrementFailCount failed: %v", err)
	}
	if state.FailCount != 1 {
		t.Errorf("FailCount = %d, want 1", state.FailCount)
	}
	if state.LastError != "connection timeout" {
		t.Errorf("LastError = %q, want %q", state.LastError, "connection timeout")
	}

	// Increment again with different error
	state, err = store.IncrementFailCount(ctx, "p1", now.Add(time.Second), "server error")
	if err != nil {
		t.Fatalf("IncrementFailCount failed: %v", err)
	}
	if state.FailCount != 2 {
		t.Errorf("FailCount = %d, want 2", state.FailCount)
	}
	if state.LastError != "server error" {
		t.Errorf("LastError = %q, want %q", state.LastError, "server error")
	}
}

func TestTriggerCircuitBreaker(t *testing.T) {
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

	now := time.Now()

	// First create a health state entry
	if _, err := store.IncrementFailCount(ctx, "p1", now, "error"); err != nil {
		t.Fatalf("IncrementFailCount failed: %v", err)
	}

	// Trigger circuit breaker
	disabledUntil := now.Add(5 * time.Minute)
	if err := store.TriggerCircuitBreaker(ctx, "p1", disabledUntil, "auto: circuit breaker triggered"); err != nil {
		t.Fatalf("TriggerCircuitBreaker failed: %v", err)
	}

	// Verify state
	state, err := store.GetHealthState(ctx, "p1")
	if err != nil {
		t.Fatalf("GetHealthState failed: %v", err)
	}
	if state.Available {
		t.Error("expected Available = false after circuit breaker")
	}
	if state.DisabledReason != "auto: circuit breaker triggered" {
		t.Errorf("DisabledReason = %q, want %q", state.DisabledReason, "auto: circuit breaker triggered")
	}
	if state.DisabledUntil == nil {
		t.Error("expected DisabledUntil to be set")
	}
}
