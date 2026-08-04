package errorrule

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/apicontract"
)

func TestCompileRuleSetAndImmutability(t *testing.T) {
	rule0 := testRule(t, 0, 0)
	rule1 := testRule(t, 1, 1)
	rule1.Enabled = false
	rules := []Rule{rule1, rule0}
	snapshot, err := CompileRuleSet(7, rules)
	if err != nil {
		t.Fatalf("CompileRuleSet() error = %v", err)
	}
	if snapshot.Revision() != 7 || snapshot.MatcherBytes() != len(rule0.Keywords[0]) {
		t.Fatalf("snapshot facts = revision %d, matcher bytes %d", snapshot.Revision(), snapshot.MatcherBytes())
	}
	ordered := snapshot.Rules()
	if len(ordered) != 2 || ordered[0].ID != rule0.ID || ordered[1].ID != rule1.ID {
		t.Fatalf("ordered rules = %#v", ordered)
	}

	wantKeyword := rule0.Keywords[0]
	wantAPIType := *rule0.APIType
	rules[1].Keywords[0] = "mutated-input"
	*rules[1].APIType = apicontract.APITypeClaude
	ordered[0].Keywords[0] = "mutated-output"
	*ordered[0].APIType = apicontract.APITypeGemini
	stable, ok := snapshot.Rule(rule0.ID)
	if !ok || stable.Keywords[0] != wantKeyword || *stable.APIType != wantAPIType {
		t.Fatalf("snapshot was mutated through an alias: %#v", stable)
	}
	if _, ok := snapshot.Rule("99999999-9999-4999-8999-999999999999"); ok {
		t.Fatal("unknown rule unexpectedly found")
	}

	var nilSnapshot *CompiledRuleSet
	if nilSnapshot.Revision() != 0 || nilSnapshot.MatcherBytes() != 0 || nilSnapshot.Rules() != nil {
		t.Fatal("nil snapshot accessors are not zero-safe")
	}
	if _, ok := nilSnapshot.Rule(rule0.ID); ok {
		t.Fatal("nil snapshot returned a rule")
	}
}

func TestCompileRuleSetValidation(t *testing.T) {
	valid := []Rule{testRule(t, 0, 0), testRule(t, 1, 1)}
	cases := []struct {
		name     string
		revision Revision
		mutate   func([]Rule) []Rule
	}{
		{name: "negative revision", revision: -1, mutate: func(r []Rule) []Rule { return r }},
		{name: "capacity", revision: 0, mutate: func([]Rule) []Rule {
			rules := make([]Rule, MaxRuleCount+1)
			return rules
		}},
		{name: "invalid ID", revision: 0, mutate: func(r []Rule) []Rule { r[0].ID = "bad"; return r }},
		{name: "duplicate ID", revision: 0, mutate: func(r []Rule) []Rule { r[1].ID = r[0].ID; return r }},
		{name: "missing generation", revision: 0, mutate: func(r []Rule) []Rule { r[0].generation = RuleGeneration{}; return r }},
		{name: "duplicate generation", revision: 0, mutate: func(r []Rule) []Rule { r[1].generation = r[0].generation; return r }},
		{name: "negative position", revision: 0, mutate: func(r []Rule) []Rule { r[0].Position = -1; return r }},
		{name: "position outside", revision: 0, mutate: func(r []Rule) []Rule { r[1].Position = 2; return r }},
		{name: "duplicate position", revision: 0, mutate: func(r []Rule) []Rule { r[1].Position = 0; return r }},
		{name: "missing timestamp", revision: 0, mutate: func(r []Rule) []Rule { r[0].CreatedAt = time.Time{}; return r }},
		{name: "reverse timestamp", revision: 0, mutate: func(r []Rule) []Rule { r[0].UpdatedAt = r[0].CreatedAt.Add(-time.Second); return r }},
		{name: "non UTC timestamp", revision: 0, mutate: func(r []Rule) []Rule { r[0].UpdatedAt = r[0].UpdatedAt.In(time.FixedZone("offset", 3600)); return r }},
		{name: "non canonical spec", revision: 0, mutate: func(r []Rule) []Rule { r[0].Keywords[0] = " UPPER "; return r }},
	}
	for _, current := range cases {
		t.Run(current.name, func(t *testing.T) {
			rules := make([]Rule, len(valid))
			for index := range valid {
				rules[index] = cloneRule(valid[index])
			}
			if _, err := CompileRuleSet(current.revision, current.mutate(rules)); err == nil {
				t.Fatal("CompileRuleSet() unexpectedly succeeded")
			}
		})
	}

	empty, err := CompileRuleSet(0, nil)
	if err != nil || len(empty.Rules()) != 0 {
		t.Fatalf("empty CompileRuleSet() = (%#v, %v)", empty, err)
	}
}

type mutableRuleSetProvider struct {
	current *CompiledRuleSet
}

func (p *mutableRuleSetProvider) CurrentRuleSet() *CompiledRuleSet {
	return p.current
}

