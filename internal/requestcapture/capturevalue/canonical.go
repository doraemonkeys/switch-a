package capturevalue

import "slices"

func CanonicalSelectionMode(value SelectionMode) (SelectionMode, bool) {
	switch value {
	case SelectionModeInitial:
		return SelectionModeInitial, true
	case SelectionModeReplacement:
		return SelectionModeReplacement, true
	case SelectionModeFailover:
		return SelectionModeFailover, true
	default:
		return "", false
	}
}

func CanonicalSelectionSource(value SelectionSource) (SelectionSource, bool) {
	switch value {
	case SelectionSourceStrategy:
		return SelectionSourceStrategy, true
	case SelectionSourceStickyContinuity:
		return SelectionSourceStickyContinuity, true
	case SelectionSourceActiveContinuity:
		return SelectionSourceActiveContinuity, true
	default:
		return "", false
	}
}

func CanonicalCredentialPhase(value CredentialPhase) (CredentialPhase, bool) {
	switch value {
	case CredentialPhaseInitial:
		return CredentialPhaseInitial, true
	case CredentialPhaseRefreshed:
		return CredentialPhaseRefreshed, true
	default:
		return "", false
	}
}

func CanonicalMessageDirection(value MessageDirection) (MessageDirection, bool) {
	switch value {
	case MessageDirectionClientToUpstream:
		return MessageDirectionClientToUpstream, true
	case MessageDirectionUpstreamToClient:
		return MessageDirectionUpstreamToClient, true
	default:
		return "", false
	}
}

func CanonicalMessageType(value MessageType) (MessageType, bool) {
	switch value {
	case MessageTypeText:
		return MessageTypeText, true
	case MessageTypeBinary:
		return MessageTypeBinary, true
	default:
		return "", false
	}
}

func CanonicalMessageSource(value MessageSource) (MessageSource, bool) {
	switch value {
	case MessageSourceLive:
		return MessageSourceLive, true
	case MessageSourceReplay:
		return MessageSourceReplay, true
	default:
		return "", false
	}
}

func CanonicalMessageDisposition(value MessageDisposition) (MessageDisposition, bool) {
	switch value {
	case MessageDispositionForwarded:
		return MessageDispositionForwarded, true
	case MessageDispositionSuppressed:
		return MessageDispositionSuppressed, true
	case MessageDispositionWriteFailed:
		return MessageDispositionWriteFailed, true
	case MessageDispositionIdentityRejected:
		return MessageDispositionIdentityRejected, true
	case MessageDispositionProtocolRejected:
		return MessageDispositionProtocolRejected, true
	case MessageDispositionStorageRejected:
		return MessageDispositionStorageRejected, true
	default:
		return "", false
	}
}

func CanonicalTerminationReason(value TerminationReason) (TerminationReason, bool) {
	switch value {
	case TerminationReasonEOF:
		return TerminationReasonEOF, true
	case TerminationReasonStatusFailoverDrain:
		return TerminationReasonStatusFailoverDrain, true
	case TerminationReasonCredentialRefreshDrain:
		return TerminationReasonCredentialRefreshDrain, true
	case TerminationReasonInternalErrorAbsorbed:
		return TerminationReasonInternalErrorAbsorbed, true
	case TerminationReasonInternalErrorCommitted:
		return TerminationReasonInternalErrorCommitted, true
	case TerminationReasonClientDisconnect:
		return TerminationReasonClientDisconnect, true
	case TerminationReasonTimeout:
		return TerminationReasonTimeout, true
	case TerminationReasonCanceled:
		return TerminationReasonCanceled, true
	case TerminationReasonPreparationError:
		return TerminationReasonPreparationError, true
	case TerminationReasonGatewayFinished:
		return TerminationReasonGatewayFinished, true
	case TerminationReasonCaptureFault:
		return TerminationReasonCaptureFault, true
	case TerminationReasonTransportError:
		return TerminationReasonTransportError, true
	case TerminationReasonReadError:
		return TerminationReasonReadError, true
	case TerminationReasonWriteError:
		return TerminationReasonWriteError, true
	case TerminationReasonWebSocketClose:
		return TerminationReasonWebSocketClose, true
	case TerminationReasonWebSocketRelayError:
		return TerminationReasonWebSocketRelayError, true
	default:
		return "", false
	}
}

