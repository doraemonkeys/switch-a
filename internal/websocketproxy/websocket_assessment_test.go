package websocketproxy

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"
)

func TestClassifyWebSocketUpstreamFailure_UsageLimitReachedUsesSemanticResetEvidence(t *testing.T) {
	observedAt := time.Date(2026, time.March, 26, 10, 0, 0, 0, time.UTC)
	resetAt := observedAt.Add(30 * time.Minute).Truncate(time.Second)

	disposition := classifyWebSocketUpstreamFailure(&WebSocketUpstreamError{
		EventType: codexUsageLimitErrorType, StatusCode: http.StatusTooManyRequests,
		ObservedAt: observedAt, ResetAt: &resetAt,
	})
	if disposition.switchReason != SwitchReasonUsageLimitReached {
		t.Fatalf("switchReason = %q, want %q", disposition.switchReason, SwitchReasonUsageLimitReached)
	}
	if disposition.autoDisableUntil != nil {
		t.Fatalf("autoDisableUntil = %v, want nil", disposition.autoDisableUntil)
	}
}

func TestClassifyWebSocketUpstreamFailure_ManagedProviderSuspendsOnUsageLimit(t *testing.T) {
	observedAt := time.Date(2026, time.March, 26, 10, 0, 0, 0, time.UTC)
	resetAt := observedAt.Add(30 * time.Minute).Truncate(time.Second)
	disposition := classifyWebSocketUpstreamFailureForProvider(&model.Provider{
		CredentialType: model.ProviderCredentialTypeChatGPT,
	}, &WebSocketUpstreamError{
		EventType: codexUsageLimitErrorType, StatusCode: http.StatusTooManyRequests,
		ObservedAt: observedAt, ResetAt: &resetAt,
	})
	if disposition.switchReason != SwitchReasonUsageLimitReached {
		t.Fatalf("switchReason = %q, want %q", disposition.switchReason, SwitchReasonUsageLimitReached)
	}
	if disposition.autoDisableUntil == nil || !disposition.autoDisableUntil.Equal(resetAt) {
		t.Fatalf("autoDisableUntil = %v, want %v", disposition.autoDisableUntil, resetAt)
	}
}

func TestClassifyWebSocketUpstreamFailure_UsesStatusCodeFieldAndLatestResetEvidence(t *testing.T) {
	observedAt := time.Date(2026, time.March, 26, 10, 0, 0, 0, time.UTC)
	eventReset := observedAt.Add(30 * time.Minute).Truncate(time.Second)
	laterReset := observedAt.Add(2 * time.Hour).Truncate(time.Second)
	payload := []byte(`{"type":"error","status_code":429,"error":{"type":"usage_limit_reached","resets_at":` + strconv.FormatInt(eventReset.Unix(), 10) + `}}`)

	upstreamErr := buildWebSocketUpstreamError(&codexWebSocketEventEnvelope{
		Type: "error",
		Error: &codexWebSocketEventError{
			Type: codexUsageLimitErrorType, StatusCode: http.StatusTooManyRequests, ResetsAt: laterReset.Unix(),
		},
	}, payload, observedAt)
	if upstreamErr == nil {
		t.Fatal("buildWebSocketUpstreamError() = nil, want semantic error")
	}
	disposition := classifyWebSocketUpstreamFailureForProvider(&model.Provider{
		CredentialType: model.ProviderCredentialTypeChatGPT,
	}, upstreamErr)
	if disposition.switchReason != SwitchReasonUsageLimitReached {
		t.Fatalf("switchReason = %q, want %q", disposition.switchReason, SwitchReasonUsageLimitReached)
	}
	if disposition.autoDisableUntil == nil || !disposition.autoDisableUntil.Equal(laterReset) {
		t.Fatalf("autoDisableUntil = %v, want %v", disposition.autoDisableUntil, laterReset)
	}
}

func TestWebSocketProviderConfigErrorErrorAndUnwrap(t *testing.T) {
	var nilErr *webSocketProviderConfigError
	if got := nilErr.Error(); got != "" {
		t.Fatalf("nil Error() = %q, want empty string", got)
	}
	if got := nilErr.Unwrap(); got != nil {
		t.Fatalf("nil Unwrap() = %v, want nil", got)
	}
	baseErr := errors.New("missing managed credential")
	cfgErr := &webSocketProviderConfigError{missingField: "credentials", err: baseErr}
	if got := cfgErr.Error(); got != baseErr.Error() {
		t.Fatalf("Error() = %q, want %q", got, baseErr.Error())
	}
	if got := cfgErr.Unwrap(); !errors.Is(got, baseErr) {
		t.Fatalf("Unwrap() = %v, want wrapped %v", got, baseErr)
	}
}

