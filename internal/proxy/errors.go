package proxy

import "errors"

// Proxy error codes.
const (
	ErrCodeUnknownAPIType      = "UNKNOWN_API_TYPE"
	ErrCodeProviderUnavailable = "PROVIDER_UNAVAILABLE"
	ErrCodeProviderExhausted   = "PROVIDER_EXHAUSTED"
	ErrCodeBodyTooLarge        = "BODY_TOO_LARGE"
	ErrCodeInternalError       = "INTERNAL_ERROR"
)

// Error definitions.
var (
	ErrBodyTooLarge = errors.New("request body too large")
)
