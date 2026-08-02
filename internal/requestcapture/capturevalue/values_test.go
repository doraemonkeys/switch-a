package capturevalue

import "testing"

func TestCanonicalCaptureValues(t *testing.T) {
	t.Run("selection mode", func(t *testing.T) {
		assertCanonicalValues(t, []SelectionMode{
			SelectionModeInitial,
			SelectionModeReplacement,
			SelectionModeFailover,
		}, SelectionMode("hostile"), SelectionMode(""), CanonicalSelectionMode)
	})
	t.Run("selection source", func(t *testing.T) {
		assertCanonicalValues(t, []SelectionSource{
			SelectionSourceStrategy,
			SelectionSourceStickyContinuity,
			SelectionSourceActiveContinuity,
		}, SelectionSource("hostile"), SelectionSource(""), CanonicalSelectionSource)
	})
	t.Run("credential phase", func(t *testing.T) {
		assertCanonicalValues(t, []CredentialPhase{
			CredentialPhaseInitial,
			CredentialPhaseRefreshed,
		}, CredentialPhase("hostile"), CredentialPhase(""), CanonicalCredentialPhase)
	})
	t.Run("message direction", func(t *testing.T) {
		assertCanonicalValues(t, []MessageDirection{
			MessageDirectionClientToUpstream,
			MessageDirectionUpstreamToClient,
		}, MessageDirection("hostile"), MessageDirection(""), CanonicalMessageDirection)
	})
	t.Run("message type", func(t *testing.T) {
		assertCanonicalValues(t, []MessageType{
			MessageTypeText,
			MessageTypeBinary,
		}, MessageType("hostile"), MessageType(""), CanonicalMessageType)
	})
	t.Run("message source", func(t *testing.T) {
		assertCanonicalValues(t, []MessageSource{
			MessageSourceLive,
			MessageSourceReplay,
		}, MessageSource("hostile"), MessageSource(""), CanonicalMessageSource)
	})
	t.Run("message disposition", func(t *testing.T) {
		assertCanonicalValues(t, []MessageDisposition{
			MessageDispositionForwarded,
			MessageDispositionSuppressed,
			MessageDispositionWriteFailed,
		}, MessageDisposition("hostile"), MessageDisposition(""), CanonicalMessageDisposition)
	})
	t.Run("termination reason", func(t *testing.T) {
		assertCanonicalValues(t, []TerminationReason{
			TerminationReasonEOF,
			TerminationReasonStatusFailoverDrain,
			TerminationReasonCredentialRefreshDrain,
			TerminationReasonClientDisconnect,
			TerminationReasonTimeout,
			TerminationReasonCanceled,
			TerminationReasonPreparationError,
			TerminationReasonGatewayFinished,
			TerminationReasonCaptureFault,
			TerminationReasonTransportError,
			TerminationReasonReadError,
			TerminationReasonWriteError,
			TerminationReasonWebSocketClose,
			TerminationReasonWebSocketRelayError,
		}, TerminationReason("hostile"), TerminationReason(""), CanonicalTerminationReason)
	})
	t.Run("source completion", func(t *testing.T) {
		assertCanonicalValues(t, []SourceCompletion{
			SourceCompletionComplete,
			SourceCompletionPartial,
		}, SourceCompletion("hostile"), SourceCompletion(""), CanonicalSourceCompletion)
	})
	t.Run("failure site", func(t *testing.T) {
		assertCanonicalValues(t, []FailureSite{
			FailureSiteUnknown,
			FailureSiteGateway,
			FailureSitePreparation,
			FailureSiteTransport,
			FailureSiteResponseStatus,
			FailureSiteResponseDrain,
			FailureSiteResponseRead,
			FailureSiteResponseWrite,
			FailureSiteWebSocketHandshake,
			FailureSiteWebSocketUpgrade,
			FailureSiteWebSocketReplay,
			FailureSiteWebSocketRelay,
			FailureSiteWebSocketMessage,
			FailureSiteWebSocketClose,
		}, FailureSite("hostile"), FailureSiteUnknown, CanonicalFailureSite)
	})
	t.Run("failure peer", func(t *testing.T) {
		assertCanonicalValues(t, []FailurePeer{
			FailurePeerUnknown,
			FailurePeerGateway,
			FailurePeerClient,
			FailurePeerUpstream,
			FailurePeerProvider,
		}, FailurePeer("hostile"), FailurePeerUnknown, CanonicalFailurePeer)
	})
	t.Run("failure class", func(t *testing.T) {
		assertCanonicalValues(t, []FailureClass{
			FailureClassUnknown,
			FailureClassTimeout,
			FailureClassCanceled,
			FailureClassConfiguration,
			FailureClassTransport,
			FailureClassHTTPStatus,
			FailureClassRead,
			FailureClassWrite,
			FailureClassProtocol,
			FailureClassWebSocketClose,
			FailureClassUpstreamSemantic,
		}, FailureClass("hostile"), FailureClassUnknown, CanonicalFailureClass)
	})
	t.Run("failure code", func(t *testing.T) {
		assertCanonicalValues(t, []FailureCode{
			FailureCodeUnknown,
			FailureCodeMissingBaseURL,
			FailureCodeMissingAPIKey,
			FailureCodeMissingCredentials,
			FailureCodeRequestBuild,
			FailureCodeCredentialApply,
			FailureCodeGatewayContext,
			FailureCodeDNS,
			FailureCodeConnection,
			FailureCodeRoundTrip,
			FailureCodeUnexpectedStatus,
			FailureCodeFailureBodyRead,
			FailureCodeDrainRead,
			FailureCodeUpstreamRead,
			FailureCodeClientWrite,
			FailureCodeClientAccept,
			FailureCodeWebSocketDial,
			FailureCodeHandshakeRejected,
			FailureCodeWebSocketUpgrade,
			FailureCodeReplayWrite,
			FailureCodeRelayRead,
			FailureCodeRelayWrite,
			FailureCodeMessageRead,
			FailureCodeMessageWrite,
			FailureCodeProtocolViolation,
			FailureCodeWebSocketClose,
			FailureCodeProviderSemantic,
		}, FailureCode("hostile"), FailureCodeUnknown, CanonicalFailureCode)
	})
}

func assertCanonicalValues[T comparable](
	t *testing.T,
	valid []T,
	invalid T,
	fallback T,
	canonicalize func(T) (T, bool),
) {
	t.Helper()
	for _, value := range valid {
		got, ok := canonicalize(value)
		if !ok || got != value {
			t.Fatalf("canonicalize(%v) = (%v, %t), want (%v, true)", value, got, ok, value)
		}
	}
	got, ok := canonicalize(invalid)
	if ok || got != fallback {
		t.Fatalf("canonicalize(%v) = (%v, %t), want (%v, false)", invalid, got, ok, fallback)
	}
}
