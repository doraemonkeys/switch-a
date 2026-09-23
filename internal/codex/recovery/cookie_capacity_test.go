package codexrecovery_test

import (
	"fmt"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/codex/cookie"
	"github.com/doraemonkeys/switch-a/internal/codex/recovery"
)

func TestCookieCapacityIsDistinctFromStorageFailureOnEveryCarrier(t *testing.T) {
	for _, limit := range []providercookie.LimitName{providercookie.LimitHandleBindingsGlobal, providercookie.LimitGlobalEntries} {
		cause := &providercookie.LimitError{Limit: limit, Max: 1, Actual: 2}
		want := contractByCondition(t, codexrecovery.ConditionCookieCapacityExhausted)
		for _, phase := range carrierPhases {
			assertDecision(t, codexrecovery.Classify(fmt.Errorf("commit: %w", cause), phase), phase, want)
			assertDecision(t, codexrecovery.ClassifyWithFallback(cause, phase, codexrecovery.ConditionStateStoreUnavailable), phase, want)
		}
	}
	if codexrecovery.ClientMessage(codexrecovery.ConditionCookieCapacityExhausted) == "" {
		t.Fatal("capacity failure has no client guidance")
	}
}
