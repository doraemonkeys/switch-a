// Package admin provides HTTP handlers for the admin API.
package admin

import (
	"fmt"
	"strconv"
	"strings"

	"switch-a/internal/defaults"
)

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

// CustomAPITypePrefix is the prefix for custom API types (e.g., "custom:mytool").
const CustomAPITypePrefix = "custom:"

// IsValidAPIType checks if the given API type is valid.
// Accepts both predefined API types and custom:* pattern for custom tools.
func IsValidAPIType(t string) bool {
	if ValidAPITypes[t] {
		return true
	}
	// Support custom:* format for custom API tools
	if len(t) > len(CustomAPITypePrefix) && t[:len(CustomAPITypePrefix)] == CustomAPITypePrefix {
		return true
	}
	return false
}

// IsValidConfigKey checks if the given config key is valid.
func IsValidConfigKey(k string) bool {
	return ValidConfigKeys[k]
}

// ConfigValidator is a function that validates a config value.
type ConfigValidator func(value string) error

// configValidators maps config keys to their validator functions.
var configValidators = map[string]ConfigValidator{
	"auth_mode":                validateAuthModeConfig,
	"user_header":              nil, // Any string is valid
	"trust_proxy_headers":      validateBoolConfig,
	"upstream_connect_timeout": validatePositiveIntConfig,
	"upstream_read_timeout":    validatePositiveIntConfig,
	"sticky_enabled":           validateBoolConfig,
	"sticky_ttl":               validatePositiveIntConfig,
	"circuit_failure":          validatePositiveIntConfig,
	"circuit_window":           validatePositiveIntConfig,
	"circuit_disable":          validatePositiveIntConfig,
	"max_body_size":            validatePositiveIntConfig,
	"max_retries":              validateNonNegativeIntConfig,
	"log_retention_days":       validatePositiveIntConfig,
	"inter_group_strategy":     validateStrategyConfig,
}

// ValidateConfigValue validates a config value for the given key.
// Returns nil if validation passes or if no validator is defined for the key.
func ValidateConfigValue(key, value string) error {
	validator, exists := configValidators[key]
	if !exists || validator == nil {
		return nil
	}
	return validator(value)
}

func validatePositiveIntConfig(value string) error {
	n, err := strconv.Atoi(value)
	if err != nil {
		return fmt.Errorf("must be a valid integer")
	}
	if n <= 0 {
		return fmt.Errorf("must be a positive integer")
	}
	return nil
}

func validateNonNegativeIntConfig(value string) error {
	n, err := strconv.Atoi(value)
	if err != nil {
		return fmt.Errorf("must be a valid integer")
	}
	if n < 0 {
		return fmt.Errorf("must be a non-negative integer")
	}
	return nil
}

func validateBoolConfig(value string) error {
	lower := strings.ToLower(value)
	if lower != "true" && lower != "false" && lower != "1" && lower != "0" {
		return fmt.Errorf("must be 'true' or 'false'")
	}
	return nil
}

func validateAuthModeConfig(value string) error {
	if !IsValidAuthMode(value) {
		return fmt.Errorf("must be 'auto', 'bearer', or 'x-api-key'")
	}
	return nil
}

func validateStrategyConfig(value string) error {
	if !IsValidStrategy(value) {
		return fmt.Errorf("must be 'priority', 'random', or 'weight'")
	}
	return nil
}
