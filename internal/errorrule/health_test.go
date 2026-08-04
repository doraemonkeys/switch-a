package errorrule

import "testing"

func TestClassifyAttemptPrecedence(t *testing.T) {
	cases := []struct {
		name  string
		facts AttemptFacts
		want  AttemptClass
	}{
		{name: "transport dominates all", facts: AttemptFacts{TransportFailure: true, CredentialRefreshPending: true, HTTPStatusFailure: true, SemanticMatched: true, Committable2xx: true, Completed: true, ClientCancelled: true}, want: AttemptTransportFailure},
		{name: "credential dominates status", facts: AttemptFacts{CredentialRefreshPending: true, HTTPStatusFailure: true, SemanticMatched: true, Committable2xx: true}, want: AttemptCredentialRefresh},
		{name: "status dominates semantic", facts: AttemptFacts{HTTPStatusFailure: true, SemanticMatched: true, Committable2xx: true}, want: AttemptHTTPStatusFailure},
		{name: "semantic eligible", facts: AttemptFacts{SemanticMatched: true, Committable2xx: true, Completed: true}, want: AttemptSemanticError},
		{name: "semantic ineligible is normal completion", facts: AttemptFacts{SemanticMatched: true, Completed: true}, want: AttemptNormalCompletion},
		{name: "normal completion", facts: AttemptFacts{Completed: true}, want: AttemptNormalCompletion},
		{name: "completion precedes inconsistent cancellation", facts: AttemptFacts{Completed: true, ClientCancelled: true}, want: AttemptNormalCompletion},
		{name: "cancellation", facts: AttemptFacts{ClientCancelled: true}, want: AttemptClientCancelled},
		{name: "incomplete", facts: AttemptFacts{}, want: AttemptIncomplete},
	}
	for _, current := range cases {
		t.Run(current.name, func(t *testing.T) {
			if got := ClassifyAttempt(current.facts); got != current.want {
				t.Fatalf("ClassifyAttempt() = %q, want %q", got, current.want)
			}
		})
	}
}

func TestHealthVerdictTable(t *testing.T) {
	cases := []struct {
		name       string
		class      AttemptClass
		action     Action
		assessment HealthAssessment
		available  bool
	}{
		{name: "transport", class: AttemptTransportFailure, assessment: HealthAssessment{HealthFailure, HealthCauseTransportFailure, HealthApplyImmediately}, available: true},
		{name: "credential subexchange", class: AttemptCredentialRefresh},
		{name: "status", class: AttemptHTTPStatusFailure, assessment: HealthAssessment{HealthFailure, HealthCauseHTTPStatusFailure, HealthApplyImmediately}, available: true},
		{name: "semantic passthrough neutral", class: AttemptSemanticError, action: NewPassthroughAction(), assessment: HealthAssessment{HealthNeutral, HealthCauseSemanticNeutral, HealthApplyDeferred}, available: true},
		{name: "semantic retry only neutral", class: AttemptSemanticError, action: testRetryAction(t, ActionRetryOnly, 1), assessment: HealthAssessment{HealthNeutral, HealthCauseSemanticNeutral, HealthApplyDeferred}, available: true},
		{name: "semantic retry then switch failure", class: AttemptSemanticError, action: testRetryAction(t, ActionRetryThenSwitch, 1), assessment: HealthAssessment{HealthFailure, HealthCauseSemanticRetryThenSwitch, HealthApplyImmediately}, available: true},
		{name: "normal success", class: AttemptNormalCompletion, assessment: HealthAssessment{HealthSuccess, HealthCauseNormalCompletion, HealthApplyImmediately}, available: true},
		{name: "cancel neutral", class: AttemptClientCancelled, assessment: HealthAssessment{HealthNeutral, HealthCauseClientCancelled, HealthApplyImmediately}, available: true},
		{name: "incomplete neutral", class: AttemptIncomplete, assessment: HealthAssessment{HealthNeutral, HealthCauseIncomplete, HealthApplyImmediately}, available: true},
	}
	for _, current := range cases {
		t.Run(current.name, func(t *testing.T) {
			assessment, available, err := AssessHealth(current.class, current.action)
			if err != nil {
				t.Fatalf("AssessHealth() error = %v", err)
			}
			if assessment != current.assessment || available != current.available {
				t.Fatalf("AssessHealth() = (%#v, %v), want (%#v, %v)", assessment, available, current.assessment, current.available)
			}
		})
	}
}

func TestHealthSemanticInvalidActionAndUnknownClass(t *testing.T) {
	if _, _, err := AssessHealth(AttemptSemanticError, Action{}); err == nil {
		t.Fatal("semantic health accepted invalid action")
	}
	if _, _, err := AssessHealth("unknown", Action{}); err == nil {
		t.Fatal("unknown class unexpectedly accepted")
	}
}

func TestHealthVerdictInvariantAcrossWindowState(t *testing.T) {
	action := testRetryAction(t, ActionRetryThenSwitch, 2)
	inside, availableInside, err := AssessHealth(ClassifyAttempt(AttemptFacts{SemanticMatched: true, Committable2xx: true}), action)
	if err != nil {
		t.Fatalf("inside AssessHealth() error = %v", err)
	}
	outside, availableOutside, err := AssessHealth(ClassifyAttempt(AttemptFacts{SemanticMatched: true, Committable2xx: true, Completed: true}), action)
	if err != nil {
		t.Fatalf("outside AssessHealth() error = %v", err)
	}
	if inside != outside || !availableInside || !availableOutside {
		t.Fatalf("window changed verdict: inside=(%#v,%v), outside=(%#v,%v)", inside, availableInside, outside, availableOutside)
	}
}
