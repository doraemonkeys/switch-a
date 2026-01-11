// Package store provides data storage implementations.
package store

import (
	"strconv"

	"switch-a/internal/defaults"
)

// boolToString converts a bool to "true" or "false" string.
func boolToString(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// Default runtime configuration values as strings for database storage.
// These are derived from the centralized defaults package.
var (
	// DefaultAuthMode is the default authentication mode.
	DefaultAuthMode = defaults.AuthMode
	// DefaultUserHeader is the default header for user identification.
	DefaultUserHeader = defaults.UserHeader
	// DefaultTrustProxyHeaders indicates whether proxy headers are trusted by default.
	DefaultTrustProxyHeaders = boolToString(defaults.TrustProxyHeaders)
	// DefaultUpstreamConnectTimeout is the default upstream connection timeout in seconds.
	DefaultUpstreamConnectTimeout = strconv.Itoa(defaults.UpstreamConnectTimeoutSec)
	// DefaultFirstByteTimeout is the default timeout for receiving the first response byte in seconds.
	DefaultFirstByteTimeout = strconv.Itoa(defaults.FirstByteTimeoutSec)
	// DefaultUpstreamReadTimeout is the default upstream read timeout in seconds (0 = no timeout).
	DefaultUpstreamReadTimeout = strconv.Itoa(defaults.UpstreamReadTimeoutSec)
	// DefaultSSEIdleTimeout is the default SSE idle timeout in seconds (0 = no timeout).
	DefaultSSEIdleTimeout = strconv.Itoa(defaults.SSEIdleTimeoutSec)
	// DefaultStickyEnabled indicates whether sticky sessions are enabled by default.
	DefaultStickyEnabled = boolToString(defaults.StickyEnabled)
	// DefaultStickyTTL is the default sticky session TTL in seconds.
	DefaultStickyTTL = strconv.Itoa(defaults.StickyTTLSeconds)
	// DefaultCircuitFailure is the default number of failures before circuit opens.
	DefaultCircuitFailure = strconv.Itoa(defaults.CircuitFailure)
	// DefaultCircuitWindow is the default circuit breaker window in seconds.
	DefaultCircuitWindow = strconv.Itoa(defaults.CircuitWindowSec)
	// DefaultCircuitDisable is the default circuit breaker disable duration in seconds.
	DefaultCircuitDisable = strconv.Itoa(defaults.CircuitDisableSec)
	// DefaultMaxBodySize is the default maximum request body size in MB.
	DefaultMaxBodySize = strconv.FormatInt(defaults.MaxBodySizeMB, 10)
	// DefaultMaxRetries is the default maximum number of retries.
	DefaultMaxRetries = strconv.Itoa(defaults.MaxRetries)
	// DefaultLogRetentionDays is the default log retention period in days.
	DefaultLogRetentionDays = strconv.Itoa(defaults.LogRetentionDays)
	// DefaultInterGroupStrategy is the default inter-group routing strategy.
	DefaultInterGroupStrategy = defaults.InterGroupStrategy
)
