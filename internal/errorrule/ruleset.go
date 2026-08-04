package errorrule

import (
	"fmt"
	"sort"
	"time"
)

type CompiledRuleSet struct {
	revision     Revision
	rules        []Rule
	enabled      []Rule
	matcherBytes int
}

func CompileRuleSet(revision Revision, rules []Rule) (*CompiledRuleSet, error) {
	if err := revision.Validate(); err != nil {
		return nil, err
	}
	if len(rules) > MaxRuleCount {
		return nil, fmt.Errorf("rule set cannot contain more than %d rules", MaxRuleCount)
	}

	compiled := &CompiledRuleSet{revision: revision, rules: make([]Rule, len(rules))}
	positions := make([]bool, len(rules))
	ids := make(map[RuleID]struct{}, len(rules))
	generations := make(map[RuleGeneration]struct{}, len(rules))
	for index, rule := range rules {
		if err := validateRule(rule, len(rules), positions, ids, generations); err != nil {
			return nil, fmt.Errorf("rule %d: %w", index, err)
		}
		compiled.rules[index] = cloneRule(rule)
		if rule.Enabled {
			compiled.enabled = append(compiled.enabled, cloneRule(rule))
			for _, keyword := range rule.Keywords {
				compiled.matcherBytes += len(keyword)
			}
		}
	}
	if compiled.matcherBytes > MaxCompiledMatcherBytes {
		return nil, fmt.Errorf("compiled matcher bytes exceed %d", MaxCompiledMatcherBytes)
	}

	sort.Slice(compiled.rules, func(left, right int) bool {
		return compiled.rules[left].Position < compiled.rules[right].Position
	})
	return compiled, nil
}

func validateRule(
	rule Rule,
	ruleCount int,
	positions []bool,
	ids map[RuleID]struct{},
	generations map[RuleGeneration]struct{},
) error {
	if err := rule.ID.Validate(); err != nil {
		return err
	}
	if _, duplicate := ids[rule.ID]; duplicate {
		return fmt.Errorf("duplicate rule ID %q", rule.ID)
	}
	ids[rule.ID] = struct{}{}

	if rule.generation.IsZero() {
		return fmt.Errorf("rule generation is required")
	}
	if _, duplicate := generations[rule.generation]; duplicate {
		return fmt.Errorf("duplicate rule generation %q", rule.generation.String())
	}
	generations[rule.generation] = struct{}{}

	if rule.Position < 0 || rule.Position >= int64(ruleCount) {
		return fmt.Errorf("position %d is outside the dense rule set", rule.Position)
	}
	if positions[rule.Position] {
		return fmt.Errorf("duplicate position %d", rule.Position)
	}
	positions[rule.Position] = true
	if err := validateRuleTimes(rule.CreatedAt, rule.UpdatedAt); err != nil {
		return err
	}
	return ValidateNormalizedRuleSpec(rule.RuleSpec)
}

func validateRuleTimes(createdAt, updatedAt time.Time) error {
	if createdAt.IsZero() || updatedAt.IsZero() {
		return fmt.Errorf("created_at and updated_at are required")
	}
	if updatedAt.Before(createdAt) {
		return fmt.Errorf("updated_at cannot precede created_at")
	}
	_, createdOffset := createdAt.Zone()
	_, updatedOffset := updatedAt.Zone()
	if createdOffset != 0 || updatedOffset != 0 {
		return fmt.Errorf("created_at and updated_at must be UTC")
	}
	return nil
}

func (s *CompiledRuleSet) Revision() Revision {
	if s == nil {
		return 0
	}
	return s.revision
}

func (s *CompiledRuleSet) MatcherBytes() int {
	if s == nil {
		return 0
	}
	return s.matcherBytes
}

func (s *CompiledRuleSet) Rules() []Rule {
	if s == nil {
		return nil
	}
	rules := make([]Rule, len(s.rules))
	for index, rule := range s.rules {
		rules[index] = cloneRule(rule)
	}
	return rules
}

func (s *CompiledRuleSet) Rule(id RuleID) (Rule, bool) {
	if s == nil {
		return Rule{}, false
	}
	for _, rule := range s.rules {
		if rule.ID == id {
			return cloneRule(rule), true
		}
	}
	return Rule{}, false
}

type RuleSetProvider interface {
	CurrentRuleSet() *CompiledRuleSet
}

type RuleSetPublisher interface {
	PublishRuleSet(*CompiledRuleSet) error
}

func PinRuleSet(provider RuleSetProvider) (*CompiledRuleSet, error) {
	if provider == nil {
		return nil, fmt.Errorf("rule set provider is required")
	}
	snapshot := provider.CurrentRuleSet()
	if snapshot == nil {
		return nil, fmt.Errorf("rule set provider returned nil")
	}
	return snapshot, nil
}
