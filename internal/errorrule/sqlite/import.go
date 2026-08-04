package sqlite

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/doraemonkeys/switch-a/internal/errorrule"
)

type ImportMode string

const (
	ImportModeFull      ImportMode = "full"
	ImportModePreserve  ImportMode = "preserve"
	ImportModeSelection ImportMode = "selection"
)

type ImportedRule struct {
	ID errorrule.RuleID
	errorrule.RuleSpec
}

type ImportRequest struct {
	Mode                ImportMode
	Rules               []ImportedRule
	SelectedProviderIDs []string
}

type ImportCounts struct {
	Add       int
	Update    int
	Delete    int
	Unchanged int
}

func BuildImportCandidate(
	current []errorrule.Rule,
	request ImportRequest,
) ([]errorrule.Rule, ImportCounts, error) {
	if request.Mode == ImportModePreserve {
		return cloneRules(current), ImportCounts{}, nil
	}
	imported, err := normalizeImportedRules(request.Rules)
	if err != nil {
		return nil, ImportCounts{}, err
	}

	var candidate []errorrule.Rule
	switch request.Mode {
	case ImportModeFull:
		candidate = imported
	case ImportModeSelection:
		selected := selectedProviderSet(request.SelectedProviderIDs)
		if len(selected) == 0 {
			return nil, ImportCounts{}, fmt.Errorf("selection import requires at least one provider")
		}
		preserved := make([]errorrule.Rule, 0, len(current))
		preservedIDs := make(map[errorrule.RuleID]struct{}, len(current))
		for _, rule := range current {
			providerID, scoped := rule.Target.ProviderID()
			if scoped {
				if _, replace := selected[string(providerID)]; replace {
					continue
				}
			}
			preserved = append(preserved, rule)
			preservedIDs[rule.ID] = struct{}{}
		}
		candidate = append(candidate, preserved...)
		for _, rule := range imported {
			providerID, scoped := rule.Target.ProviderID()
			if !scoped {
				continue
			}
			if _, selectedProvider := selected[string(providerID)]; !selectedProvider {
				continue
			}
			if _, collision := preservedIDs[rule.ID]; collision {
				return nil, ImportCounts{}, fmt.Errorf("%w: %s", ErrImportIDCollision, rule.ID)
			}
			candidate = append(candidate, rule)
		}
	default:
		return nil, ImportCounts{}, fmt.Errorf("unsupported rule import mode %q", request.Mode)
	}
	if len(candidate) > errorrule.MaxRuleCount {
		return nil, ImportCounts{}, ErrRuleCapacity
	}
	return candidate, countImportChanges(current, candidate), nil
}

func normalizeImportedRules(imported []ImportedRule) ([]errorrule.Rule, error) {
	if len(imported) > errorrule.MaxRuleCount {
		return nil, ErrRuleCapacity
	}
	seen := make(map[errorrule.RuleID]struct{}, len(imported))
	result := make([]errorrule.Rule, len(imported))
	for index, raw := range imported {
		if err := raw.ID.Validate(); err != nil {
			return nil, err
		}
		if _, duplicate := seen[raw.ID]; duplicate {
			return nil, fmt.Errorf("duplicate imported rule ID %q", raw.ID)
		}
		seen[raw.ID] = struct{}{}
		spec, err := errorrule.NormalizeRuleSpec(raw.RuleSpec)
		if err != nil {
			return nil, fmt.Errorf("normalize imported rule %q: %w", raw.ID, err)
		}
		result[index] = errorrule.NewRule(spec, errorrule.RuleMetadata{ID: raw.ID, Position: int64(index)})
	}
	return result, nil
}

func selectedProviderSet(ids []string) map[string]struct{} {
	selected := make(map[string]struct{}, len(ids))
	for _, raw := range ids {
		id := strings.TrimSpace(raw)
		if id != "" {
			selected[id] = struct{}{}
		}
	}
	return selected
}

func countImportChanges(current, candidate []errorrule.Rule) ImportCounts {
	currentByID := make(map[errorrule.RuleID]errorrule.Rule, len(current))
	for _, rule := range current {
		currentByID[rule.ID] = rule
	}
	candidateByID := make(map[errorrule.RuleID]errorrule.Rule, len(candidate))
	counts := ImportCounts{}
	for index, rule := range candidate {
		candidateByID[rule.ID] = rule
		existing, found := currentByID[rule.ID]
		switch {
		case !found:
			counts.Add++
		case existing.Position != int64(index) || !reflect.DeepEqual(existing.RuleSpec, rule.RuleSpec):
			counts.Update++
		default:
			counts.Unchanged++
		}
	}
	for _, rule := range current {
		if _, retained := candidateByID[rule.ID]; !retained {
			counts.Delete++
		}
	}
	return counts
}
