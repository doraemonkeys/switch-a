package errorrule

import (
	"strings"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/apicontract"
)

func TestNormalizeRuleSpec(t *testing.T) {
	spec := testRule(t, 0, 0).RuleSpec
	spec.Name = "\u2003 Capacity rule \n"
	spec.Keywords = []string{" Foo ", "foo", " BAR\u00a0", "ß"}
	normalized, err := NormalizeRuleSpec(spec)
	if err != nil {
		t.Fatalf("NormalizeRuleSpec() error = %v", err)
	}
	if normalized.Name != "Capacity rule" {
		t.Fatalf("name = %q", normalized.Name)
	}
	wantKeywords := []string{"foo", "bar", "ß"}
	if strings.Join(normalized.Keywords, "|") != strings.Join(wantKeywords, "|") {
		t.Fatalf("keywords = %#v, want %#v", normalized.Keywords, wantKeywords)
	}
	if err := ValidateNormalizedRuleSpec(normalized); err != nil {
		t.Fatalf("normalized result invalid: %v", err)
	}
	if err := ValidateNormalizedRuleSpec(spec); err == nil {
		t.Fatal("non-canonical spec unexpectedly accepted")
	}

	rule := testRule(t, 1, 0)
	rule.Name = " Rule "
	normalizedRule, err := NormalizeRule(rule)
	if err != nil {
		t.Fatalf("NormalizeRule() error = %v", err)
	}
	if normalizedRule.Name != "Rule" || normalizedRule.ID != rule.ID || normalizedRule.Generation() != rule.Generation() {
		t.Fatalf("normalized rule did not preserve identity: %#v", normalizedRule)
	}
}

func TestRuleSpecValidationBoundaries(t *testing.T) {
	base := testRule(t, 0, 0).RuleSpec
	custom := apicontract.APIType("custom:tool")
	unknown := apicontract.APIType("unknown")

	cases := []struct {
		name   string
		mutate func(*RuleSpec)
		valid  bool
	}{
		{name: "valid", mutate: func(*RuleSpec) {}, valid: true},
		{name: "nil API means all builtins", mutate: func(s *RuleSpec) { s.APIType = nil }, valid: true},
		{name: "disabled custom retained", mutate: func(s *RuleSpec) { s.Enabled = false; s.APIType = &custom }, valid: true},
		{name: "enabled custom rejected", mutate: func(s *RuleSpec) { s.APIType = &custom }},
		{name: "unknown API rejected", mutate: func(s *RuleSpec) { s.Enabled = false; s.APIType = &unknown }},
		{name: "name exact bytes", mutate: func(s *RuleSpec) { s.Name = strings.Repeat("n", MaxRuleNameBytes) }, valid: true},
		{name: "name over bytes", mutate: func(s *RuleSpec) { s.Name = strings.Repeat("n", MaxRuleNameBytes+1) }},
		{name: "name empty", mutate: func(s *RuleSpec) { s.Name = " " }},
		{name: "name control", mutate: func(s *RuleSpec) { s.Name = "bad\x00name" }},
		{name: "keyword exact bytes", mutate: func(s *RuleSpec) { s.Keywords = []string{strings.Repeat("k", MaxKeywordBytes)} }, valid: true},
		{name: "keyword over bytes", mutate: func(s *RuleSpec) { s.Keywords = []string{strings.Repeat("k", MaxKeywordBytes+1)} }},
		{name: "maximum keyword count and bytes", mutate: func(s *RuleSpec) {
			s.Keywords = make([]string, MaxKeywordsPerRule)
			for index := range s.Keywords {
				s.Keywords[index] = strings.Repeat(string(rune('a'+index)), MaxKeywordBytes)
			}
		}, valid: true},
		{name: "too many keywords", mutate: func(s *RuleSpec) {
			s.Keywords = make([]string, MaxKeywordsPerRule+1)
			for index := range s.Keywords {
				s.Keywords[index] = string(rune('a' + index))
			}
		}},
		{name: "empty keyword", mutate: func(s *RuleSpec) { s.Keywords = []string{" "} }},
		{name: "keyword control", mutate: func(s *RuleSpec) { s.Keywords = []string{"bad\nvalue"} }},
		{name: "keyword invalid UTF8", mutate: func(s *RuleSpec) { s.Keywords = []string{string([]byte{0xff})} }},
		{name: "duplicate normalized", mutate: func(s *RuleSpec) { s.Keywords = []string{"same", "same"} }, valid: true},
		{name: "invalid target", mutate: func(s *RuleSpec) { s.Target = Target{} }},
		{name: "invalid match mode", mutate: func(s *RuleSpec) { s.MatchMode = "some" }},
		{name: "invalid action", mutate: func(s *RuleSpec) { s.Action = Action{} }},
	}

	for _, current := range cases {
		t.Run(current.name, func(t *testing.T) {
			spec := cloneRuleSpec(base)
			current.mutate(&spec)
			_, err := NormalizeRuleSpec(spec)
			if current.valid && err != nil {
				t.Fatalf("NormalizeRuleSpec() error = %v", err)
			}
			if !current.valid && err == nil {
				t.Fatal("NormalizeRuleSpec() unexpectedly succeeded")
			}
		})
	}
}

func TestNormalizeDeduplicatesBeforeLimits(t *testing.T) {
	spec := testRule(t, 0, 0).RuleSpec
	spec.Keywords = make([]string, MaxKeywordsPerRule+10)
	for index := range spec.Keywords {
		spec.Keywords[index] = " DUPLICATE "
	}
	normalized, err := NormalizeRuleSpec(spec)
	if err != nil {
		t.Fatalf("NormalizeRuleSpec() error = %v", err)
	}
	if len(normalized.Keywords) != 1 || normalized.Keywords[0] != "duplicate" {
		t.Fatalf("keywords = %#v", normalized.Keywords)
	}
}
