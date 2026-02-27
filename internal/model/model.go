// Package model defines the core data models for switch-a.
package model

import "time"

// Scope defines the failover scope for vendor isolation.
// Controls which providers can be used as failover targets.
type Scope string

const (
	// ScopeNone means no failover is allowed.
	ScopeNone Scope = "none"
	// ScopeVendor means failover is only allowed within the same vendor.
	ScopeVendor Scope = "vendor"
	// ScopeAny means failover to any provider is allowed (default).
	ScopeAny Scope = "any"
)

// StickyMode defines sticky session routing granularity.
type StickyMode string

const (
	// StickyModeOff disables sticky sessions.
	StickyModeOff StickyMode = "off"
	// StickyModeAPIType keeps stickiness by client and API type.
	StickyModeAPIType StickyMode = "api_type"
	// StickyModeModel keeps stickiness by client, API type, and model.
	StickyModeModel StickyMode = "model"
)

// VendorWildcard is the wildcard value that matches any vendor.
const VendorWildcard = "*"

// IsValidScope checks if the given scope is valid.
// Empty string is valid and treated as ScopeAny (the default).
func IsValidScope(s Scope) bool {
	switch s {
	case ScopeNone, ScopeVendor, ScopeAny, "":
		return true
	default:
		return false
	}
}

// IsValidStickyMode checks if the given sticky mode is valid.
func IsValidStickyMode(m StickyMode) bool {
	switch m {
	case StickyModeOff, StickyModeAPIType, StickyModeModel:
		return true
	default:
		return false
	}
}

