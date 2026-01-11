package health

import (
	"context"
	"errors"
	"testing"
	"time"

	"switch-a/internal/model"

	"go.uber.org/zap"
)

// mockStore implements the Store interface for testing.
type mockStore struct {
	healthStates map[string]*model.HealthState
	configs      map[string]string
	updateErr    error
}

func newMockStore() *mockStore {
	return &mockStore{
		healthStates: make(map[string]*model.HealthState),
		configs: map[string]string{
			"circuit_failure": "3",
			"circuit_window":  "60",
			"circuit_disable": "300",
		},
	}
}

func (m *mockStore) GetHealthState(_ context.Context, providerID string) (*model.HealthState, error) {
	if state, ok := m.healthStates[providerID]; ok {
		return state, nil
	}
	return &model.HealthState{
		ProviderID: providerID,
		Available:  true,
	}, nil
}

func (m *mockStore) UpdateHealthState(_ context.Context, state *model.HealthState) error {
	if m.updateErr != nil {
		return m.updateErr
	}
	m.healthStates[state.ProviderID] = state
	return nil
}

func (m *mockStore) GetConfig(_ context.Context, key string) (string, error) {
	return m.configs[key], nil
}

func TestManager_MarkSuccess(t *testing.T) {
	clock := &mockClock{now: time.Now()}
	store := newMockStore()
	logger := zap.NewNop()

	mgr := NewManager(Config{
		Store:  store,
		Clock:  clock,
		Logger: logger,
	})

	ctx := context.Background()
	providerID := "p1"

	mgr.MarkSuccess(ctx, providerID)

	state := store.healthStates[providerID]
	if state == nil {
		t.Fatal("expected health state to be saved")
	}
	if state.SuccessCount != 1 {
		t.Errorf("SuccessCount = %d, want 1", state.SuccessCount)
	}
	if state.LastSuccess == nil {
		t.Error("LastSuccess should be set")
	}
	if !state.Available {
		t.Error("Available should be true")
	}
}

func TestManager_MarkFailure(t *testing.T) {
	clock := &mockClock{now: time.Now()}
	store := newMockStore()
	logger := zap.NewNop()

	mgr := NewManager(Config{
		Store:  store,
		Clock:  clock,
		Logger: logger,
	})

	ctx := context.Background()
	providerID := "p1"
	testErr := errors.New("connection refused")

	// First failure - should not trigger
	triggered := mgr.MarkFailure(ctx, providerID, testErr)
	if triggered {
		t.Error("first failure should not trigger circuit breaker")
	}

	state := store.healthStates[providerID]
	if state.FailCount != 1 {
		t.Errorf("FailCount = %d, want 1", state.FailCount)
	}
	if state.LastError != testErr.Error() {
		t.Errorf("LastError = %q, want %q", state.LastError, testErr.Error())
	}
}

func TestManager_MarkFailure_CircuitBreaker(t *testing.T) {
	clock := &mockClock{now: time.Now()}
	store := newMockStore()
	store.configs["circuit_failure"] = "3"
	logger := zap.NewNop()

	mgr := NewManager(Config{
		Store:  store,
		Clock:  clock,
		Logger: logger,
	})

	ctx := context.Background()
	providerID := "p1"
	testErr := errors.New("connection refused")

	// Record failures up to threshold
	mgr.MarkFailure(ctx, providerID, testErr)
	mgr.MarkFailure(ctx, providerID, testErr)
	triggered := mgr.MarkFailure(ctx, providerID, testErr)

	if !triggered {
		t.Error("third failure should trigger circuit breaker")
	}

	state := store.healthStates[providerID]
	if state.Available {
		t.Error("provider should be unavailable after circuit break")
	}
	if state.DisabledUntil == nil {
		t.Error("DisabledUntil should be set")
	}
	if state.DisabledReason == "" {
		t.Error("DisabledReason should be set")
	}
}

