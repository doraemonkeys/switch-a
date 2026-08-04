package capturefailure

import "github.com/doraemonkeys/switch-a/internal/requestcapture"

type HTTPForwardOrigin uint8

const (
	HTTPForwardOriginUpstreamRead HTTPForwardOrigin = iota
	HTTPForwardOriginReadTimeout
	HTTPForwardOriginClientWrite
)

func HTTPFetch(
	contextError error,
	err error,
) (requestcapture.TerminationReason, requestcapture.FailureObservation) {
	if err == nil {
		return requestcapture.TerminationReasonEOF, requestcapture.FailureObservation{}
	}
	fact := fromErrorWithContext(
		contextError,
		requestcapture.FailureSiteTransport,
		requestcapture.FailurePeerUpstream,
		requestcapture.FailureClassTransport,
		requestcapture.FailureCodeRoundTrip,
		err,
	)
	return terminationForClass(
		contextError,
		fact.Class,
		requestcapture.TerminationReasonTransportError,
	), Observation(fact, requestcapture.FailureFact{})
}

func HTTPPreparation(
	contextError error,
	err error,
	code requestcapture.FailureCode,
) (requestcapture.TerminationReason, requestcapture.FailureObservation) {
	peer := requestcapture.FailurePeerProvider
	if code == requestcapture.FailureCodeRequestBuild {
		peer = requestcapture.FailurePeerGateway
	}
	fact := fromErrorWithContext(
		contextError,
		requestcapture.FailureSitePreparation,
		peer,
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

func HTTPForward(
	contextError error,
	err error,
	origin HTTPForwardOrigin,
) (requestcapture.TerminationReason, requestcapture.FailureObservation) {
	if err == nil {
		return requestcapture.TerminationReasonEOF, requestcapture.FailureObservation{}
	}

	site := requestcapture.FailureSiteResponseRead
	peer := requestcapture.FailurePeerUpstream
	class := requestcapture.FailureClassRead
	code := requestcapture.FailureCodeUpstreamRead
	fallback := requestcapture.TerminationReasonReadError
	switch origin {
	case HTTPForwardOriginReadTimeout:
		class = requestcapture.FailureClassTimeout
		fallback = requestcapture.TerminationReasonTimeout
	case HTTPForwardOriginClientWrite:
		site = requestcapture.FailureSiteResponseWrite
		peer = requestcapture.FailurePeerClient
		class = requestcapture.FailureClassWrite
		code = requestcapture.FailureCodeClientWrite
		fallback = requestcapture.TerminationReasonWriteError
	}

	fact := fromErrorWithContext(contextError, site, peer, class, code, err)
	return terminationForClass(contextError, fact.Class, fallback),
		Observation(fact, requestcapture.FailureFact{})
}

func fromErrorWithContext(
	contextError error,
	site requestcapture.FailureSite,
	peer requestcapture.FailurePeer,
	fallbackClass requestcapture.FailureClass,
	code requestcapture.FailureCode,
	err error,
) requestcapture.FailureFact {
	if contextClass, ok := ContextClass(contextError); ok {
		fallbackClass = contextClass
	}
	return FromError(site, peer, fallbackClass, code, err)
}

func terminationForClass(
	contextError error,
	class requestcapture.FailureClass,
	fallback requestcapture.TerminationReason,
) requestcapture.TerminationReason {
	switch class {
	case requestcapture.FailureClassTimeout:
		return requestcapture.TerminationReasonTimeout
	case requestcapture.FailureClassCanceled:
		if contextError != nil {
			return requestcapture.TerminationReasonClientDisconnect
		}
		return requestcapture.TerminationReasonCanceled
	default:
		return fallback
	}
}
