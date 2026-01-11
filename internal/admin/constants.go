// Package admin provides HTTP handlers for the admin API.
package admin

import "switch-a/internal/defaults"

// Error codes for API responses.
const (
	ErrCodeValidation   = "VALIDATION_ERROR"
	ErrCodeInternal     = "INTERNAL_ERROR"
	ErrCodeNotFound     = "NOT_FOUND"
	ErrCodeConflict     = "CONFLICT"
	ErrCodeUnauthorized = "UNAUTHORIZED"
)

// Default values for resources.
// Core defaults are derived from the centralized defaults package.
const (
	DefaultStrategy   = defaults.InterGroupStrategy
	DefaultAuthMode   = defaults.AuthMode
	DefaultWeight     = defaults.ProviderWeight
	DefaultMaxRetries = -1 // Use global default (special value for admin API)
	DefaultLogsLimit  = 100
	MaxLogsLimit      = 1000
)

// Request body size limits.
const (
	MaxRequestBodySize = 1 << 20 // 1MB limit for JSON requests
	MaxConfigUpdates   = 50      // Maximum number of config keys per update request
)

// ValidStrategies contains the allowed strategy values.
var ValidStrategies = map[string]bool{
	"priority": true,
	"random":   true,
	"weight":   true,
}

// ValidAuthModes contains the allowed auth mode values.
var ValidAuthModes = map[string]bool{
	"auto":      true,
	"bearer":    true,
	"x-api-key": true,
}

// ValidAPITypes contains the allowed API type values.
// These can be functional types (chat, embedding) or provider-specific identifiers.
var ValidAPITypes = map[string]bool{
	// Functional API types
	"chat":       true,
	"completion": true,
	"embedding":  true,
	"image":      true,
	"audio":      true,
	"moderation": true,
	// Provider-specific types for routing
	"claude":  true,
	"gpt":     true,
	"codex":   true,
	"gemini":  true,
	"llama":   true,
	"mistral": true,
}

// ValidConfigKeys contains the allowed configuration keys.
var ValidConfigKeys = map[string]bool{
	"auth_mode":                true,
	"user_header":              true,
	"trust_proxy_headers":      true,
	"upstream_connect_timeout": true,
	"upstream_read_timeout":    true,
	"sticky_enabled":           true,
	"sticky_ttl":               true,
	"circuit_failure":          true,
	"circuit_window":           true,
	"circuit_disable":          true,
	"max_body_size":            true,
	"max_retries":              true,
	"log_retention_days":       true,
	"inter_group_strategy":     true,
}

// IsValidStrategy checks if the given strategy is valid.
func IsValidStrategy(s string) bool {
	return ValidStrategies[s]
}

// IsValidAuthMode checks if the given auth mode is valid.
func IsValidAuthMode(m string) bool {
	return ValidAuthModes[m]
}

// IsValidAPIType checks if the given API type is valid.
func IsValidAPIType(t string) bool {
	return ValidAPITypes[t]
}

// IsValidConfigKey checks if the given config key is valid.
func IsValidConfigKey(k string) bool {
	return ValidConfigKeys[k]
}
