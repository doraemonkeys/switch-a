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

func TestSelector_Select_FiltersByRoutingPolicyAndAuthState(t *testing.T) {
	g1 := "g1"
	g2 := "g2"

	store := newMockStore()
	store.providers = []model.Provider{
		{
			ID:       "p-allowed",
			Name:     "Allowed Provider",
			Enabled:  true,
			GroupID:  &g1,
			Priority: 10,
			APITypes: []model.ProviderAPIType{{ProviderID: "p-allowed", APIType: "codex"}},
		},
		{
			ID:       "p-reauth",
			Name:     "Reauth Provider",
			Enabled:  true,
			GroupID:  &g1,
			Priority: 0,
			APITypes: []model.ProviderAPIType{{ProviderID: "p-reauth", APIType: "codex"}},
		},
		{
			ID:       "p-outside",
			Name:     "Outside Group Provider",
			Enabled:  true,
			GroupID:  &g2,
			Priority: 0,
			APITypes: []model.ProviderAPIType{{ProviderID: "p-outside", APIType: "codex"}},
		},
	}
	store.groups = map[string]*model.Group{
		"g1": {ID: "g1", Name: "Allowed Group", Strategy: StrategyPriority, Enabled: true},
		"g2": {ID: "g2", Name: "Outside Group", Strategy: StrategyPriority, Enabled: true},
	}
	store.authStates["p-allowed"] = &model.ProviderAuthState{ProviderID: "p-allowed", Status: model.ProviderAuthStatusActive}
	store.authStates["p-reauth"] = &model.ProviderAuthState{ProviderID: "p-reauth", Status: model.ProviderAuthStatusReauthRequired}
	store.authStates["p-outside"] = &model.ProviderAuthState{ProviderID: "p-outside", Status: model.ProviderAuthStatusActive}
	store.routingPolicies = []model.RoutingPolicy{
		{
			APIType: "codex",
			Groups:  []model.RoutingPolicyGroup{{GroupID: "g1"}},
		},
	}

	sel := NewSelector(Config{
		Store:         store,
		HealthChecker: newMockHealthChecker(),
		StickyCache:   NewMemoryStickyCache(internal.RealClock{}),
		Clock:         internal.RealClock{},
		Logger:        zap.NewNop(),
	})

	provider, err := sel.Select(context.Background(), &model.SelectRequest{
		APIType:    "codex",
		StickyMode: model.StickyModeOff,
	})
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

func TestSelector_Select_NoRoutingPolicyForAPITypeKeepsDefaultSelection(t *testing.T) {
	g1 := "g1"
	g2 := "g2"

	store := newMockStore()
	store.providers = []model.Provider{
		{
			ID:       "p1",
			Name:     "Priority Provider",
			Enabled:  true,
			GroupID:  &g1,
			Priority: 0,
			APITypes: []model.ProviderAPIType{{ProviderID: "p1", APIType: "claude"}},
		},
		{
			ID:       "p2",
			Name:     "Secondary Provider",
			Enabled:  true,
			GroupID:  &g2,
			Priority: 10,
			APITypes: []model.ProviderAPIType{{ProviderID: "p2", APIType: "claude"}},
		},
	}
	store.groups = map[string]*model.Group{
		"g1": {ID: "g1", Name: "Group 1", Strategy: StrategyPriority, Enabled: true},
		"g2": {ID: "g2", Name: "Group 2", Strategy: StrategyPriority, Enabled: true},
	}
	store.authStates["p1"] = &model.ProviderAuthState{ProviderID: "p1", Status: model.ProviderAuthStatusActive}
	store.authStates["p2"] = &model.ProviderAuthState{ProviderID: "p2", Status: model.ProviderAuthStatusActive}
	store.routingPolicies = []model.RoutingPolicy{
		{
			APIType: "codex",
			Groups:  []model.RoutingPolicyGroup{{GroupID: "some-other-group"}},
		},
	}

	sel := NewSelector(Config{
		Store:         store,
		HealthChecker: newMockHealthChecker(),
		Clock:         internal.RealClock{},
		Logger:        zap.NewNop(),
	})

	provider, err := sel.Select(context.Background(), &model.SelectRequest{
		APIType:    "claude",
		StickyMode: model.StickyModeOff,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider == nil {
		t.Fatal("expected provider to be selected")
	}
	if provider.ID != "p1" {
		t.Fatalf("provider.ID = %q, want %q", provider.ID, "p1")
	}
}

func TestSelector_Select_StickyCacheEvictsProviderRejectedByRoutingPolicy(t *testing.T) {
	gAllowed := "g-allowed"
	gBlocked := "g-blocked"

	store := newMockStore()
	store.providers = []model.Provider{
		{
			ID:       "p-allowed",
			Name:     "Allowed Provider",
			Enabled:  true,
			GroupID:  &gAllowed,
			Priority: 10,
			APITypes: []model.ProviderAPIType{{ProviderID: "p-allowed", APIType: "codex"}},
		},
		{
			ID:       "p-blocked",
			Name:     "Blocked Provider",
			Enabled:  true,
			GroupID:  &gBlocked,
			Priority: 0,
			APITypes: []model.ProviderAPIType{{ProviderID: "p-blocked", APIType: "codex"}},
		},
	}
	store.groups = map[string]*model.Group{
		"g-allowed": {ID: "g-allowed", Name: "Allowed Group", Strategy: StrategyPriority, Enabled: true},
		"g-blocked": {ID: "g-blocked", Name: "Blocked Group", Strategy: StrategyPriority, Enabled: true},
	}
	store.authStates["p-allowed"] = &model.ProviderAuthState{ProviderID: "p-allowed", Status: model.ProviderAuthStatusActive}
	store.authStates["p-blocked"] = &model.ProviderAuthState{ProviderID: "p-blocked", Status: model.ProviderAuthStatusActive}
	store.routingPolicies = []model.RoutingPolicy{
		{
			APIType: "codex",
			Groups:  []model.RoutingPolicyGroup{{GroupID: "g-allowed"}},
		},
	}

	sticky := NewMemoryStickyCache(internal.RealClock{})
	req := &model.SelectRequest{
		ClientIP:   "192.168.1.1",
		User:       "user-1",
		APIType:    "codex",
		StickyMode: model.StickyModeAPIType,
	}
	sticky.Set(buildStickyKey(req), "p-blocked", time.Minute)

	sel := NewSelector(Config{
		Store:         store,
		HealthChecker: newMockHealthChecker(),
		StickyCache:   sticky,
		Clock:         internal.RealClock{},
		Logger:        zap.NewNop(),
	})

	provider, err := sel.Select(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider == nil {
		t.Fatal("expected provider to be selected")
	}
	if provider.ID != "p-allowed" {
		t.Fatalf("provider.ID = %q, want %q", provider.ID, "p-allowed")
	}
	if _, found := sticky.Get(buildStickyKey(req)); found {
		t.Fatal("expected sticky entry to be evicted when routing policy rejects the cached provider")
	}
}

func TestSelector_Select_StickyCachePreservesAffinityOnAuthStateReadError(t *testing.T) {
	store := newMockStore()
	store.providers = []model.Provider{
		{
			ID:       "p-primary",
			Name:     "Primary Provider",
			Enabled:  true,
			Priority: 10,
			APITypes: []model.ProviderAPIType{{ProviderID: "p-primary", APIType: "codex"}},
		},
		{
			ID:       "p-sticky",
			Name:     "Sticky Provider",
			Enabled:  true,
			Priority: 1,
			APITypes: []model.ProviderAPIType{{ProviderID: "p-sticky", APIType: "codex"}},
		},
	}
	store.authStateErr = errors.New("temporary auth-state read failure")

	sticky := NewMemoryStickyCache(internal.RealClock{})
	req := &model.SelectRequest{
		ClientIP:   "192.168.1.20",
		User:       "user-2",
		APIType:    "codex",
		StickyMode: model.StickyModeAPIType,
	}
	sticky.Set(buildStickyKey(req), "p-sticky", time.Minute)

	sel := NewSelector(Config{
		Store:         store,
		HealthChecker: newMockHealthChecker(),
		StickyCache:   sticky,
		Clock:         internal.RealClock{},
		Logger:        zap.NewNop(),
	})

	provider, err := sel.Select(context.Background(), req)
	if err == nil {
		t.Fatal("expected sticky auth-state read failure to be returned")
	}
	if provider != nil {
		t.Fatal("expected no provider when sticky auth-state read fails")
	}
	if !errors.Is(err, store.authStateErr) {
		t.Fatalf("error = %v, want %v", err, store.authStateErr)
	}
	if providerID, found := sticky.Get(buildStickyKey(req)); !found || providerID != "p-sticky" {
		t.Fatalf("expected sticky entry to remain after transient auth-state read failure, got %q (found=%v)", providerID, found)
	}
}

func TestSelector_SelectExcluding_FailoverStaysWithinRoutingPolicyClosure(t *testing.T) {
	gAllowed := "g-allowed"
	gBlocked := "g-blocked"

	store := newMockStore()
	store.providers = []model.Provider{
		{
			ID:             "p-primary",
			Name:           "Primary Provider",
			Enabled:        true,
			GroupID:        &gAllowed,
			Priority:       0,
			Vendor:         "vendor-a",
			AcceptFailover: model.ScopeAny,
			FailoverScope:  model.ScopeAny,
			APITypes:       []model.ProviderAPIType{{ProviderID: "p-primary", APIType: "codex"}},
		},
		{
			ID:             "p-allowed-failover",
			Name:           "Allowed Failover Provider",
			Enabled:        true,
			GroupID:        &gAllowed,
			Priority:       10,
			Vendor:         "vendor-b",
			AcceptFailover: model.ScopeAny,
			FailoverScope:  model.ScopeAny,
			APITypes:       []model.ProviderAPIType{{ProviderID: "p-allowed-failover", APIType: "codex"}},
		},
		{
			ID:             "p-outside-policy",
			Name:           "Outside Policy Provider",
			Enabled:        true,
			GroupID:        &gBlocked,
			Priority:       0,
			Vendor:         "vendor-a",
			AcceptFailover: model.ScopeAny,
			FailoverScope:  model.ScopeAny,
			APITypes:       []model.ProviderAPIType{{ProviderID: "p-outside-policy", APIType: "codex"}},
		},
	}
	store.groups = map[string]*model.Group{
		"g-allowed": {ID: "g-allowed", Name: "Allowed Group", Strategy: StrategyPriority, Enabled: true},
		"g-blocked": {ID: "g-blocked", Name: "Blocked Group", Strategy: StrategyPriority, Enabled: true},
	}
	store.authStates["p-primary"] = &model.ProviderAuthState{ProviderID: "p-primary", Status: model.ProviderAuthStatusActive}
	store.authStates["p-allowed-failover"] = &model.ProviderAuthState{ProviderID: "p-allowed-failover", Status: model.ProviderAuthStatusActive}
	store.authStates["p-outside-policy"] = &model.ProviderAuthState{ProviderID: "p-outside-policy", Status: model.ProviderAuthStatusActive}
	store.routingPolicies = []model.RoutingPolicy{
		{
			APIType: "codex",
			Groups:  []model.RoutingPolicyGroup{{GroupID: "g-allowed"}},
		},
	}

	sel := NewSelector(Config{
		Store:         store,
		HealthChecker: newMockHealthChecker(),
		Clock:         internal.RealClock{},
		Logger:        zap.NewNop(),
	})

	req := &model.SelectRequest{
		APIType:    "codex",
		StickyMode: model.StickyModeOff,
	}
	first, err := sel.Select(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected first selection error: %v", err)
	}
	if first == nil || first.ID != "p-primary" {
		t.Fatalf("first provider = %#v, want p-primary", first)
	}

	req.FailoverContext = model.NewFailoverContext(first)
	second, err := sel.SelectExcluding(context.Background(), req, map[string]bool{first.ID: true})
	if err != nil {
		t.Fatalf("unexpected failover selection error: %v", err)
	}
	if second == nil {
		t.Fatal("expected failover provider to be selected")
	}
	if second.ID != "p-allowed-failover" {
		t.Fatalf("failover provider = %q, want %q", second.ID, "p-allowed-failover")
	}
}

func TestBuildContinuityKey_ModelModeFallsBackWhenModelUnknown(t *testing.T) {
	req := &model.SelectRequest{
		ClientIP:   "192.168.1.1",
		User:       "user-1",
		APIType:    "codex",
		Model:      unknownModelSentinel,
		StickyMode: model.StickyModeModel,
	}

	key := BuildContinuityKey(req)
	if key.Model != "" {
		t.Fatalf("key.Model = %q, want empty when request model is unknown", key.Model)
	}

	req.Model = "gpt-5.4"
	key = BuildContinuityKey(req)
	if key.Model != "gpt-5.4" {
		t.Fatalf("key.Model = %q, want %q", key.Model, "gpt-5.4")
	}
}
