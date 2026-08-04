package errorrule

import (
	"reflect"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/apicontract"
)

func TestMatcherAnyAllAndFieldEvidence(t *testing.T) {
	anyRule := testRule(t, 0, 0)
	anyRule.Keywords = []string{"capacity", "server_is_overloaded", "absent"}
	anyRule.MatchMode = MatchAny
	allRule := testRule(t, 1, 1)
	allRule.Keywords = []string{"capacity", "server_is_overloaded"}
	allRule.MatchMode = MatchAll
	snapshot, err := CompileRuleSet(9, []Rule{anyRule, allRule})
	if err != nil {
		t.Fatalf("CompileRuleSet() error = %v", err)
	}

	result := snapshot.Match(RequestScope{APIType: apicontract.APITypeCodex}, SemanticFields{
		Type:    " SERVER_IS_OVERLOADED ",
		Message: " Our servers are at CAPACITY ",
	})
	if len(result.All) != 2 || result.Winner == nil || result.Winner.Rule.ID != anyRule.ID {
		t.Fatalf("Match() = %#v", result)
	}
	winner := result.All[0]
	if !reflect.DeepEqual(winner.MatchedKeywords, []string{"capacity", "server_is_overloaded"}) ||
		!reflect.DeepEqual(winner.MatchedKeywordIndexes, []int{0, 1}) ||
		!reflect.DeepEqual(winner.MatchedFields, []SemanticField{FieldType, FieldMessage}) {
		t.Fatalf("winner evidence = %#v", winner)
	}

	result.All[0].Rule.Keywords[0] = "mutated"
	result.Winner.MatchedKeywords[0] = "mutated"
	again := snapshot.Match(RequestScope{APIType: apicontract.APITypeCodex}, SemanticFields{Message: "capacity"})
	if again.All[0].Rule.Keywords[0] != "capacity" || again.Winner.MatchedKeywords[0] != "capacity" {
		t.Fatal("match result mutated compiled snapshot")
	}
}

func TestMatcherNeverConcatenatesFieldsOrScansOtherContent(t *testing.T) {
	rule := testRule(t, 0, 0)
	rule.Keywords = []string{"foo bar"}
	rule.MatchMode = MatchAll
	snapshot, err := CompileRuleSet(1, []Rule{rule})
	if err != nil {
		t.Fatalf("CompileRuleSet() error = %v", err)
	}
	scope := RequestScope{APIType: apicontract.APITypeCodex}
	if result := snapshot.Match(scope, SemanticFields{Type: "foo", Message: "bar"}); result.Winner != nil {
		t.Fatalf("cross-field concatenation matched: %#v", result)
	}
	ordinaryOutput := "foo bar"
	if ordinaryOutput == "" {
		t.Fatal("test setup invalid")
	}
	if result := snapshot.Match(scope, SemanticFields{}); result.Winner != nil {
		t.Fatalf("matcher inspected unextracted ordinary output: %#v", result)
	}
	if result := snapshot.Match(scope, SemanticFields{Reason: "prefix foo bar suffix"}); result.Winner == nil {
		t.Fatal("substring in one whitelisted field did not match")
	}
}

func TestMatcherAllRequiresEveryKeyword(t *testing.T) {
	rule := testRule(t, 0, 0)
	rule.Keywords = []string{"one", "two"}
	rule.MatchMode = MatchAll
	snapshot, err := CompileRuleSet(1, []Rule{rule})
	if err != nil {
		t.Fatalf("CompileRuleSet() error = %v", err)
	}
	scope := RequestScope{APIType: apicontract.APITypeCodex}
	if result := snapshot.Match(scope, SemanticFields{Code: "one"}); result.Winner != nil {
		t.Fatal("all mode matched a partial keyword set")
	}
	if result := snapshot.Match(scope, SemanticFields{Code: "one", Reason: "two"}); result.Winner == nil {
		t.Fatal("all mode did not match keywords across fields")
	}
}

func TestCandidateScopeAndDetectionPlan(t *testing.T) {
	providerTarget, err := NewProviderTarget("provider-a")
	if err != nil {
		t.Fatalf("NewProviderTarget() error = %v", err)
	}
	passthrough := testRule(t, 0, 0)
	passthrough.Action = NewPassthroughAction()
	passthrough.APIType = nil
	retryZero := testRule(t, 1, 1)
	retryZero.Action = testRetryAction(t, ActionRetryOnly, 0)
	retryZero.Target = providerTarget
	retryPositive := testRule(t, 2, 2)
	retryPositive.Action = testRetryAction(t, ActionRetryOnly, 1)
	retryPositive.Target = providerTarget
	retryPositive.APIType = testAPIType(apicontract.APITypeClaude)
	switchZero := testRule(t, 3, 3)
	switchZero.Action = testRetryAction(t, ActionRetryThenSwitch, 0)
	switchZero.Target = providerTarget
	switchZero.APIType = testAPIType(apicontract.APITypeGemini)
	disabled := testRule(t, 4, 4)
	disabled.Enabled = false

	snapshot, err := CompileRuleSet(1, []Rule{passthrough, retryZero, retryPositive, switchZero, disabled})
	if err != nil {
		t.Fatalf("CompileRuleSet() error = %v", err)
	}
	cases := []struct {
		name  string
		scope RequestScope
		plan  DetectionPlan
		count int
	}{
		{name: "global observer", scope: RequestScope{APIType: apicontract.APITypeCodex}, plan: DetectionObserveOnly, count: 1},
		{name: "provider zero retry observer", scope: RequestScope{ProviderID: "provider-a", APIType: apicontract.APITypeCodex}, plan: DetectionObserveOnly, count: 2},
		{name: "positive retry probes", scope: RequestScope{ProviderID: "provider-a", APIType: apicontract.APITypeClaude}, plan: DetectionProbe, count: 2},
		{name: "zero retry switch probes", scope: RequestScope{ProviderID: "provider-a", APIType: apicontract.APITypeGemini}, plan: DetectionProbe, count: 2},
		{name: "other provider gets global", scope: RequestScope{ProviderID: "provider-b", APIType: apicontract.APITypeCodex}, plan: DetectionObserveOnly, count: 1},
		{name: "custom unsupported", scope: RequestScope{ProviderID: "provider-a", APIType: "custom:tool"}, plan: DetectionNoCandidate, count: 0},
		{name: "unknown unsupported", scope: RequestScope{APIType: "unknown"}, plan: DetectionNoCandidate, count: 0},
	}
	for _, current := range cases {
		t.Run(current.name, func(t *testing.T) {
			if got := snapshot.DetectionPlan(current.scope); got != current.plan {
				t.Fatalf("DetectionPlan() = %q, want %q", got, current.plan)
			}
			candidates := snapshot.Candidates(current.scope)
			if len(candidates) != current.count {
				t.Fatalf("candidate count = %d, want %d", len(candidates), current.count)
			}
			if len(candidates) > 0 {
				candidates[0].Keywords[0] = "mutated"
				if snapshot.Candidates(current.scope)[0].Keywords[0] == "mutated" {
					t.Fatal("Candidates exposed snapshot storage")
				}
			}
		})
	}

	var nilSnapshot *CompiledRuleSet
	if nilSnapshot.DetectionPlan(RequestScope{APIType: apicontract.APITypeCodex}) != DetectionNoCandidate || nilSnapshot.Candidates(RequestScope{}) != nil {
		t.Fatal("nil snapshot candidate planning is not zero-safe")
	}
}
