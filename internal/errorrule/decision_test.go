package errorrule

import (
	"testing"
)

func decisionInput(t *testing.T, action Action, globalMax uint, provider ProviderEligibility) DecisionInput {
	t.Helper()
	ledger, err := (RetryLedger{}).StartAttempt("provider-a", globalMax)
	if err != nil {
		t.Fatalf("start current attempt: %v", err)
	}
	return DecisionInput{
		Action:            action,
		ProviderID:        "provider-a",
		RuleID:            "11111111-1111-4111-8111-111111111111",
		Ledger:            ledger,
		GlobalMaxAttempts: globalMax,
		Provider:          provider,
	}
}

func chargeRuleRetries(t *testing.T, input DecisionInput, count int) DecisionInput {
	t.Helper()
	key := ProviderRuleKey{ProviderID: input.ProviderID, RuleID: input.RuleID}
	for range count {
		next, err := input.Ledger.StartRuleRetry(key, 0)
		if err != nil {
			t.Fatalf("charge rule retry: %v", err)
		}
		input.Ledger = next
	}
	return input
}

func TestDecisionCartesianTable(t *testing.T) {
	retryOnly := testRetryAction(t, ActionRetryOnly, 2)
	retryOnlyZero := testRetryAction(t, ActionRetryOnly, 0)
	retrySwitch := testRetryAction(t, ActionRetryThenSwitch, 2)
	retrySwitchZero := testRetryAction(t, ActionRetryThenSwitch, 0)

	type expected struct {
		value        DecisionValue
		reason       DecisionReason
		switchReason SwitchReason
	}
	cases := []struct {
		name   string
		input  DecisionInput
		mutate func(DecisionInput) DecisionInput
		want   expected
	}{
		{name: "visible response is observation only", input: decisionInput(t, retrySwitch, 3, EligibleProvider()), mutate: func(i DecisionInput) DecisionInput { i.ResponseVisible = true; return i }, want: expected{DecisionObserveOnly, ReasonResponseAlreadyVisible, ""}},
		{name: "passthrough commits immediately", input: decisionInput(t, NewPassthroughAction(), 1, EligibleProvider()), want: expected{DecisionPassthrough, ReasonActionPassthrough, ""}},
		{name: "retry only unlimited", input: decisionInput(t, retryOnly, 0, EligibleProvider()), want: expected{DecisionRetrySame, ReasonRetryBudgetAvailable, ""}},
		{name: "retry only finite slot", input: decisionInput(t, retryOnly, 2, EligibleProvider()), want: expected{DecisionRetrySame, ReasonRetryBudgetAvailable, ""}},
		{name: "retry only global exhausted", input: decisionInput(t, retryOnly, 1, EligibleProvider()), want: expected{DecisionCommitCurrent, ReasonGlobalAttemptBudgetExhausted, ""}},
		{name: "retry only zero rule budget", input: decisionInput(t, retryOnlyZero, 0, EligibleProvider()), want: expected{DecisionCommitCurrent, ReasonRuleRetryBudgetExhausted, ""}},
		{name: "retry only exhausted after scheduling", input: decisionInput(t, retryOnly, 0, EligibleProvider()), mutate: func(i DecisionInput) DecisionInput { return chargeRuleRetries(t, i, 2) }, want: expected{DecisionCommitCurrent, ReasonRuleRetryBudgetExhausted, ""}},
		{name: "retry only deleted provider", input: decisionInput(t, retryOnly, 0, IneligibleProvider(ReasonProviderDeleted)), want: expected{DecisionCommitCurrent, ReasonProviderDeleted, ""}},
		{name: "switch action unlimited retries same", input: decisionInput(t, retrySwitch, 0, EligibleProvider()), want: expected{DecisionRetrySame, ReasonRetryBudgetAvailable, ""}},
		{name: "switch action with two finite slots retries same", input: decisionInput(t, retrySwitch, 3, EligibleProvider()), want: expected{DecisionRetrySame, ReasonRetryBudgetAvailable, ""}},
		{name: "switch action reserves final slot", input: decisionInput(t, retrySwitch, 2, EligibleProvider()), want: expected{DecisionSwitchProvider, ReasonReservedSwitchAttempt, SwitchReasonRuleExhausted}},
		{name: "switch action global exhausted", input: decisionInput(t, retrySwitch, 1, EligibleProvider()), want: expected{DecisionCommitCurrent, ReasonGlobalAttemptBudgetExhausted, ""}},
		{name: "switch action zero retries", input: decisionInput(t, retrySwitchZero, 0, EligibleProvider()), want: expected{DecisionSwitchProvider, ReasonRuleRetryBudgetExhausted, SwitchReasonRuleExhausted}},
		{name: "switch action exhausted retries", input: decisionInput(t, retrySwitch, 0, EligibleProvider()), mutate: func(i DecisionInput) DecisionInput { return chargeRuleRetries(t, i, 2) }, want: expected{DecisionSwitchProvider, ReasonRuleRetryBudgetExhausted, SwitchReasonRuleExhausted}},
		{name: "switch action disabled provider", input: decisionInput(t, retrySwitch, 0, IneligibleProvider(ReasonProviderDisabled)), want: expected{DecisionSwitchProvider, ReasonProviderDisabled, SwitchReasonProviderUnavailable}},
		{name: "provider invalidation explains final-slot switch", input: decisionInput(t, retrySwitch, 2, IneligibleProvider(ReasonAuthUnavailable)), want: expected{DecisionSwitchProvider, ReasonAuthUnavailable, SwitchReasonProviderUnavailable}},
	}

	for _, current := range cases {
		t.Run(current.name, func(t *testing.T) {
			input := current.input
			if current.mutate != nil {
				input = current.mutate(input)
			}
			got, err := DecideRetry(input)
			if err != nil {
				t.Fatalf("DecideRetry() error = %v", err)
			}
			if got.Value != current.want.value || got.Reason != current.want.reason {
				t.Fatalf("DecideRetry() = %#v, want value=%q reason=%q", got, current.want.value, current.want.reason)
			}
			switchReason, switched := got.SwitchReason()
			wantSwitched := current.want.switchReason != ""
			if switched != wantSwitched || switchReason != current.want.switchReason {
				t.Fatalf("SwitchReason() = (%q, %v), want (%q, %v)", switchReason, switched, current.want.switchReason, wantSwitched)
			}
		})
	}
}

