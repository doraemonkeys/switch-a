package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"switch-a/internal"
	"switch-a/internal/model"
)

// mockClock is a mock clock for testing.
type mockClock struct {
	now time.Time
}

func (c *mockClock) Now() time.Time {
	return c.now
}

func (c *mockClock) NewTicker(d time.Duration) *time.Ticker {
	return time.NewTicker(d)
}

func (c *mockClock) Advance(d time.Duration) {
	c.now = c.now.Add(d)
}

// configOnlyStore wraps an internal.Store and tracks config method calls.
type configOnlyStore struct {
	internal.Store
	configs   map[string]string
	getCount  int
	setCount  int
	setErr    error
	setsCount int
}

func newConfigOnlyStore(base internal.Store) *configOnlyStore {
	return &configOnlyStore{
		Store:   base,
		configs: make(map[string]string),
	}
}

func (s *configOnlyStore) GetConfig(ctx context.Context, key string) (string, error) {
	s.getCount++
	return s.configs[key], nil
}

func (s *configOnlyStore) SetConfig(ctx context.Context, key, value string) error {
	s.setCount++
	if s.setErr != nil {
		return s.setErr
	}
	s.configs[key] = value
	return nil
}

func (s *configOnlyStore) SetConfigs(ctx context.Context, configs map[string]string) error {
	s.setsCount++
	if s.setErr != nil {
		return s.setErr
	}
	for k, v := range configs {
		s.configs[k] = v
	}
	return nil
}

func setupCachedStoreTest(t *testing.T) (*CachedStore, *configOnlyStore, *mockClock) {
	t.Helper()

	// Create temporary SQLite store as base
	sqlStore, err := NewSQLiteStore(":memory:", internal.RealClock{})
	if err != nil {
		t.Fatalf("failed to create SQLite store: %v", err)
	}
	t.Cleanup(func() { _ = sqlStore.Close() })

	// Wrap with config tracking
	mock := newConfigOnlyStore(sqlStore)
	mock.configs["key1"] = "value1"
	mock.configs["key2"] = "value2"

	clock := &mockClock{now: time.Now()}
	cached := NewCachedStore(CachedStoreConfig{
		Store:    mock,
		CacheTTL: 5 * time.Second,
		Clock:    clock,
	})

	return cached, mock, clock
}

func TestCachedStore_GetConfig_CachesValue(t *testing.T) {
	cached, mock, _ := setupCachedStoreTest(t)
	ctx := context.Background()

	// First call should hit the store
	val, err := cached.GetConfig(ctx, "key1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "value1" {
		t.Errorf("expected 'value1', got '%s'", val)
	}
	if mock.getCount != 1 {
		t.Errorf("expected 1 store call, got %d", mock.getCount)
	}

	// Second call should use cache
	val, err = cached.GetConfig(ctx, "key1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "value1" {
		t.Errorf("expected 'value1', got '%s'", val)
	}
	if mock.getCount != 1 {
		t.Errorf("expected still 1 store call (cached), got %d", mock.getCount)
	}
}

func TestCachedStore_GetConfig_ExpiresAfterTTL(t *testing.T) {
	cached, mock, clock := setupCachedStoreTest(t)
	ctx := context.Background()

	// First call caches the value
	_, _ = cached.GetConfig(ctx, "key1")
	if mock.getCount != 1 {
		t.Errorf("expected 1 store call, got %d", mock.getCount)
	}

	// Advance time past TTL
	clock.Advance(6 * time.Second)

	// Update the underlying value
	mock.configs["key1"] = "value2"

	// Should hit the store again due to expiration
	val, err := cached.GetConfig(ctx, "key1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "value2" {
		t.Errorf("expected 'value2', got '%s'", val)
	}
	if mock.getCount != 2 {
		t.Errorf("expected 2 store calls after expiry, got %d", mock.getCount)
	}
}

func TestCachedStore_SetConfig_InvalidatesCache(t *testing.T) {
	cached, mock, _ := setupCachedStoreTest(t)
	ctx := context.Background()

	// Cache the value
	_, _ = cached.GetConfig(ctx, "key1")
	if mock.getCount != 1 {
		t.Errorf("expected 1 store call, got %d", mock.getCount)
	}

	// Update via SetConfig
	err := cached.SetConfig(ctx, "key1", "newvalue")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Cache should be invalidated, next read should hit store
	val, _ := cached.GetConfig(ctx, "key1")
	if val != "newvalue" {
		t.Errorf("expected 'newvalue', got '%s'", val)
	}
	if mock.getCount != 2 {
		t.Errorf("expected 2 store calls after invalidation, got %d", mock.getCount)
	}
}