func TestManager_IsAvailable(t *testing.T) {
	clock := &mockClock{now: time.Now()}
	store := newMockStore()
	logger := zap.NewNop()

	mgr := NewManager(Config{
		Store:  store,
		Clock:  clock,
		Logger: logger,
	})

	ctx := context.Background()
	providerID := "p1"

	// Initially available
	if !mgr.IsAvailable(ctx, providerID) {
		t.Error("new provider should be available")
	}

	// Auto-disable (simulating circuit breaker)
	disableUntil := clock.Now().Add(5 * time.Minute)
	store.healthStates[providerID] = &model.HealthState{
		ProviderID:     providerID,
		Available:      false,
		DisabledUntil:  &disableUntil,
		DisabledReason: "auto: test",
	}

	// IsAvailable is now a pure query - should return false
	if mgr.IsAvailable(ctx, providerID) {
		t.Error("disabled provider should not be available")
	}

	// Advance time past disable period
	clock.Advance(6 * time.Minute)

	// IsAvailable alone should still return false (pure query, no side effects)
	if mgr.IsAvailable(ctx, providerID) {
		t.Error("IsAvailable should not auto-recover, it's a pure query now")
	}

	// RecoverIfExpired should trigger recovery and return true
	if !mgr.RecoverIfExpired(ctx, providerID) {
		t.Error("RecoverIfExpired should return true when recovery is performed")
	}

	// Now IsAvailable should return true
	if !mgr.IsAvailable(ctx, providerID) {
		t.Error("provider should be available after RecoverIfExpired")
	}

	// State should be updated
	state := store.healthStates[providerID]
	if !state.Available {
		t.Error("state should be available after recovery")
	}
	if state.DisabledUntil != nil {
		t.Error("DisabledUntil should be cleared after recovery")
	}
}

func TestManager_RecoverIfExpired(t *testing.T) {
	clock := &mockClock{now: time.Now()}
	store := newMockStore()
	logger := zap.NewNop()

	mgr := NewManager(Config{
		Store:  store,
		Clock:  clock,
		Logger: logger,
	})

	ctx := context.Background()
	providerID := "p1"

	// Initially available - RecoverIfExpired should return false (no recovery needed)
	if mgr.RecoverIfExpired(ctx, providerID) {
		t.Error("RecoverIfExpired should return false for available provider")
	}

	// Set up auto-disabled state
	disableUntil := clock.Now().Add(5 * time.Minute)
	store.healthStates[providerID] = &model.HealthState{
		ProviderID:     providerID,
		Available:      false,
		DisabledUntil:  &disableUntil,
		DisabledReason: "auto: circuit breaker",
	}

	// Disable period not expired yet - should return false
	if mgr.RecoverIfExpired(ctx, providerID) {
		t.Error("RecoverIfExpired should return false when disable period not expired")
	}

	// Advance time past disable period
	clock.Advance(6 * time.Minute)

	// Now should return true and recover
	if !mgr.RecoverIfExpired(ctx, providerID) {
		t.Error("RecoverIfExpired should return true when disable period expired")
	}

	// Calling again should return false (already recovered)
	if mgr.RecoverIfExpired(ctx, providerID) {
		t.Error("RecoverIfExpired should return false after already recovered")
	}
}

func TestManager_ManualDisable(t *testing.T) {
	clock := &mockClock{now: time.Now()}
	store := newMockStore()
	logger := zap.NewNop()

	mgr := NewManager(Config{
		Store:  store,
		Clock:  clock,
		Logger: logger,
	})

	ctx := context.Background()
	providerID := "p1"

	err := mgr.ManualDisable(ctx, providerID, "maintenance")
	if err != nil {
		t.Fatalf("ManualDisable failed: %v", err)
	}

	state := store.healthStates[providerID]
	if state.Available {
		t.Error("provider should be unavailable after manual disable")
	}
	if state.DisabledReason != "manual: maintenance" {
		t.Errorf("DisabledReason = %q, want %q", state.DisabledReason, "manual: maintenance")
	}

	// Should not auto-recover (no DisabledUntil)
	clock.Advance(24 * time.Hour)
	if mgr.IsAvailable(ctx, providerID) {
		t.Error("manually disabled provider should not auto-recover")
	}
}

