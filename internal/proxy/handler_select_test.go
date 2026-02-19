package proxy

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"switch-a/internal/model"
	"switch-a/internal/selector"

	"go.uber.org/zap"
)

// mockSelector implements the Selector interface for testing.
type mockSelector struct {
	selectWithMetadataFunc func(ctx context.Context, req *model.SelectRequest) (*selectResult, error)
	selectExcludingFunc    func(ctx context.Context, req *model.SelectRequest, excludeIDs map[string]bool) (*model.Provider, error)
	selectFunc             func(ctx context.Context, req *model.SelectRequest) (*model.Provider, error)

	mu            sync.Mutex
	stickyUpdates []stickyUpdate // Records all UpdateStickyWithTTL calls
}

// stickyUpdate records a single call to UpdateStickyWithTTL.
type stickyUpdate struct {
	ProviderID string
	TTL        time.Duration
}

// selectResult mirrors selector.SelectResult for testing.
type selectResult struct {
	Provider        *model.Provider
	FromStickyCache bool
}

func (m *mockSelector) Select(ctx context.Context, req *model.SelectRequest) (*model.Provider, error) {
	if m.selectFunc != nil {
		return m.selectFunc(ctx, req)
	}
	return nil, nil
}

func (m *mockSelector) SelectExcluding(ctx context.Context, req *model.SelectRequest, excludeIDs map[string]bool) (*model.Provider, error) {
	if m.selectExcludingFunc != nil {
		return m.selectExcludingFunc(ctx, req, excludeIDs)
	}
	return nil, nil
}

func (m *mockSelector) SelectWithMetadata(ctx context.Context, req *model.SelectRequest) (*selectorSelectResult, error) {
	if m.selectWithMetadataFunc != nil {
		result, err := m.selectWithMetadataFunc(ctx, req)
		if result == nil {
			return nil, err
		}
		return &selectorSelectResult{
			Provider:        result.Provider,
			FromStickyCache: result.FromStickyCache,
		}, err
	}
	return nil, nil
}

func (m *mockSelector) UpdateStickyWithTTL(_ *model.SelectRequest, providerID string, ttl time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stickyUpdates = append(m.stickyUpdates, stickyUpdate{ProviderID: providerID, TTL: ttl})
}

func (m *mockSelector) ReleaseConcurrency(_ string) {}

func (m *mockSelector) ClearConcurrency(_ string) {}

// StickyUpdatesLen returns the number of sticky updates in a thread-safe manner.
func (m *mockSelector) StickyUpdatesLen() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.stickyUpdates)
}

// mockHealthManager implements the HealthManager interface for testing.
type mockHealthManager struct {
	availableProviders map[string]bool
	recoverCalled      map[string]bool
}

func newMockHealthManager() *mockHealthManager {
	return &mockHealthManager{
		availableProviders: make(map[string]bool),
		recoverCalled:      make(map[string]bool),
	}
}

func (m *mockHealthManager) MarkSuccess(_ context.Context, _ string) {}

func (m *mockHealthManager) MarkFailure(_ context.Context, _ string, _ error) bool {
	return false
}

func (m *mockHealthManager) RecoverIfExpired(_ context.Context, providerID string) bool {
	m.recoverCalled[providerID] = true
	return false
}

func (m *mockHealthManager) IsAvailable(_ context.Context, providerID string) bool {
	available, ok := m.availableProviders[providerID]
	if !ok {
		return true // Default to available
	}
	return available
}

func (m *mockHealthManager) ManualDisable(_ context.Context, _ string, _ string) error {
	return nil
}

func (m *mockHealthManager) ManualEnable(_ context.Context, _ string) error {
	return nil
}

func (m *mockHealthManager) ResetCircuitBreaker(_ string) {}

// selectorSelectResult mirrors selector.SelectResult for the proxy.Selector interface.
type selectorSelectResult = selector.SelectResult

