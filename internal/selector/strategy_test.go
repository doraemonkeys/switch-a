package selector

import (
	"testing"

	"switch-a/internal/model"
)

func TestSelectByPriority(t *testing.T) {
	tests := []struct {
		name      string
		providers []*model.Provider
		wantID    string
	}{
		{
			name:      "empty list",
			providers: nil,
			wantID:    "",
		},
		{
			name: "single provider",
			providers: []*model.Provider{
				{ID: "p1", Priority: 10},
			},
			wantID: "p1",
		},
		{
			name: "lowest priority first",
			providers: []*model.Provider{
				{ID: "p1", Priority: 10},
				{ID: "p2", Priority: 5},
				{ID: "p3", Priority: 15},
			},
			wantID: "p2",
		},
		{
			name: "tie-breaker by ID",
			providers: []*model.Provider{
				{ID: "b", Priority: 5},
				{ID: "a", Priority: 5},
				{ID: "c", Priority: 5},
			},
			wantID: "a",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SelectByPriority(tt.providers)
			if tt.wantID == "" {
				if got != nil {
					t.Errorf("SelectByPriority() = %v, want nil", got)
				}
				return
			}
			if got == nil || got.ID != tt.wantID {
				t.Errorf("SelectByPriority() = %v, want ID %s", got, tt.wantID)
			}
		})
	}
}

func TestSelectByRandom(t *testing.T) {
	// Test empty list
	got := SelectByRandom(nil)
	if got != nil {
		t.Errorf("SelectByRandom(nil) = %v, want nil", got)
	}

	// Test with providers - should return one of them
	providers := []*model.Provider{
		{ID: "p1"},
		{ID: "p2"},
		{ID: "p3"},
	}

	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		p := SelectByRandom(providers)
		if p == nil {
			t.Fatal("SelectByRandom returned nil for non-empty list")
		}
		seen[p.ID] = true
	}

	// Should have selected at least 2 different providers in 100 tries
	if len(seen) < 2 {
		t.Errorf("SelectByRandom appears non-random, only saw %d unique providers", len(seen))
	}
}

func TestSelectByWeight(t *testing.T) {
	// Test empty list
	got := SelectByWeight(nil)
	if got != nil {
		t.Errorf("SelectByWeight(nil) = %v, want nil", got)
	}

	// Test with weighted providers
	providers := []*model.Provider{
		{ID: "heavy", Weight: 100},
		{ID: "light", Weight: 1},
	}

	heavyCount := 0
	for i := 0; i < 1000; i++ {
		p := SelectByWeight(providers)
		if p.ID == "heavy" {
			heavyCount++
		}
	}

	// Heavy should be selected much more often (roughly 99%)
	if heavyCount < 900 {
		t.Errorf("SelectByWeight: heavy selected %d/1000 times, expected ~990", heavyCount)
	}
}

func TestSelectByWeight_ZeroWeight(t *testing.T) {
	// Test with zero weight (should default to 1)
	providers := []*model.Provider{
		{ID: "p1", Weight: 0},
		{ID: "p2", Weight: 0},
	}

	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		p := SelectByWeight(providers)
		if p == nil {
			t.Fatal("SelectByWeight returned nil")
		}
		seen[p.ID] = true
	}

	// Should select both providers
	if len(seen) < 2 {
		t.Errorf("SelectByWeight with zero weights: expected both providers to be selected")
	}
}

func TestSelectProvider(t *testing.T) {
	providers := []*model.Provider{
		{ID: "p1", Priority: 10, Weight: 50},
		{ID: "p2", Priority: 5, Weight: 50},
	}

	// Priority strategy
	p := SelectProvider(providers, StrategyPriority)
	if p.ID != "p2" {
		t.Errorf("SelectProvider(priority) = %s, want p2", p.ID)
	}

	// Unknown strategy should default to priority
	p = SelectProvider(providers, "unknown")
	if p.ID != "p2" {
		t.Errorf("SelectProvider(unknown) = %s, want p2 (default to priority)", p.ID)
	}
}