func TestShouldTrackWebSocketFailureInHealthHandlesAuthBoundaries(t *testing.T) {
	tests := []struct {
		name   string
		result *WebSocketResult
		want   bool
	}{
		{name: "nil result", want: false},
		{name: "handshake unauthorized", result: &WebSocketResult{HandshakeStatusCode: http.StatusUnauthorized}, want: false},
		{name: "non-auth upstream failure", result: &WebSocketResult{UpstreamError: &WebSocketUpstreamError{StatusCode: http.StatusTooManyRequests, EventType: "rate_limit_error"}}, want: true},
		{name: "forbidden auth error", result: &WebSocketResult{UpstreamError: &WebSocketUpstreamError{StatusCode: http.StatusForbidden, EventType: " auth_error "}}, want: false},
		{name: "auth error without forbidden status", result: &WebSocketResult{UpstreamError: &WebSocketUpstreamError{StatusCode: http.StatusBadGateway, EventType: "auth_error"}}, want: false},
		{name: "handshake failure", result: &WebSocketResult{HandshakeStatusCode: http.StatusBadGateway}, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := shouldTrackWebSocketFailureInHealth(test.result); got != test.want {
				t.Fatalf("shouldTrackWebSocketFailureInHealth() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestAssessWebSocketSession_UsageLimitAfterAcceptKeeps101AndRequiresReconnect(t *testing.T) {
	t.Parallel()

	assessment := assessWebSocketSession(&WebSocketSessionResult{
		FinalProvider: &model.Provider{ID: "ws-p1"},
		FinalResult: &WebSocketResult{
			ClientAccepted: true,
			ClientVisible:  true,
			TerminalCause:  model.TerminalUpstreamSemanticError,
			UpstreamError: &WebSocketUpstreamError{
				EventType:  codexUsageLimitErrorType,
				StatusCode: http.StatusTooManyRequests,
				Raw:        `{"type":"error"}`,
			},
		},
	})

	if assessment.ClientTransportStatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("ClientTransportStatusCode = %d, want %d", assessment.ClientTransportStatusCode, http.StatusSwitchingProtocols)
	}
	if assessment.ClientAction != model.ClientActionReconnectRequired {
		t.Fatalf("ClientAction = %q, want %q", assessment.ClientAction, model.ClientActionReconnectRequired)
	}
	if assessment.ServiceOutcome != model.ServiceOutcomeInterrupted {
		t.Fatalf("ServiceOutcome = %q, want %q", assessment.ServiceOutcome, model.ServiceOutcomeInterrupted)
	}
	if assessment.TerminationReason == nil || *assessment.TerminationReason != model.TerminationReasonUsageLimitReached {
		t.Fatalf("TerminationReason = %v, want %q", assessment.TerminationReason, model.TerminationReasonUsageLimitReached)
	}
}

func TestAssessWebSocketSession_PreVisibleProviderScopedReplacementClaimsTransparentRetry(t *testing.T) {
	t.Parallel()

	assessment := assessWebSocketSession(&WebSocketSessionResult{
		FinalProvider: &model.Provider{ID: "ws-p1"},
		FinalResult: &WebSocketResult{
			ClientAccepted:     true,
			ClientVisible:      true,
			SessionCommitted:   true,
			CompletionObserved: true,
			CommitSource:       model.CommitSemantic,
		},
		Attempts: []WebSocketAttemptResult{
			{
				Provider: &model.Provider{ID: "ws-origin"},
				Attempt:  0,
				Result: &WebSocketResult{
					HandshakeStatusCode: http.StatusTooManyRequests,
					TerminalCause:       model.TerminalUpstreamHandshakeRejected,
					Err:                 errors.New("usage limit during handshake"),
				},
				SwitchReason: SwitchReasonUsageLimitReached,
			},
			{
				Provider:      &model.Provider{ID: "ws-p1"},
				Attempt:       1,
				SelectionMode: providerSwitchModeReplacement,
				Result: &WebSocketResult{
					ClientAccepted:     true,
					ClientVisible:      true,
					SessionCommitted:   true,
					CompletionObserved: true,
					CommitSource:       model.CommitSemantic,
				},
			},
		},
	})

	if assessment.ServiceOutcome != model.ServiceOutcomeCompleted {
		t.Fatalf("ServiceOutcome = %q, want %q", assessment.ServiceOutcome, model.ServiceOutcomeCompleted)
	}
	if assessment.ClientAction != model.ClientActionTransparentRetry {
		t.Fatalf("ClientAction = %q, want %q", assessment.ClientAction, model.ClientActionTransparentRetry)
	}
}

func TestAssessWebSocketSession_GenericTransportReplacementDoesNotClaimTransparentRetry(t *testing.T) {
	t.Parallel()

	assessment := assessWebSocketSession(&WebSocketSessionResult{
		FinalProvider: &model.Provider{ID: "ws-p1"},
		FinalResult: &WebSocketResult{
			ClientAccepted:     true,
			ClientVisible:      true,
			SessionCommitted:   true,
			CompletionObserved: true,
			CommitSource:       model.CommitSemantic,
		},
		Attempts: []WebSocketAttemptResult{
			{
				Provider: &model.Provider{ID: "ws-origin"},
				Attempt:  0,
				Result: &WebSocketResult{
					TerminalCause: model.TerminalUpstreamTransportError,
					Err:           io.EOF,
				},
				SwitchReason: string(model.TerminalUpstreamTransportError),
			},
			{
				Provider:      &model.Provider{ID: "ws-p1"},
				Attempt:       1,
				SelectionMode: providerSwitchModeReplacement,
				Result: &WebSocketResult{
					ClientAccepted:     true,
					ClientVisible:      true,
					SessionCommitted:   true,
					CompletionObserved: true,
					CommitSource:       model.CommitSemantic,
				},
			},
		},
	})

	if assessment.ServiceOutcome != model.ServiceOutcomeCompleted {
		t.Fatalf("ServiceOutcome = %q, want %q", assessment.ServiceOutcome, model.ServiceOutcomeCompleted)
	}
	if assessment.ClientAction != model.ClientActionNone {
		t.Fatalf("ClientAction = %q, want %q", assessment.ClientAction, model.ClientActionNone)
	}
}

func TestAssessWebSocketSession_PostVisibleTransportErrorWithoutCompletionRemainsUnknown(t *testing.T) {
	t.Parallel()

	assessment := assessWebSocketSession(&WebSocketSessionResult{
		FinalProvider: &model.Provider{ID: "ws-p1"},
		FinalResult: &WebSocketResult{
			ClientAccepted:     true,
			ClientVisible:      true,
			SessionCommitted:   true,
			TerminalCause:      model.TerminalUpstreamTransportError,
			CompletionObserved: false,
			Err:                io.EOF,
		},
	})

	if assessment.ServiceOutcome != model.ServiceOutcomeUnknown {
		t.Fatalf("ServiceOutcome = %q, want %q", assessment.ServiceOutcome, model.ServiceOutcomeUnknown)
	}
	if assessment.ClientAction != model.ClientActionNone {
		t.Fatalf("ClientAction = %q, want %q", assessment.ClientAction, model.ClientActionNone)
	}
	if assessment.CompletionState != model.CompletionStateUnknown {
		t.Fatalf("CompletionState = %q, want %q", assessment.CompletionState, model.CompletionStateUnknown)
	}
	if assessment.providerFailure.isProviderScoped() {
		t.Fatal("providerFailure should remain false for generic post-visible transport loss")
	}
}

func TestAssessWebSocketSession_WebSocketConnectionLimitDerivesOutcomeFromRuntimeFacts(t *testing.T) {
	t.Parallel()

	const rawPayload = `{"type":"error","message":"connection limit reached"}`

	testCases := []struct {
		name                string
		result              *WebSocketResult
		wantOutcome         model.ServiceOutcome
		wantCompletionState model.CompletionState
	}{
		{
			name: "visible session without completion stays unknown",
			result: &WebSocketResult{
				ClientAccepted:   true,
				ClientVisible:    true,
				SessionCommitted: true,
				TerminalCause:    model.TerminalUpstreamSemanticError,
				UpstreamError: &WebSocketUpstreamError{
					EventType:  webSocketConnectionLimitErrorType,
					StatusCode: http.StatusTooManyRequests,
					Raw:        rawPayload,
				},
			},
			wantOutcome:         model.ServiceOutcomeUnknown,
			wantCompletionState: model.CompletionStateUnknown,
		},
		{
			name: "observed completion stays completed",
			result: &WebSocketResult{
				ClientAccepted:     true,
				ClientVisible:      true,
				SessionCommitted:   true,
				CompletionObserved: true,
				TerminalCause:      model.TerminalUpstreamSemanticError,
				UpstreamError: &WebSocketUpstreamError{
					EventType:  webSocketConnectionLimitErrorType,
					StatusCode: http.StatusTooManyRequests,
					Raw:        rawPayload,
				},
			},
			wantOutcome:         model.ServiceOutcomeCompleted,
			wantCompletionState: model.CompletionStateCompleted,
		},
		{
			name: "pre-visible termination stays never started",
			result: &WebSocketResult{
				ClientAccepted: true,
				TerminalCause:  model.TerminalUpstreamSemanticError,
				UpstreamError: &WebSocketUpstreamError{
					EventType:  webSocketConnectionLimitErrorType,
					StatusCode: http.StatusTooManyRequests,
					Raw:        rawPayload,
				},
			},
			wantOutcome:         model.ServiceOutcomeNeverStarted,
			wantCompletionState: model.CompletionStateIncomplete,
		},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			assessment := assessWebSocketSession(&WebSocketSessionResult{
				FinalProvider: &model.Provider{ID: "ws-p1"},
				FinalResult:   tt.result,
			})

			if assessment.ServiceOutcome != tt.wantOutcome {
				t.Fatalf("ServiceOutcome = %q, want %q", assessment.ServiceOutcome, tt.wantOutcome)
			}
			if assessment.CompletionState != tt.wantCompletionState {
				t.Fatalf("CompletionState = %q, want %q", assessment.CompletionState, tt.wantCompletionState)
			}
			if assessment.ClientAction != model.ClientActionNone {
				t.Fatalf("ClientAction = %q, want %q", assessment.ClientAction, model.ClientActionNone)
			}
			if assessment.TerminationReason == nil || *assessment.TerminationReason != model.TerminationReasonWebSocketConnectionLimitReached {
				t.Fatalf("TerminationReason = %v, want %q", assessment.TerminationReason, model.TerminationReasonWebSocketConnectionLimitReached)
			}
			if assessment.providerFailure.isProviderScoped() {
				t.Fatal("providerFailure should remain false for websocket_connection_limit_reached")
			}
			if assessment.SessionEvidenceJSON == nil {
				t.Fatal("SessionEvidenceJSON = nil, want upstream provider evidence")
			}

			var evidence webSocketEvidence
			if err := json.Unmarshal([]byte(*assessment.SessionEvidenceJSON), &evidence); err != nil {
				t.Fatalf("json.Unmarshal(SessionEvidenceJSON) = %v", err)
			}
			if evidence.UpstreamEvent == nil {
				t.Fatal("UpstreamEvent = nil, want upstream event evidence")
			}
			if evidence.UpstreamEvent.RawPayloadSnippet != rawPayload {
				t.Fatalf("UpstreamEvent.RawPayloadSnippet = %q, want %q", evidence.UpstreamEvent.RawPayloadSnippet, rawPayload)
			}
		})
	}
}

func TestAssessWebSocketSession_CompletedCleanCloseOmitsTerminationAttribution(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name               string
		completionObserved bool
	}{
		{
			name:               "explicit completion",
			completionObserved: true,
		},
		{
			name:               "inferred from clean close",
			completionObserved: false,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			assessment := assessWebSocketSession(&WebSocketSessionResult{
				FinalProvider: &model.Provider{ID: "ws-p1"},
				FinalResult: &WebSocketResult{
					ClientAccepted:     true,
					ClientVisible:      true,
					SessionCommitted:   true,
					CompletionObserved: testCase.completionObserved,
					CommitSource:       model.CommitSemantic,
					TerminalCause:      model.TerminalCleanClose,
				},
			})

			if assessment.ServiceOutcome != model.ServiceOutcomeCompleted {
				t.Fatalf("ServiceOutcome = %q, want %q", assessment.ServiceOutcome, model.ServiceOutcomeCompleted)
			}
			if assessment.CompletionState != model.CompletionStateCompleted {
				t.Fatalf("CompletionState = %q, want %q", assessment.CompletionState, model.CompletionStateCompleted)
			}
			if assessment.TerminationReason != nil {
				t.Fatalf("TerminationReason = %v, want nil for nominal clean close", assessment.TerminationReason)
			}
			if assessment.TerminationActor != nil {
				t.Fatalf("TerminationActor = %v, want nil for nominal clean close", assessment.TerminationActor)
			}
		})
	}
}

