package errorrule

import "fmt"

type DecisionValue string

const (
	DecisionPassthrough    DecisionValue = "passthrough"
	DecisionObserveOnly    DecisionValue = "observe_only"
	DecisionCommitCurrent  DecisionValue = "commit_current"
	DecisionRetrySame      DecisionValue = "retry_same"
	DecisionSwitchProvider DecisionValue = "switch_provider"
	DecisionAbortClient    DecisionValue = "abort_client"
)

type DecisionReason string

const (
	ReasonActionPassthrough            DecisionReason = "action_passthrough"
	ReasonObserverOnly                 DecisionReason = "observer_only"
	ReasonRetryBudgetAvailable         DecisionReason = "retry_budget_available"
	ReasonRuleRetryBudgetExhausted     DecisionReason = "rule_retry_budget_exhausted"
	ReasonGlobalAttemptBudgetExhausted DecisionReason = "global_attempt_budget_exhausted"
	ReasonReservedSwitchAttempt        DecisionReason = "reserved_switch_attempt"
	ReasonAlternateProviderUnavailable DecisionReason = "alternate_provider_unavailable"
	ReasonAlternateReservationFailed   DecisionReason = "alternate_reservation_failed"
	ReasonProviderDeleted              DecisionReason = "provider_deleted"
	ReasonProviderDisabled             DecisionReason = "provider_disabled"
	ReasonAPIRemoved                   DecisionReason = "api_removed"
	ReasonRoutingChanged               DecisionReason = "routing_changed"
	ReasonGroupDisabled                DecisionReason = "group_disabled"
	ReasonAuthUnavailable              DecisionReason = "auth_unavailable"
	ReasonProviderLookupError          DecisionReason = "provider_lookup_error"
	ReasonResponseAlreadyVisible       DecisionReason = "response_already_visible"
	ReasonClientRetryRequested         DecisionReason = "client_retry_requested"
	ReasonClientCancelled              DecisionReason = "client_cancelled"
)

type SwitchReason string

const (
	SwitchReasonRuleExhausted       SwitchReason = "internal_error_rule_exhausted"
	SwitchReasonProviderUnavailable SwitchReason = "internal_error_provider_unavailable"
)

type ProviderEligibility struct {
	Authorized      bool
	RejectionReason DecisionReason
}

func EligibleProvider() ProviderEligibility {
	return ProviderEligibility{Authorized: true}
}

func IneligibleProvider(reason DecisionReason) ProviderEligibility {
	return ProviderEligibility{RejectionReason: reason}
}

func (e ProviderEligibility) Validate() error {
	if e.Authorized {
		if e.RejectionReason != "" {
			return fmt.Errorf("authorized provider cannot have rejection reason")
		}
		return nil
	}
	if !isProviderRejection(e.RejectionReason) {
		return fmt.Errorf("invalid provider rejection reason %q", e.RejectionReason)
	}
	return nil
}

func isProviderRejection(reason DecisionReason) bool {
	switch reason {
	case ReasonProviderDeleted,
		ReasonProviderDisabled,
		ReasonAPIRemoved,
		ReasonRoutingChanged,
		ReasonGroupDisabled,
		ReasonAuthUnavailable,
		ReasonProviderLookupError:
		return true
	default:
		return false
	}
}

type DecisionInput struct {
	Action            Action
	ProviderID        ProviderID
	RuleID            RuleID
	Ledger            RetryLedger
	GlobalMaxAttempts uint
	Provider          ProviderEligibility
	ResponseVisible   bool
}

type Decision struct {
	Value        DecisionValue
	Reason       DecisionReason
	switchReason SwitchReason
}

func (d Decision) SwitchReason() (SwitchReason, bool) {
	return d.switchReason, d.Value == DecisionSwitchProvider
}

