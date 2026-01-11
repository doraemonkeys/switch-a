package selector

import (
	"context"
	"errors"
	"testing"
	"time"

	"switch-a/internal"
	"switch-a/internal/model"

	"go.uber.org/zap"
)

// mockStore implements the Store interface for testing.
type mockStore struct {
	providers []model.Provider
	groups    map[string]*model.Group
	configs   map[string]string
	err       error
}

func newMockStore() *mockStore {
	return &mockStore{
		providers: []model.Provider{},
		groups:    make(map[string]*model.Group),
		configs: map[string]string{
			"inter_group_strategy": "priority",
			"sticky_enabled":       "true",
			"sticky_ttl":           "300",
		},
	}
}

func (m *mockStore) ListProvidersByAPIType(_ context.Context, _ string) ([]model.Provider, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.providers, nil
}

func (m *mockStore) GetGroup(_ context.Context, id string) (*model.Group, error) {
	if m.err != nil {
		return nil, m.err
	}
	if g, ok := m.groups[id]; ok {
		return g, nil
	}
	return nil, errors.New("group not found")
}

func (m *mockStore) GetConfig(_ context.Context, key string) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	return m.configs[key], nil
}

func (m *mockStore) GetProvider(_ context.Context, id string) (*model.Provider, error) {
	if m.err != nil {
		return nil, m.err
	}
	for _, p := range m.providers {
		if p.ID == id {
			provider := p
			return &provider, nil
		}
	}
	return nil, nil
}

// mockHealthChecker implements the HealthChecker interface for testing.
type mockHealthChecker struct {
	available map[string]bool
}

func newMockHealthChecker() *mockHealthChecker {
	return &mockHealthChecker{
		available: make(map[string]bool),
	}
}

func (h *mockHealthChecker) RecoverIfExpired(_ context.Context, _ string) bool {
	return false // Mock does not perform recovery
}

func (h *mockHealthChecker) IsAvailable(_ context.Context, providerID string) bool {
	if available, ok := h.available[providerID]; ok {
		return available
	}
	return true // Default to available
}

func TestSelector_Select_NoProviders(t *testing.T) {
	store := newMockStore()
	clock := &mockClock{now: time.Now()}
	logger := zap.NewNop()

	sel := NewSelector(Config{
		Store:  store,
		Clock:  clock,
		Logger: logger,
	})

	req := &model.SelectRequest{
		ClientIP: "192.168.1.1",
		User:     "user1",
		APIType:  "claude",
	}

	_, err := sel.Select(context.Background(), req)
	if !errors.Is(err, internal.ErrNoProvider) {
		t.Errorf("expected ErrNoProvider, got %v", err)
	}
}

