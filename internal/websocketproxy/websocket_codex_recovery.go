package websocketproxy

import (
	codexrecovery "github.com/doraemonkeys/switch-a/internal/codex/recovery"
	codexws "github.com/doraemonkeys/switch-a/internal/codex/websocket"
)

// WebSocket boundary failures carry transport-local classes for observability.
// The shared classifier still gets first authority so precise continuity and
// Cookie causes cannot be collapsed into these coarse adapter defaults.
func codexWebSocketRecoveryDecision(
	err error,
	phase codexrecovery.CarrierPhase,
) codexrecovery.Decision {
	fallback := codexrecovery.ConditionInternalFailure
	switch codexws.Classify(err) {
	case codexws.FailureIdentity:
		fallback = codexrecovery.ConditionStateConflict
	case codexws.FailureProtocol:
		fallback = codexrecovery.ConditionProtocolInvalid
	case codexws.FailureStorage:
		fallback = codexrecovery.ConditionStateStoreUnavailable
	}
	return codexrecovery.ClassifyWithFallback(err, phase, fallback)
}
