package sqlite

import (
	"context"
	"fmt"

	"github.com/doraemonkeys/switch-a/internal/errorrule"
	"gorm.io/gorm"
)

func (r *Repository) CreateRule(
	ctx context.Context,
	expected errorrule.Revision,
	spec errorrule.RuleSpec,
) (MutationResult, error) {
	result, err := r.Coordinate(ctx, &expected, func(_ *gorm.DB, current []errorrule.Rule) ([]errorrule.Rule, error) {
		if len(current) >= errorrule.MaxRuleCount {
			return nil, ErrRuleCapacity
		}
		id := errorrule.RuleID(r.ids.NewID())
		if err := id.Validate(); err != nil {
			return nil, fmt.Errorf("generate rule ID: %w", err)
		}
		return append(current, errorrule.NewRule(spec, errorrule.RuleMetadata{ID: id})), nil
	})
	if err != nil {
		return MutationResult{}, err
	}
	return result, nil
}

func (r *Repository) UpdateRule(
	ctx context.Context,
	expected errorrule.Revision,
	id errorrule.RuleID,
	spec errorrule.RuleSpec,
) (MutationResult, error) {
	return r.Coordinate(ctx, &expected, func(_ *gorm.DB, current []errorrule.Rule) ([]errorrule.Rule, error) {
		for index := range current {
			if current[index].ID != id {
				continue
			}
			current[index] = errorrule.NewRule(spec, errorrule.RuleMetadata{ID: id})
			return current, nil
		}
		return nil, ErrRuleNotFound
	})
}

func (r *Repository) DeleteRule(
	ctx context.Context,
	expected errorrule.Revision,
	id errorrule.RuleID,
) (MutationResult, error) {
	return r.Coordinate(ctx, &expected, func(_ *gorm.DB, current []errorrule.Rule) ([]errorrule.Rule, error) {
		for index := range current {
			if current[index].ID != id {
				continue
			}
			return append(current[:index:index], current[index+1:]...), nil
		}
		return nil, ErrRuleNotFound
	})
}

func (r *Repository) ReorderRules(
	ctx context.Context,
	expected errorrule.Revision,
	ordered []errorrule.RuleID,
) (MutationResult, error) {
	return r.Coordinate(ctx, &expected, func(_ *gorm.DB, current []errorrule.Rule) ([]errorrule.Rule, error) {
		if len(ordered) != len(current) {
			return nil, fmt.Errorf("reorder must contain every rule exactly once")
		}
		byID := make(map[errorrule.RuleID]errorrule.Rule, len(current))
		for _, rule := range current {
			byID[rule.ID] = rule
		}
		candidate := make([]errorrule.Rule, len(ordered))
		for index, id := range ordered {
			rule, exists := byID[id]
			if !exists {
				return nil, fmt.Errorf("reorder contains duplicate or unknown rule %q", id)
			}
			candidate[index] = rule
			delete(byID, id)
		}
		if len(byID) != 0 {
			return nil, fmt.Errorf("reorder is missing a rule")
		}
		return candidate, nil
	})
}

func RemoveProviderRules(current []errorrule.Rule, providerID string) []errorrule.Rule {
	candidate := make([]errorrule.Rule, 0, len(current))
	for _, rule := range current {
		targetProvider, scoped := rule.Target.ProviderID()
		if scoped && string(targetProvider) == providerID {
			continue
		}
		candidate = append(candidate, rule)
	}
	return candidate
}
