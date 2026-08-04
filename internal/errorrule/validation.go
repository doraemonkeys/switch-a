package errorrule

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/doraemonkeys/switch-a/internal/apicontract"
)

func NormalizeRuleSpec(spec RuleSpec) (RuleSpec, error) {
	if !utf8.ValidString(spec.Name) {
		return RuleSpec{}, fmt.Errorf("rule name must be valid UTF-8")
	}
	for index, keyword := range spec.Keywords {
		if !utf8.ValidString(keyword) {
			return RuleSpec{}, fmt.Errorf("keyword %d must be valid UTF-8", index)
		}
	}
	normalized := cloneRuleSpec(spec)
	normalized.Name = strings.TrimSpace(spec.Name)
	normalized.Keywords = normalizeKeywords(spec.Keywords)
	if err := ValidateNormalizedRuleSpec(normalized); err != nil {
		return RuleSpec{}, err
	}
	return normalized, nil
}

func NormalizeRule(rule Rule) (Rule, error) {
	spec, err := NormalizeRuleSpec(rule.RuleSpec)
	if err != nil {
		return Rule{}, err
	}
	clone := cloneRule(rule)
	clone.RuleSpec = spec
	return clone, nil
}

func normalizeKeywords(keywords []string) []string {
	normalized := make([]string, 0, len(keywords))
	seen := make(map[string]struct{}, len(keywords))
	for _, keyword := range keywords {
		keyword = strings.ToLower(strings.TrimSpace(keyword))
		if _, exists := seen[keyword]; exists {
			continue
		}
		seen[keyword] = struct{}{}
		normalized = append(normalized, keyword)
	}
	return normalized
}

// ValidateNormalizedRuleSpec deliberately rejects non-canonical input. Startup
// compilation must expose corrupt persisted data instead of silently repairing
// it into a snapshot whose bytes differ from storage.
func ValidateNormalizedRuleSpec(spec RuleSpec) error {
	if strings.TrimSpace(spec.Name) != spec.Name {
		return fmt.Errorf("rule name must be trimmed")
	}
	if spec.Name == "" {
		return fmt.Errorf("rule name is required")
	}
	if !utf8.ValidString(spec.Name) || len(spec.Name) > MaxRuleNameBytes {
		return fmt.Errorf("rule name must be valid UTF-8 and at most %d bytes", MaxRuleNameBytes)
	}
	if containsControl(spec.Name) {
		return fmt.Errorf("rule name cannot contain control characters")
	}
	if err := spec.Target.Validate(); err != nil {
		return fmt.Errorf("target: %w", err)
	}
	if err := validateAPIType(spec); err != nil {
		return err
	}
	if err := validateKeywords(spec.Keywords); err != nil {
		return err
	}
	if spec.MatchMode != MatchAny && spec.MatchMode != MatchAll {
		return fmt.Errorf("match_mode must be %q or %q", MatchAny, MatchAll)
	}
	if err := spec.Action.Validate(); err != nil {
		return fmt.Errorf("action: %w", err)
	}
	return nil
}

func validateAPIType(spec RuleSpec) error {
	if spec.APIType == nil {
		return nil
	}
	apiType := string(*spec.APIType)
	if apicontract.IsBuiltIn(apiType) {
		if spec.Enabled && !apicontract.SupportsSemanticErrors(apiType) {
			return fmt.Errorf("enabled rule API type %q does not support semantic errors", apiType)
		}
		return nil
	}
	if _, custom := apicontract.ParseCustomAPIType(apiType); custom {
		if spec.Enabled {
			return fmt.Errorf("enabled rule cannot target custom API type %q", apiType)
		}
		return nil
	}
	return fmt.Errorf("unknown API type %q", apiType)
}

func validateKeywords(keywords []string) error {
	if len(keywords) == 0 {
		return fmt.Errorf("at least one keyword is required")
	}
	if len(keywords) > MaxKeywordsPerRule {
		return fmt.Errorf("keywords cannot contain more than %d entries", MaxKeywordsPerRule)
	}

	seen := make(map[string]struct{}, len(keywords))
	totalBytes := 0
	for index, keyword := range keywords {
		if keyword != strings.ToLower(strings.TrimSpace(keyword)) {
			return fmt.Errorf("keyword %d is not normalized", index)
		}
		if keyword == "" {
			return fmt.Errorf("keyword %d is empty", index)
		}
		if !utf8.ValidString(keyword) || len(keyword) > MaxKeywordBytes {
			return fmt.Errorf("keyword %d must be valid UTF-8 and at most %d bytes", index, MaxKeywordBytes)
		}
		if containsControl(keyword) {
			return fmt.Errorf("keyword %d cannot contain control characters", index)
		}
		if _, duplicate := seen[keyword]; duplicate {
			return fmt.Errorf("keyword %d duplicates a normalized keyword", index)
		}
		seen[keyword] = struct{}{}
		totalBytes += len(keyword)
	}
	if totalBytes > MaxKeywordBytesPerRule {
		return fmt.Errorf("keyword bytes cannot exceed %d per rule", MaxKeywordBytesPerRule)
	}
	return nil
}

func containsControl(value string) bool {
	for _, current := range value {
		if unicode.IsControl(current) {
			return true
		}
	}
	return false
}