func TestManager_ManualDisable_OverridesAutoDisable(t *testing.T) {
	clock := &mockClock{now: time.Now()}
	store := newMockStore()
	logger := zap.NewNop()

	mgr := NewManager(Config{
		Store:  store,
		Clock:  clock,
		Logger: logger,
	})

	ctx := context.Background()
	providerID := "p1"

	// Simulate an auto-disable (circuit breaker) state.
	autoDisableUntil := clock.Now().Add(5 * time.Minute)
	store.healthStates[providerID] = &model.HealthState{
		ProviderID:     providerID,
		Available:      false,
		DisabledUntil:  &autoDisableUntil,
		DisabledReason: DisabledReasonPrefixAuto + "circuit breaker triggered",
	}

	// Manual disable should override auto-disable and prevent auto-recovery.
	if err := mgr.ManualDisable(ctx, providerID, "maintenance"); err != nil {
		t.Fatalf("ManualDisable failed: %v", err)
	}

	state := store.healthStates[providerID]
	if state.DisabledUntil != nil {
		t.Error("DisabledUntil should be cleared on manual disable to prevent auto-recovery")
	}
	if state.DisabledReason != "manual: maintenance" {
		t.Errorf("DisabledReason = %q, want %q", state.DisabledReason, "manual: maintenance")
	}

	clock.Advance(6 * time.Minute)
	if mgr.IsAvailable(ctx, providerID) {
		t.Error("manually disabled provider should not auto-recover even after previous auto-disable would have expired")
	}
}

func TestManager_ManualEnable(t *testing.T) {
	clock := &mockClock{now: time.Now()}
	store := newMockStore()
	logger := zap.NewNop()

	mgr := NewManager(Config{
		Store:  store,
		Clock:  clock,
		Logger: logger,
	})

	ctx := context.Background()
	providerID := "p1"

	// First disable
	_ = mgr.ManualDisable(ctx, providerID, "maintenance")

	// Then enable
	err := mgr.ManualEnable(ctx, providerID)
	if err != nil {
		t.Fatalf("ManualEnable failed: %v", err)
	}

	state := store.healthStates[providerID]
	if !state.Available {
		t.Error("provider should be available after manual enable")
	}
	if state.DisabledReason != "" {
		t.Errorf("DisabledReason should be cleared, got %q", state.DisabledReason)
	}
}

func TestManager_MarkSuccess_ResetsCircuitBreaker(t *testing.T) {
	clock := &mockClock{now: time.Now()}
	store := newMockStore()
	store.configs["circuit_failure"] = "3"
	logger := zap.NewNop()

	mgr := NewManager(Config{
		Store:  store,
		Clock:  clock,
		Logger: logger,
	})

	ctx := context.Background()
	providerID := "p1"
	testErr := errors.New("error")

	// Record 2 failures
	mgr.MarkFailure(ctx, providerID, testErr)
	mgr.MarkFailure(ctx, providerID, testErr)

	// Success should reset the counter
	mgr.MarkSuccess(ctx, providerID)

	// Now 3 more failures should trigger (not 1)
	mgr.MarkFailure(ctx, providerID, testErr)
	mgr.MarkFailure(ctx, providerID, testErr)
	triggered := mgr.MarkFailure(ctx, providerID, testErr)

	if !triggered {
		t.Error("circuit breaker should trigger after 3 failures post-reset")
	}
}

func TestManager_MarkFailure_NilError(t *testing.T) {
	clock := &mockClock{now: time.Now()}
	store := newMockStore()
	logger := zap.NewNop()

	mgr := NewManager(Config{
		Store:  store,
		Clock:  clock,
		Logger: logger,
	})

	ctx := context.Background()

	// Should not panic with nil error
	mgr.MarkFailure(ctx, "p1", nil)

	state := store.healthStates["p1"]
	if state.LastError != "" {
		t.Errorf("LastError should be empty for nil error, got %q", state.LastError)
	}
}
