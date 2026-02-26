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
	AuthMode          = "auto"
	UserHeader        = "X-User-ID"
	TrustProxyHeaders = true
)

// Connection pool defaults.
const (
	MaxIdleConns = 100
	// MaxIdleConnsPerHost: http.Transport default is only 2, too small for a proxy.
	MaxIdleConnsPerHost = 20
	IdleConnTimeout     = 90 * time.Second
	TLSHandshakeTimeout = 10 * time.Second
	TCPKeepAlive        = 30 * time.Second
)

// Timeout defaults.
const (
	UpstreamConnectTimeout = 20 * time.Second
	// FirstByteTimeout: 0 means wait indefinitely for the first byte.
	// Supports AI model inference scenarios where the model may take 60+ seconds
	// to start responding, but once started, responds quickly.
	FirstByteTimeout = 0 * time.Second
	// UpstreamReadTimeout: 0 means no timeout. When set, connection closes
	// if no data received within this duration during data transfer.
	UpstreamReadTimeout = 0 * time.Second
	// SSEIdleTimeout: 0 trusts upstream to close connection.
	// Recommended: 0 for trusted providers (OpenAI, Anthropic), 300 for user-defined providers.
	SSEIdleTimeout = 0 * time.Second
)

// Sticky session defaults.
const (
	// StickyMode is a string literal to avoid importing model package here.
	// model already imports defaults, so importing model would create a cycle.
	StickyMode       = "model"
	StickyTTLSeconds = 300
	StickyTTL        = StickyTTLSeconds * time.Second
)

// Circuit breaker defaults.
const (
	CircuitFailure  = 3
	CircuitWindow   = 60 * time.Second
	CircuitDisabled = 300 * time.Second
)

// Request handling defaults.
const (
	MaxBodySizeMB int64 = 128
	// GlobalMaxAttempts is the maximum number of upstream attempts for a single request.
	// 0 means unlimited (will iterate through all providers subject to per-provider retries).
	GlobalMaxAttempts int = 0
	LogRetentionDays  int = 7
)

// Logger defaults.
const (
	LogPath        = "./logs/switch-a.log"
	LogMaxSizeMB   = 100
	LogMaxKeepDays = 7
	LogLevel       = "info"
)

// Strategy defaults.
const (
	InterGroupStrategy = "priority"
	ProviderWeight     = 1
	// ProviderMaxRetries is the default retry count for a provider.
	// 0 means try once, no retry.
	ProviderMaxRetries = 0
)

// Backoff defaults for same-provider retries.
const (
	BackoffInitialDelay = 100 * time.Millisecond
	BackoffMaxDelay     = 5 * time.Second
	BackoffMultiplier   = 2.0
)

// HTTP status codes for failover logic.
// These semantic aliases make failover logic more readable.
const (
	// StatusSuccessMin is the lower bound (inclusive) for successful HTTP responses.
	StatusSuccessMin = http.StatusOK
	// StatusSuccessMax is the upper bound (exclusive) for successful HTTP responses (2xx range).
	StatusSuccessMax  = http.StatusMultipleChoices
	StatusServerError = http.StatusInternalServerError
	// StatusPaymentRequired: triggers failover since another provider may have available quota.
	StatusPaymentRequired = http.StatusPaymentRequired
	StatusTooManyRequests = http.StatusTooManyRequests
	// StatusUnauthorized/StatusForbidden: trigger failover as they indicate provider misconfiguration.
	StatusUnauthorized = http.StatusUnauthorized
	StatusForbidden    = http.StatusForbidden
	// StatusClientError marks the boundary between success (2xx/3xx) and client error (4xx) responses.
	StatusClientError = http.StatusBadRequest
)
