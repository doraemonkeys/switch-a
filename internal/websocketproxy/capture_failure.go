package websocketproxy

import (
	"github.com/doraemonkeys/switch-a/internal/requestcapture"
	"github.com/doraemonkeys/switch-a/internal/requestcapture/capturebridge"
	"github.com/doraemonkeys/switch-a/internal/requestcapture/capturefailure"

	"github.com/coder/websocket"
)

// The WebSocket module owns workflow-specific mapping names while the bounded,
// hostile-error-safe fact extraction remains in the shared capture boundary.
func IsEOF(err error) bool {
	return capturefailure.IsEOF(err)
}

func IsUnexpectedEOF(err error) bool {
	return capturefailure.IsUnexpectedEOF(err)
}

func Fact(
	site requestcapture.FailureSite,
	peer requestcapture.FailurePeer,
	class requestcapture.FailureClass,
	code requestcapture.FailureCode,
) requestcapture.FailureFact {
	return capturefailure.Fact(site, peer, class, code)
}

func FromError(
	site requestcapture.FailureSite,
	peer requestcapture.FailurePeer,
	class requestcapture.FailureClass,
	code requestcapture.FailureCode,
	err error,
) requestcapture.FailureFact {
	return capturefailure.FromError(site, peer, class, code, err)
}

func ContextClass(err error) (requestcapture.FailureClass, bool) {
	return capturefailure.ContextClass(err)
}

func HTTPStatus(
	site requestcapture.FailureSite,
	peer requestcapture.FailurePeer,
	statusCode int,
) requestcapture.FailureFact {
	return capturefailure.HTTPStatus(site, peer, statusCode)
}

func WithHTTPStatus(fact requestcapture.FailureFact, statusCode int) requestcapture.FailureFact {
	return capturefailure.WithHTTPStatus(fact, statusCode)
}

func WebSocketClose(
	site requestcapture.FailureSite,
	peer requestcapture.FailurePeer,
	closeError *websocket.CloseError,
) (requestcapture.FailureFact, bool) {
	return capturefailure.WebSocketClose(site, peer, closeError)
}

func ProviderSemantic(
	site requestcapture.FailureSite,
	peer requestcapture.FailurePeer,
	statusCode int,
	providerErrorType string,
	providerErrorCode string,
	message string,
) (requestcapture.FailureFact, bool) {
	return capturefailure.ProviderSemantic(
		site,
		peer,
		statusCode,
		providerErrorType,
		providerErrorCode,
		message,
	)
}

func Observation(
	primary requestcapture.FailureFact,
	secondary requestcapture.FailureFact,
) requestcapture.FailureObservation {
	return capturefailure.Observation(primary, secondary)
}

func webSocketPreparation(
	contextError error,
	err error,
	code requestcapture.FailureCode,
) (requestcapture.TerminationReason, requestcapture.FailureObservation) {
	return capturefailure.WebSocketPreparation(contextError, err, code)
}

func webSocketHandshake(
	statusCode int,
	dialError error,
	failureBodyReadError error,
) requestcapture.FailureObservation {
	return capturefailure.WebSocketHandshake(statusCode, dialError, failureBodyReadError)
}

func webSocketClientAccept(
	contextError error,
	err error,
) (requestcapture.TerminationReason, requestcapture.FailureObservation) {
	return capturefailure.WebSocketClientAccept(contextError, err)
}

func webSocketReplayWrite(
	contextError error,
	err error,
) (requestcapture.TerminationReason, requestcapture.FailureObservation) {
	return capturefailure.WebSocketReplayWrite(contextError, err)
}

func webSocketMessageWrite(
	peer requestcapture.FailurePeer,
	err error,
) requestcapture.FailureObservation {
	return capturefailure.WebSocketMessageWrite(peer, err)
}

type captureMode = capturebridge.Mode

const (
	captureModeNone       = capturebridge.ModeNone
	captureModeTransition = capturebridge.ModeTransition
	captureModePayload    = capturebridge.ModePayload
)

func captureModeForRecorder(recorder requestcapture.Recorder) captureMode {
	return capturebridge.ModeForRecorder(recorder)
}

func sourceEndpointComplete(observedBytes, expectedBytes int64, reachedEOF, readFailed bool) bool {
	return capturebridge.SourceEndpointComplete(observedBytes, expectedBytes, reachedEOF, readFailed)
}

func captureCredentialMaterial(injectedAPIKey string) (
	requestcapture.SensitiveHeaderEvidence,
	requestcapture.CredentialEvidence,
) {
	return capturebridge.CredentialMaterial(injectedAPIKey)
}

func emptyCaptureCredentialEvidence() requestcapture.CredentialEvidence {
	_, evidence := captureCredentialMaterial("")
	return evidence
}