func TestAssessWebSocketSession_NeverStartedReplacementDoesNotClaimTransparentRetry(t *testing.T) {
	t.Parallel()

	assessment := assessWebSocketSession(&WebSocketSessionResult{
		FinalProvider: &model.Provider{ID: "ws-p1"},
		FinalResult: &WebSocketResult{
			HandshakeStatusCode: http.StatusBadGateway,
			TerminalCause:       model.TerminalUpstreamHandshakeRejected,
			Err:                 errors.New("replacement handshake failed"),
		},
		FinalErr: errors.New("replacement handshake failed"),
		Attempts: []WebSocketAttemptResult{
			{
				Provider: &model.Provider{ID: "ws-origin"},
				Attempt:  0,
				Result: &WebSocketResult{
					TerminalCause: model.TerminalUpstreamTransportError,
					Err:           io.EOF,
				},
				SwitchReason: string(model.TerminalUpstreamTransportError),
			},
			{
				Provider:      &model.Provider{ID: "ws-p1"},
				Attempt:       1,
				SelectionMode: providerSwitchModeReplacement,
				Result: &WebSocketResult{
					HandshakeStatusCode: http.StatusBadGateway,
					TerminalCause:       model.TerminalUpstreamHandshakeRejected,
					Err:                 errors.New("replacement handshake failed"),
				},
			},
		},
	})

	if assessment.ServiceOutcome != model.ServiceOutcomeNeverStarted {
		t.Fatalf("ServiceOutcome = %q, want %q", assessment.ServiceOutcome, model.ServiceOutcomeNeverStarted)
	}
	if assessment.ClientAction != model.ClientActionNone {
		t.Fatalf("ClientAction = %q, want %q", assessment.ClientAction, model.ClientActionNone)
	}
}

