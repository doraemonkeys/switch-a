package proxy

import (
	"net/http"
	"testing"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/model"
)

func TestAssessNonWebSocketRequest_UsesRuntimeFacts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		facts          nonWebSocketRuntimeFacts
		wantOutcome    model.ServiceOutcome
		wantCompletion model.CompletionState
		wantReason     model.TerminationReason
	}{
		{
			name: "gateway rewrite before service start stays never started",
			facts: nonWebSocketRuntimeFacts{
				ClientTransportStatusCode: http.StatusServiceUnavailable,
				ResponseCommitted:         false,
				ServiceStarted:            false,
			},
			wantOutcome:    model.ServiceOutcomeNeverStarted,
			wantCompletion: model.CompletionStateIncomplete,
			wantReason:     model.TerminationReasonProviderUnavailable,
		},
		{
			name: "continuity routing conflict is gateway configuration failure",
			facts: nonWebSocketRuntimeFacts{
				ClientTransportStatusCode: http.StatusConflict,
				TerminalErr: &internal.ProviderSelectionError{
					Reason: internal.ProviderSelectionFailureContinuityRoutingConflict,
				},
			},
			wantOutcome:    model.ServiceOutcomeNeverStarted,
			wantCompletion: model.CompletionStateIncomplete,
			wantReason:     model.TerminationReasonProviderConfigurationError,
		},
		{
			name: "mid flight transport loss stays unknown",
			facts: nonWebSocketRuntimeFacts{
				ClientTransportStatusCode: http.StatusOK,
				ResponseCommitted:         true,
				ServiceStarted:            true,
				TerminalErr:               ErrReadTimeout,
			},
			wantOutcome:    model.ServiceOutcomeUnknown,
			wantCompletion: model.CompletionStateUnknown,
			wantReason:     model.TerminationReasonTransportError,
		},
		{
			name: "client canceled stream becomes abandoned by client",
			facts: nonWebSocketRuntimeFacts{
				ClientTransportStatusCode: http.StatusOK,
				ResponseCommitted:         true,
				ServiceStarted:            true,
				ClientTermination:         clientTerminationDisconnect,
				Success:                   true,
			},
			wantOutcome:    model.ServiceOutcomeAbandonedByClient,
			wantCompletion: model.CompletionStateIncomplete,
			wantReason:     model.TerminationReasonClientDisconnect,
		},
		{
			name: "client deadline stays distinct from disconnect",
			facts: nonWebSocketRuntimeFacts{
				ClientTransportStatusCode: http.StatusOK,
				ResponseCommitted:         true,
				ServiceStarted:            true,
				ClientTermination:         clientTerminationTimeout,
			},
			wantOutcome:    model.ServiceOutcomeAbandonedByClient,
			wantCompletion: model.CompletionStateIncomplete,
			wantReason:     model.TerminationReasonTimeout,
		},
		{
			name: "direct client error stays never started",
			facts: nonWebSocketRuntimeFacts{
				ClientTransportStatusCode: http.StatusBadRequest,
				ResponseCommitted:         true,
				ServiceStarted:            false,
			},
			wantOutcome:    model.ServiceOutcomeNeverStarted,
			wantCompletion: model.CompletionStateIncomplete,
			wantReason:     model.TerminationReasonClientRequestError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assessment := assessNonWebSocketRequest(tt.facts)
			if assessment.ServiceOutcome != tt.wantOutcome {
				t.Fatalf("ServiceOutcome = %q, want %q", assessment.ServiceOutcome, tt.wantOutcome)
			}
			if assessment.CompletionState != tt.wantCompletion {
				t.Fatalf("CompletionState = %q, want %q", assessment.CompletionState, tt.wantCompletion)
			}
			if assessment.TerminationReason == nil || *assessment.TerminationReason != tt.wantReason {
				t.Fatalf("TerminationReason = %v, want %q", assessment.TerminationReason, tt.wantReason)
			}
		})
	}
}

