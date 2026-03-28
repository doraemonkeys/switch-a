package model

// WebSocketProbeOutcome belongs in the shared model layer because persistence,
// admin diagnostics, and proxy runtime all need one stable vocabulary for
// pre-selection probe outcomes.
type WebSocketProbeOutcome string

const (
	WebSocketProbeOutcomeUnknown                     WebSocketProbeOutcome = "unknown"
	WebSocketProbeOutcomeBypassed                    WebSocketProbeOutcome = "bypassed"
	WebSocketProbeOutcomeDemandResolutionFailed      WebSocketProbeOutcome = "demand_resolution_failed"
	WebSocketProbeOutcomeUnsupported                 WebSocketProbeOutcome = "unsupported"
	WebSocketProbeOutcomeObservedUsableModel         WebSocketProbeOutcome = "observed_usable_model"
	WebSocketProbeOutcomeCompletedWithoutUsableModel WebSocketProbeOutcome = "completed_without_usable_model"
	WebSocketProbeOutcomeTransportFailed             WebSocketProbeOutcome = "transport_failed"
)

// IsValidWebSocketProbeOutcome keeps admin/query parsing aligned with the
// persisted enum so invalid diagnostics filters fail fast.
func IsValidWebSocketProbeOutcome(outcome WebSocketProbeOutcome) bool {
	switch outcome {
	case WebSocketProbeOutcomeUnknown,
		WebSocketProbeOutcomeBypassed,
		WebSocketProbeOutcomeDemandResolutionFailed,
		WebSocketProbeOutcomeUnsupported,
		WebSocketProbeOutcomeObservedUsableModel,
		WebSocketProbeOutcomeCompletedWithoutUsableModel,
		WebSocketProbeOutcomeTransportFailed:
		return true
	default:
		return false
	}
}
