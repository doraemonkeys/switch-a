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
	MaxIdleConnsPerHost    = 20
	IdleConnTimeoutSec     = 90
	TLSHandshakeTimeoutSec = 10
	TCPKeepAliveSec        = 30
)

// Timeout defaults.
const (
	UpstreamConnectTimeoutSec = 20
	// FirstByteTimeoutSec: 0 means wait indefinitely for the first byte.
	// Supports AI model inference scenarios where the model may take 60+ seconds
	// to start responding, but once started, responds quickly.
	FirstByteTimeoutSec = 0
	// UpstreamReadTimeoutSec: 0 means no timeout. When set, connection closes
	// if no data received within this duration during data transfer.
	UpstreamReadTimeoutSec = 0
	// SSEIdleTimeoutSec: 0 trusts upstream to close connection.
	// Recommended: 0 for trusted providers (OpenAI, Anthropic), 300 for user-defined providers.
	SSEIdleTimeoutSec = 0
)

// Sticky session defaults.
const (
	StickyEnabled    = true
	StickyTTLSeconds = 300
	StickyTTL        = StickyTTLSeconds * time.Second
)

// Circuit breaker defaults.
const (
	CircuitFailure    = 3
	CircuitWindowSec  = 60
	CircuitDisableSec = 300
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

// HTTP status codes for failover logic.
// These semantic aliases make failover logic more readable.
const (
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