func TestCachedStore_SetConfigs_InvalidatesAllKeys(t *testing.T) {
	cached, mock, _ := setupCachedStoreTest(t)
	ctx := context.Background()

	// Cache both values
	_, _ = cached.GetConfig(ctx, "key1")
	_, _ = cached.GetConfig(ctx, "key2")
	if mock.getCount != 2 {
		t.Errorf("expected 2 store calls, got %d", mock.getCount)
	}

	// Update both via SetConfigs
	err := cached.SetConfigs(ctx, map[string]string{
		"key1": "new1",
		"key2": "new2",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Both should be invalidated
	val1, _ := cached.GetConfig(ctx, "key1")
	val2, _ := cached.GetConfig(ctx, "key2")
	if val1 != "new1" {
		t.Errorf("expected 'new1', got '%s'", val1)
	}
	if val2 != "new2" {
		t.Errorf("expected 'new2', got '%s'", val2)
	}
	if mock.getCount != 4 {
		t.Errorf("expected 4 store calls after invalidation, got %d", mock.getCount)
	}
}

func TestCachedStore_InvalidateConfig(t *testing.T) {
	cached, mock, _ := setupCachedStoreTest(t)
	ctx := context.Background()

	// Cache the value
	_, _ = cached.GetConfig(ctx, "key1")
	if mock.getCount != 1 {
		t.Errorf("expected 1 store call, got %d", mock.getCount)
	}

	// Manually invalidate
	cached.InvalidateConfig("key1")

	// Should hit store again
	mock.configs["key1"] = "updated"
	val, _ := cached.GetConfig(ctx, "key1")
	if val != "updated" {
		t.Errorf("expected 'updated', got '%s'", val)
	}
	if mock.getCount != 2 {
		t.Errorf("expected 2 store calls after invalidation, got %d", mock.getCount)
	}
}

func TestCachedStore_InvalidateAllConfig(t *testing.T) {
	cached, mock, _ := setupCachedStoreTest(t)
	ctx := context.Background()

	// Cache both values
	_, _ = cached.GetConfig(ctx, "key1")
	_, _ = cached.GetConfig(ctx, "key2")
	if mock.getCount != 2 {
		t.Errorf("expected 2 store calls, got %d", mock.getCount)
	}

	// Invalidate all
	cached.InvalidateAllConfig()

	// Both should hit store again
	_, _ = cached.GetConfig(ctx, "key1")
	_, _ = cached.GetConfig(ctx, "key2")
	if mock.getCount != 4 {
		t.Errorf("expected 4 store calls after invalidation, got %d", mock.getCount)
	}
}

func TestCachedStore_SetConfig_ErrorDoesNotInvalidate(t *testing.T) {
	cached, mock, _ := setupCachedStoreTest(t)
	mock.setErr = errors.New("write failed")
	ctx := context.Background()

	// Cache the value
	_, _ = cached.GetConfig(ctx, "key1")
	if mock.getCount != 1 {
		t.Errorf("expected 1 store call, got %d", mock.getCount)
	}

	// Try to update (should fail)
	err := cached.SetConfig(ctx, "key1", "newvalue")
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// Cache should still be valid (not invalidated on error)
	val, _ := cached.GetConfig(ctx, "key1")
	if val != "value1" {
		t.Errorf("expected cached 'value1', got '%s'", val)
	}
	if mock.getCount != 1 {
		t.Errorf("expected still 1 store call (cached), got %d", mock.getCount)
	}
}

func TestCachedStore_DefaultTTL(t *testing.T) {
	sqlStore, err := NewSQLiteStore(":memory:", internal.RealClock{})
	if err != nil {
		t.Fatalf("failed to create SQLite store: %v", err)
	}
	defer sqlStore.Close()

	cached := NewCachedStore(CachedStoreConfig{
		Store: sqlStore,
		// CacheTTL not set, should use default
	})

	if cached.cacheTTL != DefaultConfigCacheTTL {
		t.Errorf("expected default TTL %v, got %v", DefaultConfigCacheTTL, cached.cacheTTL)
	}
}

func TestCachedStore_GetConfig_StoreError(t *testing.T) {
	sqlStore, err := NewSQLiteStore(":memory:", internal.RealClock{})
	if err != nil {
		t.Fatalf("failed to create SQLite store: %v", err)
	}
	defer sqlStore.Close()

	// Create a mock that returns error on GetConfig
	mock := &errorOnGetStore{Store: sqlStore, getErr: errors.New("db connection failed")}
	clock := &mockClock{now: time.Now()}
	cached := NewCachedStore(CachedStoreConfig{
		Store:    mock,
		CacheTTL: 5 * time.Second,
		Clock:    clock,
	})

	ctx := context.Background()

	// Should return error from store
	_, err = cached.GetConfig(ctx, "key1")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "db connection failed" {
		t.Errorf("expected 'db connection failed', got '%s'", err.Error())
	}
}

func TestCachedStore_SetConfigs_StoreError(t *testing.T) {
	cached, mock, _ := setupCachedStoreTest(t)
	mock.setErr = errors.New("write failed")
	ctx := context.Background()

	// Cache the value first
	_, _ = cached.GetConfig(ctx, "key1")

	// Try to update (should fail)
	err := cached.SetConfigs(ctx, map[string]string{"key1": "newvalue"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// Cache should still be valid (not invalidated on error)
	val, _ := cached.GetConfig(ctx, "key1")
	if val != "value1" {
		t.Errorf("expected cached 'value1', got '%s'", val)
	}
}

// errorOnGetStore is a mock store that returns error on GetConfig.
type errorOnGetStore struct {
	internal.Store
	getErr error
}

func (s *errorOnGetStore) GetConfig(ctx context.Context, key string) (string, error) {
	return "", s.getErr
}

// Ensure CachedStore still passes through other Store methods
func TestCachedStore_PassthroughMethods(t *testing.T) {
	sqlStore, err := NewSQLiteStore(":memory:", internal.RealClock{})
	if err != nil {
		t.Fatalf("failed to create SQLite store: %v", err)
	}
	defer sqlStore.Close()

	cached := NewCachedStore(CachedStoreConfig{
		Store: sqlStore,
	})

	ctx := context.Background()

	// Test that provider operations pass through
	provider := &model.Provider{
		ID:      "test-provider",
		Name:    "Test Provider",
		BaseURL: "https://api.example.com",
		Enabled: true,
	}

	err = cached.CreateProvider(ctx, provider)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}

	// Verify it was created
	got, err := cached.GetProvider(ctx, "test-provider")
	if err != nil {
		t.Fatalf("failed to get provider: %v", err)
	}
	if got.Name != "Test Provider" {
		t.Errorf("expected provider name 'Test Provider', got '%s'", got.Name)
	}
}