func TestPinRuleSetRetainsSnapshot(t *testing.T) {
	first, err := CompileRuleSet(1, []Rule{testRule(t, 0, 0)})
	if err != nil {
		t.Fatalf("compile first: %v", err)
	}
	second, err := CompileRuleSet(2, []Rule{testRule(t, 1, 0)})
	if err != nil {
		t.Fatalf("compile second: %v", err)
	}
	provider := &mutableRuleSetProvider{current: first}
	pinned, err := PinRuleSet(provider)
	if err != nil {
		t.Fatalf("PinRuleSet() error = %v", err)
	}
	provider.current = second
	if pinned.Revision() != 1 || provider.CurrentRuleSet().Revision() != 2 {
		t.Fatalf("pinned revision = %d, current = %d", pinned.Revision(), provider.CurrentRuleSet().Revision())
	}
	if _, err := PinRuleSet(nil); err == nil {
		t.Fatal("nil provider unexpectedly accepted")
	}
	provider.current = nil
	if _, err := PinRuleSet(provider); err == nil {
		t.Fatal("nil provider snapshot unexpectedly accepted")
	}
}

func TestCompiledRuleSetPrecedencePermutation(t *testing.T) {
	providerTarget, err := NewProviderTarget("provider-a")
	if err != nil {
		t.Fatalf("NewProviderTarget() error = %v", err)
	}
	rules := []Rule{
		testRule(t, 0, 0),
		testRule(t, 1, 1),
		testRule(t, 2, 2),
		testRule(t, 3, 3),
		testRule(t, 4, 4),
	}
	for index := range rules {
		rules[index].Keywords = []string{"overloaded"}
	}
	// Global exact positions deliberately precede provider rules; scope axes
	// still dominate the operator-controlled position.
	rules[0].Target = NewGlobalTarget()
	rules[0].APIType = nil
	rules[1].Target = NewGlobalTarget()
	rules[1].APIType = testAPIType(apicontract.APITypeCodex)
	rules[2].Target = providerTarget
	rules[2].APIType = nil
	rules[3].Target = providerTarget
	rules[3].APIType = testAPIType(apicontract.APITypeCodex)
	rules[4].Target = providerTarget
	rules[4].APIType = testAPIType(apicontract.APITypeCodex)

	want := []RuleID{rules[3].ID, rules[4].ID, rules[2].ID, rules[1].ID, rules[0].ID}
	permutation := append([]Rule(nil), rules...)
	permutations := 0
	for {
		snapshot, compileErr := CompileRuleSet(1, permutation)
		if compileErr != nil {
			t.Fatalf("CompileRuleSet() error = %v", compileErr)
		}
		result := snapshot.Match(RequestScope{ProviderID: "provider-a", APIType: apicontract.APITypeCodex}, SemanticFields{Message: "OVERLOADED"})
		got := make([]RuleID, len(result.All))
		for index := range result.All {
			got[index] = result.All[index].Rule.ID
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("permutation %d order = %v, want %v", permutations, got, want)
		}
		permutations++
		if !nextRulePermutation(permutation) {
			break
		}
	}
	if permutations != 120 {
		t.Fatalf("tested %d permutations, want 120", permutations)
	}
	if result := comparePrecedence(
		Rule{ID: "22222222-2222-4222-8222-222222222222", Position: 0},
		Rule{ID: "11111111-1111-4111-8111-111111111111", Position: 0},
		RequestScope{},
	); result <= 0 {
		t.Fatalf("ID fallback comparison = %d, want positive", result)
	}
}

func nextRulePermutation(rules []Rule) bool {
	pivot := len(rules) - 2
	for pivot >= 0 && rules[pivot].ID > rules[pivot+1].ID {
		pivot--
	}
	if pivot < 0 {
		return false
	}
	successor := len(rules) - 1
	for rules[successor].ID < rules[pivot].ID {
		successor--
	}
	rules[pivot], rules[successor] = rules[successor], rules[pivot]
	for left, right := pivot+1, len(rules)-1; left < right; left, right = left+1, right-1 {
		rules[left], rules[right] = rules[right], rules[left]
	}
	return true
}

func TestCompileRuleSetCapacityBoundary(t *testing.T) {
	rules := make([]Rule, MaxRuleCount)
	for index := range rules {
		rules[index] = testRule(t, index, int64(index))
		rules[index].Keywords = []string{strings.Repeat("x", MaxKeywordBytes)}
	}
	snapshot, err := CompileRuleSet(1, rules)
	if err != nil {
		t.Fatalf("CompileRuleSet(max) error = %v", err)
	}
	wantBytes := MaxRuleCount * MaxKeywordBytes
	if snapshot.MatcherBytes() != wantBytes || snapshot.MatcherBytes() > MaxCompiledMatcherBytes {
		t.Fatalf("matcher bytes = %d, want %d below %d", snapshot.MatcherBytes(), wantBytes, MaxCompiledMatcherBytes)
	}
}

func ExampleCompiledRuleSet_Match() {
	fmt.Println("rules are resolved from immutable request-scoped snapshots")
	// Output: rules are resolved from immutable request-scoped snapshots
}