func TestAssessWebSocketSession_ClientDisconnectMapsAbandonedByClient(t *testing.T) {
	t.Parallel()

	assessment := assessWebSocketSession(&WebSocketSessionResult{
		FinalProvider: &model.Provider{ID: "ws-p1"},
		FinalResult: &WebSocketResult{
			ClientAccepted:   true,
			ClientVisible:    true,
			SessionCommitted: true,
			TerminalCause:    model.TerminalClientDisconnect,
			Err:              errors.New("client disconnected"),
		},
	})

	if assessment.ServiceOutcome != model.ServiceOutcomeAbandonedByClient {
		t.Fatalf("ServiceOutcome = %q, want %q", assessment.ServiceOutcome, model.ServiceOutcomeAbandonedByClient)
	}
	if assessment.ClientAction != model.ClientActionNone {
		t.Fatalf("ClientAction = %q, want %q", assessment.ClientAction, model.ClientActionNone)
	}
	if assessment.TerminationReason == nil || *assessment.TerminationReason != model.TerminationReasonClientDisconnect {
		t.Fatalf("TerminationReason = %v, want %q", assessment.TerminationReason, model.TerminationReasonClientDisconnect)
	}
}

func TestAssessWebSocketSession_FinalAndAttemptEvidenceStayDistinct(t *testing.T) {
	t.Parallel()

	session := &WebSocketSessionResult{
		FinalProvider: &model.Provider{ID: "provider-final"},
		FinalResult: &WebSocketResult{
			ClientAccepted:   true,
			SessionCommitted: true,
			TerminalCause:    model.TerminalUpstreamTransportError,
			Err:              errors.New("final transport loss"),
		},
		Attempts: []WebSocketAttemptResult{
			{
				Provider: &model.Provider{ID: "provider-origin"},
				Result: &WebSocketResult{
					HandshakeAccepted: true,
					TerminalCause:     model.TerminalUpstreamSemanticError,
					UpstreamError: &WebSocketUpstreamError{
						EventType: "provider_failure",
						Raw:       `{"message":"origin failure"}`,
					},
				},
				ForwardErr: errors.New("origin failure"),
			},
		},
	}

	assessment := assessWebSocketSession(session)
	if assessment.SessionEvidenceJSON == nil {
		t.Fatal("SessionEvidenceJSON = nil, want final session evidence")
	}
	if strings.Contains(*assessment.SessionEvidenceJSON, "origin failure") {
		t.Fatalf("SessionEvidenceJSON = %q, must not contain replaced-attempt evidence", *assessment.SessionEvidenceJSON)
	}

	attemptEvidence := buildWebSocketAttemptEvidence(session.Attempts[0])
	if attemptEvidence == nil {
		t.Fatal("AttemptEvidenceJSON = nil, want replaced-attempt evidence")
	}
	if !strings.Contains(*attemptEvidence, "origin failure") {
		t.Fatalf("AttemptEvidenceJSON = %q, want replaced-attempt payload", *attemptEvidence)
	}

	var evidence webSocketEvidence
	if err := json.Unmarshal([]byte(*attemptEvidence), &evidence); err != nil {
		t.Fatalf("json.Unmarshal(attempt evidence) = %v", err)
	}
	if evidence.UpstreamEvent == nil || evidence.UpstreamEvent.RawPayloadSnippet == "" {
		t.Fatalf("attempt evidence = %+v, want upstream_event payload", evidence)
	}
}

