package proxy

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"switch-a/internal"
	"switch-a/internal/model"
	"switch-a/internal/selector"

	"go.uber.org/zap"
)

// mockSelector implements the Selector interface for testing.
type mockSelector struct {
	selectWithMetadataFunc func(ctx context.Context, req *model.SelectRequest) (*selectResult, error)
	selectExcludingFunc    func(ctx context.Context, req *model.SelectRequest, excludeIDs map[string]bool) (*model.Provider, error)
	selectFunc             func(ctx context.Context, req *model.SelectRequest) (*model.Provider, error)

	mu                  sync.Mutex
	stickyUpdates       []stickyUpdate // Records all UpdateStickyWithTTL calls
	concurrencyReleased []string       // Records provider IDs passed to ReleaseConcurrency
}

// stickyUpdate records a single call to UpdateStickyWithTTL.
type stickyUpdate struct {
	ProviderID string
	Model      string
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

func (m *mockSelector) UpdateStickyWithTTL(req *model.SelectRequest, providerID string, ttl time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	update := stickyUpdate{ProviderID: providerID, TTL: ttl}
	if req != nil {
		update.Model = req.Model
	}
	m.stickyUpdates = append(m.stickyUpdates, update)
}

func (m *mockSelector) ReleaseConcurrency(providerID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.concurrencyReleased = append(m.concurrencyReleased, providerID)
}

func (m *mockSelector) ClearConcurrency(_ string) {}

// StickyUpdatesLen returns the number of sticky updates in a thread-safe manner.
func (m *mockSelector) StickyUpdatesLen() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.stickyUpdates)
}

// LastStickyUpdate returns the latest sticky update in a thread-safe manner.
func (m *mockSelector) LastStickyUpdate() (stickyUpdate, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.stickyUpdates) == 0 {
		return stickyUpdate{}, false
	}
	return m.stickyUpdates[len(m.stickyUpdates)-1], true
}

// mockHealthManager implements the HealthManager interface for testing.
type mockHealthManager struct {
	availableProviders map[string]bool
	recoverCalled      map[string]bool
	suspendedUntil     map[string]time.Time
	suspendReasons     map[string]string
}

