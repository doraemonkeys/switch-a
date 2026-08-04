package capturefailure

import (
	"net/http"

	"github.com/doraemonkeys/switch-a/internal/requestcapture"
)

func WebSocketPreparation(
	contextError error,
	err error,
	code requestcapture.FailureCode,
) (requestcapture.TerminationReason, requestcapture.FailureObservation) {
	fact := fromErrorWithContext(
		contextError,
		requestcapture.FailureSitePreparation,
		requestcapture.FailurePeerProvider,
		requestcapture.FailureClassConfiguration,
		code,
		err,
	)
	return terminationForClass(
		contextError,
		fact.Class,
		requestcapture.TerminationReasonPreparationError,
	), Observation(fact, requestcapture.FailureFact{})
}

func WebSocketHandshake(
	statusCode int,
	dialError error,
	failureBodyReadError error,
) requestcapture.FailureObservation {
	var primary requestcapture.FailureFact
	switch {
	case statusCode > 0 && statusCode != http.StatusSwitchingProtocols:
		primary = Fact(
			requestcapture.FailureSiteWebSocketHandshake,
			requestcapture.FailurePeerUpstream,
			requestcapture.FailureClassHTTPStatus,
			requestcapture.FailureCodeHandshakeRejected,
		)
		primary.HTTPStatusCode = statusCode
	case statusCode == http.StatusSwitchingProtocols:
		primary = FromError(
			requestcapture.FailureSiteWebSocketHandshake,
			requestcapture.FailurePeerUpstream,
			requestcapture.FailureClassProtocol,
			requestcapture.FailureCodeWebSocketUpgrade,
			dialError,
		)
	default:
		primary = FromError(
			requestcapture.FailureSiteWebSocketHandshake,
			requestcapture.FailurePeerUpstream,
			requestcapture.FailureClassTransport,
			requestcapture.FailureCodeWebSocketDial,
			dialError,
		)
	}
	secondary := FromError(
		requestcapture.FailureSiteWebSocketHandshake,
		requestcapture.FailurePeerUpstream,
		requestcapture.FailureClassRead,
		requestcapture.FailureCodeFailureBodyRead,
		failureBodyReadError,
	)
	return Observation(primary, secondary)
}

func WebSocketClientAccept(
	contextError error,
	err error,
) (requestcapture.TerminationReason, requestcapture.FailureObservation) {
	fact := fromErrorWithContext(
		contextError,
		requestcapture.FailureSiteWebSocketUpgrade,
		requestcapture.FailurePeerClient,
		requestcapture.FailureClassProtocol,
		requestcapture.FailureCodeClientAccept,
		err,
	)
	return terminationForClass(
		contextError,
		fact.Class,
		requestcapture.TerminationReasonClientDisconnect,
	), Observation(fact, requestcapture.FailureFact{})
}

func WebSocketReplayWrite(
	contextError error,
	err error,
) (requestcapture.TerminationReason, requestcapture.FailureObservation) {
	fact := fromErrorWithContext(
		contextError,
		requestcapture.FailureSiteWebSocketReplay,
		requestcapture.FailurePeerUpstream,
		requestcapture.FailureClassWrite,
		requestcapture.FailureCodeReplayWrite,
		err,
	)
	return terminationForClass(
		contextError,
		fact.Class,
		requestcapture.TerminationReasonWriteError,
	), Observation(fact, requestcapture.FailureFact{})
}

func WebSocketMessageWrite(
	peer requestcapture.FailurePeer,
	err error,
) requestcapture.FailureObservation {
	return Observation(FromError(
		requestcapture.FailureSiteWebSocketMessage,
		peer,
		requestcapture.FailureClassWrite,
		requestcapture.FailureCodeMessageWrite,
		err,
	), requestcapture.FailureFact{})
}