func TestAssessWebSocketSession_PreservesEnvelopeAndProviderErrorTypesInEvidence(t *testing.T) {
	t.Parallel()

	assessment := assessWebSocketSession(&WebSocketSessionResult{
		FinalProvider: &model.Provider{ID: "provider-final"},
		FinalResult: &WebSocketResult{
			TerminalCause: model.TerminalUpstreamSemanticError,
			UpstreamError: &WebSocketUpstreamError{
				EnvelopeType:      "error",
				ProviderErrorType: "model_not_allowed",
				EventType:         "model_not_allowed",
				Code:              "model_not_allowed",
				StatusCode:        http.StatusForbidden,
				Message:           "Model not allowed",
				Raw:               `{"type":"error","error":{"type":"model_not_allowed"}}`,
			},
		},
	})

	if assessment.SessionEvidenceJSON == nil {
		t.Fatal("SessionEvidenceJSON = nil, want upstream evidence")
	}

	var evidence webSocketEvidence
	if err := json.Unmarshal([]byte(*assessment.SessionEvidenceJSON), &evidence); err != nil {
		t.Fatalf("json.Unmarshal(SessionEvidenceJSON) = %v", err)
	}
	if evidence.UpstreamEvent == nil {
		t.Fatal("UpstreamEvent = nil, want upstream evidence")
	}
	if evidence.UpstreamEvent.EnvelopeType != "error" {
		t.Fatalf("EnvelopeType = %q, want %q", evidence.UpstreamEvent.EnvelopeType, "error")
	}
	if evidence.UpstreamEvent.ProviderErrorType != "model_not_allowed" {
		t.Fatalf("ProviderErrorType = %q, want %q", evidence.UpstreamEvent.ProviderErrorType, "model_not_allowed")
	}
}

func TestAssessWebSocketSession_RedactsOnlyInjectedAPIKey(t *testing.T) {
	t.Parallel()

	const injectedKey = "provider-key"
	assessment := assessWebSocketSession(&WebSocketSessionResult{
		APIType:       "codex",
		FinalProvider: &model.Provider{ID: "provider-final", CredentialType: model.ProviderCredentialTypeAPIKey, APIKey: injectedKey},
		FinalResult: &WebSocketResult{
			TerminalCause: model.TerminalUpstreamSemanticError,
			UpstreamError: &WebSocketUpstreamError{
				EnvelopeType: "error",
				Message:      "provider-key rejected client-token",
				Raw:          `{"message":"provider-key rejected client-token"}`,
			},
		},
	})
	if assessment.SessionEvidenceJSON == nil {
		t.Fatal("SessionEvidenceJSON = nil, want upstream evidence")
	}
	if strings.Contains(*assessment.SessionEvidenceJSON, injectedKey) ||
		!strings.Contains(*assessment.SessionEvidenceJSON, "client-token") ||
		!strings.Contains(*assessment.SessionEvidenceJSON, "[REDACTED]") {
		t.Fatalf("SessionEvidenceJSON = %q, want only injected key redacted", *assessment.SessionEvidenceJSON)
	}
}

func TestAssessWebSocketSession_RedactsOAuthAccessTokenOnly(t *testing.T) {
	t.Parallel()

	const (
		accessToken  = "oauth-access-token"
		refreshToken = "oauth-refresh-token"
		idToken      = "oauth-id-token"
	)
	secretData, err := model.EncodeChatGPTProviderSecret(&model.ChatGPTProviderSecret{
		AccessToken: accessToken, RefreshToken: refreshToken, IDToken: idToken,
	})
	if err != nil {
		t.Fatalf("EncodeChatGPTProviderSecret() error = %v", err)
	}
	assessment := assessWebSocketSession(&WebSocketSessionResult{
		APIType: "codex",
		FinalProvider: &model.Provider{
			ID:             "provider-final",
			CredentialType: model.ProviderCredentialTypeChatGPT,
			Credential:     &model.ProviderCredential{SecretData: secretData},
		},
		FinalResult: &WebSocketResult{
			TerminalCause: model.TerminalUpstreamSemanticError,
			UpstreamError: &WebSocketUpstreamError{
				EnvelopeType: "error",
				Message:      accessToken + " rejected " + refreshToken + " " + idToken + " client-token",
				Raw:          `{"message":"` + accessToken + ` rejected ` + refreshToken + ` ` + idToken + ` client-token"}`,
			},
		},
	})
	if assessment.SessionEvidenceJSON == nil {
		t.Fatal("SessionEvidenceJSON = nil, want upstream evidence")
	}
	evidence := *assessment.SessionEvidenceJSON
	if strings.Contains(evidence, accessToken) ||
		!strings.Contains(evidence, refreshToken) ||
		!strings.Contains(evidence, idToken) ||
		!strings.Contains(evidence, "client-token") ||
		!strings.Contains(evidence, "[REDACTED]") {
		t.Fatalf("SessionEvidenceJSON = %q, want only OAuth access token redacted", evidence)
	}
}

