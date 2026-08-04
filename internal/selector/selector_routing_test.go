package selector

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/model"

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
			Enabled: true,
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

	provider, err := sel.selectForTest(t, context.Background(), &model.SelectRequest{
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
			Enabled: true,
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

	provider, err := sel.selectForTest(t, context.Background(), &model.SelectRequest{
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

func TestSelector_Select_DisabledRoutingPolicyFallsBackToDefaultSelection(t *testing.T) {
	groupID := "g-default"

	store := newMockStore()
	store.providers = []model.Provider{
		{
			ID:       "p-default",
			Name:     "Default Provider",
			Enabled:  true,
			GroupID:  &groupID,
			Priority: 0,
			APITypes: []model.ProviderAPIType{{ProviderID: "p-default", APIType: "codex"}},
		},
		{
			ID:       "p-disabled-target",
			Name:     "Disabled Target Provider",
			Enabled:  true,
			GroupID:  &groupID,
			Priority: 10,
			APITypes: []model.ProviderAPIType{{ProviderID: "p-disabled-target", APIType: "codex"}},
		},
	}
	store.groups = map[string]*model.Group{
		"g-default": {ID: "g-default", Name: "Default Group", Strategy: StrategyPriority, Enabled: true},
	}
	store.authStates["p-default"] = &model.ProviderAuthState{ProviderID: "p-default", Status: model.ProviderAuthStatusActive}
	store.authStates["p-disabled-target"] = &model.ProviderAuthState{ProviderID: "p-disabled-target", Status: model.ProviderAuthStatusActive}
	store.routingPolicies = []model.RoutingPolicy{
		{
			ID:               1,
			APIType:          "codex",
			Enabled:          false,
			TargetProviderID: stringPtr("p-disabled-target"),
		},
	}

	sel := NewSelector(Config{
		Store:         store,
		HealthChecker: newMockHealthChecker(),
		Clock:         internal.RealClock{},
		Logger:        zap.NewNop(),
	})

	provider, err := sel.selectForTest(t, context.Background(), &model.SelectRequest{
		APIType:    "codex",
		StickyMode: model.StickyModeOff,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider == nil {
		t.Fatal("expected provider to be selected")
	}
	if provider.ID != "p-default" {
		t.Fatalf("provider.ID = %q, want %q", provider.ID, "p-default")
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
			Enabled: true,
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

	provider, err := sel.selectForTest(t, context.Background(), req)
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

	provider, err := sel.selectForTest(t, context.Background(), req)
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

func TestSelector_Select_StickyCacheEvictsProviderRejectedByExactProviderRule(t *testing.T) {
	groupID := "g-exact"

	store := newMockStore()
	store.providers = []model.Provider{
		{
			ID:       "p-exact",
			Name:     "Exact Provider",
			Enabled:  true,
			GroupID:  &groupID,
			Priority: 10,
			APITypes: []model.ProviderAPIType{{ProviderID: "p-exact", APIType: "codex"}},
		},
		{
			ID:       "p-sticky-blocked",
			Name:     "Blocked Sticky Provider",
			Enabled:  true,
			GroupID:  &groupID,
			Priority: 0,
			APITypes: []model.ProviderAPIType{{ProviderID: "p-sticky-blocked", APIType: "codex"}},
		},
	}
	store.groups = map[string]*model.Group{
		"g-exact": {ID: "g-exact", Name: "Exact Group", Strategy: StrategyPriority, Enabled: true},
	}
	store.authStates["p-exact"] = &model.ProviderAuthState{ProviderID: "p-exact", Status: model.ProviderAuthStatusActive}
	store.authStates["p-sticky-blocked"] = &model.ProviderAuthState{ProviderID: "p-sticky-blocked", Status: model.ProviderAuthStatusActive}
	store.routingPolicies = []model.RoutingPolicy{
		{
			Enabled:          true,
			APIType:          "codex",
			TargetProviderID: stringPtr("p-exact"),
		},
	}

	sticky := NewMemoryStickyCache(internal.RealClock{})
	req := &model.SelectRequest{
		ClientIP:   "192.168.1.11",
		User:       "user-exact",
		APIType:    "codex",
		StickyMode: model.StickyModeAPIType,
	}
	sticky.Set(buildStickyKey(req), "p-sticky-blocked", time.Minute)

	sel := NewSelector(Config{
		Store:         store,
		HealthChecker: newMockHealthChecker(),
		StickyCache:   sticky,
		Clock:         internal.RealClock{},
		Logger:        zap.NewNop(),
	})

	provider, err := sel.selectForTest(t, context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider == nil {
		t.Fatal("expected provider to be selected")
	}
	if provider.ID != "p-exact" {
		t.Fatalf("provider.ID = %q, want %q", provider.ID, "p-exact")
	}
	if _, found := sticky.Get(buildStickyKey(req)); found {
		t.Fatal("expected sticky entry to be evicted when exact-provider routing rejects the cached provider")
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
			Enabled: true,
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
	first, err := sel.selectForTest(t, context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected first selection error: %v", err)
	}
	if first == nil || first.ID != "p-primary" {
		t.Fatalf("first provider = %#v, want p-primary", first)
	}

	req.FailoverContext = model.NewFailoverContext(first)
	second, err := sel.selectExcludingForTest(t, context.Background(), req, map[string]bool{first.ID: true})
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

func TestSelector_SelectExcluding_ExactProviderRuleDoesNotEscapeOnRetry(t *testing.T) {
	groupID := "g-exact"

	store := newMockStore()
	store.providers = []model.Provider{
		{
			ID:       "p-exact",
			Name:     "Exact Provider",
			Enabled:  true,
			GroupID:  &groupID,
			Priority: 10,
			APITypes: []model.ProviderAPIType{{ProviderID: "p-exact", APIType: "codex"}},
		},
		{
			ID:       "p-other",
			Name:     "Other Provider",
			Enabled:  true,
			GroupID:  &groupID,
			Priority: 0,
			APITypes: []model.ProviderAPIType{{ProviderID: "p-other", APIType: "codex"}},
		},
	}
	store.groups = map[string]*model.Group{
		"g-exact": {ID: "g-exact", Name: "Exact Group", Strategy: StrategyPriority, Enabled: true},
	}
	store.authStates["p-exact"] = &model.ProviderAuthState{ProviderID: "p-exact", Status: model.ProviderAuthStatusActive}
	store.authStates["p-other"] = &model.ProviderAuthState{ProviderID: "p-other", Status: model.ProviderAuthStatusActive}
	store.routingPolicies = []model.RoutingPolicy{
		{
			Enabled:          true,
			APIType:          "codex",
			TargetProviderID: stringPtr("p-exact"),
		},
	}

	sel := NewSelector(Config{
		Store:         store,
		HealthChecker: newMockHealthChecker(),
		Clock:         internal.RealClock{},
		Logger:        zap.NewNop(),
	})

	req := &model.SelectRequest{APIType: "codex", StickyMode: model.StickyModeOff}
	first, err := sel.selectForTest(t, context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected first selection error: %v", err)
	}
	if first == nil || first.ID != "p-exact" {
		t.Fatalf("first provider = %#v, want p-exact", first)
	}

	second, err := sel.selectExcludingForTest(t, context.Background(), req, map[string]bool{"p-exact": true})
	if !errors.Is(err, internal.ErrNoProvider) {
		t.Fatalf("retry error = %v, want %v", err, internal.ErrNoProvider)
	}
	if second != nil {
		t.Fatalf("retry provider = %#v, want nil", second)
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

func TestSelectorSelect_ModelPrefixRuleOnlyConstrainsMatchingModels(t *testing.T) {
	t.Parallel()

	const (
		apiType                 = "codex"
		defaultProviderID       = "p-default"
		targetProviderID        = "p-gpt-5-5"
		targetedModelPrefix     = "gpt-5.5"
		matchingModel           = "gpt-5.5-codex"
		nonMatchingRequestModel = "gpt-5.6-sol"
	)
	groupID := "g-default"

	store := newMockStore()
	store.providers = []model.Provider{
		{
			ID:       defaultProviderID,
			Name:     "Default Provider",
			Enabled:  true,
			GroupID:  &groupID,
			Priority: 0,
			APITypes: []model.ProviderAPIType{{ProviderID: defaultProviderID, APIType: apiType}},
		},
		{
			ID:       targetProviderID,
			Name:     "GPT 5.5 Provider",
			Enabled:  true,
			GroupID:  &groupID,
			Priority: 10,
			APITypes: []model.ProviderAPIType{{ProviderID: targetProviderID, APIType: apiType}},
		},
	}
	store.groups = map[string]*model.Group{
		"g-default": {ID: "g-default", Name: "Default Group", Strategy: StrategyPriority, Enabled: true},
	}
	store.authStates[defaultProviderID] = &model.ProviderAuthState{ProviderID: defaultProviderID, Status: model.ProviderAuthStatusActive}
	store.authStates[targetProviderID] = &model.ProviderAuthState{ProviderID: targetProviderID, Status: model.ProviderAuthStatusActive}
	store.routingPolicies = []model.RoutingPolicy{
		{
			Enabled:          true,
			APIType:          apiType,
			ModelMatchType:   model.RoutingPolicyModelMatchTypePrefix,
			ModelMatchValue:  targetedModelPrefix,
			TargetProviderID: stringPtr(targetProviderID),
		},
	}

	sel := NewSelector(Config{
		Store:         store,
		HealthChecker: newMockHealthChecker(),
		Clock:         internal.RealClock{},
		Logger:        zap.NewNop(),
	})

	tests := []struct {
		name           string
		requestModel   string
		wantProviderID string
	}{
		{
			name:           "matching model uses the targeted provider",
			requestModel:   matchingModel,
			wantProviderID: targetProviderID,
		},
		{
			name:           "non-matching model keeps normal selection",
			requestModel:   nonMatchingRequestModel,
			wantProviderID: defaultProviderID,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, err := sel.selectForTest(t, context.Background(), &model.SelectRequest{
				APIType:    apiType,
				Model:      tt.requestModel,
				StickyMode: model.StickyModeOff,
			})
			if err != nil {
				t.Fatalf("Select() error = %v, want provider %q", err, tt.wantProviderID)
			}
			if provider == nil || provider.ID != tt.wantProviderID {
				t.Fatalf("Select() provider = %#v, want %q", provider, tt.wantProviderID)
			}
		})
	}
}

func TestSelectorSelectUnknownModelIgnoresModelOnlyRoutingRules(t *testing.T) {
	t.Parallel()

	gDefault := "g-default"
	gModelOnly := "g-model-only"

	store := newMockStore()
	store.providers = []model.Provider{
		{
			ID:       "p-default",
			Name:     "Default Provider",
			Enabled:  true,
			GroupID:  &gDefault,
			Priority: 0,
			APITypes: []model.ProviderAPIType{{ProviderID: "p-default", APIType: "codex"}},
		},
		{
			ID:       "p-model-only",
			Name:     "Model Only Provider",
			Enabled:  true,
			GroupID:  &gModelOnly,
			Priority: 10,
			APITypes: []model.ProviderAPIType{{ProviderID: "p-model-only", APIType: "codex"}},
		},
	}
	store.groups = map[string]*model.Group{
		"g-default":    {ID: "g-default", Name: "Default Group", Strategy: StrategyPriority, Enabled: true},
		"g-model-only": {ID: "g-model-only", Name: "Model Group", Strategy: StrategyPriority, Enabled: true},
	}
	store.authStates["p-default"] = &model.ProviderAuthState{ProviderID: "p-default", Status: model.ProviderAuthStatusActive}
	store.authStates["p-model-only"] = &model.ProviderAuthState{ProviderID: "p-model-only", Status: model.ProviderAuthStatusActive}
	store.routingPolicies = []model.RoutingPolicy{
		{
			Enabled:         true,
			APIType:         "codex",
			ModelMatchType:  model.RoutingPolicyModelMatchTypeExact,
			ModelMatchValue: "gpt-5.4",
			Groups:          []model.RoutingPolicyGroup{{GroupID: "g-model-only"}},
		},
	}

	sel := NewSelector(Config{
		Store:         store,
		HealthChecker: newMockHealthChecker(),
		Clock:         internal.RealClock{},
		Logger:        zap.NewNop(),
	})

	provider, err := sel.selectForTest(t, context.Background(), &model.SelectRequest{
		APIType:    "codex",
		Model:      unknownModelSentinel,
		StickyMode: model.StickyModeOff,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider == nil {
		t.Fatal("expected provider to be selected under unknown-model fallback semantics")
	}
	if provider.ID != "p-default" {
		t.Fatalf("provider.ID = %q, want %q", provider.ID, "p-default")
	}
}
