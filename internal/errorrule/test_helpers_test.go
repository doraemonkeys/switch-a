package errorrule

import (
	"fmt"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/apicontract"
	"github.com/doraemonkeys/switch-a/internal/model"
)

var testNow = time.Date(2026, time.August, 3, 1, 2, 3, 0, time.UTC)

func testAPIType(apiType apicontract.APIType) *apicontract.APIType {
	return &apiType
}

func testRetryAction(t *testing.T, actionType ActionType, maxRetries int) Action {
	t.Helper()
	backoff := model.BackoffPolicy{
		InitialDelay: model.Duration(250 * time.Millisecond),
		MaxDelay:     model.Duration(2 * time.Second),
		Multiplier:   2,
		Jitter:       true,
	}
	var (
		action Action
		err    error
	)
	if actionType == ActionRetryOnly {
		action, err = NewRetryOnlyAction(maxRetries, backoff)
	} else {
		action, err = NewRetryThenSwitchAction(maxRetries, backoff)
	}
	if err != nil {
		t.Fatalf("create test action: %v", err)
	}
	return action
}

func testRule(t *testing.T, sequence int, position int64) Rule {
	t.Helper()
	id := RuleID(fmt.Sprintf("00000000-0000-4000-8000-%012x", sequence+1))
	generation, err := ParseRuleGeneration(fmt.Sprintf("10000000-0000-4000-8000-%012x", sequence+1))
	if err != nil {
		t.Fatalf("parse test generation: %v", err)
	}
	return NewRule(RuleSpec{
		Name:      fmt.Sprintf("Rule %d", sequence),
		Enabled:   true,
		Target:    NewGlobalTarget(),
		APIType:   testAPIType(apicontract.APITypeCodex),
		Keywords:  []string{fmt.Sprintf("keyword-%d", sequence)},
		MatchMode: MatchAny,
		Action:    testRetryAction(t, ActionRetryThenSwitch, 2),
	}, RuleMetadata{
		ID:         id,
		Generation: generation,
		Position:   position,
		CreatedAt:  testNow,
		UpdatedAt:  testNow,
	})
}

type sequenceGenerator struct {
	values []string
	index  int
}

func (g *sequenceGenerator) NewID() string {
	if g.index >= len(g.values) {
		return ""
	}
	value := g.values[g.index]
	g.index++
	return value
}