func TestAssessWebSocketSession_PostVisibleClientScopedSemanticErrorStaysUnknown(t *testing.T) {
	t.Parallel()

	assessment := assessWebSocketSession(&WebSocketSessionResult{
		FinalProvider: &model.Provider{ID: "ws-p1"},
		FinalResult: &WebSocketResult{
			ClientAccepted:   true,
			ClientVisible:    true,
			SessionCommitted: true,
			TerminalCause:    model.TerminalUpstreamSemanticError,
			UpstreamError: &WebSocketUpstreamError{
				EventType:  "invalid_request_error",
				StatusCode: http.StatusBadRequest,
				Message:    "invalid request",
				Raw:        `{"type":"error","error":{"type":"invalid_request_error"}}`,
			},
		},
	})

	if assessment.ClientTransportStatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("ClientTransportStatusCode = %d, want %d", assessment.ClientTransportStatusCode, http.StatusSwitchingProtocols)
	}
	if assessment.ServiceOutcome != model.ServiceOutcomeUnknown {
		t.Fatalf("ServiceOutcome = %q, want %q", assessment.ServiceOutcome, model.ServiceOutcomeUnknown)
	}
	if assessment.CompletionState != model.CompletionStateUnknown {
		t.Fatalf("CompletionState = %q, want %q", assessment.CompletionState, model.CompletionStateUnknown)
	}
	if assessment.ClientAction != model.ClientActionNone {
		t.Fatalf("ClientAction = %q, want %q", assessment.ClientAction, model.ClientActionNone)
	}
	if assessment.TerminationReason == nil || *assessment.TerminationReason != model.TerminationReasonClientRequestError {
		t.Fatalf("TerminationReason = %v, want %q", assessment.TerminationReason, model.TerminationReasonClientRequestError)
	}
	if assessment.TerminationActor == nil || *assessment.TerminationActor != model.TerminationActorClient {
		t.Fatalf("TerminationActor = %v, want %q", assessment.TerminationActor, model.TerminationActorClient)
	}
	if assessment.providerFailure.isProviderScoped() {
		t.Fatal("providerFailure should remain false for client-scoped semantic errors")
	}
}

func TestAssessWebSocketSession_GatewayProviderUnavailableBeforeStartStaysNeverStarted(t *testing.T) {
	t.Parallel()

	assessment := assessWebSocketSession(&WebSocketSessionResult{
		GatewayStatusCode: http.StatusServiceUnavailable,
		GatewayErrorCode:  ErrCodeProviderUnavailable,
		GatewayMessage:    "no available provider",
	})

	if assessment.ClientTransportStatusCode != http.StatusServiceUnavailable {
		t.Fatalf("ClientTransportStatusCode = %d, want %d", assessment.ClientTransportStatusCode, http.StatusServiceUnavailable)
	}
	if assessment.ServiceOutcome != model.ServiceOutcomeNeverStarted {
		t.Fatalf("ServiceOutcome = %q, want %q", assessment.ServiceOutcome, model.ServiceOutcomeNeverStarted)
	}
	if assessment.CompletionState != model.CompletionStateIncomplete {
		t.Fatalf("CompletionState = %q, want %q", assessment.CompletionState, model.CompletionStateIncomplete)
	}
	if assessment.ClientAction != model.ClientActionNone {
		t.Fatalf("ClientAction = %q, want %q", assessment.ClientAction, model.ClientActionNone)
	}
	if assessment.TerminationReason == nil || *assessment.TerminationReason != model.TerminationReasonProviderUnavailable {
		t.Fatalf("TerminationReason = %v, want %q", assessment.TerminationReason, model.TerminationReasonProviderUnavailable)
	}
	if assessment.TerminationActor == nil || *assessment.TerminationActor != model.TerminationActorGateway {
		t.Fatalf("TerminationActor = %v, want %q", assessment.TerminationActor, model.TerminationActorGateway)
	}
	if assessment.SessionEvidenceJSON == nil {
		t.Fatal("SessionEvidenceJSON = nil, want gateway evidence")
	}

	var evidence webSocketEvidence
	if err := json.Unmarshal([]byte(*assessment.SessionEvidenceJSON), &evidence); err != nil {
		t.Fatalf("json.Unmarshal(SessionEvidenceJSON) = %v", err)
	}
	if evidence.Gateway == nil {
		t.Fatal("Gateway = nil, want gateway evidence")
	}
	if evidence.Gateway.TerminalErrorCode != ErrCodeProviderUnavailable {
		t.Fatalf("Gateway.TerminalErrorCode = %q, want %q", evidence.Gateway.TerminalErrorCode, ErrCodeProviderUnavailable)
	}
}

