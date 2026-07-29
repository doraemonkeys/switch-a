package store

import (
	"context"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"
)

func TestHealthState(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	// Create provider first
	provider := &model.Provider{
		ID:      "p1",
		Name:    "Test Provider",
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

func TestGetHealthStatesByProviderIDs(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	if err := store.CreateProvider(ctx, &model.Provider{
		ID:      "p1",
		Name:    "Provider One",
		APIKey:  "key-1",
		Enabled: true,
	}); err != nil {
		t.Fatalf("CreateProvider(p1) failed: %v", err)
	}
	if err := store.CreateProvider(ctx, &model.Provider{
		ID:      "p2",
		Name:    "Provider Two",
		APIKey:  "key-2",
		Enabled: true,
	}); err != nil {
		t.Fatalf("CreateProvider(p2) failed: %v", err)
	}

	emptyStates, err := store.GetHealthStatesByProviderIDs(ctx, nil)
	if err != nil {
		t.Fatalf("GetHealthStatesByProviderIDs(nil) failed: %v", err)
	}
	if len(emptyStates) != 0 {
		t.Fatalf("len(emptyStates) = %d, want 0", len(emptyStates))
	}

	now := time.Now()
	lastError := "backend timeout"
	if err := store.UpdateHealthState(ctx, &model.HealthState{
		ProviderID:   "p1",
		Available:    false,
		SuccessCount: 2,
		FailCount:    1,
		LastError:    lastError,
		LastFailure:  &now,
	}); err != nil {
		t.Fatalf("UpdateHealthState(p1) failed: %v", err)
	}

	states, err := store.GetHealthStatesByProviderIDs(ctx, []string{"p1", "p2"})
	if err != nil {
		t.Fatalf("GetHealthStatesByProviderIDs(mixed) failed: %v", err)
	}
	if len(states) != 2 {
		t.Fatalf("len(states) = %d, want 2", len(states))
	}

	existing := states["p1"]
	if existing == nil {
		t.Fatal("states[p1] = nil, want persisted state")
	}
	if existing.Available {
		t.Fatal("states[p1].Available = true, want false")
	}
	if existing.SuccessCount != 2 || existing.FailCount != 1 {
		t.Fatalf("states[p1] counters = %+v, want success=2 fail=1", existing)
	}
	if existing.LastError != lastError {
		t.Fatalf("states[p1].LastError = %q, want %q", existing.LastError, lastError)
	}

	defaulted := states["p2"]
	if defaulted == nil {
		t.Fatal("states[p2] = nil, want default state")
	}
	if !defaulted.Available {
		t.Fatal("states[p2].Available = false, want true")
	}
	if defaulted.ProviderID != "p2" {
		t.Fatalf("states[p2].ProviderID = %q, want p2", defaulted.ProviderID)
	}
}

func TestIncrementSuccessCount(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	// Create provider first
	provider := &model.Provider{
		ID:      "p1",
		Name:    "Test Provider",
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

func TestAutoDisableUntil(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	// Create provider first
	provider := &model.Provider{
		ID:      "p1",
		Name:    "Test Provider",
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
	if err := store.AutoDisableUntil(ctx, "p1", disabledUntil, "auto: circuit breaker triggered"); err != nil {
		t.Fatalf("AutoDisableUntil failed: %v", err)
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

func TestAutoDisableUntil_PreservesManualDisable(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	provider := &model.Provider{ID: "p1", Name: "Test Provider", Enabled: true}
	if err := store.CreateProvider(ctx, provider); err != nil {
		t.Fatalf("CreateProvider failed: %v", err)
	}

	now := time.Now()
	manualDisabledUntil := now.Add(30 * time.Minute)
	if err := store.UpdateHealthState(ctx, &model.HealthState{
		ProviderID:     "p1",
		Available:      false,
		DisabledUntil:  &manualDisabledUntil,
		DisabledReason: "manual: maintenance",
	}); err != nil {
		t.Fatalf("UpdateHealthState failed: %v", err)
	}

	if err := store.AutoDisableUntil(ctx, "p1", now.Add(5*time.Minute), "auto: usage limit reached"); err != nil {
		t.Fatalf("AutoDisableUntil failed: %v", err)
	}

	state, err := store.GetHealthState(ctx, "p1")
	if err != nil {
		t.Fatalf("GetHealthState failed: %v", err)
	}
	if state.DisabledReason != "manual: maintenance" {
		t.Fatalf("DisabledReason = %q, want manual disable to be preserved", state.DisabledReason)
	}
	if state.DisabledUntil == nil || !state.DisabledUntil.Equal(manualDisabledUntil) {
		t.Fatalf("DisabledUntil = %v, want %v", state.DisabledUntil, manualDisabledUntil)
	}
}

func TestAutoDisableUntil_PreservesLongerAutomaticDisable(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	provider := &model.Provider{ID: "p1", Name: "Test Provider", Enabled: true}
	if err := store.CreateProvider(ctx, provider); err != nil {
		t.Fatalf("CreateProvider failed: %v", err)
	}

	now := time.Now()
	longerDisableUntil := now.Add(30 * time.Minute)
	if err := store.AutoDisableUntil(ctx, "p1", longerDisableUntil, "auto: usage limit reached"); err != nil {
		t.Fatalf("AutoDisableUntil failed: %v", err)
	}

	if err := store.AutoDisableUntil(ctx, "p1", now.Add(5*time.Minute), "auto: circuit breaker triggered"); err != nil {
		t.Fatalf("AutoDisableUntil failed: %v", err)
	}

	state, err := store.GetHealthState(ctx, "p1")
	if err != nil {
		t.Fatalf("GetHealthState failed: %v", err)
	}
	if state.DisabledReason != "auto: usage limit reached" {
		t.Fatalf("DisabledReason = %q, want longer automatic disable to be preserved", state.DisabledReason)
	}
	if state.DisabledUntil == nil || !state.DisabledUntil.Equal(longerDisableUntil) {
		t.Fatalf("DisabledUntil = %v, want %v", state.DisabledUntil, longerDisableUntil)
	}
}

func TestAutoDisableUntil_ExtendsAutomaticDisable(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	provider := &model.Provider{ID: "p1", Name: "Test Provider", Enabled: true}
	if err := store.CreateProvider(ctx, provider); err != nil {
		t.Fatalf("CreateProvider failed: %v", err)
	}

	now := time.Now()
	if err := store.AutoDisableUntil(ctx, "p1", now.Add(5*time.Minute), "auto: circuit breaker triggered"); err != nil {
		t.Fatalf("AutoDisableUntil failed: %v", err)
	}

	longerDisableUntil := now.Add(30 * time.Minute)
	if err := store.AutoDisableUntil(ctx, "p1", longerDisableUntil, "auto: usage limit reached"); err != nil {
		t.Fatalf("AutoDisableUntil failed: %v", err)
	}

	state, err := store.GetHealthState(ctx, "p1")
	if err != nil {
		t.Fatalf("GetHealthState failed: %v", err)
	}
	if state.DisabledReason != "auto: usage limit reached" {
		t.Fatalf("DisabledReason = %q, want extending disable reason to replace the shorter one", state.DisabledReason)
	}
	if state.DisabledUntil == nil || !state.DisabledUntil.Equal(longerDisableUntil) {
		t.Fatalf("DisabledUntil = %v, want %v", state.DisabledUntil, longerDisableUntil)
	}
}

func TestAtomicRecoverIfExpired(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	// Create provider first
	provider := &model.Provider{
		ID:      "p1",
		Name:    "Test Provider",
		APIKey:  "key",
		Enabled: true,
	}
	if err := store.CreateProvider(ctx, provider); err != nil {
		t.Fatalf("CreateProvider failed: %v", err)
	}

	now := time.Now()

	// Test 1: No health state exists - should return false (no recovery needed)
	recovered, err := store.AtomicRecoverIfExpired(ctx, "p1", now)
	if err != nil {
		t.Fatalf("AtomicRecoverIfExpired failed: %v", err)
	}
	if recovered {
		t.Error("expected recovered = false for provider with no health state")
	}

	// Test 2: Provider is available - should return false
	availableState := &model.HealthState{
		ProviderID: "p1",
		Available:  true,
	}
	if err := store.UpdateHealthState(ctx, availableState); err != nil {
		t.Fatalf("UpdateHealthState failed: %v", err)
	}
	recovered, err = store.AtomicRecoverIfExpired(ctx, "p1", now)
	if err != nil {
		t.Fatalf("AtomicRecoverIfExpired failed: %v", err)
	}
	if recovered {
		t.Error("expected recovered = false for available provider")
	}

	// Test 3: Auto-disabled but not yet expired - should return false
	disabledUntil := now.Add(5 * time.Minute)
	autoDisabledState := &model.HealthState{
		ProviderID:     "p1",
		Available:      false,
		DisabledUntil:  &disabledUntil,
		DisabledReason: "auto: circuit breaker triggered",
	}
	if err := store.UpdateHealthState(ctx, autoDisabledState); err != nil {
		t.Fatalf("UpdateHealthState failed: %v", err)
	}
	recovered, err = store.AtomicRecoverIfExpired(ctx, "p1", now)
	if err != nil {
		t.Fatalf("AtomicRecoverIfExpired failed: %v", err)
	}
	if recovered {
		t.Error("expected recovered = false for non-expired auto-disable")
	}

	// Test 4: Auto-disabled and expired - should return true and recover
	expiredTime := now.Add(6 * time.Minute)
	recovered, err = store.AtomicRecoverIfExpired(ctx, "p1", expiredTime)
	if err != nil {
		t.Fatalf("AtomicRecoverIfExpired failed: %v", err)
	}
	if !recovered {
		t.Error("expected recovered = true for expired auto-disable")
	}

	// Verify state was updated
	state, err := store.GetHealthState(ctx, "p1")
	if err != nil {
		t.Fatalf("GetHealthState failed: %v", err)
	}
	if !state.Available {
		t.Error("expected Available = true after recovery")
	}
	if state.DisabledUntil != nil {
		t.Error("expected DisabledUntil = nil after recovery")
	}
	if state.DisabledReason != "" {
		t.Errorf("expected DisabledReason = \"\", got %q", state.DisabledReason)
	}

	// Test 5: Calling again should return false (already recovered)
	recovered, err = store.AtomicRecoverIfExpired(ctx, "p1", expiredTime)
	if err != nil {
		t.Fatalf("AtomicRecoverIfExpired failed: %v", err)
	}
	if recovered {
		t.Error("expected recovered = false after already recovered")
	}
}

func TestAtomicRecoverIfExpired_ManualDisable(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	// Create provider first
	provider := &model.Provider{
		ID:      "p1",
		Name:    "Test Provider",
		APIKey:  "key",
		Enabled: true,
	}
	if err := store.CreateProvider(ctx, provider); err != nil {
		t.Fatalf("CreateProvider failed: %v", err)
	}

	now := time.Now()

	// Manually disabled provider should NOT be recovered even if disabled_until is expired
	disabledUntil := now.Add(-1 * time.Minute) // Already expired
	manualDisabledState := &model.HealthState{
		ProviderID:     "p1",
		Available:      false,
		DisabledUntil:  &disabledUntil,
		DisabledReason: "manual: maintenance",
	}
	if err := store.UpdateHealthState(ctx, manualDisabledState); err != nil {
		t.Fatalf("UpdateHealthState failed: %v", err)
	}

	recovered, err := store.AtomicRecoverIfExpired(ctx, "p1", now)
	if err != nil {
		t.Fatalf("AtomicRecoverIfExpired failed: %v", err)
	}
	if recovered {
		t.Error("expected recovered = false for manually disabled provider")
	}

	// Verify state was NOT changed
	state, err := store.GetHealthState(ctx, "p1")
	if err != nil {
		t.Fatalf("GetHealthState failed: %v", err)
	}
	if state.Available {
		t.Error("expected Available = false (manual disable should not be recovered)")
	}
}
