package selector

import (
	"testing"

	"github.com/doraemonkeys/switch-a/internal/model"
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
	for range 100 {
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
	for range 1000 {
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
	for range 100 {
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

func TestSelectRootCandidate_Priority(t *testing.T) {
	candidates := []*rootCandidate{
		newExplicitGroupRootCandidate(&groupCandidate{GroupID: "g1", Priority: 10, Weight: 50}),
		newExplicitGroupRootCandidate(&groupCandidate{GroupID: "g2", Priority: 5, Weight: 50}),
		newStandaloneRootCandidate(&model.Provider{ID: "p1", Priority: 7, Weight: 50}),
	}

	candidate := selectRootCandidate(candidates, StrategyPriority)
	if candidate.id() != "g2" {
		t.Errorf("selectRootCandidate(priority) = %s, want g2", candidate.id())
	}

	candidate = selectRootCandidate(nil, StrategyPriority)
	if candidate != nil {
		t.Errorf("selectRootCandidate(nil) = %v, want nil", candidate)
	}
}

func TestSelectRootCandidate_Weight(t *testing.T) {
	candidates := []*rootCandidate{
		newExplicitGroupRootCandidate(&groupCandidate{GroupID: "heavy", Weight: 100}),
		newStandaloneRootCandidate(&model.Provider{ID: "light", Weight: 1}),
	}

	heavyCount := 0
	for range 1000 {
		candidate := selectRootCandidate(candidates, StrategyWeight)
		if candidate.id() == "heavy" {
			heavyCount++
		}
	}

	if heavyCount < 900 {
		t.Errorf("selectRootCandidate(weight): heavy selected %d/1000 times, expected ~990", heavyCount)
	}
}

func TestSelectRootCandidate_Random(t *testing.T) {
	candidates := []*rootCandidate{
		newExplicitGroupRootCandidate(&groupCandidate{GroupID: "g1"}),
		newStandaloneRootCandidate(&model.Provider{ID: "p1"}),
		newStandaloneRootCandidate(&model.Provider{ID: "p2"}),
	}

	seen := make(map[string]bool)
	for range 100 {
		candidate := selectRootCandidate(candidates, StrategyRandom)
		if candidate == nil {
			t.Fatal("selectRootCandidate returned nil")
		}
		seen[candidate.id()] = true
	}

	if len(seen) < 2 {
		t.Errorf("selectRootCandidate(random): expected multiple candidates to be selected")
	}
}

func TestSelectProvider_RandomStrategy(t *testing.T) {
	providers := []*model.Provider{
		{ID: "p1"},
		{ID: "p2"},
		{ID: "p3"},
	}

	seen := make(map[string]bool)
	for range 100 {
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
	for range 1000 {
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
	for range 100 {
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

func TestSelectRootCandidate_PriorityTiebreaker(t *testing.T) {
	candidates := []*rootCandidate{
		newStandaloneRootCandidate(&model.Provider{ID: "b", Priority: 5}),
		newExplicitGroupRootCandidate(&groupCandidate{GroupID: "a", Priority: 5}),
		newStandaloneRootCandidate(&model.Provider{ID: "c", Priority: 5}),
	}

	candidate := selectRootCandidate(candidates, StrategyPriority)
	if candidate.id() != "a" {
		t.Errorf("selectRootCandidate(priority) with tie = %s, want a (alphabetical tiebreaker)", candidate.id())
	}
}

func TestSelectRootCandidate_ZeroWeight(t *testing.T) {
	candidates := []*rootCandidate{
		newExplicitGroupRootCandidate(&groupCandidate{GroupID: "g1", Weight: 0}),
		newStandaloneRootCandidate(&model.Provider{ID: "p1", Weight: 0}),
	}

	seen := make(map[string]bool)
	for range 100 {
		candidate := selectRootCandidate(candidates, StrategyWeight)
		if candidate == nil {
			t.Fatal("selectRootCandidate returned nil")
		}
		seen[candidate.id()] = true
	}

	// Invalid weights are normalized consistently across both candidate kinds.
	if len(seen) < 2 {
		t.Errorf("selectRootCandidate with zero weights: expected both candidates to be selected")
	}
}

func TestSelectByWeight_LargeWeight(t *testing.T) {
	// Test that extremely large weights are capped to prevent overflow
	providers := []*model.Provider{
		{ID: "huge", Weight: 999999999}, // Exceeds MaxWeight, will be capped
		{ID: "normal", Weight: 100},
	}

	// This should not panic - weights are capped at MaxWeight
	hugeCount := 0
	for range 100 {
		p := SelectByWeight(providers)
		if p == nil {
			t.Fatal("SelectByWeight returned nil")
		}
		if p.ID == "huge" {
			hugeCount++
		}
	}

	// "huge" should be selected most of the time since MaxWeight >> 100
	if hugeCount < 50 {
		t.Errorf("SelectByWeight: huge selected %d/100 times, expected majority", hugeCount)
	}
}

func TestSelectByWeight_MaxWeightConstant(t *testing.T) {
	// Verify MaxWeight constant is reasonable
	if MaxWeight <= 0 {
		t.Errorf("MaxWeight should be positive, got %d", MaxWeight)
	}
	if MaxWeight > 1_000_000_000 {
		t.Errorf("MaxWeight should be <= 1 billion to prevent overflow issues, got %d", MaxWeight)
	}
}
