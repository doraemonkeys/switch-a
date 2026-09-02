package selector

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/model"

	"go.uber.org/zap"
)

// mockStore implements the Store interface for testing.
type mockStore struct {
	providers        []model.Provider
	groups           map[string]*model.Group
	configs          map[string]string
	authStates       map[string]*credentialsession.AuthState
	routingPolicies  []model.RoutingPolicy
	err              error
	authStateErr     error
	routingPolicyErr error
}

func newMockStore() *mockStore {
	return &mockStore{
		providers: []model.Provider{},
		groups:    make(map[string]*model.Group),
		configs: map[string]string{
			ConfigKeyRootCandidateStrategy: "priority",
			"sticky_mode":                  "model",
			"sticky_ttl":                   "300",
		},
		authStates: make(map[string]*credentialsession.AuthState),
	}
}

func stringPtr(value string) *string {
	return &value
}

func (m *mockStore) ListProvidersByAPIType(_ context.Context, apiType string) ([]model.Provider, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.authStateErr != nil {
		return nil, m.authStateErr
	}
	providers := make([]model.Provider, len(m.providers))
	for index := range m.providers {
		providers[index] = m.credentialSessionProvider(m.providers[index], apiType)
	}
	return providers, nil
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
	if m.authStateErr != nil {
		return nil, m.authStateErr
	}
	for _, p := range m.providers {
		if p.ID == id {
			apiType := ""
			if len(p.APITypes) > 0 {
				apiType = p.APITypes[0].APIType
			}
			provider := m.credentialSessionProvider(p, apiType)
			return &provider, nil
		}
	}
	return nil, nil
}

func (m *mockStore) credentialSessionProvider(provider model.Provider, apiType string) model.Provider {
	provider = *cloneProviderSelectionSnapshot(&provider)
	if apiType == "" && len(provider.APITypes) > 0 {
		apiType = provider.APITypes[0].APIType
	}
	if _, ok := provider.CredentialSessionForAPIType(apiType); ok {
		return provider
	}
	kind := credentialsession.KindAPIKey
	secret := "test-secret"
	status := credentialsession.DefaultAuthStatus(kind)
	if authState := m.authStates[provider.ID]; authState != nil {
		status = authState.Status
	}
	digest := sha256.Sum256([]byte("test-subject-" + provider.ID))
	subject, _ := credentialsession.KeyedDigestSubject("test-hmac", digest[:])
	provider.CredentialSessions = append(provider.CredentialSessions, credentialsession.RouteSnapshot{
		RouteTargetID: provider.ID,
		APIType:       apiType,
		VendorScope:   provider.Vendor,
		Credential: credentialsession.Snapshot{
			SessionID:  "test-session-" + provider.ID + "-" + apiType,
			Kind:       kind,
			SecretData: secret,
			Version:    1,
			Subject:    subject,
			AuthState:  credentialsession.AuthState{Status: status},
		},
	})
	for index := range provider.APITypes {
		if provider.APITypes[index].APIType == apiType && provider.APITypes[index].BaseURL == "" {
			provider.APITypes[index].BaseURL = "https://" + provider.ID + ".example.test"
		}
	}
	return provider
}

