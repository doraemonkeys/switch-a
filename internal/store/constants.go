// Package store provides data storage implementations.
package store

import (
	"strconv"
	"time"

	"switch-a/internal/defaults"
)

// boolToString converts a bool to "true" or "false" string.
func boolToString(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// durationToSecondsString converts a time.Duration to its seconds value as a string.
func durationToSecondsString(d time.Duration) string {
	return strconv.Itoa(int(d / time.Second))
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
	DefaultUpstreamConnectTimeout = durationToSecondsString(defaults.UpstreamConnectTimeout)
	// DefaultFirstByteTimeout is the default timeout for receiving the first response byte in seconds.
	DefaultFirstByteTimeout = durationToSecondsString(defaults.FirstByteTimeout)
	// DefaultUpstreamReadTimeout is the default upstream read timeout in seconds (0 = no timeout).
	DefaultUpstreamReadTimeout = durationToSecondsString(defaults.UpstreamReadTimeout)
	// DefaultSSEIdleTimeout is the default SSE idle timeout in seconds (0 = no timeout).
	DefaultSSEIdleTimeout = durationToSecondsString(defaults.SSEIdleTimeout)
	// DefaultStickyMode is the default sticky session mode.
	DefaultStickyMode = defaults.StickyMode
	// DefaultStickyTTL is the default sticky session TTL in seconds.
	DefaultStickyTTL = strconv.Itoa(defaults.StickyTTLSeconds)
	// DefaultCircuitFailure is the default number of failures before circuit opens.
	DefaultCircuitFailure = strconv.Itoa(defaults.CircuitFailure)
	// DefaultCircuitWindow is the default circuit breaker window in seconds.
	DefaultCircuitWindow = durationToSecondsString(defaults.CircuitWindow)
	// DefaultCircuitDisable is the default circuit breaker disable duration in seconds.
	DefaultCircuitDisable = durationToSecondsString(defaults.CircuitDisabled)
	// DefaultMaxBodySize is the default maximum request body size in MB.
	DefaultMaxBodySize = strconv.FormatInt(defaults.MaxBodySizeMB, 10)
	// DefaultGlobalMaxAttempts is the default maximum number of upstream attempts per request.
	// 0 means unlimited.
	DefaultGlobalMaxAttempts = strconv.Itoa(defaults.GlobalMaxAttempts)
	// DefaultLogRetentionDays is the default log retention period in days.
	DefaultLogRetentionDays = strconv.Itoa(defaults.LogRetentionDays)
	// DefaultInterGroupStrategy is the default inter-group routing strategy.
	DefaultInterGroupStrategy = defaults.InterGroupStrategy
)