func CanonicalSourceCompletion(value SourceCompletion) (SourceCompletion, bool) {
	switch value {
	case SourceCompletionComplete:
		return SourceCompletionComplete, true
	case SourceCompletionPartial:
		return SourceCompletionPartial, true
	default:
		return "", false
	}
}

func CanonicalFailureSite(value FailureSite) (FailureSite, bool) {
	switch value {
	case FailureSiteUnknown:
		return FailureSiteUnknown, true
	case FailureSiteGateway:
		return FailureSiteGateway, true
	case FailureSitePreparation:
		return FailureSitePreparation, true
	case FailureSiteTransport:
		return FailureSiteTransport, true
	case FailureSiteResponseStatus:
		return FailureSiteResponseStatus, true
	case FailureSiteResponseDrain:
		return FailureSiteResponseDrain, true
	case FailureSiteResponseRead:
		return FailureSiteResponseRead, true
	case FailureSiteResponseWrite:
		return FailureSiteResponseWrite, true
	case FailureSiteWebSocketHandshake:
		return FailureSiteWebSocketHandshake, true
	case FailureSiteWebSocketUpgrade:
		return FailureSiteWebSocketUpgrade, true
	case FailureSiteWebSocketReplay:
		return FailureSiteWebSocketReplay, true
	case FailureSiteWebSocketRelay:
		return FailureSiteWebSocketRelay, true
	case FailureSiteWebSocketMessage:
		return FailureSiteWebSocketMessage, true
	case FailureSiteWebSocketClose:
		return FailureSiteWebSocketClose, true
	default:
		return FailureSiteUnknown, false
	}
}

func CanonicalFailurePeer(value FailurePeer) (FailurePeer, bool) {
	switch value {
	case FailurePeerUnknown:
		return FailurePeerUnknown, true
	case FailurePeerGateway:
		return FailurePeerGateway, true
	case FailurePeerClient:
		return FailurePeerClient, true
	case FailurePeerUpstream:
		return FailurePeerUpstream, true
	case FailurePeerProvider:
		return FailurePeerProvider, true
	default:
		return FailurePeerUnknown, false
	}
}

func CanonicalFailureClass(value FailureClass) (FailureClass, bool) {
	switch value {
	case FailureClassUnknown:
		return FailureClassUnknown, true
	case FailureClassTimeout:
		return FailureClassTimeout, true
	case FailureClassCanceled:
		return FailureClassCanceled, true
	case FailureClassConfiguration:
		return FailureClassConfiguration, true
	case FailureClassTransport:
		return FailureClassTransport, true
	case FailureClassHTTPStatus:
		return FailureClassHTTPStatus, true
	case FailureClassRead:
		return FailureClassRead, true
	case FailureClassWrite:
		return FailureClassWrite, true
	case FailureClassProtocol:
		return FailureClassProtocol, true
	case FailureClassWebSocketClose:
		return FailureClassWebSocketClose, true
	case FailureClassUpstreamSemantic:
		return FailureClassUpstreamSemantic, true
	default:
		return FailureClassUnknown, false
	}
}

// knownFailureCodes is the canonical FailureCode registry. A single list keeps
// the catalog extensible without a two-line-per-code switch; every code in the
// enum block above must appear here, and new codes cost one entry, not three.
var knownFailureCodes = []FailureCode{
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
	FailureCodeClientCancel,
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
}

func CanonicalFailureCode(value FailureCode) (FailureCode, bool) {
	if slices.Contains(knownFailureCodes, value) {
		return value, true
	}
	return FailureCodeUnknown, false
}
