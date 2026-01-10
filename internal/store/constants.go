// Package store provides data storage implementations.
package store

// Default runtime configuration values.
const (
	// DefaultAuthMode is the default authentication mode.
	DefaultAuthMode = "auto"
	// DefaultUserHeader is the default header for user identification.
	DefaultUserHeader = "X-User-ID"
	// DefaultTrustProxyHeaders indicates whether proxy headers are trusted by default.
	DefaultTrustProxyHeaders = "true"
	// DefaultUpstreamConnectTimeout is the default upstream connection timeout in seconds.
	DefaultUpstreamConnectTimeout = "10"
	// DefaultUpstreamReadTimeout is the default upstream read timeout in seconds (0 = no timeout).
	DefaultUpstreamReadTimeout = "0"
	// DefaultStickyEnabled indicates whether sticky sessions are enabled by default.
	DefaultStickyEnabled = "true"
	// DefaultStickyTTL is the default sticky session TTL in seconds.
	DefaultStickyTTL = "300"
	// DefaultCircuitFailure is the default number of failures before circuit opens.
	DefaultCircuitFailure = "3"
	// DefaultCircuitWindow is the default circuit breaker window in seconds.
	DefaultCircuitWindow = "60"
	// DefaultCircuitDisable is the default circuit breaker disable duration in seconds.
	DefaultCircuitDisable = "300"
	// DefaultMaxBodySize is the default maximum request body size in MB.
	DefaultMaxBodySize = "10"
	// DefaultMaxRetries is the default maximum number of retries.
	DefaultMaxRetries = "3"
	// DefaultLogRetentionDays is the default log retention period in days.
	DefaultLogRetentionDays = "7"
	// DefaultInterGroupStrategy is the default inter-group routing strategy.
	DefaultInterGroupStrategy = "priority"
)
