package errorrule

import "fmt"

type AttemptClass string

const (
	AttemptTransportFailure  AttemptClass = "transport_failure"
	AttemptCredentialRefresh AttemptClass = "credential_refresh"
	AttemptHTTPStatusFailure AttemptClass = "http_status_failure"
	AttemptSemanticError     AttemptClass = "semantic_error"
	AttemptNormalCompletion  AttemptClass = "normal_completion"
	AttemptClientCancelled   AttemptClass = "client_cancelled"
	AttemptIncomplete        AttemptClass = "incomplete"
)

type AttemptFacts struct {
	TransportFailure         bool
	CredentialRefreshPending bool
	HTTPStatusFailure        bool
	SemanticMatched          bool
	Committable2xx           bool
	Completed                bool
	ClientCancelled          bool
}

// ClassifyAttempt freezes failure precedence before retry and health logic see
// the facts, so a semantic 2xx rule can never override transport or status.
func ClassifyAttempt(facts AttemptFacts) AttemptClass {
	switch {
	case facts.TransportFailure:
		return AttemptTransportFailure
	case facts.CredentialRefreshPending:
		return AttemptCredentialRefresh
	case facts.HTTPStatusFailure:
		return AttemptHTTPStatusFailure
	case facts.SemanticMatched && facts.Committable2xx:
		return AttemptSemanticError
	case facts.Completed:
		return AttemptNormalCompletion
	case facts.ClientCancelled:
		return AttemptClientCancelled
	default:
		return AttemptIncomplete
	}
}

type HealthVerdict string

const (
	HealthSuccess HealthVerdict = "success"
	HealthFailure HealthVerdict = "failure"
	HealthNeutral HealthVerdict = "neutral"
)

type HealthCause string

const (
	HealthCauseNormalCompletion        HealthCause = "normal_completion"
	HealthCauseTransportFailure        HealthCause = "transport_failure"
	HealthCauseHTTPStatusFailure       HealthCause = "http_status_failure"
	HealthCauseSemanticRetryThenSwitch HealthCause = "semantic_retry_then_switch"
	HealthCauseSemanticNeutral         HealthCause = "semantic_neutral"
	HealthCauseClientCancelled         HealthCause = "client_cancelled"
	HealthCauseIncomplete              HealthCause = "incomplete"
)

type HealthAssessment struct {
	Verdict HealthVerdict
	Cause   HealthCause
	Timing  HealthApplicationTiming
}

type HealthApplicationTiming string

const (
	HealthApplyImmediately HealthApplicationTiming = "immediate"
	HealthApplyDeferred    HealthApplicationTiming = "deferred"
)

// AssessHealth returns available=false for a credential refresh because it is
// a subexchange of the same logical attempt, not a second health observation.
// Neutral semantic verdicts are deferred so a later body transport failure can
// retain precedence without allowing the generic 2xx success path to run.
func AssessHealth(class AttemptClass, semanticAction Action) (assessment HealthAssessment, available bool, err error) {
	switch class {
	case AttemptTransportFailure:
		return HealthAssessment{Verdict: HealthFailure, Cause: HealthCauseTransportFailure, Timing: HealthApplyImmediately}, true, nil
	case AttemptCredentialRefresh:
		return HealthAssessment{}, false, nil
	case AttemptHTTPStatusFailure:
		return HealthAssessment{Verdict: HealthFailure, Cause: HealthCauseHTTPStatusFailure, Timing: HealthApplyImmediately}, true, nil
	case AttemptSemanticError:
		if err := semanticAction.Validate(); err != nil {
			return HealthAssessment{}, false, fmt.Errorf("semantic action: %w", err)
		}
		if semanticAction.Type() == ActionRetryThenSwitch {
			return HealthAssessment{Verdict: HealthFailure, Cause: HealthCauseSemanticRetryThenSwitch, Timing: HealthApplyImmediately}, true, nil
		}
		return HealthAssessment{Verdict: HealthNeutral, Cause: HealthCauseSemanticNeutral, Timing: HealthApplyDeferred}, true, nil
	case AttemptNormalCompletion:
		return HealthAssessment{Verdict: HealthSuccess, Cause: HealthCauseNormalCompletion, Timing: HealthApplyImmediately}, true, nil
	case AttemptClientCancelled:
		return HealthAssessment{Verdict: HealthNeutral, Cause: HealthCauseClientCancelled, Timing: HealthApplyImmediately}, true, nil
	case AttemptIncomplete:
		return HealthAssessment{Verdict: HealthNeutral, Cause: HealthCauseIncomplete, Timing: HealthApplyImmediately}, true, nil
	default:
		return HealthAssessment{}, false, fmt.Errorf("unknown attempt class %q", class)
	}
}
