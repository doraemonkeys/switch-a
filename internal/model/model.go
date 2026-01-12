// Package model defines the core data models for switch-a.
package model

import "time"

// Provider represents an AI provider configuration.
type Provider struct {
	ID          string            `gorm:"primaryKey" json:"id"`
	Name        string            `gorm:"not null" json:"name"`
	BaseURL     string            `gorm:"not null" json:"base_url"`
	APIKey      string            `gorm:"not null" json:"api_key"`
	APITypes    []ProviderAPIType `gorm:"foreignKey:ProviderID" json:"api_types"`
	AuthMode    string            `gorm:"default:auto" json:"auth_mode"`
	GroupID     *string           `gorm:"index" json:"group_id"`
	Group       *Group            `gorm:"foreignKey:GroupID" json:"-"`
	Weight      int               `gorm:"default:1" json:"weight"`
	Priority    int               `gorm:"default:0" json:"priority"`
	Concurrency int               `gorm:"default:0" json:"concurrency"`
	MaxRetries  int               `gorm:"default:-1" json:"max_retries"`
	Enabled     bool              `gorm:"default:true;index" json:"enabled"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
	// Health is populated by admin API handlers, not stored in database.
	Health *HealthState `gorm:"-" json:"health,omitempty"`
}

// ProviderAPIType represents the association between Provider and API types.
type ProviderAPIType struct {
	ProviderID string `gorm:"primaryKey" json:"provider_id"`
	APIType    string `gorm:"primaryKey;index" json:"api_type"`
}

// Group represents a provider group configuration.
type Group struct {
	ID        string     `gorm:"primaryKey" json:"id"`
	Name      string     `gorm:"not null" json:"name"`
	Strategy  string     `gorm:"default:priority" json:"strategy"`
	Priority  int        `gorm:"default:0" json:"priority"`
	Weight    int        `gorm:"default:1" json:"weight"`
	Enabled   bool       `gorm:"default:true" json:"enabled"`
	Providers []Provider `gorm:"foreignKey:GroupID" json:"providers,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// HealthState represents the health status of a provider.
type HealthState struct {
	ProviderID     string     `gorm:"primaryKey" json:"provider_id"`
	Provider       *Provider  `gorm:"foreignKey:ProviderID;constraint:OnDelete:CASCADE" json:"-"`
	Available      bool       `gorm:"default:true" json:"available"`
	SuccessCount   int64      `gorm:"default:0" json:"success_count"`
	FailCount      int64      `gorm:"default:0" json:"fail_count"`
	LastSuccess    *time.Time `json:"last_success"`
	LastFailure    *time.Time `json:"last_failure"`
	LastError      string     `json:"last_error"`
	DisabledUntil  *time.Time `json:"disabled_until"`
	DisabledReason string     `json:"disabled_reason"`
}

// RuntimeConfig represents a runtime configuration key-value pair.
type RuntimeConfig struct {
	Key       string    `gorm:"primaryKey" json:"key"`
	Value     string    `gorm:"not null" json:"value"`
	UpdatedAt time.Time `json:"updated_at"`
}

// RequestLog represents a request log entry.
type RequestLog struct {
	ID         uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	ProviderID string    `gorm:"index" json:"provider_id"`
	APIType    string    `json:"api_type"`
	Model      string    `json:"model"`
	ClientIP   string    `json:"client_ip"`
	UserID     string    `json:"user_id"`
	StatusCode int       `json:"status_code"`
	LatencyMs  int64     `json:"latency_ms"`
	Success    bool      `json:"success"`
	ErrorMsg   string    `json:"error_msg"`
	CreatedAt  time.Time `gorm:"index" json:"created_at"`
}

// LogFilter represents filter and sort parameters for log queries.
type LogFilter struct {
	ProviderID string     // Filter by provider ID
	APIType    string     // Filter by API type (claude/codex/gemini/custom:*)
	Success    *bool      // Filter by success/failure (nil = no filter)
	UserID     string     // Filter by user ID
	StartTime  *time.Time // Filter by start time (inclusive)
	EndTime    *time.Time // Filter by end time (exclusive)
	MinLatency *int64     // Filter by minimum latency in ms
	SortBy     string     // Sort field: "created_at" or "latency_ms"
	SortOrder  string     // Sort direction: "asc" or "desc"
	Limit      int        // Maximum number of results
	Offset     int        // Offset for pagination
}

// StickyKey represents the cache key for sticky session.
type StickyKey struct {
	IP      string
	User    string
	APIType string
}

// SelectRequest represents a provider selection request.
type SelectRequest struct {
	ClientIP      string
	User          string
	APIType       string
	Model         string
	StickyEnabled bool // Whether sticky sessions are enabled (pre-loaded from config)
}

// GatewayError represents an error response from the gateway.
type GatewayError struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// NewGatewayError creates a new GatewayError with the given code and message.
func NewGatewayError(code, message string) GatewayError {
	e := GatewayError{}
	e.Error.Code = code
	e.Error.Message = message
	return e
}

// ErrorResponse represents a management API error response.
type ErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// LogStats represents aggregated statistics from request logs.
type LogStats struct {
	TotalRequests int64              // Total number of requests
	SuccessCount  int64              // Number of successful requests
	FailCount     int64              // Number of failed requests
	SuccessRate   float64            // Success rate (0.0 to 1.0)
	AvgLatencyMs  int64              // Average latency in milliseconds
	ByAPIType     map[string]int64   // Request count by API type
	ByProvider    []ProviderLogStats // Request statistics by provider
	EarliestLog   time.Time          // Earliest log timestamp (for "all" period)
}

// ProviderLogStats represents log statistics for a single provider.
type ProviderLogStats struct {
	ProviderID   string  // Provider ID
	Count        int64   // Total request count
	SuccessCount int64   // Successful request count
	SuccessRate  float64 // Success rate (0.0 to 1.0)
}
