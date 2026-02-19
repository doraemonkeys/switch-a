package proxy

import (
	"context"
	"strconv"
	"strings"
	"time"

	"switch-a/internal/defaults"
	"switch-a/internal/model"
	"switch-a/internal/selector"

	"go.uber.org/zap"
)

// Default configuration values - derived from centralized defaults package.
const (
	DefaultMaxBodySizeMB     = defaults.MaxBodySizeMB
	DefaultGlobalMaxAttempts = defaults.GlobalMaxAttempts
	DefaultUserHeader        = defaults.UserHeader
	DefaultStickyMode        = model.StickyModeModel // Default stickiness includes model dimension
	DefaultGlobalAuthMode    = defaults.AuthMode     // Default auth mode for provider authentication
)

// Default timeout values - derived from centralized defaults package.
var (
	DefaultConnectTimeout   = defaults.UpstreamConnectTimeout
	DefaultFirstByteTimeout = defaults.FirstByteTimeout
	DefaultSSEIdleTimeout   = defaults.SSEIdleTimeout
)

// StatusCodeNoResponse indicates no upstream response was received.
// Used when logging requests where no provider was available to handle the request.
const StatusCodeNoResponse = 0

// Config keys for runtime configuration stored in the database.
const (
	ConfigKeyTrustProxyHeaders      = "trust_proxy_headers"
	ConfigKeyUserHeader             = "user_header"
	ConfigKeyMaxBodySize            = "max_body_size"
	ConfigKeyAuthMode               = "auth_mode"
	ConfigKeyGlobalMaxAttempts      = "global_max_attempts"
	ConfigKeyUpstreamConnectTimeout = "upstream_connect_timeout"
	ConfigKeyFirstByteTimeout       = "first_byte_timeout"
	ConfigKeyUpstreamReadTimeout    = "upstream_read_timeout"
	ConfigKeySSEIdleTimeout         = "sse_idle_timeout"
	ConfigKeyStickyMode             = "sticky_mode"
	ConfigKeyStickyTTL              = "sticky_ttl"
	ConfigKeyInterGroupStrategy     = selector.ConfigKeyInterGroupStrategy
)

// defaultStickyTTLSeconds uses canonical value from selector package for consistency.
const defaultStickyTTLSeconds = selector.DefaultStickyTTLSeconds

// runtimeConfig holds configuration loaded from the store per-request (immutable once created).
type runtimeConfig struct {
	trustProxy        bool
	userHeader        string
	maxBodySizeMB     int64
	globalAuthMode    string
	globalMaxAttempts int
	connectTimeout    time.Duration
	firstByteTimeout  time.Duration
	readTimeout       time.Duration
	sseIdleTimeout    time.Duration
	stickyMode        model.StickyMode
	stickyTTL         time.Duration
}

