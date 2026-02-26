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
		{"global_max_attempts zero", "global_max_attempts", "0", false},
		{"global_max_attempts positive", "global_max_attempts", "3", false},
		{"global_max_attempts negative", "global_max_attempts", "-1", true},

		// Bool validators
		{"trust_proxy_headers true", "trust_proxy_headers", "true", false},
		{"trust_proxy_headers false", "trust_proxy_headers", "false", false},
		{"trust_proxy_headers TRUE", "trust_proxy_headers", "TRUE", false},
		{"trust_proxy_headers 1", "trust_proxy_headers", "1", false},
		{"trust_proxy_headers 0", "trust_proxy_headers", "0", false},
		{"trust_proxy_headers invalid", "trust_proxy_headers", "yes", true},
		{"sticky_mode off", "sticky_mode", "off", false},
		{"sticky_mode api_type", "sticky_mode", "api_type", false},
		{"sticky_mode model", "sticky_mode", "model", false},
		{"sticky_mode invalid", "sticky_mode", "maybe", true},

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
		// Valid routable types (match proxy router)
		{"claude", true},
		{"codex", true},
		{"gemini", true},
		{"custom:mytool", true},
		// Invalid: empty custom name
		{"custom:", false},
		// Invalid: case sensitive prefix
		{"Custom:mytool", false},
		// Invalid: these API types have no matching proxy routes
		{"chat", false},
		{"completion", false},
		{"embedding", false},
		{"image", false},
		{"audio", false},
		{"moderation", false},
		{"gpt", false},
		{"llama", false},
		{"mistral", false},
		// Invalid: unknown types
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
		{"sticky_mode", true},
		{"sticky_ttl", true},
		{"circuit_failure", true},
		{"circuit_window", true},
		{"circuit_disable", true},
		{"max_body_size", true},
		{"global_max_attempts", true},
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

// Tests for individual validation helper functions (lines 166-208 in constants.go)

func TestValidatePositiveIntConfig(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{"valid positive integer", "10", false},
		{"valid large positive integer", "1000000", false},
		{"valid minimum positive", "1", false},
		{"invalid zero", "0", true},
		{"invalid negative", "-1", true},
		{"invalid large negative", "-1000", true},
		{"invalid non-numeric", "abc", true},
		{"invalid empty string", "", true},
		{"invalid float", "1.5", true},
		{"invalid mixed", "10abc", true},
		{"invalid whitespace", " 10 ", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePositiveIntConfig(tt.value)
			if (err != nil) != tt.wantErr {
				t.Errorf("validatePositiveIntConfig(%q) error = %v, wantErr %v", tt.value, err, tt.wantErr)
			}
		})
	}
}

func TestValidateNonNegativeIntConfig(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{"valid positive integer", "10", false},
		{"valid zero", "0", false},
		{"valid large positive integer", "1000000", false},
		{"invalid negative", "-1", true},
		{"invalid large negative", "-1000", true},
		{"invalid non-numeric", "abc", true},
		{"invalid empty string", "", true},
		{"invalid float", "1.5", true},
		{"invalid mixed", "10abc", true},
		{"invalid whitespace", " 0 ", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateNonNegativeIntConfig(tt.value)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateNonNegativeIntConfig(%q) error = %v, wantErr %v", tt.value, err, tt.wantErr)
			}
		})
	}
}

func TestValidateBoolConfig(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{"valid true lowercase", "true", false},
		{"valid false lowercase", "false", false},
		{"valid TRUE uppercase", "TRUE", false},
		{"valid FALSE uppercase", "FALSE", false},
		{"valid True mixed case", "True", false},
		{"valid False mixed case", "False", false},
		{"valid 1", "1", false},
		{"valid 0", "0", false},
		{"invalid yes", "yes", true},
		{"invalid no", "no", true},
		{"invalid empty string", "", true},
		{"invalid numeric non-bool", "2", true},
		{"invalid random string", "maybe", true},
		{"invalid whitespace", " true ", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateBoolConfig(tt.value)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateBoolConfig(%q) error = %v, wantErr %v", tt.value, err, tt.wantErr)
			}
		})
	}
}

func TestValidateAuthModeConfig(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{"valid auto", "auto", false},
		{"valid bearer", "bearer", false},
		{"valid x-api-key", "x-api-key", false},
		{"invalid empty string", "", true},
		{"invalid uppercase AUTO", "AUTO", true},
		{"invalid uppercase BEARER", "BEARER", true},
		{"invalid basic", "basic", true},
		{"invalid oauth", "oauth", true},
		{"invalid random string", "something", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateAuthModeConfig(tt.value)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateAuthModeConfig(%q) error = %v, wantErr %v", tt.value, err, tt.wantErr)
			}
		})
	}
}

func TestValidateStrategyConfig(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{"valid priority", "priority", false},
		{"valid random", "random", false},
		{"valid weight", "weight", false},
		{"invalid empty string", "", true},
		{"invalid uppercase PRIORITY", "PRIORITY", true},
		{"invalid uppercase RANDOM", "RANDOM", true},
		{"invalid round-robin", "round-robin", true},
		{"invalid least-connections", "least-connections", true},
		{"invalid random string", "anything", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateStrategyConfig(tt.value)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateStrategyConfig(%q) error = %v, wantErr %v", tt.value, err, tt.wantErr)
			}
		})
	}
}

func TestValidateStickyModeConfig(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{"valid off", "off", false},
		{"valid api_type", "api_type", false},
		{"valid model", "model", false},
		{"invalid uppercase", "MODEL", true},
		{"invalid empty", "", true},
		{"invalid other", "true", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateStickyModeConfig(tt.value)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateStickyModeConfig(%q) error = %v, wantErr %v", tt.value, err, tt.wantErr)
			}
		})
	}
}
