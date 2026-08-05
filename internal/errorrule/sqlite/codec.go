package sqlite

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/doraemonkeys/switch-a/internal/apicontract"
	"github.com/doraemonkeys/switch-a/internal/errorrule"
	"github.com/doraemonkeys/switch-a/internal/model"
)

func encodeRule(rule errorrule.Rule) (ruleRow, error) {
	keywords, err := json.Marshal(rule.Keywords)
	if err != nil {
		return ruleRow{}, fmt.Errorf("encode keywords: %w", err)
	}
	row := ruleRow{
		ID:           string(rule.ID),
		Generation:   rule.Generation().String(),
		Name:         rule.Name,
		Enabled:      rule.Enabled,
		TargetKind:   string(rule.Target.Kind()),
		KeywordsJSON: string(keywords),
		MatchMode:    string(rule.MatchMode),
		ActionType:   string(rule.Action.Type()),
		Position:     rule.Position,
		CreatedAt:    rule.CreatedAt.UTC(),
		UpdatedAt:    rule.UpdatedAt.UTC(),
	}
	if rule.Action.Type() != errorrule.ActionPassthrough {
		policy := string(rule.Action.VisibleResponsePolicy())
		row.VisiblePolicy = &policy
	}
	if providerID, scoped := rule.Target.ProviderID(); scoped {
		value := string(providerID)
		row.ProviderID = &value
	}
	if rule.APIType != nil {
		value := string(*rule.APIType)
		row.APIType = &value
	}
	if retry, ok := rule.Action.RetryPolicy(); ok {
		initialDelay := int64(retry.Backoff.InitialDelay)
		maxDelay := int64(retry.Backoff.MaxDelay)
		row.MaxRetries = intPointer(retry.MaxRetries)
		row.BackoffInitialDelay = &initialDelay
		row.BackoffMaxDelay = &maxDelay
		row.BackoffMultiplier = floatPointer(retry.Backoff.Multiplier)
		row.BackoffJitter = boolPointer(retry.Backoff.Jitter)
	}
	return row, nil
}

func decodeRule(row ruleRow) (errorrule.Rule, error) {
	generation, err := errorrule.ParseRuleGeneration(row.Generation)
	if err != nil {
		return errorrule.Rule{}, err
	}
	target, err := decodeTarget(row)
	if err != nil {
		return errorrule.Rule{}, err
	}
	action, err := decodeAction(row)
	if err != nil {
		return errorrule.Rule{}, err
	}
	var keywords []string
	if err := json.Unmarshal([]byte(row.KeywordsJSON), &keywords); err != nil {
		return errorrule.Rule{}, fmt.Errorf("decode keywords for rule %q: %w", row.ID, err)
	}
	var apiType *apicontract.APIType
	if row.APIType != nil {
		value := apicontract.APIType(*row.APIType)
		apiType = &value
	}
	rule := errorrule.NewRule(errorrule.RuleSpec{
		Name:      row.Name,
		Enabled:   row.Enabled,
		Target:    target,
		APIType:   apiType,
		Keywords:  keywords,
		MatchMode: errorrule.MatchMode(row.MatchMode),
		Action:    action,
	}, errorrule.RuleMetadata{
		ID:         errorrule.RuleID(row.ID),
		Generation: generation,
		Position:   row.Position,
		CreatedAt:  row.CreatedAt.UTC(),
		UpdatedAt:  row.UpdatedAt.UTC(),
	})
	return rule, nil
}

func decodeTarget(row ruleRow) (errorrule.Target, error) {
	switch errorrule.TargetKind(row.TargetKind) {
	case errorrule.TargetGlobal:
		if row.ProviderID != nil {
			return errorrule.Target{}, fmt.Errorf("global rule %q has provider_id", row.ID)
		}
		return errorrule.NewGlobalTarget(), nil
	case errorrule.TargetProvider:
		if row.ProviderID == nil {
			return errorrule.Target{}, fmt.Errorf("provider rule %q is missing provider_id", row.ID)
		}
		return errorrule.NewProviderTarget(errorrule.ProviderID(*row.ProviderID))
	default:
		return errorrule.Target{}, fmt.Errorf("rule %q has unknown target kind %q", row.ID, row.TargetKind)
	}
}

func decodeAction(row ruleRow) (errorrule.Action, error) {
	switch errorrule.ActionType(row.ActionType) {
	case errorrule.ActionPassthrough:
		if row.VisiblePolicy != nil || row.MaxRetries != nil || row.BackoffInitialDelay != nil || row.BackoffMaxDelay != nil ||
			row.BackoffMultiplier != nil || row.BackoffJitter != nil {
			return errorrule.Action{}, fmt.Errorf("passthrough rule %q contains retry fields", row.ID)
		}
		return errorrule.NewPassthroughAction(), nil
	case errorrule.ActionRetryOnly, errorrule.ActionRetryThenSwitch:
		if row.MaxRetries == nil || row.BackoffInitialDelay == nil || row.BackoffMaxDelay == nil ||
			row.BackoffMultiplier == nil || row.BackoffJitter == nil {
			return errorrule.Action{}, fmt.Errorf("retry rule %q is missing retry fields", row.ID)
		}
		backoff := model.BackoffPolicy{
			InitialDelay: model.Duration(*row.BackoffInitialDelay),
			MaxDelay:     model.Duration(*row.BackoffMaxDelay),
			Multiplier:   *row.BackoffMultiplier,
			Jitter:       *row.BackoffJitter,
		}
		visiblePolicy := errorrule.VisibleResponseDisconnect
		if row.VisiblePolicy != nil {
			visiblePolicy = errorrule.VisibleResponsePolicy(*row.VisiblePolicy)
		}
		if errorrule.ActionType(row.ActionType) == errorrule.ActionRetryOnly {
			return errorrule.NewRetryOnlyActionWithVisibleResponse(*row.MaxRetries, backoff, visiblePolicy)
		}
		return errorrule.NewRetryThenSwitchActionWithVisibleResponse(*row.MaxRetries, backoff, visiblePolicy)
	default:
		return errorrule.Action{}, fmt.Errorf("rule %q has unknown action type %q", row.ID, row.ActionType)
	}
}

func intPointer(value int) *int           { return &value }
func floatPointer(value float64) *float64 { return &value }
func boolPointer(value bool) *bool        { return &value }

func utcNow(clock Clock) time.Time {
	return clock.Now().UTC().Round(0)
}