// Provider represents an AI provider configuration.
type Provider struct {
	ID          string            `gorm:"primaryKey" json:"id"`
	Name        string            `gorm:"not null" json:"name"`
	APIKey      string            `gorm:"not null" json:"api_key"`
	APITypes    []ProviderAPIType `gorm:"foreignKey:ProviderID" json:"api_types"`
	AuthMode    string            `gorm:"default:auto" json:"auth_mode"`
	GroupID     *string           `gorm:"index" json:"group_id"`
	Group       *Group            `gorm:"foreignKey:GroupID" json:"-"`
	Weight      int               `gorm:"default:1" json:"weight"`
	Priority    int               `gorm:"default:0" json:"priority"`
	Concurrency int               `gorm:"default:0" json:"concurrency"`
	// MaxRetries is the number of retries allowed for this provider (0 = try once, no retry).
	MaxRetries int `gorm:"default:0" json:"max_retries"`
	// Backoff defines exponential backoff for same-provider retries.
	// GORM embedded tag flattens the struct fields into Provider's table with "backoff_" prefix.
	Backoff BackoffPolicy `gorm:"embedded;embeddedPrefix:backoff_" json:"backoff,omitempty"`
	// Vendor identifies the vendor for failover isolation.
	// Empty string means the provider doesn't participate in vendor isolation.
	// "*" (VendorWildcard) matches any non-empty vendor.
	Vendor string `gorm:"index" json:"vendor"`
	// FailoverScope controls outbound failover: where we can failover TO after this provider fails.
	FailoverScope Scope `gorm:"default:any" json:"failover_scope"`
	// AcceptFailover controls inbound failover: which sources we accept failover FROM.
	AcceptFailover Scope     `gorm:"default:any" json:"accept_failover"`
	Enabled        bool      `gorm:"default:true;index" json:"enabled"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	// Health is populated by admin API handlers, not stored in database.
	Health *HealthState `gorm:"-" json:"health,omitempty"`
}

// BaseURLForAPIType returns the base URL for the given API type.
// Empty return allows caller to decide failure behavior, since some code paths
// handle missing API types differently (e.g., admin validation vs proxy routing).
func (p *Provider) BaseURLForAPIType(apiType string) string {
	for _, at := range p.APITypes {
		if at.APIType == apiType {
			return at.BaseURL
		}
	}
	return ""
}

// ProviderAPIType represents the association between Provider and API types.
// Each entry carries its own BaseURL, allowing different endpoints per API type.
type ProviderAPIType struct {
	ProviderID string `gorm:"primaryKey" json:"provider_id"`
	APIType    string `gorm:"primaryKey;index" json:"api_type"`
	BaseURL    string `gorm:"not null;default:''" json:"base_url"`
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
	ID          uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	RequestID   string    `gorm:"index" json:"request_id"`
	ProviderID  string    `gorm:"index" json:"provider_id"`
	APIType     string    `json:"api_type"`
	Model       string    `json:"model"`
	ClientIP    string    `json:"client_ip"`
	UserID      string    `json:"user_id"`
	StatusCode  int       `json:"status_code"`
	LatencyMs   int64     `json:"latency_ms"`
	Success     bool      `json:"success"`
	IsSSE       bool      `json:"is_sse"`
	IsWebSocket bool      `gorm:"default:false" json:"is_websocket"`
	ErrorMsg    string    `json:"error_msg"`
	RetryCount  int       `json:"retry_count"`
	IsSticky    bool      `json:"is_sticky"`
	CreatedAt   time.Time `gorm:"index" json:"created_at"`
	// Phase 1 diagnostic fields (P0)
	RequestPath     string `gorm:"default:''" json:"request_path"`      // Relative path like /v1/messages (without base_url)
	RequestMethod   string `gorm:"default:''" json:"request_method"`    // HTTP method: GET/POST/PUT/DELETE
	UserAgent       string `gorm:"default:''" json:"user_agent"`        // Client User-Agent (truncated to 512 chars)
	RequestIDHeader string `gorm:"default:''" json:"request_id_header"` // Client's X-Request-ID for tracing
	// Phase 2 diagnostic fields (P1)
	FirstTokenMs *int64 `gorm:"default:null" json:"first_token_ms"` // Time to first token for SSE requests (ms), null for non-SSE
	// Phase 3 transfer statistics (P2)
	RequestBytes  int64  `gorm:"default:0" json:"request_bytes"`  // Request body size in bytes
	ResponseBytes int64  `gorm:"default:0" json:"response_bytes"` // Response body size in bytes
	ContentType   string `gorm:"default:''" json:"content_type"`  // Request Content-Type header
	// Phase 4 token usage statistics (P3)
	// Uses pointers to distinguish NULL (unknown/parse failed) from 0 (explicitly zero)
	PromptTokens             *int64  `gorm:"default:null;index" json:"prompt_tokens,omitempty"`         // Input tokens (OpenAI: prompt_tokens, Claude: input_tokens)
	CompletionTokens         *int64  `gorm:"default:null;index" json:"completion_tokens,omitempty"`     // Output tokens (OpenAI: completion_tokens, Claude: output_tokens)
	TotalTokens              *int64  `gorm:"default:null;index" json:"total_tokens,omitempty"`          // Total tokens
	CacheReadInputTokens     *int64  `gorm:"default:null" json:"cache_read_input_tokens,omitempty"`     // Claude: tokens read from cache (billed at 10%)
	CacheCreationInputTokens *int64  `gorm:"default:null" json:"cache_creation_input_tokens,omitempty"` // Claude: tokens written to cache (billed at 125%)
	UsageDetails             *string `gorm:"type:text;default:null" json:"usage_details,omitempty"`     // JSON: full usage details (service_tier, TTL breakdown, etc.)
	// Attempts is populated by API, not stored directly in database.
	Attempts []RequestAttempt `gorm:"-" json:"attempts,omitempty"`
}

// RequestAttempt represents a single attempt within a request (for retry tracking).
type RequestAttempt struct {
	ID             uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	RequestID      string    `gorm:"index" json:"request_id"`
	ProviderID     string    `json:"provider_id"`
	Attempt        int       `json:"attempt"`
	StatusCode     int       `json:"status_code"`
	Error          string    `json:"error"`
	BodySnippet    string    `json:"body_snippet,omitempty"`     // First ~512 bytes of error response (failover scenarios only)
	ReqBodySnippet string    `json:"req_body_snippet,omitempty"` // First ~512 bytes of request body (error attempts only)
	LatencyMs      int64     `json:"latency_ms"`
	SwitchReason   string    `json:"switch_reason,omitempty"` // Reason for switching to next provider (if any)
	CreatedAt      time.Time `json:"created_at"`
}

// LogFilter represents filter and sort parameters for log queries.
type LogFilter struct {
	ProviderID    string     // Filter by provider ID
	APIType       string     // Filter by API type (claude/codex/gemini/custom:*)
	Success       *bool      // Filter by success/failure (nil = no filter)
	IsSSE         *bool      // Filter by SSE/regular request (nil = no filter)
	UserID        string     // Filter by user ID
	StartTime     *time.Time // Filter by start time (inclusive)
	EndTime       *time.Time // Filter by end time (exclusive)
	MinLatency    *int64     // Filter by minimum latency in ms
	MinRetryCount *int       // Filter by minimum retry count (e.g., 1 for "has retries")
	HasRetries    *bool      // Filter by has retries (true = retry_count > 0, false = retry_count = 0)
	SortBy        string     // Sort field: "created_at" or "latency_ms"
	SortOrder     string     // Sort direction: "asc" or "desc"
	Limit         int        // Maximum number of results
	Offset        int        // Offset for pagination
}

// StickyKey represents the cache key for sticky session.
type StickyKey struct {
	IP      string
	User    string
	APIType string
	Model   string
}

// SelectRequest represents a provider selection request.
type SelectRequest struct {
	ClientIP        string
	User            string
	APIType         string
	Model           string
	StickyMode      StickyMode       // Sticky session mode pre-loaded from runtime config
	FailoverContext *FailoverContext // Optional: failover context for vendor isolation (nil = first attempt)
	// MaxProviderSwitches limits the number of provider switches (failover attempts) allowed.
	// 0 = no limit. This counts only provider changes, not per-provider retries.
	// Distinct from globalMaxAttempts which limits total loop iterations including per-provider retries.
	MaxProviderSwitches int
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

// TimeSeriesPoint represents a single data point in a time series.
type TimeSeriesPoint struct {
	Time         time.Time `json:"time"`
	Requests     int64     `json:"requests"`
	SuccessCount int64     `json:"success_count"`
	FailCount    int64     `json:"fail_count"`
	SuccessRate  float64   `json:"success_rate"`
	AvgLatencyMs int64     `json:"avg_latency_ms"`
}