func TestAssessWebSocketSession_VisibleSessionWithoutTerminalEvidenceNormalizesUnknown(t *testing.T) {
	t.Parallel()

	assessment := assessWebSocketSession(&WebSocketSessionResult{
		FinalProvider: &model.Provider{ID: "ws-p1"},
		FinalResult: &WebSocketResult{
			ClientAccepted:   true,
			ClientVisible:    true,
			SessionCommitted: true,
		},
	})

	if assessment.ServiceOutcome != model.ServiceOutcomeUnknown {
		t.Fatalf("ServiceOutcome = %q, want %q", assessment.ServiceOutcome, model.ServiceOutcomeUnknown)
	}
	if assessment.CompletionState != model.CompletionStateUnknown {
		t.Fatalf("CompletionState = %q, want %q", assessment.CompletionState, model.CompletionStateUnknown)
	}
	if assessment.TerminationReason == nil || *assessment.TerminationReason != model.TerminationReasonUnknown {
		t.Fatalf("TerminationReason = %v, want %q", assessment.TerminationReason, model.TerminationReasonUnknown)
	}
	if assessment.TerminationActor == nil || *assessment.TerminationActor != model.TerminationActorUnknown {
		t.Fatalf("TerminationActor = %v, want %q", assessment.TerminationActor, model.TerminationActorUnknown)
	}
}

func TestAssessWebSocketSession_CommittedInternalErrorRemainsUnknown(t *testing.T) {
	t.Parallel()

	assessment := assessWebSocketSession(&WebSocketSessionResult{
		FinalProvider: &model.Provider{ID: "ws-p1"},
		FinalResult: &WebSocketResult{
			ClientAccepted:   true,
			SessionCommitted: true,
			TerminalCause:    model.TerminalInternalError,
			Err:              errors.New("relay bookkeeping failed"),
		},
	})

	if assessment.ServiceOutcome != model.ServiceOutcomeUnknown {
		t.Fatalf("ServiceOutcome = %q, want %q", assessment.ServiceOutcome, model.ServiceOutcomeUnknown)
	}
	if assessment.CompletionState != model.CompletionStateUnknown {
		t.Fatalf("CompletionState = %q, want %q", assessment.CompletionState, model.CompletionStateUnknown)
	}
	if assessment.ClientAction != model.ClientActionNone {
		t.Fatalf("ClientAction = %q, want %q", assessment.ClientAction, model.ClientActionNone)
	}
	if assessment.TerminationReason == nil || *assessment.TerminationReason != model.TerminationReasonInternalError {
		t.Fatalf("TerminationReason = %v, want %q", assessment.TerminationReason, model.TerminationReasonInternalError)
	}
	if assessment.TerminationActor == nil || *assessment.TerminationActor != model.TerminationActorInternal {
		t.Fatalf("TerminationActor = %v, want %q", assessment.TerminationActor, model.TerminationActorInternal)
	}
}

func TestAssessWebSocketSession_PreVisibleClientScopedSemanticErrorStaysNeverStarted(t *testing.T) {
	t.Parallel()

	assessment := assessWebSocketSession(&WebSocketSessionResult{
		FinalProvider: &model.Provider{ID: "ws-p1"},
		FinalResult: &WebSocketResult{
			ClientAccepted: true,
			TerminalCause:  model.TerminalUpstreamSemanticError,
			UpstreamError: &WebSocketUpstreamError{
				EventType:  "invalid_request_error",
				StatusCode: http.StatusBadRequest,
				Message:    "invalid request",
			},
		},
	})

	if assessment.ServiceOutcome != model.ServiceOutcomeNeverStarted {
		t.Fatalf("ServiceOutcome = %q, want %q", assessment.ServiceOutcome, model.ServiceOutcomeNeverStarted)
	}
	if assessment.CompletionState != model.CompletionStateIncomplete {
		t.Fatalf("CompletionState = %q, want %q", assessment.CompletionState, model.CompletionStateIncomplete)
	}
	if assessment.ClientAction != model.ClientActionNone {
		t.Fatalf("ClientAction = %q, want %q", assessment.ClientAction, model.ClientActionNone)
	}
	if assessment.TerminationReason == nil || *assessment.TerminationReason != model.TerminationReasonClientRequestError {
		t.Fatalf("TerminationReason = %v, want %q", assessment.TerminationReason, model.TerminationReasonClientRequestError)
	}
	if assessment.TerminationActor == nil || *assessment.TerminationActor != model.TerminationActorClient {
		t.Fatalf("TerminationActor = %v, want %q", assessment.TerminationActor, model.TerminationActorClient)
	}
}