func DecideRetry(input DecisionInput) (Decision, error) {
	if err := input.Action.Validate(); err != nil {
		return Decision{}, err
	}
	if err := input.Provider.Validate(); err != nil {
		return Decision{}, err
	}
	key := ProviderRuleKey{ProviderID: input.ProviderID, RuleID: input.RuleID}
	if err := key.Validate(); err != nil {
		return Decision{}, err
	}

	if input.ResponseVisible {
		return Decision{Value: DecisionObserveOnly, Reason: ReasonResponseAlreadyVisible}, nil
	}
	if input.Action.Type() == ActionPassthrough {
		return Decision{Value: DecisionPassthrough, Reason: ReasonActionPassthrough}, nil
	}

	retryPolicy, _ := input.Action.RetryPolicy()
	remaining, unlimited := input.Ledger.GlobalRemaining(input.GlobalMaxAttempts)
	if !unlimited && remaining == 0 {
		return Decision{Value: DecisionCommitCurrent, Reason: ReasonGlobalAttemptBudgetExhausted}, nil
	}
	if !input.Provider.Authorized {
		return providerUnavailableDecision(input.Action.Type(), input.Provider.RejectionReason), nil
	}

	ruleRetriesRemain := input.Ledger.RuleRetriesRemaining(key, retryPolicy.MaxRetries) > 0
	switch input.Action.Type() {
	case ActionRetryOnly:
		if !ruleRetriesRemain {
			return Decision{Value: DecisionCommitCurrent, Reason: ReasonRuleRetryBudgetExhausted}, nil
		}
		return Decision{Value: DecisionRetrySame, Reason: ReasonRetryBudgetAvailable}, nil
	case ActionRetryThenSwitch:
		if !unlimited && remaining == 1 {
			return switchDecision(ReasonReservedSwitchAttempt, SwitchReasonRuleExhausted), nil
		}
		if !ruleRetriesRemain {
			return switchDecision(ReasonRuleRetryBudgetExhausted, SwitchReasonRuleExhausted), nil
		}
		return Decision{Value: DecisionRetrySame, Reason: ReasonRetryBudgetAvailable}, nil
	default:
		return Decision{}, fmt.Errorf("unsupported action type %q", input.Action.Type())
	}
}

// DecideVisibleResponse is kept separate from retry-budget decisions because a
// response that crossed the client boundary can no longer consume a gateway
// retry slot. It only selects whether to preserve the current stream or close
// it so the client owns recovery.
func DecideVisibleResponse(action Action) (Decision, error) {
	if err := action.Validate(); err != nil {
		return Decision{}, err
	}
	if action.Type() == ActionPassthrough || action.VisibleResponsePolicy() == VisibleResponseCommit {
		return Decision{Value: DecisionObserveOnly, Reason: ReasonResponseAlreadyVisible}, nil
	}
	return Decision{Value: DecisionAbortClient, Reason: ReasonClientRetryRequested}, nil
}

func providerUnavailableDecision(actionType ActionType, reason DecisionReason) Decision {
	if actionType == ActionRetryThenSwitch {
		return switchDecision(reason, SwitchReasonProviderUnavailable)
	}
	return Decision{Value: DecisionCommitCurrent, Reason: reason}
}

func switchDecision(reason DecisionReason, switchReason SwitchReason) Decision {
	return Decision{Value: DecisionSwitchProvider, Reason: reason, switchReason: switchReason}
}

type AlternateOutcome string

const (
	AlternateReserved    AlternateOutcome = "reserved"
	AlternateUnavailable AlternateOutcome = "unavailable"
	AlternateFailed      AlternateOutcome = "failed"
	AlternateCancelled   AlternateOutcome = "cancelled"
)

func ResolveAlternate(decision Decision, outcome AlternateOutcome) (Decision, error) {
	if decision.Value != DecisionSwitchProvider {
		return Decision{}, fmt.Errorf("alternate outcome requires a switch decision")
	}
	switch outcome {
	case AlternateReserved:
		return decision, nil
	case AlternateUnavailable:
		return Decision{Value: DecisionCommitCurrent, Reason: ReasonAlternateProviderUnavailable}, nil
	case AlternateFailed:
		return Decision{Value: DecisionCommitCurrent, Reason: ReasonAlternateReservationFailed}, nil
	case AlternateCancelled:
		return Decision{Value: DecisionObserveOnly, Reason: ReasonClientCancelled}, nil
	default:
		return Decision{}, fmt.Errorf("unknown alternate outcome %q", outcome)
	}
}