func TestSelector_Select_SingleProvider(t *testing.T) {
	store := newMockStore()
	store.providers = []model.Provider{
		{ID: "p1", Name: "Provider 1", Enabled: true},
	}

	clock := &mockClock{now: time.Now()}
	logger := zap.NewNop()

	sel := NewSelector(Config{
		Store:  store,
		Clock:  clock,
		Logger: logger,
	})

	req := &model.SelectRequest{
		ClientIP: "192.168.1.1",
		User:     "user1",
		APIType:  "claude",
	}

	provider, err := sel.Select(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider.ID != "p1" {
		t.Errorf("expected p1, got %s", provider.ID)
	}
}

func TestSelector_Select_WithStickyCache(t *testing.T) {
	store := newMockStore()
	store.providers = []model.Provider{
		{ID: "p1", Name: "Provider 1", Enabled: true, APITypes: []model.ProviderAPIType{{ProviderID: "p1", APIType: "claude"}}},
		{ID: "p2", Name: "Provider 2", Enabled: true, APITypes: []model.ProviderAPIType{{ProviderID: "p2", APIType: "claude"}}},
	}

	clock := &mockClock{now: time.Now()}
	sticky := NewMemoryStickyCache(clock)
	logger := zap.NewNop()

	sel := NewSelector(Config{
		Store:       store,
		StickyCache: sticky,
		Clock:       clock,
		Logger:      logger,
	})

	req := &model.SelectRequest{
		ClientIP: "192.168.1.1",
		User:     "user1",
		APIType:  "claude",
	}

	// Pre-populate sticky cache
	stickyKey := model.StickyKey{
		IP:      req.ClientIP,
		User:    req.User,
		APIType: req.APIType,
	}
	sticky.Set(stickyKey, "p2", 5*time.Minute)

	// Should return sticky cached provider
	provider, err := sel.Select(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider.ID != "p2" {
		t.Errorf("expected sticky p2, got %s", provider.ID)
	}
}

func TestSelector_Select_StickyExpired(t *testing.T) {
	store := newMockStore()
	store.providers = []model.Provider{
		{ID: "p1", Name: "Provider 1", Enabled: true, Priority: 1},
		{ID: "p2", Name: "Provider 2", Enabled: true, Priority: 2},
	}

	clock := &mockClock{now: time.Now()}
	sticky := NewMemoryStickyCache(clock)
	logger := zap.NewNop()

	sel := NewSelector(Config{
		Store:       store,
		StickyCache: sticky,
		Clock:       clock,
		Logger:      logger,
	})

	req := &model.SelectRequest{
		ClientIP: "192.168.1.1",
		User:     "user1",
		APIType:  "claude",
	}

	// Pre-populate sticky cache
	stickyKey := model.StickyKey{
		IP:      req.ClientIP,
		User:    req.User,
		APIType: req.APIType,
	}
	sticky.Set(stickyKey, "p2", 1*time.Minute)

	// Expire the cache
	clock.Advance(2 * time.Minute)

	// Should select by priority (p1)
	provider, err := sel.Select(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider.ID != "p1" {
		t.Errorf("expected p1 (highest priority), got %s", provider.ID)
	}
}

func TestSelector_Select_HealthFiltering(t *testing.T) {
	store := newMockStore()
	store.providers = []model.Provider{
		{ID: "p1", Name: "Provider 1", Enabled: true, Priority: 1},
		{ID: "p2", Name: "Provider 2", Enabled: true, Priority: 2},
	}

	clock := &mockClock{now: time.Now()}
	health := newMockHealthChecker()
	health.available["p1"] = false // p1 is unhealthy
	health.available["p2"] = true

	logger := zap.NewNop()

	sel := NewSelector(Config{
		Store:         store,
		HealthChecker: health,
		Clock:         clock,
		Logger:        logger,
	})

	req := &model.SelectRequest{
		ClientIP: "192.168.1.1",
		User:     "user1",
		APIType:  "claude",
	}

	provider, err := sel.Select(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider.ID != "p2" {
		t.Errorf("expected p2 (p1 unhealthy), got %s", provider.ID)
	}
}

func TestSelector_Select_ConcurrencyLimit(t *testing.T) {
	store := newMockStore()
	store.providers = []model.Provider{
		{ID: "p1", Name: "Provider 1", Enabled: true, Priority: 1, Concurrency: 1},
		{ID: "p2", Name: "Provider 2", Enabled: true, Priority: 2, Concurrency: 1},
	}

	clock := &mockClock{now: time.Now()}
	limiter := NewConcurrencyLimiter()
	logger := zap.NewNop()

	sel := NewSelector(Config{
		Store:   store,
		Limiter: limiter,
		Clock:   clock,
		Logger:  logger,
	})

	req := &model.SelectRequest{
		ClientIP: "192.168.1.1",
		User:     "user1",
		APIType:  "claude",
	}

	// First selection should get p1
	provider1, err := sel.Select(context.Background(), req)
	if err != nil {
		t.Fatalf("first select error: %v", err)
	}
	if provider1.ID != "p1" {
		t.Errorf("expected p1, got %s", provider1.ID)
	}

	// Second selection should get p2 (p1 at limit)
	provider2, err := sel.Select(context.Background(), req)
	if err != nil {
		t.Fatalf("second select error: %v", err)
	}
	if provider2.ID != "p2" {
		t.Errorf("expected p2 (p1 at limit), got %s", provider2.ID)
	}

	// Release p1
	sel.ReleaseConcurrency("p1")

	// Third selection should get p1 again
	provider3, err := sel.Select(context.Background(), req)
	if err != nil {
		t.Fatalf("third select error: %v", err)
	}
	if provider3.ID != "p1" {
		t.Errorf("expected p1 after release, got %s", provider3.ID)
	}
}

func TestSelector_SelectExcluding(t *testing.T) {
	store := newMockStore()
	store.providers = []model.Provider{
		{ID: "p1", Name: "Provider 1", Enabled: true, Priority: 1},
		{ID: "p2", Name: "Provider 2", Enabled: true, Priority: 2},
		{ID: "p3", Name: "Provider 3", Enabled: true, Priority: 3},
	}

	clock := &mockClock{now: time.Now()}
	logger := zap.NewNop()

	sel := NewSelector(Config{
		Store:  store,
		Clock:  clock,
		Logger: logger,
	})

	req := &model.SelectRequest{
		ClientIP: "192.168.1.1",
		User:     "user1",
		APIType:  "claude",
	}

	// Exclude p1 and p2
	excluded := map[string]bool{"p1": true, "p2": true}

	provider, err := sel.SelectExcluding(context.Background(), req, excluded)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider.ID != "p3" {
		t.Errorf("expected p3 (others excluded), got %s", provider.ID)
	}
}

func TestSelector_Select_WithGroups(t *testing.T) {
	g1ID := "g1"
	g2ID := "g2"

	store := newMockStore()
	store.providers = []model.Provider{
		{ID: "p1", Name: "Provider 1", Enabled: true, GroupID: &g1ID, Priority: 1},
		{ID: "p2", Name: "Provider 2", Enabled: true, GroupID: &g2ID, Priority: 1},
	}
	store.groups = map[string]*model.Group{
		"g1": {ID: "g1", Name: "Group 1", Strategy: "priority", Priority: 1, Weight: 1, Enabled: true},
		"g2": {ID: "g2", Name: "Group 2", Strategy: "priority", Priority: 2, Weight: 1, Enabled: true},
	}
	store.configs["inter_group_strategy"] = "priority"

	clock := &mockClock{now: time.Now()}
	logger := zap.NewNop()

	sel := NewSelector(Config{
		Store:  store,
		Clock:  clock,
		Logger: logger,
	})

	req := &model.SelectRequest{
		ClientIP: "192.168.1.1",
		User:     "user1",
		APIType:  "claude",
	}

	provider, err := sel.Select(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should select from g1 (higher priority)
	if provider.ID != "p1" {
		t.Errorf("expected p1 (from g1, higher priority), got %s", provider.ID)
	}
}

func TestSelector_Select_DisabledGroup(t *testing.T) {
	g1ID := "g1"
	g2ID := "g2"

	store := newMockStore()
	store.providers = []model.Provider{
		{ID: "p1", Name: "Provider 1", Enabled: true, GroupID: &g1ID, Priority: 1},
		{ID: "p2", Name: "Provider 2", Enabled: true, GroupID: &g2ID, Priority: 1},
	}
	store.groups = map[string]*model.Group{
		"g1": {ID: "g1", Name: "Group 1", Strategy: "priority", Priority: 1, Enabled: false}, // Disabled
		"g2": {ID: "g2", Name: "Group 2", Strategy: "priority", Priority: 2, Enabled: true},
	}

	clock := &mockClock{now: time.Now()}
	logger := zap.NewNop()

	sel := NewSelector(Config{
		Store:  store,
		Clock:  clock,
		Logger: logger,
	})

	req := &model.SelectRequest{
		ClientIP: "192.168.1.1",
		User:     "user1",
		APIType:  "claude",
	}

	provider, err := sel.Select(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should select from g2 (g1 disabled)
	if provider.ID != "p2" {
		t.Errorf("expected p2 (g1 disabled), got %s", provider.ID)
	}
}

func TestSelector_UpdateSticky(t *testing.T) {
	clock := &mockClock{now: time.Now()}
	sticky := NewMemoryStickyCache(clock)
	store := newMockStore()
	logger := zap.NewNop()

	sel := NewSelector(Config{
		Store:       store,
		StickyCache: sticky,
		Clock:       clock,
		Logger:      logger,
	})

	req := &model.SelectRequest{
		ClientIP: "192.168.1.1",
		User:     "user1",
		APIType:  "claude",
	}

	sel.UpdateSticky(req, "p1")

	// Verify sticky was set
	stickyKey := model.StickyKey{
		IP:      req.ClientIP,
		User:    req.User,
		APIType: req.APIType,
	}
	providerID, found := sticky.Get(stickyKey)
	if !found {
		t.Error("sticky should be set")
	}
	if providerID != "p1" {
		t.Errorf("expected p1, got %s", providerID)
	}
}

func TestSelector_StoreError(t *testing.T) {
	store := newMockStore()
	store.err = errors.New("database error")

	clock := &mockClock{now: time.Now()}
	logger := zap.NewNop()

	sel := NewSelector(Config{
		Store:  store,
		Clock:  clock,
		Logger: logger,
	})

	req := &model.SelectRequest{
		ClientIP: "192.168.1.1",
		User:     "user1",
		APIType:  "claude",
	}

	_, err := sel.Select(context.Background(), req)
	if err == nil {
		t.Error("expected error from store")
	}
}
