package errorrule

import (
	"sort"
	"strings"

	"github.com/doraemonkeys/switch-a/internal/apicontract"
)

type RequestScope struct {
	ProviderID ProviderID
	APIType    apicontract.APIType
}

type SemanticField string

const (
	FieldType    SemanticField = "type"
	FieldCode    SemanticField = "code"
	FieldMessage SemanticField = "message"
	FieldReason  SemanticField = "reason"
)

type SemanticFields struct {
	Type    string
	Code    string
	Message string
	Reason  string
}

type RuleMatch struct {
	Rule                  Rule
	MatchedKeywords       []string
	MatchedKeywordIndexes []int
	MatchedFields         []SemanticField
}

type MatchResult struct {
	All    []RuleMatch
	Winner *RuleMatch
}

func (s *CompiledRuleSet) Match(scope RequestScope, fields SemanticFields) MatchResult {
	result := MatchResult{}
	for _, rule := range s.candidates(scope) {
		matched, ok := matchRule(rule, fields)
		if !ok {
			continue
		}
		result.All = append(result.All, matched)
	}
	if len(result.All) > 0 {
		winner := cloneRuleMatch(result.All[0])
		result.Winner = &winner
	}
	return result
}

func (s *CompiledRuleSet) Candidates(scope RequestScope) []Rule {
	candidates := s.candidates(scope)
	if len(candidates) == 0 {
		return nil
	}
	result := make([]Rule, len(candidates))
	for index, rule := range candidates {
		result[index] = cloneRule(rule)
	}
	return result
}

func (s *CompiledRuleSet) candidates(scope RequestScope) []Rule {
	if s == nil || !apicontract.IsBuiltIn(string(scope.APIType)) {
		return nil
	}
	candidates := make([]Rule, 0, len(s.enabled))
	for _, rule := range s.enabled {
		if ruleApplies(rule, scope) {
			candidates = append(candidates, rule)
		}
	}
	sort.Slice(candidates, func(left, right int) bool {
		return comparePrecedence(candidates[left], candidates[right], scope) < 0
	})
	return candidates
}

func ruleApplies(rule Rule, scope RequestScope) bool {
	if rule.Target.kind == TargetProvider && rule.Target.providerID != scope.ProviderID {
		return false
	}
	return rule.APIType == nil || *rule.APIType == scope.APIType
}

func comparePrecedence(left, right Rule, scope RequestScope) int {
	leftProvider := left.Target.kind == TargetProvider
	rightProvider := right.Target.kind == TargetProvider
	if leftProvider != rightProvider {
		if leftProvider {
			return -1
		}
		return 1
	}

	leftExact := left.APIType != nil && *left.APIType == scope.APIType
	rightExact := right.APIType != nil && *right.APIType == scope.APIType
	if leftExact != rightExact {
		if leftExact {
			return -1
		}
		return 1
	}
	if left.Position < right.Position {
		return -1
	}
	if left.Position > right.Position {
		return 1
	}
	return strings.Compare(string(left.ID), string(right.ID))
}

func matchRule(rule Rule, fields SemanticFields) (RuleMatch, bool) {
	normalizedFields := []struct {
		name  SemanticField
		value string
	}{
		{name: FieldType, value: normalizeField(fields.Type)},
		{name: FieldCode, value: normalizeField(fields.Code)},
		{name: FieldMessage, value: normalizeField(fields.Message)},
		{name: FieldReason, value: normalizeField(fields.Reason)},
	}

	fieldHits := make(map[SemanticField]bool, len(normalizedFields))
	match := RuleMatch{Rule: cloneRule(rule)}
	for keywordIndex, keyword := range rule.Keywords {
		keywordMatched := false
		for _, field := range normalizedFields {
			if field.value != "" && strings.Contains(field.value, keyword) {
				keywordMatched = true
				fieldHits[field.name] = true
			}
		}
		if keywordMatched {
			match.MatchedKeywords = append(match.MatchedKeywords, keyword)
			match.MatchedKeywordIndexes = append(match.MatchedKeywordIndexes, keywordIndex)
		}
	}

	matched := len(match.MatchedKeywords) > 0
	if rule.MatchMode == MatchAll {
		matched = len(match.MatchedKeywords) == len(rule.Keywords)
	}
	if !matched {
		return RuleMatch{}, false
	}
	for _, field := range normalizedFields {
		if fieldHits[field.name] {
			match.MatchedFields = append(match.MatchedFields, field.name)
		}
	}
	return match, true
}

func normalizeField(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func cloneRuleMatch(match RuleMatch) RuleMatch {
	clone := match
	clone.Rule = cloneRule(match.Rule)
	clone.MatchedKeywords = append([]string(nil), match.MatchedKeywords...)
	clone.MatchedKeywordIndexes = append([]int(nil), match.MatchedKeywordIndexes...)
	clone.MatchedFields = append([]SemanticField(nil), match.MatchedFields...)
	return clone
}

type DetectionPlan string

const (
	DetectionNoCandidate DetectionPlan = "no_candidate"
	DetectionObserveOnly DetectionPlan = "observe_only"
	DetectionProbe       DetectionPlan = "probe"
)

func (s *CompiledRuleSet) DetectionPlan(scope RequestScope) DetectionPlan {
	candidates := s.candidates(scope)
	if len(candidates) == 0 {
		return DetectionNoCandidate
	}
	for _, rule := range candidates {
		retry, hasRetry := rule.Action.RetryPolicy()
		// Client-owned recovery still needs a probe when gateway retries are
		// disabled: the semantic frame must be detected before it is forwarded,
		// otherwise the client cannot turn the incomplete SSE stream into its own
		// retry signal.
		if rule.Action.Type() == ActionRetryThenSwitch ||
			hasRetry && (retry.MaxRetries > 0 || rule.Action.VisibleResponsePolicy() == VisibleResponseDisconnect) {
			return DetectionProbe
		}
	}
	return DetectionObserveOnly
}