// loadConfig loads runtime configuration from the store.
// Returns an immutable runtimeConfig struct for use during the request.
func (h *Handler) loadConfig(ctx context.Context) (*runtimeConfig, error) {
	cfg := &runtimeConfig{}

	// Trust proxy headers
	trustProxy, err := h.store.GetConfig(ctx, ConfigKeyTrustProxyHeaders)
	if err != nil { // coverage-ignore -- config errors are rare after successful startup
		return nil, err
	}
	cfg.trustProxy = parseBoolOrDefault(trustProxy, false)

	// User header
	userHeader, err := h.store.GetConfig(ctx, ConfigKeyUserHeader)
	if err != nil { // coverage-ignore -- config errors are rare after successful startup
		return nil, err
	}
	if cfg.userHeader = DefaultUserHeader; userHeader != "" {
		cfg.userHeader = userHeader
	}

	// Max body size
	maxBodySize, err := h.store.GetConfig(ctx, ConfigKeyMaxBodySize)
	if err != nil { // coverage-ignore -- config errors are rare after successful startup
		return nil, err
	}
	cfg.maxBodySizeMB = parseInt64OrDefault(maxBodySize, DefaultMaxBodySizeMB)

	// Global auth mode
	authMode, err := h.store.GetConfig(ctx, ConfigKeyAuthMode)
	if err != nil { // coverage-ignore -- config errors are rare after successful startup
		return nil, err
	}
	if cfg.globalAuthMode = DefaultGlobalAuthMode; authMode != "" {
		cfg.globalAuthMode = authMode
	}

	// Global max attempts
	globalMaxAttempts, err := h.store.GetConfig(ctx, ConfigKeyGlobalMaxAttempts)
	if err != nil { // coverage-ignore -- config errors are rare after successful startup
		return nil, err
	}
	cfg.globalMaxAttempts = parseIntOrDefault(globalMaxAttempts, DefaultGlobalMaxAttempts)
	if cfg.globalMaxAttempts < 0 {
		cfg.globalMaxAttempts = DefaultGlobalMaxAttempts
	}

	// Non-critical configs (timeouts, sticky sessions): log warnings and use defaults on error.
	// Unlike critical configs above (trust_proxy, user_header, max_body_size, auth_mode, max_attempts)
	// which affect security/correctness, these are performance tuning parameters that can safely
	// fall back to sensible defaults without impacting request correctness.
	connectTimeout, err := h.store.GetConfig(ctx, ConfigKeyUpstreamConnectTimeout)
	if err != nil { // coverage-ignore -- config errors are rare after successful startup
		h.logger.Warn("failed to get upstream_connect_timeout, using default", zap.Error(err))
	}
	firstByteTimeout, err := h.store.GetConfig(ctx, ConfigKeyFirstByteTimeout)
	if err != nil { // coverage-ignore -- config errors are rare after successful startup
		h.logger.Warn("failed to get first_byte_timeout, using default", zap.Error(err))
	}
	readTimeout, err := h.store.GetConfig(ctx, ConfigKeyUpstreamReadTimeout)
	if err != nil { // coverage-ignore -- config errors are rare after successful startup
		h.logger.Warn("failed to get upstream_read_timeout, using default", zap.Error(err))
	}

	cfg.connectTimeout = parseDurationSecondsOrDefault(connectTimeout, DefaultConnectTimeout)
	cfg.firstByteTimeout = parseDurationSecondsOrDefault(firstByteTimeout, DefaultFirstByteTimeout)
	cfg.readTimeout = parseDurationSecondsOrDefault(readTimeout, 0)

	// SSE idle timeout - protects against silent upstream connections
	sseIdleTimeout, err := h.store.GetConfig(ctx, ConfigKeySSEIdleTimeout)
	if err != nil { // coverage-ignore -- config errors are rare after successful startup
		h.logger.Warn("failed to get sse_idle_timeout, using default", zap.Error(err))
	}
	cfg.sseIdleTimeout = parseDurationSecondsOrDefault(sseIdleTimeout, DefaultSSEIdleTimeout)

	// Sticky session config
	stickyModeStr, err := h.store.GetConfig(ctx, ConfigKeyStickyMode)
	if err != nil { // coverage-ignore -- config errors are rare after successful startup
		h.logger.Warn("failed to get sticky_mode, using default", zap.Error(err))
		stickyModeStr = string(DefaultStickyMode)
	}
	stickyMode := model.StickyMode(stickyModeStr)
	if !model.IsValidStickyMode(stickyMode) {
		h.logger.Warn("invalid sticky_mode, using default", zap.String("value", stickyModeStr))
		stickyMode = DefaultStickyMode
	}
	cfg.stickyMode = stickyMode

	stickyTTL, err := h.store.GetConfig(ctx, ConfigKeyStickyTTL)
	if err != nil { // coverage-ignore -- config errors are rare after successful startup
		h.logger.Warn("failed to get sticky_ttl, using default", zap.Error(err))
	}
	cfg.stickyTTL = time.Duration(parseIntOrDefault(stickyTTL, defaultStickyTTLSeconds)) * time.Second

	if h.activeRegistry != nil {
		h.activeRegistry.SetStickyPerModel(cfg.stickyMode == model.StickyModeModel)
	}

	return cfg, nil
}

// parseIntOrDefault parses a string to int, returning defaultVal on error.
func parseIntOrDefault(s string, defaultVal int) int {
	if s == "" {
		return defaultVal
	}
	if v, err := strconv.Atoi(s); err == nil {
		return v
	}
	return defaultVal
}

// parseDurationSecondsOrDefault parses a string as seconds to time.Duration, returning defaultVal on error.
func parseDurationSecondsOrDefault(s string, defaultVal time.Duration) time.Duration {
	if s == "" {
		return defaultVal
	}
	if v, err := strconv.Atoi(s); err == nil {
		return time.Duration(v) * time.Second
	}
	return defaultVal
}

// parseInt64OrDefault parses a string to int64, returning defaultVal on error.
func parseInt64OrDefault(s string, defaultVal int64) int64 {
	if s == "" {
		return defaultVal
	}
	if v, err := strconv.ParseInt(s, 10, 64); err == nil {
		return v
	}
	return defaultVal
}

// parseBoolOrDefault parses a string to bool, returning defaultVal if empty or invalid.
// Accepts "true", "1" as true, and "false", "0" as false (case-insensitive).
// Invalid values (e.g., "xyz") return defaultVal instead of false.
func parseBoolOrDefault(s string, defaultVal bool) bool {
	if s == "" {
		return defaultVal
	}
	lower := strings.ToLower(s)
	switch lower {
	case "true", "1":
		return true
	case "false", "0":
		return false
	default:
		return defaultVal
	}
}