func newMockHealthManager() *mockHealthManager {
	return &mockHealthManager{
		availableProviders: make(map[string]bool),
		recoverCalled:      make(map[string]bool),
		suspendedUntil:     make(map[string]time.Time),
		suspendReasons:     make(map[string]string),
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

func (m *mockHealthManager) SuspendUntil(_ context.Context, providerID string, disabledUntil time.Time, reason string) error {
	m.availableProviders[providerID] = false
	m.suspendedUntil[providerID] = disabledUntil
	m.suspendReasons[providerID] = reason
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
	selectReq := &model.SelectRequest{
		APIType: "claude",
	}

	// First attempt should select a provider
	provider, useStickyBehavior, err := handler.selectProviderWithTracking(ctx, selectReq, 0, nil)
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
	selectReq := &model.SelectRequest{
		APIType:    "claude",
		StickyMode: model.StickyModeAPIType,
	}

	provider, useStickyBehavior, err := handler.selectProviderWithTracking(ctx, selectReq, 0, nil)
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
	selectReq := &model.SelectRequest{
		ClientIP:   "192.168.1.1",
		User:       "user1",
		APIType:    "claude",
		StickyMode: model.StickyModeAPIType,
	}

	provider, useStickyBehavior, err := handler.selectProviderWithTracking(ctx, selectReq, 0, nil)
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

	// Verify that the originally selected provider's concurrency slot was released
	// to prevent counter leaks (SelectWithMetadata acquired a slot for fresh-p1,
	// but we're returning active-p1 instead).
	mockSel.mu.Lock()
	defer mockSel.mu.Unlock()
	if len(mockSel.concurrencyReleased) != 1 || mockSel.concurrencyReleased[0] != "fresh-p1" {
		t.Errorf("expected concurrency release for fresh-p1, got %v", mockSel.concurrencyReleased)
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
	selectReq := &model.SelectRequest{
		APIType:    "claude",
		StickyMode: model.StickyModeOff,
	}

	provider, useStickyBehavior, err := handler.selectProviderWithTracking(ctx, selectReq, 0, nil)
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
	selectReq := &model.SelectRequest{
		APIType:    "claude",
		StickyMode: model.StickyModeAPIType,
	}

	excluded := map[string]bool{"failed-p1": true}
	// attempt > 0 triggers SelectExcluding path
	provider, useStickyBehavior, err := handler.selectProviderWithTracking(ctx, selectReq, 1, excluded)
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

func TestSelectProviderWithTracking_FirstAttemptNilSelectionBecomesNoProvider(t *testing.T) {
	store := newMockStore()
	logger := zap.NewNop()

	handler := NewHandler(Config{
		Store:    store,
		Logger:   logger,
		Selector: &mockSelector{},
	})

	provider, useStickyBehavior, err := handler.selectProviderWithTracking(
		context.Background(),
		&model.SelectRequest{APIType: "claude"},
		0,
		nil,
	)

	if !errors.Is(err, internal.ErrNoProvider) {
		t.Fatalf("expected ErrNoProvider, got %v", err)
	}
	if provider != nil {
		t.Fatalf("expected nil provider, got %#v", provider)
	}
	if useStickyBehavior {
		t.Fatal("expected useStickyBehavior=false when selection fails")
	}
}

func TestSelectProviderWithTracking_RetryNilSelectionBecomesNoProvider(t *testing.T) {
	store := newMockStore()
	logger := zap.NewNop()

	handler := NewHandler(Config{
		Store:  store,
		Logger: logger,
		Selector: &mockSelector{
			selectExcludingFunc: func(_ context.Context, _ *model.SelectRequest, _ map[string]bool) (*model.Provider, error) {
				return nil, nil
			},
		},
	})

	provider, useStickyBehavior, err := handler.selectProviderWithTracking(
		context.Background(),
		&model.SelectRequest{APIType: "claude"},
		1,
		map[string]bool{"failed-p1": true},
	)

	if !errors.Is(err, internal.ErrNoProvider) {
		t.Fatalf("expected ErrNoProvider, got %v", err)
	}
	if provider != nil {
		t.Fatalf("expected nil provider, got %#v", provider)
	}
	if useStickyBehavior {
		t.Fatal("expected useStickyBehavior=false when retry selection fails")
	}
}

func TestSelectProviderWithTracking_RetryExcludedSelectionBecomesNoProvider(t *testing.T) {
	store := newMockStore()
	logger := zap.NewNop()

	handler := NewHandler(Config{
		Store:  store,
		Logger: logger,
		Selector: &mockSelector{
			selectExcludingFunc: func(_ context.Context, _ *model.SelectRequest, _ map[string]bool) (*model.Provider, error) {
				return &model.Provider{ID: "failed-p1", Name: "failed"}, nil
			},
		},
	})

	provider, useStickyBehavior, err := handler.selectProviderWithTracking(
		context.Background(),
		&model.SelectRequest{APIType: "claude"},
		1,
		map[string]bool{"failed-p1": true},
	)

	if !errors.Is(err, internal.ErrNoProvider) {
		t.Fatalf("expected ErrNoProvider, got %v", err)
	}
	if provider != nil {
		t.Fatalf("expected nil provider, got %#v", provider)
	}
	if useStickyBehavior {
		t.Fatal("expected useStickyBehavior=false when retry selector returns an excluded provider")
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
	selectReq := &model.SelectRequest{
		APIType:    "claude",
		StickyMode: model.StickyModeAPIType,
	}

	provider, useStickyBehavior, err := handler.selectProviderWithTracking(ctx, selectReq, 0, nil)
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
	selectReq := &model.SelectRequest{
		StickyMode: model.StickyModeOff,
	}

	provider := handler.tryActiveProviderFallback(ctx, selectReq)
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
	selectReq := &model.SelectRequest{
		StickyMode: model.StickyModeAPIType,
	}

	provider := handler.tryActiveProviderFallback(ctx, selectReq)
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
	selectReq := &model.SelectRequest{
		ClientIP:   "192.168.1.1",
		User:       "user1",
		APIType:    "claude",
		StickyMode: model.StickyModeAPIType,
	}

	provider := handler.tryActiveProviderFallback(ctx, selectReq)
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
		StickyMode:      model.StickyModeModel,
		HasReceivedData: true,
	})

	ctx := context.Background()
	selectReq := &model.SelectRequest{
		ClientIP:   "192.168.1.1",
		User:       "user1",
		APIType:    "claude",
		Model:      "model-b",
		StickyMode: model.StickyModeModel,
	}

	if provider := handler.tryActiveProviderFallback(ctx, selectReq); provider != nil {
		t.Fatal("expected nil for non-matching model in model sticky mode")
	}

	selectReq.Model = "model-a"
	provider := handler.tryActiveProviderFallback(ctx, selectReq)
	if provider == nil || provider.ID != "active-p1" {
		t.Fatalf("expected active-p1 for matching model, got %#v", provider)
	}
}

func TestTryActiveProviderFallback_SkipsProviderWithReauthRequired(t *testing.T) {
	store := newMockStore()
	store.providers = []model.Provider{
		{ID: "active-p1", Name: "Active Provider", Enabled: true},
	}
	store.authStates["active-p1"] = &model.ProviderAuthState{
		ProviderID: "active-p1",
		Status:     model.ProviderAuthStatusReauthRequired,
	}

	activeRegistry := NewActiveRequestRegistry()
	activeRegistry.Register(&ActiveRequest{
		RequestID:       "req-123",
		ProviderID:      "active-p1",
		ClientIP:        "192.168.1.1",
		UserID:          "user1",
		APIType:         "claude",
		StickyMode:      model.StickyModeAPIType,
		HasReceivedData: true,
	})

	handler := NewHandler(Config{
		Store:          store,
		Logger:         zap.NewNop(),
		ActiveRegistry: activeRegistry,
	})

	provider := handler.tryActiveProviderFallback(context.Background(), &model.SelectRequest{
		ClientIP:   "192.168.1.1",
		User:       "user1",
		APIType:    "claude",
		StickyMode: model.StickyModeAPIType,
	})
	if provider != nil {
		t.Fatalf("expected nil when active fallback provider requires reauthentication, got %#v", provider)
	}
}

func TestTryActiveProviderFallback_SkipsProviderRejectedByRoutingPolicy(t *testing.T) {
	blockedGroup := "g-blocked"

	store := newMockStore()
	store.providers = []model.Provider{
		{
			ID:       "active-p1",
			Name:     "Active Provider",
			Enabled:  true,
			GroupID:  &blockedGroup,
			APITypes: []model.ProviderAPIType{{ProviderID: "active-p1", APIType: "codex"}},
		},
	}
	store.authStates["active-p1"] = &model.ProviderAuthState{
		ProviderID: "active-p1",
		Status:     model.ProviderAuthStatusActive,
	}
	store.routingPolicies = []model.RoutingPolicy{
		{
			APIType: "codex",
			Groups:  []model.RoutingPolicyGroup{{GroupID: "g-allowed"}},
		},
	}

	activeRegistry := NewActiveRequestRegistry()
	activeRegistry.Register(&ActiveRequest{
		RequestID:       "req-123",
		ProviderID:      "active-p1",
		ClientIP:        "192.168.1.1",
		UserID:          "user1",
		APIType:         "codex",
		StickyMode:      model.StickyModeAPIType,
		HasReceivedData: true,
	})

	handler := NewHandler(Config{
		Store:          store,
		Logger:         zap.NewNop(),
		ActiveRegistry: activeRegistry,
	})

	provider := handler.tryActiveProviderFallback(context.Background(), &model.SelectRequest{
		ClientIP:   "192.168.1.1",
		User:       "user1",
		APIType:    "codex",
		StickyMode: model.StickyModeAPIType,
	})
	if provider != nil {
		t.Fatalf("expected nil when routing policy rejects the active fallback provider, got %#v", provider)
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
	scope, err := selector.NewProviderSelectionEligibility(ctx, nil, nil, &model.SelectRequest{APIType: "claude"})
	if err != nil {
		t.Fatalf("unexpected scope error: %v", err)
	}

	provider := handler.getProviderIfValid(ctx, scope, "p1")
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
	scope, err := selector.NewProviderSelectionEligibility(ctx, nil, nil, &model.SelectRequest{APIType: "claude"})
	if err != nil {
		t.Fatalf("unexpected scope error: %v", err)
	}

	provider := handler.getProviderIfValid(ctx, scope, "nonexistent")
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
	scope, err := selector.NewProviderSelectionEligibility(ctx, nil, nil, &model.SelectRequest{APIType: "claude"})
	if err != nil {
		t.Fatalf("unexpected scope error: %v", err)
	}

	provider := handler.getProviderIfValid(ctx, scope, "p1")
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
	scope, err := handler.selectionScope(ctx, &model.SelectRequest{APIType: "claude"})
	if err != nil {
		t.Fatalf("unexpected scope error: %v", err)
	}

	provider := handler.getProviderIfValid(ctx, scope, "p1")
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
	logger := zap.NewNop()

	handler := NewHandler(Config{
		Store:  store,
		Logger: logger,
	})

	ctx := context.Background()
	scope, err := handler.selectionScope(ctx, &model.SelectRequest{APIType: "claude"})
	if err != nil {
		t.Fatalf("unexpected scope error: %v", err)
	}

	store.err = errors.New("database error")

	provider := handler.getProviderIfValid(ctx, scope, "p1")
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
	selectReq := &model.SelectRequest{
		APIType: "claude",
	}

	provider, err := handler.selectProviderFallback(ctx, selectReq, 0, nil)
	if err == nil {
		t.Fatal("expected error for no providers")
	}
	if provider != nil {
		t.Error("expected nil provider")
	}
}

func TestSelectProviderFallback_FiltersByRoutingPolicyAndAuthState(t *testing.T) {
	allowedGroup := "g-allowed"
	blockedGroup := "g-blocked"

	store := newMockStore()
	store.providers = []model.Provider{
		{
			ID:       "p-allowed",
			Name:     "Allowed Provider",
			Enabled:  true,
			GroupID:  &allowedGroup,
			APITypes: []model.ProviderAPIType{{ProviderID: "p-allowed", APIType: "codex"}},
		},
		{
			ID:       "p-reauth",
			Name:     "Reauth Provider",
			Enabled:  true,
			GroupID:  &allowedGroup,
			APITypes: []model.ProviderAPIType{{ProviderID: "p-reauth", APIType: "codex"}},
		},
		{
			ID:       "p-outside",
			Name:     "Outside Policy Provider",
			Enabled:  true,
			GroupID:  &blockedGroup,
			APITypes: []model.ProviderAPIType{{ProviderID: "p-outside", APIType: "codex"}},
		},
	}
	store.authStates["p-allowed"] = &model.ProviderAuthState{
		ProviderID: "p-allowed",
		Status:     model.ProviderAuthStatusActive,
	}
	store.authStates["p-reauth"] = &model.ProviderAuthState{
		ProviderID: "p-reauth",
		Status:     model.ProviderAuthStatusReauthRequired,
	}
	store.authStates["p-outside"] = &model.ProviderAuthState{
		ProviderID: "p-outside",
		Status:     model.ProviderAuthStatusActive,
	}
	store.routingPolicies = []model.RoutingPolicy{
		{
			APIType: "codex",
			Groups:  []model.RoutingPolicyGroup{{GroupID: "g-allowed"}},
		},
	}

	handler := NewHandler(Config{
		Store:  store,
		Logger: zap.NewNop(),
	})

	provider, err := handler.selectProviderFallback(context.Background(), &model.SelectRequest{
		APIType: "codex",
	}, 0, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider == nil {
		t.Fatal("expected provider to be selected")
	}
	if provider.ID != "p-allowed" {
		t.Fatalf("provider.ID = %q, want %q", provider.ID, "p-allowed")
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
	selectReq := &model.SelectRequest{
		APIType: "claude",
	}

	// Make multiple selections and verify round-robin behavior
	seen := make(map[string]int)
	for i := 0; i < 9; i++ {
		provider, err := handler.selectProviderFallback(ctx, selectReq, 0, nil)
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
	selectReq := &model.SelectRequest{
		APIType: "claude",
	}

	// Get first provider
	p0, _ := handler.selectProviderFallback(ctx, selectReq, 0, nil)

	// Reset counter to get predictable behavior for test
	handler.fallbackCounter.Store(0)

	// Get provider with attempt offset
	p1, _ := handler.selectProviderFallback(ctx, selectReq, 1, nil)

	// With 2 providers, attempt=0 and attempt=1 should give different providers
	// when starting from the same counter position
	if p0.ID == p1.ID {
		t.Error("attempt offset should select different providers")
	}
}
