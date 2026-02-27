package proxy

import (
	"context"
	"errors"
	"fmt"
)

// Proxy error codes.
const (
	ErrCodeUnknownAPIType      = "UNKNOWN_API_TYPE"
	ErrCodeProviderUnavailable = "PROVIDER_UNAVAILABLE"
	ErrCodeProviderExhausted   = "PROVIDER_EXHAUSTED"
	ErrCodeBodyTooLarge        = "BODY_TOO_LARGE"
	ErrCodeWebSocketUpgrade    = "WEBSOCKET_UPGRADE_FAILED"
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

// isClientCancellation checks if the error indicates a client-side cancellation.
// These errors should NOT trigger circuit breaker as they don't indicate provider issues:
//   - context.Canceled: client disconnected or request was cancelled
//   - context.DeadlineExceeded: client-side timeout was reached
func isClientCancellation(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
