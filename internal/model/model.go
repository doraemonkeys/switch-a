// Package model defines the core data models for switch-a.
package model

import "time"

// Provider represents an AI provider configuration.
type Provider struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	BaseURL     string    `json:"base_url"`
	APIKey      string    `json:"api_key"`
	APITypes    []string  `json:"api_types"`
	AuthMode    string    `json:"auth_mode"`
	GroupID     string    `json:"group_id"`
	Weight      int       `json:"weight"`
	Priority    int       `json:"priority"`
	Concurrency int       `json:"concurrency"`
	MaxRetries  int       `json:"max_retries"`
	Enabled     bool      `json:"enabled"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Group represents a provider group configuration.
type Group struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Strategy  string    `json:"strategy"`
	Priority  int       `json:"priority"`
	Weight    int       `json:"weight"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// HealthState represents the health status of a provider.
type HealthState struct {
	ProviderID     string    `json:"provider_id"`
	Available      bool      `json:"available"`
	SuccessCount   int64     `json:"success_count"`
	FailCount      int64     `json:"fail_count"`
	LastSuccess    time.Time `json:"last_success"`
	LastFailure    time.Time `json:"last_failure"`
	LastError      string    `json:"last_error"`
	DisabledUntil  time.Time `json:"disabled_until"`
	DisabledReason string    `json:"disabled_reason"`
}

// StickyKey represents the cache key for sticky session.
type StickyKey struct {
	IP      string
	User    string
	APIType string
}

// StickyEntry represents a sticky cache entry.
type StickyEntry struct {
	ProviderID string
	ExpiresAt  time.Time
}

// RequestLog represents a request log entry.
type RequestLog struct {
	ID         int64     `json:"id"`
	ProviderID string    `json:"provider_id"`
	APIType    string    `json:"api_type"`
	Model      string    `json:"model"`
	ClientIP   string    `json:"client_ip"`
	UserID     string    `json:"user_id"`
	StatusCode int       `json:"status_code"`
	LatencyMs  int64     `json:"latency_ms"`
	Success    bool      `json:"success"`
	ErrorMsg   string    `json:"error_msg"`
	CreatedAt  time.Time `json:"created_at"`
}

// SelectRequest represents a provider selection request.
type SelectRequest struct {
	ClientIP string
	User     string
	APIType  string
	Model    string
}

// GatewayError represents an error response from the gateway.
type GatewayError struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// ErrorResponse represents a management API error response.
type ErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