// TestSelectProviderWithTracking_NoSelector tests fallback mode when no selector is configured.
func TestSelectProviderWithTracking_NoSelector(t *testing.T) {
	store := newMockStore()
	store.providers = []model.Provider{
		{ID: "p1", Name: "Provider 1", Enabled: true},
		{ID: "p2", Name: "Provider 2", Enabled: true},
	}
	logger := zap.NewNop()

	handler := NewHandler(Config{
		Store:  store,
		Logger: logger,
		// No Selector configured - uses fallback mode
	})

	ctx := context.Background()
	pctx := &proxyContext{
		apiType: "claude",
		selectReq: &model.SelectRequest{
			APIType: "claude",
		},
	}

	// First attempt should select a provider
	provider, useStickyBehavior, err := handler.selectProviderWithTracking(ctx, pctx, 0, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider == nil {
		t.Fatal("expected provider to be selected")
	}
	if useStickyBehavior {
		t.Error("expected useStickyBehavior=false for fallback mode")
	}
}

// TestSelectProviderWithTracking_SelectorStickyCacheHit tests when sticky cache returns a provider.
func TestSelectProviderWithTracking_SelectorStickyCacheHit(t *testing.T) {
	store := newMockStore()
	logger := zap.NewNop()

	stickyProvider := &model.Provider{ID: "sticky-p1", Name: "Sticky Provider", Enabled: true}

	mockSel := &mockSelector{
		selectWithMetadataFunc: func(_ context.Context, _ *model.SelectRequest) (*selectResult, error) {
			return &selectResult{
				Provider:        stickyProvider,
				FromStickyCache: true,
			}, nil
		},
	}

	handler := NewHandler(Config{
		Store:    store,
		Logger:   logger,
		Selector: mockSel,
	})

	ctx := context.Background()
	pctx := &proxyContext{
		apiType: "claude",
		cfg:     &runtimeConfig{stickyMode: model.StickyModeAPIType},
		selectReq: &model.SelectRequest{
			APIType:    "claude",
			StickyMode: model.StickyModeAPIType,
		},
	}

	provider, useStickyBehavior, err := handler.selectProviderWithTracking(ctx, pctx, 0, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider == nil {
		t.Fatal("expected provider to be selected")
	}
	if provider.ID != "sticky-p1" {
		t.Errorf("expected sticky-p1, got %s", provider.ID)
	}
	if !useStickyBehavior {
		t.Error("expected useStickyBehavior=true for sticky cache hit")
	}
}

// TestSelectProviderWithTracking_ActiveProviderFallback tests when sticky cache misses but active provider is found.
func TestSelectProviderWithTracking_ActiveProviderFallback(t *testing.T) {
	store := newMockStore()
	store.providers = []model.Provider{
		{ID: "active-p1", Name: "Active Provider", Enabled: true},
	}
	logger := zap.NewNop()

	freshProvider := &model.Provider{ID: "fresh-p1", Name: "Fresh Provider", Enabled: true}

	mockSel := &mockSelector{
		selectWithMetadataFunc: func(_ context.Context, _ *model.SelectRequest) (*selectResult, error) {
			return &selectResult{
				Provider:        freshProvider,
				FromStickyCache: false, // Not from sticky cache
			}, nil
		},
	}

	// Set up active registry with an active provider
	activeRegistry := NewActiveRequestRegistry()
	activeRegistry.Register(&ActiveRequest{
		RequestID:       "req-123",
		ProviderID:      "active-p1",
		ClientIP:        "192.168.1.1",
		UserID:          "user1",
		APIType:         "claude",
		HasReceivedData: true, // Must have received data
	})

	handler := NewHandler(Config{
		Store:          store,
		Logger:         logger,
		Selector:       mockSel,
		ActiveRegistry: activeRegistry,
	})

	ctx := context.Background()
	pctx := &proxyContext{
		apiType: "claude",
		cfg:     &runtimeConfig{stickyMode: model.StickyModeAPIType},
		selectReq: &model.SelectRequest{
			ClientIP:   "192.168.1.1",
			User:       "user1",
			APIType:    "claude",
			StickyMode: model.StickyModeAPIType,
		},
	}

	provider, useStickyBehavior, err := handler.selectProviderWithTracking(ctx, pctx, 0, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider == nil {
		t.Fatal("expected provider to be selected")
	}
	if provider.ID != "active-p1" {
		t.Errorf("expected active-p1, got %s", provider.ID)
	}
	if !useStickyBehavior {
		t.Error("expected useStickyBehavior=true for active provider fallback")
	}
}

// TestSelectProviderWithTracking_NormalSelection tests normal selection without sticky.
func TestSelectProviderWithTracking_NormalSelection(t *testing.T) {
	store := newMockStore()
	logger := zap.NewNop()

	normalProvider := &model.Provider{ID: "normal-p1", Name: "Normal Provider", Enabled: true}

	mockSel := &mockSelector{
		selectWithMetadataFunc: func(_ context.Context, _ *model.SelectRequest) (*selectResult, error) {
			return &selectResult{
				Provider:        normalProvider,
				FromStickyCache: false,
			}, nil
		},
	}

	handler := NewHandler(Config{
		Store:    store,
		Logger:   logger,
		Selector: mockSel,
		// No active registry
	})

	ctx := context.Background()
	pctx := &proxyContext{
		apiType: "claude",
		cfg:     &runtimeConfig{stickyMode: model.StickyModeOff}, // Sticky disabled
		selectReq: &model.SelectRequest{
			APIType:    "claude",
			StickyMode: model.StickyModeOff,
		},
	}

	provider, useStickyBehavior, err := handler.selectProviderWithTracking(ctx, pctx, 0, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider == nil {
		t.Fatal("expected provider to be selected")
	}
	if provider.ID != "normal-p1" {
		t.Errorf("expected normal-p1, got %s", provider.ID)
	}
	if useStickyBehavior {
		t.Error("expected useStickyBehavior=false for normal selection")
	}
}

// TestSelectProviderWithTracking_RetryWithExclusion tests retry selection with excluded providers.
func TestSelectProviderWithTracking_RetryWithExclusion(t *testing.T) {
	store := newMockStore()
	logger := zap.NewNop()

	retryProvider := &model.Provider{ID: "retry-p1", Name: "Retry Provider", Enabled: true}

	mockSel := &mockSelector{
		selectWithMetadataFunc: func(_ context.Context, _ *model.SelectRequest) (*selectResult, error) {
			t.Error("SelectWithMetadata should not be called on retry")
			return nil, nil
		},
		selectExcludingFunc: func(_ context.Context, _ *model.SelectRequest, excludeIDs map[string]bool) (*model.Provider, error) {
			if !excludeIDs["failed-p1"] {
				t.Error("expected failed-p1 to be excluded")
			}
			return retryProvider, nil
		},
	}

	handler := NewHandler(Config{
		Store:    store,
		Logger:   logger,
		Selector: mockSel,
	})

	ctx := context.Background()
	pctx := &proxyContext{
		apiType: "claude",
		cfg:     &runtimeConfig{stickyMode: model.StickyModeAPIType},
		selectReq: &model.SelectRequest{
			APIType:    "claude",
			StickyMode: model.StickyModeAPIType,
		},
	}

	excluded := map[string]bool{"failed-p1": true}
	// attempt > 0 triggers SelectExcluding path
	provider, useStickyBehavior, err := handler.selectProviderWithTracking(ctx, pctx, 1, excluded)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider == nil {
		t.Fatal("expected provider to be selected")
	}
	if provider.ID != "retry-p1" {
		t.Errorf("expected retry-p1, got %s", provider.ID)
	}
	if useStickyBehavior {
		t.Error("expected useStickyBehavior=false for retry selection")
	}
}

// TestSelectProviderWithTracking_SelectorError tests error handling from selector.
func TestSelectProviderWithTracking_SelectorError(t *testing.T) {
	store := newMockStore()
	logger := zap.NewNop()

	mockSel := &mockSelector{
		selectWithMetadataFunc: func(_ context.Context, _ *model.SelectRequest) (*selectResult, error) {
			return nil, errors.New("selector error")
		},
	}

	handler := NewHandler(Config{
		Store:    store,
		Logger:   logger,
		Selector: mockSel,
	})

	ctx := context.Background()
	pctx := &proxyContext{
		apiType: "claude",
		cfg:     &runtimeConfig{stickyMode: model.StickyModeAPIType},
		selectReq: &model.SelectRequest{
			APIType:    "claude",
			StickyMode: model.StickyModeAPIType,
		},
	}

	provider, useStickyBehavior, err := handler.selectProviderWithTracking(ctx, pctx, 0, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if provider != nil {
		t.Error("expected nil provider on error")
	}
	if useStickyBehavior {
		t.Error("expected useStickyBehavior=false on error")
	}
}

// TestTryActiveProviderFallback_StickyDisabled tests that fallback is skipped when sticky is disabled.
func TestTryActiveProviderFallback_StickyDisabled(t *testing.T) {
	store := newMockStore()
	logger := zap.NewNop()

	activeRegistry := NewActiveRequestRegistry()
	activeRegistry.Register(&ActiveRequest{
		RequestID:       "req-123",
		ProviderID:      "active-p1",
		HasReceivedData: true,
	})

	handler := NewHandler(Config{
		Store:          store,
		Logger:         logger,
		ActiveRegistry: activeRegistry,
	})

	ctx := context.Background()
	pctx := &proxyContext{
		cfg: &runtimeConfig{stickyMode: model.StickyModeOff}, // Sticky disabled
		selectReq: &model.SelectRequest{
			StickyMode: model.StickyModeOff,
		},
	}

	provider := handler.tryActiveProviderFallback(ctx, pctx)
	if provider != nil {
		t.Error("expected nil when sticky is disabled")
	}
}

// TestTryActiveProviderFallback_NoActiveRegistry tests that fallback is skipped when no registry.
func TestTryActiveProviderFallback_NoActiveRegistry(t *testing.T) {
	store := newMockStore()
	logger := zap.NewNop()

	handler := NewHandler(Config{
		Store:  store,
		Logger: logger,
		// No ActiveRegistry
	})

	ctx := context.Background()
	pctx := &proxyContext{
		cfg: &runtimeConfig{stickyMode: model.StickyModeAPIType},
		selectReq: &model.SelectRequest{
			StickyMode: model.StickyModeAPIType,
		},
	}

	provider := handler.tryActiveProviderFallback(ctx, pctx)
	if provider != nil {
		t.Error("expected nil when no active registry")
	}
}

// TestTryActiveProviderFallback_NoActiveProvider tests when no matching active provider.
func TestTryActiveProviderFallback_NoActiveProvider(t *testing.T) {
	store := newMockStore()
	logger := zap.NewNop()

	activeRegistry := NewActiveRequestRegistry()
	// No active requests registered

	handler := NewHandler(Config{
		Store:          store,
		Logger:         logger,
		ActiveRegistry: activeRegistry,
	})

	ctx := context.Background()
	pctx := &proxyContext{
		cfg: &runtimeConfig{stickyMode: model.StickyModeAPIType},
		selectReq: &model.SelectRequest{
			ClientIP:   "192.168.1.1",
			User:       "user1",
			APIType:    "claude",
			StickyMode: model.StickyModeAPIType,
		},
	}

	provider := handler.tryActiveProviderFallback(ctx, pctx)
	if provider != nil {
		t.Error("expected nil when no active provider")
	}
}

func TestTryActiveProviderFallback_ModelDimension(t *testing.T) {
	store := newMockStore()
	store.providers = []model.Provider{
		{ID: "active-p1", Name: "Active Provider", Enabled: true},
	}

	handler := NewHandler(Config{
		Store:          store,
		Logger:         zap.NewNop(),
		ActiveRegistry: NewActiveRequestRegistry(),
	})

	handler.activeRegistry.Register(&ActiveRequest{
		RequestID:       "req-123",
		ProviderID:      "active-p1",
		ClientIP:        "192.168.1.1",
		UserID:          "user1",
		APIType:         "claude",
		Model:           "model-a",
		HasReceivedData: true,
	})

	ctx := context.Background()
	pctx := &proxyContext{
		apiType: "claude",
		cfg:     &runtimeConfig{stickyMode: model.StickyModeModel},
		selectReq: &model.SelectRequest{
			ClientIP:   "192.168.1.1",
			User:       "user1",
			APIType:    "claude",
			Model:      "model-b",
			StickyMode: model.StickyModeModel,
		},
	}

	if provider := handler.tryActiveProviderFallback(ctx, pctx); provider != nil {
		t.Fatal("expected nil for non-matching model in model sticky mode")
	}

	pctx.selectReq.Model = "model-a"
	provider := handler.tryActiveProviderFallback(ctx, pctx)
	if provider == nil || provider.ID != "active-p1" {
		t.Fatalf("expected active-p1 for matching model, got %#v", provider)
	}
}

// TestGetProviderIfValid_ProviderFound tests finding a valid provider.
func TestGetProviderIfValid_ProviderFound(t *testing.T) {
	store := newMockStore()
	store.providers = []model.Provider{
		{ID: "p1", Name: "Provider 1", Enabled: true},
		{ID: "p2", Name: "Provider 2", Enabled: true},
	}
	logger := zap.NewNop()

	handler := NewHandler(Config{
		Store:  store,
		Logger: logger,
	})

	ctx := context.Background()
	pctx := &proxyContext{
		apiType: "claude",
	}

	provider := handler.getProviderIfValid(ctx, "p1", pctx)
	if provider == nil {
		t.Fatal("expected provider to be found")
	}
	if provider.ID != "p1" {
		t.Errorf("expected p1, got %s", provider.ID)
	}
}

// TestGetProviderIfValid_ProviderNotFound tests when provider is not in list.
func TestGetProviderIfValid_ProviderNotFound(t *testing.T) {
	store := newMockStore()
	store.providers = []model.Provider{
		{ID: "p1", Name: "Provider 1", Enabled: true},
	}
	logger := zap.NewNop()

	handler := NewHandler(Config{
		Store:  store,
		Logger: logger,
	})

	ctx := context.Background()
	pctx := &proxyContext{
		apiType: "claude",
	}

	provider := handler.getProviderIfValid(ctx, "nonexistent", pctx)
	if provider != nil {
		t.Error("expected nil for nonexistent provider")
	}
}

// TestGetProviderIfValid_ProviderDisabled tests when provider is disabled.
func TestGetProviderIfValid_ProviderDisabled(t *testing.T) {
	store := newMockStore()
	store.providers = []model.Provider{
		{ID: "p1", Name: "Provider 1", Enabled: false}, // Disabled
	}
	logger := zap.NewNop()

	handler := NewHandler(Config{
		Store:  store,
		Logger: logger,
	})

	ctx := context.Background()
	pctx := &proxyContext{
		apiType: "claude",
	}

	provider := handler.getProviderIfValid(ctx, "p1", pctx)
	if provider != nil {
		t.Error("expected nil for disabled provider")
	}
}

// TestGetProviderIfValid_ProviderUnhealthy tests when provider is unhealthy.
func TestGetProviderIfValid_ProviderUnhealthy(t *testing.T) {
	store := newMockStore()
	store.providers = []model.Provider{
		{ID: "p1", Name: "Provider 1", Enabled: true},
	}
	logger := zap.NewNop()

	healthMgr := newMockHealthManager()
	healthMgr.availableProviders["p1"] = false // Unhealthy

	handler := NewHandler(Config{
		Store:  store,
		Logger: logger,
		Health: healthMgr,
	})

	ctx := context.Background()
	pctx := &proxyContext{
		apiType: "claude",
	}

	provider := handler.getProviderIfValid(ctx, "p1", pctx)
	if provider != nil {
		t.Error("expected nil for unhealthy provider")
	}

	// Verify RecoverIfExpired was called
	if !healthMgr.recoverCalled["p1"] {
		t.Error("expected RecoverIfExpired to be called")
	}
}

// TestGetProviderIfValid_StoreError tests when store returns an error.
func TestGetProviderIfValid_StoreError(t *testing.T) {
	store := newMockStore()
	store.err = errors.New("database error")
	logger := zap.NewNop()

	handler := NewHandler(Config{
		Store:  store,
		Logger: logger,
	})

	ctx := context.Background()
	pctx := &proxyContext{
		apiType: "claude",
	}

	provider := handler.getProviderIfValid(ctx, "p1", pctx)
	if provider != nil {
		t.Error("expected nil on store error")
	}
}

// TestSelectProviderFallback_NoProviders tests fallback when no providers available.
func TestSelectProviderFallback_NoProviders(t *testing.T) {
	store := newMockStore()
	store.providers = []model.Provider{} // Empty
	logger := zap.NewNop()

	handler := NewHandler(Config{
		Store:  store,
		Logger: logger,
	})

	ctx := context.Background()
	pctx := &proxyContext{
		apiType: "claude",
	}

	provider, err := handler.selectProviderFallback(ctx, pctx, 0, nil)
	if err == nil {
		t.Fatal("expected error for no providers")
	}
	if provider != nil {
		t.Error("expected nil provider")
	}
}

// TestSelectProviderFallback_RoundRobin tests round-robin selection across attempts.
func TestSelectProviderFallback_RoundRobin(t *testing.T) {
	store := newMockStore()
	store.providers = []model.Provider{
		{ID: "p1", Name: "Provider 1", Enabled: true},
		{ID: "p2", Name: "Provider 2", Enabled: true},
		{ID: "p3", Name: "Provider 3", Enabled: true},
	}
	logger := zap.NewNop()

	handler := NewHandler(Config{
		Store:  store,
		Logger: logger,
	})

	ctx := context.Background()
	pctx := &proxyContext{
		apiType: "claude",
	}

	// Make multiple selections and verify round-robin behavior
	seen := make(map[string]int)
	for i := 0; i < 9; i++ {
		provider, err := handler.selectProviderFallback(ctx, pctx, 0, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		seen[provider.ID]++
	}

	// Each provider should be selected roughly equally (3 times each)
	for id, count := range seen {
		if count != 3 {
			t.Errorf("provider %s selected %d times, expected 3", id, count)
		}
	}
}

// TestSelectProviderFallback_AttemptOffset tests that attempts offset the selection.
func TestSelectProviderFallback_AttemptOffset(t *testing.T) {
	store := newMockStore()
	store.providers = []model.Provider{
		{ID: "p1", Name: "Provider 1", Enabled: true},
		{ID: "p2", Name: "Provider 2", Enabled: true},
	}
	logger := zap.NewNop()

	handler := NewHandler(Config{
		Store:  store,
		Logger: logger,
	})

	ctx := context.Background()
	pctx := &proxyContext{
		apiType: "claude",
	}

	// Get first provider
	p0, _ := handler.selectProviderFallback(ctx, pctx, 0, nil)

	// Reset counter to get predictable behavior for test
	handler.fallbackCounter.Store(0)

	// Get provider with attempt offset
	p1, _ := handler.selectProviderFallback(ctx, pctx, 1, nil)

	// With 2 providers, attempt=0 and attempt=1 should give different providers
	// when starting from the same counter position
	if p0.ID == p1.ID {
		t.Error("attempt offset should select different providers")
	}
}
