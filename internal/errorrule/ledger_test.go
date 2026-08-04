package errorrule

import (
	"errors"
	"math"
	"testing"
)

func TestRetryLedgerIndependentPureTransitions(t *testing.T) {
	providerA := ProviderID("provider-a")
	providerB := ProviderID("provider-b")
	ruleKey := ProviderRuleKey{ProviderID: providerA, RuleID: "11111111-1111-4111-8111-111111111111"}

	var initial RetryLedger
	if remaining, unlimited := initial.GlobalRemaining(0); remaining != 0 || !unlimited {
		t.Fatalf("unlimited remaining = (%d, %v)", remaining, unlimited)
	}
	started, err := initial.StartAttempt(providerA, 4)
	if err != nil {
		t.Fatalf("StartAttempt() error = %v", err)
	}
	legacy, err := started.StartLegacyRetry(providerA, 4)
	if err != nil {
		t.Fatalf("StartLegacyRetry() error = %v", err)
	}
	ruleRetry, err := legacy.StartRuleRetry(ruleKey, 4)
	if err != nil {
		t.Fatalf("StartRuleRetry() error = %v", err)
	}
	switched, err := ruleRetry.StartAttempt(providerB, 4)
	if err != nil {
		t.Fatalf("switch StartAttempt() error = %v", err)
	}

	if initial.LogicalAttemptsStarted() != 0 || started.LogicalAttemptsStarted() != 1 || switched.LogicalAttemptsStarted() != 4 {
		t.Fatalf("logical counters = initial %d, started %d, final %d", initial.LogicalAttemptsStarted(), started.LogicalAttemptsStarted(), switched.LogicalAttemptsStarted())
	}
	if switched.ProviderAttemptsStarted(providerA) != 3 || switched.ProviderAttemptsStarted(providerB) != 1 {
		t.Fatalf("provider counters = A:%d B:%d", switched.ProviderAttemptsStarted(providerA), switched.ProviderAttemptsStarted(providerB))
	}
	if switched.LegacyRetriesScheduled(providerA) != 1 || switched.RuleRetriesScheduled(ruleKey) != 1 {
		t.Fatalf("independent counters = legacy:%d rule:%d", switched.LegacyRetriesScheduled(providerA), switched.RuleRetriesScheduled(ruleKey))
	}
	if legacy.RuleRetriesScheduled(ruleKey) != 0 || started.LegacyRetriesScheduled(providerA) != 0 {
		t.Fatal("a later transition mutated an earlier ledger")
	}
	if remaining, unlimited := switched.GlobalRemaining(4); remaining != 0 || unlimited {
		t.Fatalf("finite remaining = (%d, %v)", remaining, unlimited)
	}
	if _, err := switched.StartAttempt(providerA, 4); !errors.Is(err, ErrGlobalAttemptLimit) {
		t.Fatalf("over-cap error = %v", err)
	}
	if switched.LogicalAttemptsStarted() != 4 {
		t.Fatal("rejected transition charged the source ledger")
	}
}

func TestRetryLedgerRuleBudgetAndValidation(t *testing.T) {
	key := ProviderRuleKey{ProviderID: "provider-a", RuleID: "11111111-1111-4111-8111-111111111111"}
	var ledger RetryLedger
	if ledger.RuleRetriesRemaining(key, -1) != 0 || ledger.RuleRetriesRemaining(key, 0) != 0 || ledger.RuleRetriesRemaining(key, 2) != 2 {
		t.Fatal("zero ledger rule remaining calculation is wrong")
	}
	next, err := ledger.StartRuleRetry(key, 0)
	if err != nil {
		t.Fatalf("StartRuleRetry(unlimited) error = %v", err)
	}
	if next.RuleRetriesRemaining(key, 2) != 1 || next.RuleRetriesRemaining(key, 1) != 0 {
		t.Fatal("rule retry remaining did not reflect scheduled retry")
	}

	invalidKeys := []ProviderRuleKey{
		{RuleID: key.RuleID},
		{ProviderID: key.ProviderID, RuleID: "bad"},
	}
	for _, invalid := range invalidKeys {
		if err := invalid.Validate(); err == nil {
			t.Errorf("invalid key %#v unexpectedly accepted", invalid)
		}
		if _, err := ledger.StartRuleRetry(invalid, 0); err == nil {
			t.Errorf("StartRuleRetry(%#v) unexpectedly succeeded", invalid)
		}
	}
	if _, err := ledger.StartAttempt("", 0); err == nil {
		t.Fatal("empty provider unexpectedly accepted")
	}
}

func TestRetryLedgerCounterOverflow(t *testing.T) {
	provider := ProviderID("provider-a")
	key := ProviderRuleKey{ProviderID: provider, RuleID: "11111111-1111-4111-8111-111111111111"}
	cases := []RetryLedger{
		{logicalAttemptsStarted: math.MaxUint},
		{providerAttemptsStarted: map[ProviderID]uint{provider: math.MaxUint}},
		{legacyRetriesScheduled: map[ProviderID]uint{provider: math.MaxUint}},
		{ruleRetriesScheduled: map[ProviderRuleKey]uint{key: math.MaxUint}},
	}
	if _, err := cases[0].StartAttempt(provider, 0); !errors.Is(err, ErrCounterOverflow) {
		t.Fatalf("logical overflow error = %v", err)
	}
	if _, err := cases[1].StartAttempt(provider, 0); !errors.Is(err, ErrCounterOverflow) {
		t.Fatalf("provider overflow error = %v", err)
	}
	if _, err := cases[2].StartLegacyRetry(provider, 0); !errors.Is(err, ErrCounterOverflow) {
		t.Fatalf("legacy overflow error = %v", err)
	}
	if _, err := cases[3].StartRuleRetry(key, 0); !errors.Is(err, ErrCounterOverflow) {
		t.Fatalf("rule overflow error = %v", err)
	}
}