func TestDecisionFullCartesian(t *testing.T) {
	actions := []Action{
		NewPassthroughAction(),
		testRetryAction(t, ActionRetryOnly, 1),
		testRetryAction(t, ActionRetryThenSwitch, 1),
	}
	globalCases := []struct {
		name      string
		maximum   uint
		started   uint
		remaining uint
		unlimited bool
	}{
		{name: "unlimited", maximum: 0, started: 1, unlimited: true},
		{name: "exhausted", maximum: 1, started: 1},
		{name: "one remaining", maximum: 2, started: 1, remaining: 1},
		{name: "two remaining", maximum: 3, started: 1, remaining: 2},
	}
	eligibilities := []ProviderEligibility{EligibleProvider(), IneligibleProvider(ReasonProviderDisabled)}
	key := ProviderRuleKey{ProviderID: "provider-a", RuleID: "11111111-1111-4111-8111-111111111111"}
	tested := 0
	for _, action := range actions {
		for _, visible := range []bool{false, true} {
			for _, global := range globalCases {
				for _, provider := range eligibilities {
					for _, ruleExhausted := range []bool{false, true} {
						name := string(action.Type()) + "/" + global.name
						if visible {
							name += "/visible"
						}
						if !provider.Authorized {
							name += "/ineligible"
						}
						if ruleExhausted {
							name += "/rule-exhausted"
						}
						t.Run(name, func(t *testing.T) {
							ledger := RetryLedger{logicalAttemptsStarted: global.started}
							if ruleExhausted {
								ledger.ruleRetriesScheduled = map[ProviderRuleKey]uint{key: 1}
							}
							input := DecisionInput{
								Action:            action,
								ProviderID:        key.ProviderID,
								RuleID:            key.RuleID,
								Ledger:            ledger,
								GlobalMaxAttempts: global.maximum,
								Provider:          provider,
								ResponseVisible:   visible,
							}
							got, err := DecideRetry(input)
							if err != nil {
								t.Fatalf("DecideRetry() error = %v", err)
							}
							want := cartesianDecision(action.Type(), visible, global.remaining, global.unlimited, provider.Authorized, ruleExhausted)
							if got.Value != want.Value || got.Reason != want.Reason || got.switchReason != want.switchReason {
								t.Fatalf("DecideRetry() = %#v, want %#v", got, want)
							}
						})
						tested++
					}
				}
			}
		}
	}
	if tested != 96 {
		t.Fatalf("tested %d combinations, want 96", tested)
	}
}

