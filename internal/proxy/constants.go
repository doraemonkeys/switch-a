package proxy

import "fmt"

// SwitchReason constants define the reasons for switching to a different provider.
// These values are stored in RequestAttempt.SwitchReason to help users understand
// why a provider switch occurred during request processing.
const (
	// SwitchReasonMaxRetriesExhausted indicates the provider's retry limit was reached.
	SwitchReasonMaxRetriesExhausted = "max_retries_exhausted"

	// SwitchReasonCircuitBreakerTriggered indicates the circuit breaker marked the provider unavailable.
	SwitchReasonCircuitBreakerTriggered = "circuit_breaker_triggered"

	// SwitchReasonUsageLimitReached indicates the provider exhausted an upstream
	// usage window and should be skipped until the advertised reset time.
	SwitchReasonUsageLimitReached = "usage_limit_reached"
)

// formatPermanentErrorReason formats a switch reason for permanent HTTP errors (401, 402, 403).
// These status codes indicate issues that won't be resolved by retrying the same provider.
func formatPermanentErrorReason(statusCode int) string {
	return fmt.Sprintf("permanent_error_%d", statusCode)
}