func TestSelectGroup(t *testing.T) {
	groups := []*groupCandidate{
		{GroupID: "g1", Priority: 10, Weight: 50},
		{GroupID: "g2", Priority: 5, Weight: 50},
	}

	// Priority strategy
	g := SelectGroup(groups, StrategyPriority)
	if g.GroupID != "g2" {
		t.Errorf("SelectGroup(priority) = %s, want g2", g.GroupID)
	}

	// Empty list
	g = SelectGroup(nil, StrategyPriority)
	if g != nil {
		t.Errorf("SelectGroup(nil) = %v, want nil", g)
	}
}

func TestSelectGroup_Weight(t *testing.T) {
	groups := []*groupCandidate{
		{GroupID: "heavy", Weight: 100},
		{GroupID: "light", Weight: 1},
	}

	heavyCount := 0
	for i := 0; i < 1000; i++ {
		g := SelectGroup(groups, StrategyWeight)
		if g.GroupID == "heavy" {
			heavyCount++
		}
	}

	if heavyCount < 900 {
		t.Errorf("SelectGroup(weight): heavy selected %d/1000 times, expected ~990", heavyCount)
	}
}

func TestSelectGroup_Random(t *testing.T) {
	groups := []*groupCandidate{
		{GroupID: "g1"},
		{GroupID: "g2"},
		{GroupID: "g3"},
	}

	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		g := SelectGroup(groups, StrategyRandom)
		if g == nil {
			t.Fatal("SelectGroup returned nil")
		}
		seen[g.GroupID] = true
	}

	if len(seen) < 2 {
		t.Errorf("SelectGroup(random): expected multiple groups to be selected")
	}
}

func TestSelectProvider_RandomStrategy(t *testing.T) {
	providers := []*model.Provider{
		{ID: "p1"},
		{ID: "p2"},
		{ID: "p3"},
	}

	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		p := SelectProvider(providers, StrategyRandom)
		if p == nil {
			t.Fatal("SelectProvider returned nil")
		}
		seen[p.ID] = true
	}

	if len(seen) < 2 {
		t.Errorf("SelectProvider(random): expected multiple providers to be selected")
	}
}

func TestSelectProvider_WeightStrategy(t *testing.T) {
	providers := []*model.Provider{
		{ID: "heavy", Weight: 100},
		{ID: "light", Weight: 1},
	}

	heavyCount := 0
	for i := 0; i < 1000; i++ {
		p := SelectProvider(providers, StrategyWeight)
		if p.ID == "heavy" {
			heavyCount++
		}
	}

	// Heavy should be selected much more often
	if heavyCount < 900 {
		t.Errorf("SelectProvider(weight): heavy selected %d/1000 times, expected ~990", heavyCount)
	}
}

func TestSelectByWeight_NegativeWeight(t *testing.T) {
	// Test with negative weight (should default to 1)
	providers := []*model.Provider{
		{ID: "p1", Weight: -5},
		{ID: "p2", Weight: -10},
	}

	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		p := SelectByWeight(providers)
		if p == nil {
			t.Fatal("SelectByWeight returned nil")
		}
		seen[p.ID] = true
	}

	// Should select both providers (both treated as weight 1)
	if len(seen) < 2 {
		t.Errorf("SelectByWeight with negative weights: expected both providers to be selected")
	}
}

func TestSelectGroup_PriorityTiebreaker(t *testing.T) {
	groups := []*groupCandidate{
		{GroupID: "b", Priority: 5},
		{GroupID: "a", Priority: 5},
		{GroupID: "c", Priority: 5},
	}

	g := SelectGroup(groups, StrategyPriority)
	if g.GroupID != "a" {
		t.Errorf("SelectGroup(priority) with tie = %s, want a (alphabetical tiebreaker)", g.GroupID)
	}
}

func TestSelectGroup_ZeroWeight(t *testing.T) {
	groups := []*groupCandidate{
		{GroupID: "g1", Weight: 0},
		{GroupID: "g2", Weight: 0},
	}

	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		g := SelectGroup(groups, StrategyWeight)
		if g == nil {
			t.Fatal("SelectGroup returned nil")
		}
		seen[g.GroupID] = true
	}

	// Should select both groups (both treated as weight 1)
	if len(seen) < 2 {
		t.Errorf("SelectGroup with zero weights: expected both groups to be selected")
	}
}
