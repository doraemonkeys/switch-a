package admin

import (
	"testing"
)

func TestValidateConfigValue(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		value   string
		wantErr bool
	}{
		// Positive int validators
		{"circuit_failure valid", "circuit_failure", "5", false},
		{"circuit_failure negative", "circuit_failure", "-1", true},
		{"circuit_failure zero", "circuit_failure", "0", true},
		{"circuit_failure non-number", "circuit_failure", "abc", true},
		{"circuit_window valid", "circuit_window", "60", false},
		{"circuit_disable valid", "circuit_disable", "300", false},
		{"sticky_ttl valid", "sticky_ttl", "3600", false},
		{"log_retention_days valid", "log_retention_days", "30", false},
		{"max_body_size valid", "max_body_size", "1048576", false},
		{"upstream_connect_timeout valid", "upstream_connect_timeout", "30", false},
		{"upstream_read_timeout valid", "upstream_read_timeout", "60", false},

		// Non-negative int validators
		{"max_retries zero", "max_retries", "0", false},
		{"max_retries positive", "max_retries", "3", false},
		{"max_retries negative", "max_retries", "-1", true},

		// Bool validators
		{"trust_proxy_headers true", "trust_proxy_headers", "true", false},
		{"trust_proxy_headers false", "trust_proxy_headers", "false", false},
		{"trust_proxy_headers TRUE", "trust_proxy_headers", "TRUE", false},
		{"trust_proxy_headers 1", "trust_proxy_headers", "1", false},
		{"trust_proxy_headers 0", "trust_proxy_headers", "0", false},
		{"trust_proxy_headers invalid", "trust_proxy_headers", "yes", true},
		{"sticky_enabled valid", "sticky_enabled", "true", false},
		{"sticky_enabled invalid", "sticky_enabled", "maybe", true},

		// Auth mode validator
		{"auth_mode auto", "auth_mode", "auto", false},
		{"auth_mode bearer", "auth_mode", "bearer", false},
		{"auth_mode x-api-key", "auth_mode", "x-api-key", false},
		{"auth_mode invalid", "auth_mode", "basic", true},

		// Strategy validator
		{"inter_group_strategy priority", "inter_group_strategy", "priority", false},
		{"inter_group_strategy random", "inter_group_strategy", "random", false},
		{"inter_group_strategy weight", "inter_group_strategy", "weight", false},
		{"inter_group_strategy invalid", "inter_group_strategy", "round-robin", true},

		// No validator (any value accepted)
		{"user_header any", "user_header", "X-Custom-User", false},
		{"user_header empty", "user_header", "", false},

		// Unknown key (no validator, should pass)
		{"unknown key", "unknown_key", "any_value", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateConfigValue(tt.key, tt.value)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateConfigValue(%q, %q) error = %v, wantErr %v", tt.key, tt.value, err, tt.wantErr)
			}
		})
	}
}

func TestIsValidStrategy(t *testing.T) {
	tests := []struct {
		strategy string
		want     bool
	}{
		{"priority", true},
		{"random", true},
		{"weight", true},
		{"Priority", false}, // case sensitive
		{"invalid", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.strategy, func(t *testing.T) {
			if got := IsValidStrategy(tt.strategy); got != tt.want {
				t.Errorf("IsValidStrategy(%q) = %v, want %v", tt.strategy, got, tt.want)
			}
		})
	}
}

func TestIsValidAuthMode(t *testing.T) {
	tests := []struct {
		mode string
		want bool
	}{
		{"auto", true},
		{"bearer", true},
		{"x-api-key", true},
		{"Auto", false}, // case sensitive
		{"basic", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			if got := IsValidAuthMode(tt.mode); got != tt.want {
				t.Errorf("IsValidAuthMode(%q) = %v, want %v", tt.mode, got, tt.want)
			}
		})
	}
}

func TestIsValidAPIType(t *testing.T) {
	tests := []struct {
		apiType string
		want    bool
	}{
		{"chat", true},
		{"completion", true},
		{"embedding", true},
		{"image", true},
		{"audio", true},
		{"moderation", true},
		{"claude", true},
		{"gpt", true},
		{"codex", true},
		{"gemini", true},
		{"llama", true},
		{"mistral", true},
		{"custom:mytool", true},
		{"custom:", false},       // empty custom name
		{"Custom:mytool", false}, // case sensitive prefix
		{"invalid", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.apiType, func(t *testing.T) {
			if got := IsValidAPIType(tt.apiType); got != tt.want {
				t.Errorf("IsValidAPIType(%q) = %v, want %v", tt.apiType, got, tt.want)
			}
		})
	}
}

func TestIsValidConfigKey(t *testing.T) {
	tests := []struct {
		key  string
		want bool
	}{
		{"auth_mode", true},
		{"user_header", true},
		{"trust_proxy_headers", true},
		{"upstream_connect_timeout", true},
		{"upstream_read_timeout", true},
		{"sticky_enabled", true},
		{"sticky_ttl", true},
		{"circuit_failure", true},
		{"circuit_window", true},
		{"circuit_disable", true},
		{"max_body_size", true},
		{"max_retries", true},
		{"log_retention_days", true},
		{"inter_group_strategy", true},
		{"unknown_key", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			if got := IsValidConfigKey(tt.key); got != tt.want {
				t.Errorf("IsValidConfigKey(%q) = %v, want %v", tt.key, got, tt.want)
			}
		})
	}
}