func cartesianDecision(
	actionType ActionType,
	visible bool,
	globalRemaining uint,
	globalUnlimited bool,
	providerAuthorized bool,
	ruleExhausted bool,
) Decision {
	if visible {
		return Decision{Value: DecisionObserveOnly, Reason: ReasonResponseAlreadyVisible}
	}
	if actionType == ActionPassthrough {
		return Decision{Value: DecisionPassthrough, Reason: ReasonActionPassthrough}
	}
	if !globalUnlimited && globalRemaining == 0 {
		return Decision{Value: DecisionCommitCurrent, Reason: ReasonGlobalAttemptBudgetExhausted}
	}
	if !providerAuthorized {
		return providerUnavailableDecision(actionType, ReasonProviderDisabled)
	}
	if actionType == ActionRetryOnly {
		if ruleExhausted {
			return Decision{Value: DecisionCommitCurrent, Reason: ReasonRuleRetryBudgetExhausted}
		}
		return Decision{Value: DecisionRetrySame, Reason: ReasonRetryBudgetAvailable}
	}
	if !globalUnlimited && globalRemaining == 1 {
		return switchDecision(ReasonReservedSwitchAttempt, SwitchReasonRuleExhausted)
	}
	if ruleExhausted {
		return switchDecision(ReasonRuleRetryBudgetExhausted, SwitchReasonRuleExhausted)
	}
	return Decision{Value: DecisionRetrySame, Reason: ReasonRetryBudgetAvailable}
}

func TestDecisionProviderRejectionReasons(t *testing.T) {
	reasons := []DecisionReason{
		ReasonProviderDeleted,
		ReasonProviderDisabled,
		ReasonAPIRemoved,
		ReasonRoutingChanged,
		ReasonGroupDisabled,
		ReasonAuthUnavailable,
		ReasonProviderLookupError,
	}
	for _, reason := range reasons {
		t.Run(string(reason), func(t *testing.T) {
			input := decisionInput(t, testRetryAction(t, ActionRetryThenSwitch, 2), 0, IneligibleProvider(reason))
			decision, err := DecideRetry(input)
			if err != nil {
				t.Fatalf("DecideRetry() error = %v", err)
			}
			if decision.Value != DecisionSwitchProvider || decision.Reason != reason {
				t.Fatalf("decision = %#v", decision)
			}
		})
	}
}

func TestDecisionAlternateResolution(t *testing.T) {
	decision, err := DecideRetry(decisionInput(t, testRetryAction(t, ActionRetryThenSwitch, 0), 0, EligibleProvider()))
	if err != nil {
		t.Fatalf("DecideRetry() error = %v", err)
	}
	cases := []struct {
		outcome AlternateOutcome
		value   DecisionValue
		reason  DecisionReason
	}{
		{AlternateReserved, DecisionSwitchProvider, ReasonRuleRetryBudgetExhausted},
		{AlternateUnavailable, DecisionCommitCurrent, ReasonAlternateProviderUnavailable},
		{AlternateFailed, DecisionCommitCurrent, ReasonAlternateReservationFailed},
		{AlternateCancelled, DecisionObserveOnly, ReasonClientCancelled},
	}
	for _, current := range cases {
		resolved, resolveErr := ResolveAlternate(decision, current.outcome)
		if resolveErr != nil {
			t.Fatalf("ResolveAlternate(%q) error = %v", current.outcome, resolveErr)
		}
		if resolved.Value != current.value || resolved.Reason != current.reason {
			t.Errorf("ResolveAlternate(%q) = %#v", current.outcome, resolved)
		}
		if current.outcome != AlternateReserved {
			if _, switched := resolved.SwitchReason(); switched {
				t.Errorf("fallback outcome %q retained switch reason", current.outcome)
			}
		}
	}
	if _, err := ResolveAlternate(Decision{Value: DecisionRetrySame}, AlternateReserved); err == nil {
		t.Fatal("non-switch decision unexpectedly resolved alternate")
	}
	if _, err := ResolveAlternate(decision, "unknown"); err == nil {
		t.Fatal("unknown alternate outcome unexpectedly accepted")
	}
}

func TestDecisionInputValidation(t *testing.T) {
	valid := decisionInput(t, testRetryAction(t, ActionRetryOnly, 1), 0, EligibleProvider())
	cases := []struct {
		name   string
		mutate func(*DecisionInput)
	}{
		{name: "invalid action", mutate: func(i *DecisionInput) { i.Action = Action{} }},
		{name: "empty provider", mutate: func(i *DecisionInput) { i.ProviderID = "" }},
		{name: "invalid rule", mutate: func(i *DecisionInput) { i.RuleID = "bad" }},
		{name: "authorized with reason", mutate: func(i *DecisionInput) {
			i.Provider = ProviderEligibility{Authorized: true, RejectionReason: ReasonProviderDeleted}
		}},
		{name: "invalid rejection", mutate: func(i *DecisionInput) { i.Provider = IneligibleProvider(ReasonRuleRetryBudgetExhausted) }},
	}
	for _, current := range cases {
		t.Run(current.name, func(t *testing.T) {
			input := valid
			current.mutate(&input)
			if _, err := DecideRetry(input); err == nil {
				t.Fatal("DecideRetry() unexpectedly succeeded")
			}
		})
	}
}