func (m *mockStore) ListRoutingPoliciesByAPIType(_ context.Context, apiType string) ([]model.RoutingPolicy, error) {
	if m.routingPolicyErr != nil {
		return nil, m.routingPolicyErr
	}
	if m.err != nil {
		return nil, m.err
	}
	policies := make([]model.RoutingPolicy, 0, len(m.routingPolicies))
	for _, policy := range m.routingPolicies {
		if policy.APIType == apiType {
			policies = append(policies, policy)
		}
	}
	return policies, nil
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

// mockStoreWithDisabledProvider wraps mockStore and returns a disabled provider for specific ID.
type mockStoreWithDisabledProvider struct {
	*mockStore
	disabledProviderID string
}

func (m *mockStoreWithDisabledProvider) GetProvider(_ context.Context, id string) (*model.Provider, error) {
	if id == m.disabledProviderID {
		return &model.Provider{
			ID:       id,
			Name:     "Disabled Provider",
			Enabled:  false,
			APITypes: []model.ProviderAPIType{{ProviderID: id, APIType: "claude"}},
		}, nil
	}
	return m.mockStore.GetProvider(context.Background(), id)
}

// mockStoreWithWrongAPIType wraps mockStore and returns a provider with wrong API type for specific ID.
type mockStoreWithWrongAPIType struct {
	*mockStore
	wrongTypeID string
}

func (m *mockStoreWithWrongAPIType) GetProvider(_ context.Context, id string) (*model.Provider, error) {
	if id == m.wrongTypeID {
		return &model.Provider{
			ID:       id,
			Name:     "Wrong API Type Provider",
			Enabled:  true,
			APITypes: []model.ProviderAPIType{{ProviderID: id, APIType: "different"}}, // Not "claude"
		}, nil
	}
	return m.mockStore.GetProvider(context.Background(), id)
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

	_, err := sel.selectForTest(t, context.Background(), req)
	if !errors.Is(err, internal.ErrNoProvider) {
		t.Errorf("expected ErrNoProvider, got %v", err)
	}
}

func TestSelector_Select_SingleProvider(t *testing.T) {
	store := newMockStore()
	store.providers = []model.Provider{
		{ID: "p1", Name: "Provider 1", Enabled: true, APITypes: []model.ProviderAPIType{{ProviderID: "p1", APIType: "claude"}}},
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

	provider, err := sel.selectForTest(t, context.Background(), req)
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
		ClientIP:   "192.168.1.1",
		User:       "user1",
		APIType:    "claude",
		StickyMode: model.StickyModeAPIType,
	}

	// Pre-populate sticky cache
	stickyKey := model.StickyKey{
		IP:      req.ClientIP,
		User:    req.User,
		APIType: req.APIType,
	}
	sticky.Set(stickyKey, "p2", 5*time.Minute)

	// Should return sticky cached provider
	provider, err := sel.selectForTest(t, context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider.ID != "p2" {
		t.Errorf("expected sticky p2, got %s", provider.ID)
	}
}

func TestSelector_Select_WithModelStickyCache(t *testing.T) {
	store := newMockStore()
	store.providers = []model.Provider{
		{ID: "p1", Name: "Provider 1", Enabled: true, Priority: 1, APITypes: []model.ProviderAPIType{{ProviderID: "p1", APIType: "claude"}}},
		{ID: "p2", Name: "Provider 2", Enabled: true, Priority: 2, APITypes: []model.ProviderAPIType{{ProviderID: "p2", APIType: "claude"}}},
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
		ClientIP:   "192.168.1.1",
		User:       "user1",
		APIType:    "claude",
		Model:      "claude-3-opus",
		StickyMode: model.StickyModeModel,
	}

	sticky.Set(model.StickyKey{
		IP:      req.ClientIP,
		User:    req.User,
		APIType: req.APIType,
		Model:   req.Model,
	}, "p2", 5*time.Minute)

	provider, err := sel.selectForTest(t, context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider.ID != "p2" {
		t.Errorf("expected sticky p2, got %s", provider.ID)
	}

	// Different model should not hit the cached key when sticky mode is model.
	req.Model = "claude-3-haiku"
	provider, err = sel.selectForTest(t, context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider.ID != "p1" {
		t.Errorf("expected p1 for different model key, got %s", provider.ID)
	}
}

func TestSelector_Select_StickyDisabledByConfig(t *testing.T) {
	store := newMockStore()
	store.providers = []model.Provider{
		{ID: "p1", Name: "Provider 1", Enabled: true, Priority: 1, APITypes: []model.ProviderAPIType{{ProviderID: "p1", APIType: "claude"}}},
		{ID: "p2", Name: "Provider 2", Enabled: true, Priority: 2, APITypes: []model.ProviderAPIType{{ProviderID: "p2", APIType: "claude"}}},
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
		ClientIP:   "192.168.1.1",
		User:       "user1",
		APIType:    "claude",
		StickyMode: model.StickyModeOff, // Sticky sessions disabled (pre-loaded from config by caller)
	}

	// Pre-populate sticky cache with p2
	stickyKey := model.StickyKey{
		IP:      req.ClientIP,
		User:    req.User,
		APIType: req.APIType,
	}
	sticky.Set(stickyKey, "p2", 5*time.Minute)

	// Should skip sticky cache and select by priority (p1)
	provider, err := sel.selectForTest(t, context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider.ID != "p1" {
		t.Errorf("expected p1 (sticky disabled), got %s", provider.ID)
	}
}

func TestSelector_Select_StickyModeWithNilCache(t *testing.T) {
	store := newMockStore()
	store.providers = []model.Provider{
		{ID: "p1", Name: "Provider 1", Enabled: true, Priority: 1, APITypes: []model.ProviderAPIType{{ProviderID: "p1", APIType: "claude"}}},
		{ID: "p2", Name: "Provider 2", Enabled: true, Priority: 2, APITypes: []model.ProviderAPIType{{ProviderID: "p2", APIType: "claude"}}},
	}

	clock := &mockClock{now: time.Now()}
	logger := zap.NewNop()

	// Create selector without StickyCache (nil)
	sel := NewSelector(Config{
		Store:       store,
		StickyCache: nil, // No sticky cache configured
		Clock:       clock,
		Logger:      logger,
	})

	req := &model.SelectRequest{
		ClientIP:   "192.168.1.1",
		User:       "user1",
		APIType:    "claude",
		StickyMode: model.StickyModeAPIType, // Sticky enabled but no cache configured
	}

	// Should gracefully fall back to priority selection (p1)
	provider, err := sel.selectForTest(t, context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider.ID != "p1" {
		t.Errorf("expected p1 (fallback to priority), got %s", provider.ID)
	}
}

func TestSelector_Select_StickyExpired(t *testing.T) {
	store := newMockStore()
	store.providers = []model.Provider{
		{ID: "p1", Name: "Provider 1", Enabled: true, Priority: 1, APITypes: []model.ProviderAPIType{{ProviderID: "p1", APIType: "claude"}}},
		{ID: "p2", Name: "Provider 2", Enabled: true, Priority: 2, APITypes: []model.ProviderAPIType{{ProviderID: "p2", APIType: "claude"}}},
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
		ClientIP:   "192.168.1.1",
		User:       "user1",
		APIType:    "claude",
		StickyMode: model.StickyModeAPIType,
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
	provider, err := sel.selectForTest(t, context.Background(), req)
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
		{ID: "p1", Name: "Provider 1", Enabled: true, Priority: 1, APITypes: []model.ProviderAPIType{{ProviderID: "p1", APIType: "claude"}}},
		{ID: "p2", Name: "Provider 2", Enabled: true, Priority: 2, APITypes: []model.ProviderAPIType{{ProviderID: "p2", APIType: "claude"}}},
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

	provider, err := sel.selectForTest(t, context.Background(), req)
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
		{ID: "p1", Name: "Provider 1", Enabled: true, Priority: 1, Concurrency: 1, APITypes: []model.ProviderAPIType{{ProviderID: "p1", APIType: "claude"}}},
		{ID: "p2", Name: "Provider 2", Enabled: true, Priority: 2, Concurrency: 1, APITypes: []model.ProviderAPIType{{ProviderID: "p2", APIType: "claude"}}},
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
	selection1, err := sel.SelectWithMetadata(context.Background(), req)
	if err != nil {
		t.Fatalf("first select error: %v", err)
	}
	provider1 := selection1.Provider()
	if provider1.ID != "p1" {
		t.Errorf("expected p1, got %s", provider1.ID)
	}

	// Second selection should get p2 (p1 at limit)
	selection2, err := sel.SelectWithMetadata(context.Background(), req)
	if err != nil {
		t.Fatalf("second select error: %v", err)
	}
	t.Cleanup(func() { selection2.Lease.Release() })
	provider2 := selection2.Provider()
	if provider2.ID != "p2" {
		t.Errorf("expected p2 (p1 at limit), got %s", provider2.ID)
	}

	// Releasing the exact first capability restores only p1's capacity.
	selection1.Lease.Release()

	// Third selection should get p1 again
	provider3, err := sel.selectForTest(t, context.Background(), req)
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
		{ID: "p1", Name: "Provider 1", Enabled: true, Priority: 1, APITypes: []model.ProviderAPIType{{ProviderID: "p1", APIType: "claude"}}},
		{ID: "p2", Name: "Provider 2", Enabled: true, Priority: 2, APITypes: []model.ProviderAPIType{{ProviderID: "p2", APIType: "claude"}}},
		{ID: "p3", Name: "Provider 3", Enabled: true, Priority: 3, APITypes: []model.ProviderAPIType{{ProviderID: "p3", APIType: "claude"}}},
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

	provider, err := sel.selectExcludingForTest(t, context.Background(), req, excluded)
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
		{ID: "p1", Name: "Provider 1", Enabled: true, GroupID: &g1ID, Priority: 1, APITypes: []model.ProviderAPIType{{ProviderID: "p1", APIType: "claude"}}},
		{ID: "p2", Name: "Provider 2", Enabled: true, GroupID: &g2ID, Priority: 1, APITypes: []model.ProviderAPIType{{ProviderID: "p2", APIType: "claude"}}},
	}
	store.groups = map[string]*model.Group{
		"g1": {ID: "g1", Name: "Group 1", Strategy: "priority", Priority: 1, Weight: 1, Enabled: true},
		"g2": {ID: "g2", Name: "Group 2", Strategy: "priority", Priority: 2, Weight: 1, Enabled: true},
	}
	store.configs[ConfigKeyRootCandidateStrategy] = StrategyPriority

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

	provider, err := sel.selectForTest(t, context.Background(), req)
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
		{ID: "p1", Name: "Provider 1", Enabled: true, GroupID: &g1ID, Priority: 1, APITypes: []model.ProviderAPIType{{ProviderID: "p1", APIType: "claude"}}},
		{ID: "p2", Name: "Provider 2", Enabled: true, GroupID: &g2ID, Priority: 1, APITypes: []model.ProviderAPIType{{ProviderID: "p2", APIType: "claude"}}},
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

	provider, err := sel.selectForTest(t, context.Background(), req)
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
		ClientIP:   "192.168.1.1",
		User:       "user1",
		APIType:    "claude",
		StickyMode: model.StickyModeAPIType,
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

	_, err := sel.selectForTest(t, context.Background(), req)
	if err == nil {
		t.Error("expected error from store")
	}
}

func TestSelector_ConfigError_DefaultsToStrategy(t *testing.T) {
	store := newMockStore()
	store.providers = []model.Provider{
		{ID: "p1", Name: "Provider 1", Enabled: true, Priority: 1, APITypes: []model.ProviderAPIType{{ProviderID: "p1", APIType: "claude"}}},
		{ID: "p2", Name: "Provider 2", Enabled: true, Priority: 2, APITypes: []model.ProviderAPIType{{ProviderID: "p2", APIType: "claude"}}},
	}
	// Remove the root strategy config to trigger the default.
	delete(store.configs, ConfigKeyRootCandidateStrategy)

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

	provider, err := sel.selectForTest(t, context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should use default priority strategy
	if provider.ID != "p1" {
		t.Errorf("expected p1 (highest priority), got %s", provider.ID)
	}
}

func TestSelector_StickyCache_ProviderDisabled(t *testing.T) {
	store := newMockStore()
	// In real store, ListProvidersByAPIType only returns enabled providers
	// So only p2 is in the list. p1 is accessed via GetProvider for sticky check.
	store.providers = []model.Provider{
		{ID: "p2", Name: "Provider 2", Enabled: true, Priority: 1, APITypes: []model.ProviderAPIType{{ProviderID: "p2", APIType: "claude"}}},
	}

	clock := &mockClock{now: time.Now()}
	sticky := NewMemoryStickyCache(clock)
	logger := zap.NewNop()

	// Create a custom store that returns disabled provider for GetProvider
	customStore := &mockStoreWithDisabledProvider{
		mockStore:          store,
		disabledProviderID: "p1",
	}

	sel := NewSelector(Config{
		Store:       customStore,
		StickyCache: sticky,
		Clock:       clock,
		Logger:      logger,
	})

	req := &model.SelectRequest{
		ClientIP:   "192.168.1.1",
		User:       "user1",
		APIType:    "claude",
		StickyMode: model.StickyModeAPIType,
	}

	// Pre-populate sticky cache with disabled provider
	stickyKey := model.StickyKey{
		IP:      req.ClientIP,
		User:    req.User,
		APIType: req.APIType,
	}
	sticky.Set(stickyKey, "p1", 5*time.Minute)

	// Should skip disabled sticky provider and select p2
	provider, err := sel.selectForTest(t, context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider.ID != "p2" {
		t.Errorf("expected p2, got %s", provider.ID)
	}

	// Sticky cache should have been deleted for disabled provider
	_, found := sticky.Get(stickyKey)
	if found {
		t.Error("expected sticky cache to be deleted for disabled provider")
	}
}

func TestSelector_StickyCache_ProviderWrongAPIType(t *testing.T) {
	store := newMockStore()
	// In real store, ListProvidersByAPIType only returns providers supporting the API type
	// So only p2 is in the list for "claude" API type. p1 is accessed via GetProvider for sticky check.
	store.providers = []model.Provider{
		{ID: "p2", Name: "Provider 2", Enabled: true, Priority: 1, APITypes: []model.ProviderAPIType{{ProviderID: "p2", APIType: "claude"}}},
	}

	clock := &mockClock{now: time.Now()}
	sticky := NewMemoryStickyCache(clock)
	logger := zap.NewNop()

	// Create a custom store that returns wrong API type for GetProvider
	customStore := &mockStoreWithWrongAPIType{
		mockStore:   store,
		wrongTypeID: "p1",
	}

	sel := NewSelector(Config{
		Store:       customStore,
		StickyCache: sticky,
		Clock:       clock,
		Logger:      logger,
	})

	req := &model.SelectRequest{
		ClientIP:   "192.168.1.1",
		User:       "user1",
		APIType:    "claude",
		StickyMode: model.StickyModeAPIType,
	}

	// Pre-populate sticky cache with provider that has wrong API type
	stickyKey := model.StickyKey{
		IP:      req.ClientIP,
		User:    req.User,
		APIType: req.APIType,
	}
	sticky.Set(stickyKey, "p1", 5*time.Minute)

	// Should skip sticky provider with wrong API type and select p2
	provider, err := sel.selectForTest(t, context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider.ID != "p2" {
		t.Errorf("expected p2, got %s", provider.ID)
	}

	// Sticky cache should have been deleted for wrong API type provider
	_, found := sticky.Get(stickyKey)
	if found {
		t.Error("expected sticky cache to be deleted for provider with wrong API type")
	}
}

func TestSelector_StickyCache_HealthCheck(t *testing.T) {
	store := newMockStore()
	store.providers = []model.Provider{
		{ID: "p1", Name: "Provider 1", Enabled: true, APITypes: []model.ProviderAPIType{{ProviderID: "p1", APIType: "claude"}}},
		{ID: "p2", Name: "Provider 2", Enabled: true, Priority: 1, APITypes: []model.ProviderAPIType{{ProviderID: "p2", APIType: "claude"}}},
	}

	clock := &mockClock{now: time.Now()}
	sticky := NewMemoryStickyCache(clock)
	health := newMockHealthChecker()
	health.available["p1"] = false // Sticky provider is unhealthy
	health.available["p2"] = true
	logger := zap.NewNop()

	sel := NewSelector(Config{
		Store:         store,
		StickyCache:   sticky,
		HealthChecker: health,
		Clock:         clock,
		Logger:        logger,
	})

	req := &model.SelectRequest{
		ClientIP:   "192.168.1.1",
		User:       "user1",
		APIType:    "claude",
		StickyMode: model.StickyModeAPIType,
	}

	// Pre-populate sticky cache with unhealthy provider
	stickyKey := model.StickyKey{
		IP:      req.ClientIP,
		User:    req.User,
		APIType: req.APIType,
	}
	sticky.Set(stickyKey, "p1", 5*time.Minute)

	// Should skip unhealthy sticky provider and select p2
	provider, err := sel.selectForTest(t, context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider.ID != "p2" {
		t.Errorf("expected p2, got %s", provider.ID)
	}

	// Sticky cache should have been deleted
	_, found := sticky.Get(stickyKey)
	if found {
		t.Error("expected sticky cache to be deleted for unhealthy provider")
	}
}

func TestSelector_StickyCache_ConcurrencyLimit(t *testing.T) {
	store := newMockStore()
	store.providers = []model.Provider{
		{ID: "p1", Name: "Provider 1", Enabled: true, Concurrency: 1, APITypes: []model.ProviderAPIType{{ProviderID: "p1", APIType: "claude"}}},
		{ID: "p2", Name: "Provider 2", Enabled: true, Priority: 1, APITypes: []model.ProviderAPIType{{ProviderID: "p2", APIType: "claude"}}},
	}

	clock := &mockClock{now: time.Now()}
	sticky := NewMemoryStickyCache(clock)
	limiter := NewConcurrencyLimiter()
	// Consume p1's concurrency slot
	heldLease, acquired := limiter.Acquire("p1", 1)
	if !acquired {
		t.Fatal("failed to prefill p1 concurrency")
	}
	t.Cleanup(func() { heldLease.Release() })
	logger := zap.NewNop()

	sel := NewSelector(Config{
		Store:       store,
		StickyCache: sticky,
		Limiter:     limiter,
		Clock:       clock,
		Logger:      logger,
	})

	req := &model.SelectRequest{
		ClientIP:   "192.168.1.1",
		User:       "user1",
		APIType:    "claude",
		StickyMode: model.StickyModeAPIType,
	}

	// Pre-populate sticky cache with provider at concurrency limit
	stickyKey := model.StickyKey{
		IP:      req.ClientIP,
		User:    req.User,
		APIType: req.APIType,
	}
	sticky.Set(stickyKey, "p1", 5*time.Minute)

	// Should skip sticky provider at limit and select p2
	provider, err := sel.selectForTest(t, context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider.ID != "p2" {
		t.Errorf("expected p2, got %s", provider.ID)
	}
}

func TestSelector_GroupLoadError(t *testing.T) {
	g1ID := "g1"

	store := newMockStore()
	store.providers = []model.Provider{
		{ID: "p1", Name: "Provider 1", Enabled: true, GroupID: &g1ID},
	}
	// Don't add group to mock store - will cause error

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

	// Fail-closed: provider should be skipped when group load fails
	_, err := sel.selectForTest(t, context.Background(), req)
	if err != internal.ErrNoProvider {
		t.Errorf("expected ErrNoProvider, got %v", err)
	}
}

func TestSelector_UpdateStickyWithTTL(t *testing.T) {
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
		ClientIP:   "192.168.1.1",
		User:       "user1",
		APIType:    "claude",
		StickyMode: model.StickyModeAPIType,
	}

	// Test with custom TTL
	sel.UpdateStickyWithTTL(req, "p1", 10*time.Minute)

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

	// Advance past default TTL but within custom TTL
	clock.Advance(6 * time.Minute)
	_, found = sticky.Get(stickyKey)
	if !found {
		t.Error("sticky should still be valid with custom TTL")
	}
}

func TestSelector_UpdateStickyWithTTL_NoCache(t *testing.T) {
	store := newMockStore()
	clock := &mockClock{now: time.Now()}
	logger := zap.NewNop()

	sel := NewSelector(Config{
		Store:       store,
		StickyCache: nil, // No cache
		Clock:       clock,
		Logger:      logger,
	})

	req := &model.SelectRequest{
		ClientIP: "192.168.1.1",
		User:     "user1",
		APIType:  "claude",
	}

	// Should not panic
	sel.UpdateStickyWithTTL(req, "p1", 10*time.Minute)
}

func TestBuildStickyKey_Modes(t *testing.T) {
	req := &model.SelectRequest{
		ClientIP: "192.168.1.1",
		User:     "user1",
		APIType:  "claude",
		Model:    "claude-3-opus",
	}

	req.StickyMode = model.StickyModeOff
	offKey := buildStickyKey(req)
	if offKey.Model != "" {
		t.Errorf("expected empty model in off mode, got %q", offKey.Model)
	}

	req.StickyMode = model.StickyModeAPIType
	apiTypeKey := buildStickyKey(req)
	if apiTypeKey.Model != "" {
		t.Errorf("expected empty model in api_type mode, got %q", apiTypeKey.Model)
	}

	req.StickyMode = model.StickyModeModel
	modelKey := buildStickyKey(req)
	if modelKey.Model != req.Model {
		t.Errorf("expected model key %q, got %q", req.Model, modelKey.Model)
	}
}

func TestIsStickyEnabled(t *testing.T) {
	tests := []struct {
		mode model.StickyMode
		want bool
	}{
		{model.StickyModeOff, false},
		{model.StickyModeAPIType, true},
		{model.StickyModeModel, true},
		{"", false},         // zero-value
		{"unknown", false},  // unrecognized value
		{"disabled", false}, // another invalid value
	}
	for _, tt := range tests {
		got := isStickyEnabled(tt.mode)
		if got != tt.want {
			t.Errorf("isStickyEnabled(%q) = %v, want %v", tt.mode, got, tt.want)
		}
	}
}

func TestSelector_Select_ZeroValueStickyMode(t *testing.T) {
	store := newMockStore()
	store.providers = []model.Provider{
		{ID: "p1", Name: "Provider 1", Enabled: true, Priority: 1, APITypes: []model.ProviderAPIType{{ProviderID: "p1", APIType: "claude"}}},
		{ID: "p2", Name: "Provider 2", Enabled: true, Priority: 2, APITypes: []model.ProviderAPIType{{ProviderID: "p2", APIType: "claude"}}},
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
		ClientIP:   "192.168.1.1",
		User:       "user1",
		APIType:    "claude",
		StickyMode: "", // zero-value: should be treated as disabled
	}

	// Pre-populate sticky cache
	stickyKey := model.StickyKey{IP: req.ClientIP, User: req.User, APIType: req.APIType}
	sticky.Set(stickyKey, "p2", 5*time.Minute)

	// Should skip sticky cache and select by priority (p1)
	provider, err := sel.selectForTest(t, context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider.ID != "p1" {
		t.Errorf("expected p1 (zero-value sticky mode treated as disabled), got %s", provider.ID)
	}
}

func TestSelector_UpdateSticky_ZeroValueMode(t *testing.T) {
	store := newMockStore()
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
		ClientIP:   "192.168.1.1",
		User:       "user1",
		APIType:    "claude",
		StickyMode: "", // zero-value
	}

	// UpdateSticky should be a no-op for zero-value mode
	sel.UpdateSticky(req, "p1")
	if sticky.Len() != 0 {
		t.Errorf("expected no cache entry for zero-value StickyMode, got %d", sticky.Len())
	}

	// UpdateStickyWithTTL should also be a no-op
	sel.UpdateStickyWithTTL(req, "p1", 10*time.Minute)
	if sticky.Len() != 0 {
		t.Errorf("expected no cache entry for zero-value StickyMode with TTL, got %d", sticky.Len())
	}
}

func TestSelector_UpdateSticky_NoCache(t *testing.T) {
	store := newMockStore()
	clock := &mockClock{now: time.Now()}
	logger := zap.NewNop()

	sel := NewSelector(Config{
		Store:       store,
		StickyCache: nil, // No cache
		Clock:       clock,
		Logger:      logger,
	})

	req := &model.SelectRequest{
		ClientIP: "192.168.1.1",
		User:     "user1",
		APIType:  "claude",
	}

	// Should not panic
	sel.UpdateSticky(req, "p1")
}

func TestSelector_AllProvidersExcluded(t *testing.T) {
	store := newMockStore()
	store.providers = []model.Provider{
		{ID: "p1", Name: "Provider 1", Enabled: true, APITypes: []model.ProviderAPIType{{ProviderID: "p1", APIType: "claude"}}},
		{ID: "p2", Name: "Provider 2", Enabled: true, APITypes: []model.ProviderAPIType{{ProviderID: "p2", APIType: "claude"}}},
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

	// Exclude all providers
	excluded := map[string]bool{"p1": true, "p2": true}

	_, err := sel.selectExcludingForTest(t, context.Background(), req, excluded)
	if !errors.Is(err, internal.ErrNoProvider) {
		t.Errorf("expected ErrNoProvider, got %v", err)
	}
}
