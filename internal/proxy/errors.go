package proxy

import (
	"context"
	"errors"
	"fmt"
)

// SwitchReason constants define provider-transition outcomes persisted on
// RequestAttempt. They share this boundary with proxy error codes because both
// are the stable failure vocabulary emitted by request orchestration.
const (
	SwitchReasonMaxRetriesExhausted     = "max_retries_exhausted"
	SwitchReasonCircuitBreakerTriggered = "circuit_breaker_triggered"
	SwitchReasonUsageLimitReached       = "usage_limit_reached"
)

func formatPermanentErrorReason(statusCode int) string {
	return fmt.Sprintf("permanent_error_%d", statusCode)
}

// Proxy error codes.
const (
	ErrCodeUnknownAPIType      = "UNKNOWN_API_TYPE"
	ErrCodeProviderUnavailable = "PROVIDER_UNAVAILABLE"
	ErrCodeProviderExhausted   = "PROVIDER_EXHAUSTED"
	ErrCodeBodyTooLarge        = "BODY_TOO_LARGE"
	ErrCodeWebSocketUpgrade    = "WEBSOCKET_UPGRADE_FAILED"
	ErrCodeWebSocketReconnect  = "WEBSOCKET_RECONNECT_REQUIRED"
	ErrCodeInternalError       = "INTERNAL_ERROR"
)

// Error definitions.
var (
	ErrBodyTooLarge = errors.New("request body too large")
)

// UpstreamReadError wraps errors that occur when reading from the upstream server.
// This distinguishes upstream failures (which should trigger markFailure) from
// client write errors (which should not affect provider health).
type UpstreamReadError struct {
	Err error
}

func (e *UpstreamReadError) Error() string {
	return fmt.Sprintf("upstream read error: %v", e.Err)
}

func (e *UpstreamReadError) Unwrap() error {
	return e.Err
}

// NewUpstreamReadError wraps an error as an upstream read error.
func NewUpstreamReadError(err error) error {
	if err == nil {
		return nil
	}
	return &UpstreamReadError{Err: err}
}

// IsUpstreamReadError checks if the error is an upstream read error.
func IsUpstreamReadError(err error) bool {
	var upstreamErr *UpstreamReadError
	return errors.As(err, &upstreamErr)
}

type clientTermination uint8

const (
	clientTerminationNone clientTermination = iota
	clientTerminationDisconnect
	clientTerminationTimeout
)

func classifyClientTermination(ctx context.Context) clientTermination {
	if ctx == nil {
		return clientTerminationNone
	}
	switch ctx.Err() {
	case context.Canceled:
		return clientTerminationDisconnect
	case context.DeadlineExceeded:
		return clientTerminationTimeout
	default:
		return clientTerminationNone
	}
}

func (termination clientTermination) observed() bool {
	return termination != clientTerminationNone
}

func mergeClientTermination(current, next clientTermination) clientTermination {
	if current.observed() {
		return current
	}
	return next
}
