package selector

import (
	"context"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"

	"go.uber.org/zap"
)

func TestSelector_Select_StandaloneProviderUsesRootPriority(t *testing.T) {
	store := newMockStore()
	store.providers = []model.Provider{
		{ID: "lower-precedence", Name: "Lower precedence", Enabled: true, Priority: 20, APITypes: []model.ProviderAPIType{{ProviderID: "lower-precedence", APIType: "claude"}}},
		{ID: "higher-precedence", Name: "Higher precedence", Enabled: true, Priority: 1, APITypes: []model.ProviderAPIType{{ProviderID: "higher-precedence", APIType: "claude"}}},
	}

	sel := NewSelector(Config{Store: store, Clock: &mockClock{now: time.Now()}, Logger: zap.NewNop()})
	provider, err := sel.selectForTest(t, context.Background(), &model.SelectRequest{APIType: "claude"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider.ID != "higher-precedence" {
		t.Fatalf("selected provider %q, want standalone provider priority to select higher-precedence", provider.ID)
	}
}

func TestSelector_Select_StandaloneProviderUsesRootWeight(t *testing.T) {
	store := newMockStore()
	store.configs[ConfigKeyRootCandidateStrategy] = StrategyWeight
	store.providers = []model.Provider{
		{ID: "heavy", Name: "Heavy", Enabled: true, Weight: 100, APITypes: []model.ProviderAPIType{{ProviderID: "heavy", APIType: "claude"}}},
		{ID: "light", Name: "Light", Enabled: true, Weight: 1, APITypes: []model.ProviderAPIType{{ProviderID: "light", APIType: "claude"}}},
	}

	sel := NewSelector(Config{Store: store, Clock: &mockClock{now: time.Now()}, Logger: zap.NewNop()})
	heavySelections := countProviderSelections(t, sel, "heavy")
	if heavySelections < minimumDominantSelections {
		t.Fatalf("heavy standalone provider selected %d/%d times, want provider weight to dominate", heavySelections, rootWeightSelectionCount)
	}
}

func TestSelector_Select_ExplicitGroupAndStandaloneShareRootPriority(t *testing.T) {
	groupID := "group"
	store := newMockStore()
	store.providers = []model.Provider{
		{ID: "grouped", Name: "Grouped", Enabled: true, GroupID: &groupID, Priority: 0, APITypes: []model.ProviderAPIType{{ProviderID: "grouped", APIType: "claude"}}},
		{ID: "standalone", Name: "Standalone", Enabled: true, Priority: 1, APITypes: []model.ProviderAPIType{{ProviderID: "standalone", APIType: "claude"}}},
	}
	store.groups[groupID] = &model.Group{ID: groupID, Name: "Group", Strategy: StrategyPriority, Priority: 10, Weight: 1, Enabled: true}

	sel := NewSelector(Config{Store: store, Clock: &mockClock{now: time.Now()}, Logger: zap.NewNop()})
	provider, err := sel.selectForTest(t, context.Background(), &model.SelectRequest{APIType: "claude"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider.ID != "standalone" {
		t.Fatalf("selected provider %q, want standalone root priority to outrank the explicit group", provider.ID)
	}
}

func TestSelector_Select_ExplicitGroupAndStandaloneShareRootWeight(t *testing.T) {
	groupID := "light-group"
	store := newMockStore()
	store.configs[ConfigKeyRootCandidateStrategy] = StrategyWeight
	store.providers = []model.Provider{
		{ID: "grouped", Name: "Grouped", Enabled: true, GroupID: &groupID, Weight: 100, APITypes: []model.ProviderAPIType{{ProviderID: "grouped", APIType: "claude"}}},
		{ID: "standalone", Name: "Standalone", Enabled: true, Weight: 100, APITypes: []model.ProviderAPIType{{ProviderID: "standalone", APIType: "claude"}}},
	}
	store.groups[groupID] = &model.Group{ID: groupID, Name: "Light group", Strategy: StrategyWeight, Priority: 0, Weight: 1, Enabled: true}

	sel := NewSelector(Config{Store: store, Clock: &mockClock{now: time.Now()}, Logger: zap.NewNop()})
	standaloneSelections := countProviderSelections(t, sel, "standalone")
	if standaloneSelections < minimumDominantSelections {
		t.Fatalf("standalone provider selected %d/%d times, want its root weight to dominate the explicit group", standaloneSelections, rootWeightSelectionCount)
	}
}

const (
	rootWeightSelectionCount  = 1000
	minimumDominantSelections = 900
)

func countProviderSelections(t *testing.T, selector *Selector, providerID string) int {
	t.Helper()
	selections := 0
	for range rootWeightSelectionCount {
		result, err := selector.SelectWithMetadata(context.Background(), &model.SelectRequest{APIType: "claude"})
		if err != nil {
			t.Fatalf("select provider: %v", err)
		}
		if result.Provider().ID == providerID {
			selections++
		}
		result.Lease.Release()
	}
	return selections
}