func TestAssessNonWebSocketRequest_CommittedSuccessOmitsTerminationAttribution(t *testing.T) {
	t.Parallel()

	assessment := assessNonWebSocketRequest(nonWebSocketRuntimeFacts{
		ClientTransportStatusCode: http.StatusOK,
		ResponseCommitted:         true,
		ServiceStarted:            true,
		Success:                   true,
	})

	if assessment.ServiceOutcome != model.ServiceOutcomeCompleted {
		t.Fatalf("ServiceOutcome = %q, want %q", assessment.ServiceOutcome, model.ServiceOutcomeCompleted)
	}
	if assessment.CompletionState != model.CompletionStateCompleted {
		t.Fatalf("CompletionState = %q, want %q", assessment.CompletionState, model.CompletionStateCompleted)
	}
	if assessment.TerminationActor != nil {
		t.Fatalf("TerminationActor = %v, want nil", assessment.TerminationActor)
	}
	if assessment.TerminationReason != nil {
		t.Fatalf("TerminationReason = %v, want nil", assessment.TerminationReason)
	}
}

func TestAssessNonWebSocketRequest_NoResponseUsesUnknownTermination(t *testing.T) {
	t.Parallel()

	assessment := assessNonWebSocketRequest(nonWebSocketRuntimeFacts{
		ClientTransportStatusCode: StatusCodeNoResponse,
		ResponseCommitted:         false,
		ServiceStarted:            false,
	})

	if assessment.ServiceOutcome != model.ServiceOutcomeNeverStarted {
		t.Fatalf("ServiceOutcome = %q, want %q", assessment.ServiceOutcome, model.ServiceOutcomeNeverStarted)
	}
	if assessment.CompletionState != model.CompletionStateIncomplete {
		t.Fatalf("CompletionState = %q, want %q", assessment.CompletionState, model.CompletionStateIncomplete)
	}
	if assessment.TerminationActor == nil || *assessment.TerminationActor != model.TerminationActorUnknown {
		t.Fatalf("TerminationActor = %v, want %q", assessment.TerminationActor, model.TerminationActorUnknown)
	}
	if assessment.TerminationReason == nil || *assessment.TerminationReason != model.TerminationReasonUnknown {
		t.Fatalf("TerminationReason = %v, want %q", assessment.TerminationReason, model.TerminationReasonUnknown)
	}
}

func TestAssessNonWebSocketRequest_CommittedSemanticHTTP200IsInterrupted(t *testing.T) {
	t.Parallel()
	assessment := assessNonWebSocketRequest(nonWebSocketRuntimeFacts{
		ClientTransportStatusCode: http.StatusOK,
		ResponseCommitted:         true, ServiceStarted: true, SemanticError: true,
	})
	if assessment.ServiceOutcome != model.ServiceOutcomeInterrupted ||
		assessment.CompletionState != model.CompletionStateIncomplete {
		t.Fatalf("assessment = %#v", assessment)
	}
	if assessment.TerminationActor == nil || *assessment.TerminationActor != model.TerminationActorUpstream ||
		assessment.TerminationReason == nil || *assessment.TerminationReason != model.TerminationReasonUpstreamSemanticError {
		t.Fatalf("semantic attribution = %#v", assessment)
	}
	if assessment.ClientAction != model.ClientActionNone {
		t.Fatalf("ClientAction = %q", assessment.ClientAction)
	}
}

func TestAssessNonWebSocketRequest_TransportFailurePrecedesLateSemanticMatch(t *testing.T) {
	t.Parallel()
	assessment := assessNonWebSocketRequest(nonWebSocketRuntimeFacts{
		ClientTransportStatusCode: http.StatusOK,
		ResponseCommitted:         true, ServiceStarted: true, SemanticError: true,
		TerminalErr: ErrReadTimeout,
	})
	if assessment.ServiceOutcome != model.ServiceOutcomeUnknown ||
		assessment.CompletionState != model.CompletionStateUnknown ||
		assessment.TerminationActor == nil || *assessment.TerminationActor != model.TerminationActorUpstream ||
		assessment.TerminationReason == nil || *assessment.TerminationReason != model.TerminationReasonTransportError {
		t.Fatalf("assessment = %#v", assessment)
	}
}
