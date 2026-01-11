// Package defaults provides centralized default configuration values.
// All components should derive their default values from this package
// to ensure consistency and avoid duplication (DRY principle).
package defaults

import (
	"net/http"
	"time"
)

// Authentication and user identification defaults.
const (
	// AuthMode is the default authentication mode for providers.
	AuthMode = "auto"
	// UserHeader is the default header for user identification.
	UserHeader = "X-User-ID"
	// TrustProxyHeaders indicates whether proxy headers are trusted by default.
	TrustProxyHeaders = true
)

// Timeout defaults.
const (
	// UpstreamConnectTimeoutSec is the default upstream connection timeout in seconds.
	UpstreamConnectTimeoutSec = 10
	// UpstreamReadTimeoutSec is the default upstream read timeout in seconds (0 = no timeout).
	UpstreamReadTimeoutSec = 0
)

// Sticky session defaults.
const (
	// StickyEnabled indicates whether sticky sessions are enabled by default.
	StickyEnabled = true
	// StickyTTLSeconds is the default sticky session TTL in seconds.
	StickyTTLSeconds = 300
	// StickyTTL is the default sticky session TTL as a time.Duration.
	StickyTTL = StickyTTLSeconds * time.Second
)

// Circuit breaker defaults.
const (
	// CircuitFailure is the default number of failures before circuit opens.
	CircuitFailure = 3
	// CircuitWindowSec is the default circuit breaker window in seconds.
	CircuitWindowSec = 60
	// CircuitDisableSec is the default circuit breaker disable duration in seconds.
	CircuitDisableSec = 300
)

// Request handling defaults.
const (
	// MaxBodySizeMB is the default maximum request body size in MB.
	MaxBodySizeMB int64 = 10
	// MaxRetries is the default maximum number of retries.
	MaxRetries = 3
	// LogRetentionDays is the default log retention period in days.
	LogRetentionDays = 7
)

// Strategy defaults.
const (
	// InterGroupStrategy is the default inter-group routing strategy.
	InterGroupStrategy = "priority"
	// ProviderWeight is the default weight for providers.
	ProviderWeight = 1
)

// HTTP status codes for retry logic.
// These are semantic aliases to make retry logic more readable.
const (
	// StatusServerError is the threshold for server errors that may be retried.
	StatusServerError = http.StatusInternalServerError
	// StatusTooManyRequests indicates rate limiting that may be retried.
	StatusTooManyRequests = http.StatusTooManyRequests
)
