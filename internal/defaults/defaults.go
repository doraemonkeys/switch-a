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

// Connection pool defaults.
const (
	// MaxIdleConns is the maximum number of idle connections across all hosts.
	MaxIdleConns = 100
	// MaxIdleConnsPerHost is the maximum number of idle connections per host.
	// The default http.Transport value is only 2, which is too small for a proxy.
	MaxIdleConnsPerHost = 20
	// IdleConnTimeoutSec is the idle connection timeout in seconds.
	IdleConnTimeoutSec = 90
	// TLSHandshakeTimeoutSec is the TLS handshake timeout in seconds.
	TLSHandshakeTimeoutSec = 10
	// TCPKeepAliveSec is the TCP keepalive interval in seconds.
	TCPKeepAliveSec = 30
)

// Timeout defaults.
const (
	// UpstreamConnectTimeoutSec is the default upstream connection timeout in seconds.
	UpstreamConnectTimeoutSec = 10
	// FirstByteTimeoutSec is the default timeout for receiving the first response byte in seconds.
	// 0 means no timeout (wait indefinitely for the first byte).
	// This is separate from ReadTimeout to support AI model inference scenarios where
	// the model may take 60+ seconds to start responding, but once started, responds quickly.
	FirstByteTimeoutSec = 0
	// UpstreamReadTimeoutSec is the default upstream read timeout (idle timeout) in seconds.
	// 0 means no timeout. When set, connection is closed if no data received within this duration
	// during data transfer (after first byte is received).
	UpstreamReadTimeoutSec = 0
	// SSEIdleTimeoutSec is the default SSE stream idle timeout in seconds.
	// 0 means no idle timeout (trust upstream to close connection).
	// When set, the connection is closed if no data is received within this duration.
	// Recommended: 0 for trusted providers (OpenAI, Anthropic), 300 for user-defined providers.
	SSEIdleTimeoutSec = 0
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