func TestAssessWebSocketSession_InternalErrorAfterCompletionStillReportsCompleted(t *testing.T) {
	t.Parallel()

	assessment := assessWebSocketSession(&WebSocketSessionResult{
		FinalProvider: &model.Provider{ID: "ws-p1"},
		FinalResult: &WebSocketResult{
			ClientAccepted:     true,
			ClientVisible:      true,
			SessionCommitted:   true,
			CompletionObserved: true,
			TerminalCause:      model.TerminalInternalError,
			Err:                errors.New("late cleanup error"),
		},
	})

	if assessment.ServiceOutcome != model.ServiceOutcomeCompleted {
		t.Fatalf("ServiceOutcome = %q, want %q", assessment.ServiceOutcome, model.ServiceOutcomeCompleted)
	}
	if assessment.CompletionState != model.CompletionStateCompleted {
		t.Fatalf("CompletionState = %q, want %q", assessment.CompletionState, model.CompletionStateCompleted)
	}
	if assessment.ClientAction != model.ClientActionNone {
		t.Fatalf("ClientAction = %q, want %q", assessment.ClientAction, model.ClientActionNone)
	}
	if assessment.TerminationReason == nil || *assessment.TerminationReason != model.TerminationReasonInternalError {
		t.Fatalf("TerminationReason = %v, want %q", assessment.TerminationReason, model.TerminationReasonInternalError)
	}
	if assessment.TerminationActor == nil || *assessment.TerminationActor != model.TerminationActorInternal {
		t.Fatalf("TerminationActor = %v, want %q", assessment.TerminationActor, model.TerminationActorInternal)
	}
}

func TestMarshalWebSocketEvidence_TrimsOversizedDiagnosticSnippets(t *testing.T) {
	t.Parallel()

	large := strings.Repeat("oversized-snippet-", 80)
	evidenceJSON := marshalWebSocketEvidence(webSocketEvidence{
		SchemaVersion: webSocketEvidenceSchemaVersion,
		Gateway: &webSocketGatewayEvidence{
			TerminalStatusCode:     http.StatusBadGateway,
			TerminalErrorCode:      strings.Repeat("gateway-code-", 30),
			TerminalMessageSnippet: large,
		},
		UpstreamHandshake: &webSocketUpstreamHandshakeEvidence{
			StatusCode:  http.StatusBadGateway,
			BodySnippet: large,
		},
		Transport: &transportDiagnostic{
			Source:             transportSourceUpstream,
			Stage:              transportStagePostPayloadVisible,
			Kind:               transportKindDisconnect,
			Signal:             transportSignalCloseError,
			CloseReasonSnippet: large,
			RawErrorSnippet:    large,
		},
		UpstreamEvent: &webSocketUpstreamEventEvidence{
			EnvelopeType:      "error",
			ProviderErrorType: strings.Repeat("provider-type-", 30),
			ProviderErrorCode: "rate_limit_exceeded",
			StatusCode:        http.StatusTooManyRequests,
			MessageSnippet:    large,
			RawPayloadSnippet: large,
		},
	})

	if evidenceJSON == nil {
		t.Fatal("marshalWebSocketEvidence(...) = nil, want trimmed evidence")
	}
	if len(*evidenceJSON) > webSocketEvidenceJSONLimitBytes {
		t.Fatalf("len(evidenceJSON) = %d, want <= %d", len(*evidenceJSON), webSocketEvidenceJSONLimitBytes)
	}

	var evidence webSocketEvidence
	if err := json.Unmarshal([]byte(*evidenceJSON), &evidence); err != nil {
		t.Fatalf("json.Unmarshal(evidenceJSON) = %v", err)
	}
	if evidence.SchemaVersion != webSocketEvidenceSchemaVersion {
		t.Fatalf("SchemaVersion = %d, want %d", evidence.SchemaVersion, webSocketEvidenceSchemaVersion)
	}
	if evidence.Gateway == nil || evidence.Gateway.TerminalErrorCode == "" {
		t.Fatalf("Gateway = %+v, want retained gateway classification", evidence.Gateway)
	}
	if evidence.UpstreamEvent == nil || evidence.UpstreamEvent.ProviderErrorType == "" {
		t.Fatalf("UpstreamEvent = %+v, want retained upstream classification", evidence.UpstreamEvent)
	}
	// Signal / Stage / Kind / Source are structural classification fields; the
	// trim pass must preserve them so the renderer summary still works.
	if evidence.Transport == nil || evidence.Transport.Signal == "" {
		t.Fatalf("Transport = %+v, want retained structural classification", evidence.Transport)
	}
	if evidence.Gateway != nil && evidence.Gateway.TerminalMessageSnippet != "" {
		t.Fatalf("Gateway.TerminalMessageSnippet = %q, want trimmed empty value", evidence.Gateway.TerminalMessageSnippet)
	}
	if evidence.UpstreamHandshake != nil && evidence.UpstreamHandshake.BodySnippet != "" {
		t.Fatalf("UpstreamHandshake.BodySnippet = %q, want trimmed empty value", evidence.UpstreamHandshake.BodySnippet)
	}
	if evidence.Transport != nil && evidence.Transport.CloseReasonSnippet != "" {
		t.Fatalf("Transport.CloseReasonSnippet = %q, want trimmed empty value", evidence.Transport.CloseReasonSnippet)
	}
	if evidence.Transport != nil && evidence.Transport.RawErrorSnippet != "" {
		t.Fatalf("Transport.RawErrorSnippet = %q, want trimmed empty value", evidence.Transport.RawErrorSnippet)
	}
	if evidence.UpstreamEvent != nil && evidence.UpstreamEvent.MessageSnippet != "" {
		t.Fatalf("UpstreamEvent.MessageSnippet = %q, want trimmed empty value", evidence.UpstreamEvent.MessageSnippet)
	}
	if evidence.UpstreamEvent != nil && evidence.UpstreamEvent.RawPayloadSnippet != "" {
		t.Fatalf("UpstreamEvent.RawPayloadSnippet = %q, want trimmed empty value", evidence.UpstreamEvent.RawPayloadSnippet)
	}
}
