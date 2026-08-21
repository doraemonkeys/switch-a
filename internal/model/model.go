// Package model defines the core data models for switch-a.
package model

import (
	"strings"
	"time"
)

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
	ID       string            `gorm:"primaryKey" json:"id"`
	Name     string            `gorm:"not null" json:"name"`
	APIKey   string            `gorm:"not null" json:"api_key"`
	APITypes []ProviderAPIType `gorm:"foreignKey:ProviderID" json:"api_types"`
	AuthMode string            `gorm:"default:auto" json:"auth_mode"`
	// CredentialType defines how this provider authenticates upstream requests.
	// Static API key providers continue using APIKey/API type overrides, while
	// login-backed providers resolve credentials from the split provider_credentials table.
	CredentialType ProviderCredentialType `gorm:"type:text;default:api_key" json:"credential_type"`
	// UsageLimitPolicy stores only an explicit override. Empty values inherit the
	// credential-derived default so credential_type changes can update effective
	// quota behavior without rewriting persisted state.
	UsageLimitPolicy ProviderUsageLimitPolicy `gorm:"type:text;default:''" json:"usage_limit_policy"`
	GroupID          *string                  `gorm:"index" json:"group_id"`
	Group            *Group                   `gorm:"foreignKey:GroupID" json:"-"`
	Weight           int                      `gorm:"default:1" json:"weight"`
	Priority         int                      `gorm:"default:0" json:"priority"`
	Concurrency      int                      `gorm:"default:0" json:"concurrency"`
	// MaxRetries is the number of retries allowed for this provider (0 = try once, no retry).
	MaxRetries int `gorm:"default:0" json:"max_retries"`
	// Backoff defines exponential backoff for same-provider retries.
	// GORM embedded tag flattens the struct fields into Provider's table with "backoff_" prefix.
	Backoff BackoffPolicy `gorm:"embedded;embeddedPrefix:backoff_" json:"backoff,omitzero"`
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
	// Credential persists refresh-capable secret material outside the provider row
	// so auth-state updates no longer need to rewrite the entire provider record.
	Credential *ProviderCredential `gorm:"foreignKey:ProviderID" json:"-"`
	// AuthState captures lifecycle and summary fields separately from Health so
	// reauthentication does not pollute temporary availability semantics.
	AuthState *ProviderAuthState `gorm:"foreignKey:ProviderID" json:"-"`
	// Health is populated by admin API handlers, not stored in database.
	Health *HealthState `gorm:"-" json:"health,omitempty"`
}

// BaseURLForAPIType returns the base URL for the given API type.
// Empty return allows caller to decide failure behavior, since some code paths
// handle missing API types differently (e.g., admin validation vs proxy routing).
func (p *Provider) BaseURLForAPIType(apiType string) string {
	if at, ok := p.APITypeConfig(apiType); ok {
		return at.BaseURL
	}
	return ""
}

// APIKeyForAPIType returns the effective API key for the given API type.
// API-type credentials take precedence because routing first selects api_type,
// then resolves the endpoint/auth pair for that concrete upstream contract.
func (p *Provider) APIKeyForAPIType(apiType string) string {
	if at, ok := p.APITypeConfig(apiType); ok {
		if apiKey := NormalizeAPIKey(at.APIKey); apiKey != "" {
			return apiKey
		}
	}
	return NormalizeAPIKey(p.APIKey)
}

// APITypeConfig returns the configured API-type entry for the given API type.
func (p *Provider) APITypeConfig(apiType string) (ProviderAPIType, bool) {
	for _, at := range p.APITypes {
		if at.APIType == apiType {
			return at, true
		}
	}
	return ProviderAPIType{}, false
}

// ProviderAPIType represents the association between Provider and API types.
// Each entry carries its own endpoint contract. Most providers inherit the
// provider-level API key, but api_type may override it for split credentials.
type ProviderAPIType struct {
	ProviderID string `gorm:"primaryKey" json:"provider_id"`
	APIType    string `gorm:"primaryKey;index" json:"api_type"`
	BaseURL    string `gorm:"not null;default:''" json:"base_url"`
	APIKey     string `gorm:"not null;default:''" json:"api_key"`
}

// NormalizeAPIKey trims surrounding whitespace so blank paste artifacts do not
// shadow a valid credential with an unusable override.
func NormalizeAPIKey(apiKey string) string {
	return strings.TrimSpace(apiKey)
}

// HasAPIKey reports whether the value contains an effective credential.
func HasAPIKey(apiKey string) bool {
	return NormalizeAPIKey(apiKey) != ""
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

// RequestSemanticsVersion makes the migration boundary explicit so historical rows
// never masquerade as normalized assessments.
type RequestSemanticsVersion string

const (
	RequestSemanticsVersionLegacyPreAssessment RequestSemanticsVersion = "legacy_pre_assessment"
	RequestSemanticsVersionNormalizedV1        RequestSemanticsVersion = "normalized_v1"
)

// CompletionState records whether the request/session reached an explicit end.
type CompletionState string

const (
	CompletionStateUnknown    CompletionState = "unknown"
	CompletionStateIncomplete CompletionState = "incomplete"
	CompletionStateCompleted  CompletionState = "completed"
)

// TerminationActor identifies who ended the request/session when diagnostic
// attribution applies.
type TerminationActor string

const (
	TerminationActorClient   TerminationActor = "client"
	TerminationActorGateway  TerminationActor = "gateway"
	TerminationActorUpstream TerminationActor = "upstream"
	TerminationActorInternal TerminationActor = "internal"
	TerminationActorUnknown  TerminationActor = "unknown"
)

// TerminationReason records the stable terminal reason vocabulary used by the
// normalized assessment contract.
type TerminationReason string

const (
	TerminationReasonProviderUnavailable             TerminationReason = "provider_unavailable"
	TerminationReasonProviderConfigurationError      TerminationReason = "provider_configuration_error"
	TerminationReasonUsageLimitReached               TerminationReason = "usage_limit_reached"
	TerminationReasonWebSocketConnectionLimitReached TerminationReason = "websocket_connection_limit_reached"
	TerminationReasonClientRequestError              TerminationReason = "client_request_error"
	TerminationReasonClientDisconnect                TerminationReason = "client_disconnect"
	TerminationReasonTimeout                         TerminationReason = "timeout"
	TerminationReasonTransportError                  TerminationReason = "transport_error"
	TerminationReasonUpstreamSemanticError           TerminationReason = "upstream_semantic_error"
	TerminationReasonUpstreamHandshakeRejected       TerminationReason = "upstream_handshake_rejected"
	TerminationReasonClientUpgradeRejected           TerminationReason = "client_upgrade_rejected"
	TerminationReasonInternalError                   TerminationReason = "internal_error"
	TerminationReasonCleanClose                      TerminationReason = "clean_close"
	TerminationReasonUnknown                         TerminationReason = "unknown"
)

// ClientAction captures the client-facing recovery contract without overloading
// the reporting dimension used for analytics and badges.
type ClientAction string

const (
	ClientActionNone              ClientAction = "none"
	ClientActionTransparentRetry  ClientAction = "transparent_retry"
	ClientActionReconnectRequired ClientAction = "reconnect_required"
)

// ServiceOutcome is the only request/session outcome dimension used for
// reporting across WebSocket, HTTP, and SSE rows.
type ServiceOutcome string

const (
	ServiceOutcomeCompleted         ServiceOutcome = "completed"
	ServiceOutcomeInterrupted       ServiceOutcome = "interrupted"
	ServiceOutcomeNeverStarted      ServiceOutcome = "never_started"
	ServiceOutcomeAbandonedByClient ServiceOutcome = "abandoned_by_client"
	ServiceOutcomeUnknown           ServiceOutcome = "unknown"
)

// ReasoningObservationState separates a request that omitted reasoning controls
// from one whose controls could not be observed reliably.
type ReasoningObservationState string

const (
	ReasoningObservationCaptured    ReasoningObservationState = "captured"
	ReasoningObservationAbsent      ReasoningObservationState = "absent"
	ReasoningObservationInvalid     ReasoningObservationState = "invalid"
	ReasoningObservationAmbiguous   ReasoningObservationState = "ambiguous"
	ReasoningObservationUnsupported ReasoningObservationState = "unsupported"
)

// MaxReasoningValueRunes bounds request-controlled labels without changing
// their decoded spelling or counting multi-byte characters as multiple values.
const MaxReasoningValueRunes = 32

// RequestedReasoningObservation records request-time configuration separately
// from response-derived reasoning token consumption.
type RequestedReasoningObservation struct {
	State        *ReasoningObservationState `gorm:"column:reasoning_observation_state;type:text;default:null" json:"reasoning_observation_state,omitempty"`
	Effort       *string                    `gorm:"column:reasoning_effort;type:text;default:null" json:"reasoning_effort,omitempty"`
	Mode         *string                    `gorm:"column:reasoning_mode;type:text;default:null" json:"reasoning_mode,omitempty"`
	BudgetTokens *int64                     `gorm:"column:reasoning_budget_tokens;default:null" json:"reasoning_budget_tokens,omitempty"`
}

// RequestLog represents the canonical request/session assessment record.
type RequestLog struct {
	ID        uint   `gorm:"primaryKey;autoIncrement" json:"id"`
	RequestID string `gorm:"index" json:"request_id"`
	// ProviderID belongs to the final request/session outcome visible to the client.
	ProviderID       string                  `gorm:"index" json:"provider_id"`
	APIType          string                  `json:"api_type"`
	Model            string                  `json:"model"`
	ClientIP         string                  `json:"client_ip"`
	UserID           string                  `json:"user_id"`
	SemanticsVersion RequestSemanticsVersion `gorm:"type:text;not null;default:normalized_v1;index" json:"semantics_version"`
	// Normalized assessment fields stay nullable at the schema layer so legacy rows
	// can remain explicit legacy data instead of being heuristically rewritten.
	ClientTransportStatusCode *int               `gorm:"default:null" json:"client_transport_status_code"`
	CompletionState           *CompletionState   `gorm:"type:text;default:null" json:"completion_state"`
	ServiceOutcome            *ServiceOutcome    `gorm:"type:text;default:null;index" json:"service_outcome"`
	TerminationActor          *TerminationActor  `gorm:"type:text;default:null" json:"termination_actor"`
	TerminationReason         *TerminationReason `gorm:"type:text;default:null;index" json:"termination_reason"`
	ClientAction              *ClientAction      `gorm:"type:text;default:null;index" json:"client_action"`
	SessionEvidenceJSON       *string            `gorm:"type:text;default:null" json:"session_evidence_json"`
	LatencyMs                 int64              `json:"latency_ms"`
	IsSSE                     bool               `json:"is_sse"`
	// Explicit column tag required: GORM's default snake_case produces "is_web_socket" (3 words),
	// but all API layers (JSON, SQL queries, frontend) expect "is_websocket" (2 words).
	IsWebSocket bool `gorm:"column:is_websocket;default:false" json:"is_websocket"`
	RetryCount  int  `json:"retry_count"`
	IsSticky    bool `json:"is_sticky"`
	// WebSocket lifecycle facts stay nullable so regular HTTP/SSE rows cannot
	// masquerade as concrete WebSocket end-state evidence.
	SessionCommitted *bool `gorm:"default:null" json:"session_committed"`
	// ClientVisible stays explicit because the failover boundary diverges from
	// commitment; collapsing them would make post-visible reconnect diagnostics lie.
	ClientVisible *bool `gorm:"default:null" json:"client_visible"`
	// CommitSource stays explicit because commitment alone does not explain which
	// orthogonal boundary anchored the persisted session lifecycle.
	CommitSource *CommitSource `gorm:"type:text;default:null" json:"commit_source"`
	CreatedAt    time.Time     `gorm:"index" json:"created_at"`
	// The integer key makes range semantics independent of CreatedAt's retained
	// offset-bearing representation and remains exact at half-open boundaries.
	CreatedAtUnixNano *int64 `gorm:"column:created_at_unix_nano;default:null" json:"-"`
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
	PromptTokens             *int64  `gorm:"default:null;index" json:"prompt_tokens,omitempty"`         // Observed provider-reported input token count
	CompletionTokens         *int64  `gorm:"default:null;index" json:"completion_tokens,omitempty"`     // Observed provider-reported output token count
	TotalTokens              *int64  `gorm:"default:null;index" json:"total_tokens,omitempty"`          // Observed provider-reported total token count
	ReasoningTokens          *int64  `gorm:"default:null;index" json:"reasoning_tokens,omitempty"`      // Observed reasoning subset of provider-reported output tokens
	CacheReadInputTokens     *int64  `gorm:"default:null" json:"cache_read_input_tokens,omitempty"`     // Observed input tokens read from a provider cache
	CacheCreationInputTokens *int64  `gorm:"default:null" json:"cache_creation_input_tokens,omitempty"` // Observed input tokens written to a provider cache
	UsageDetails             *string `gorm:"type:text;default:null" json:"usage_details,omitempty"`     // JSON: full usage details (service_tier, TTL breakdown, etc.)
	RequestedReasoningObservation
	// Attempts is populated by API, not stored directly in database.
	// These rows describe per-provider attempts only; the final lifecycle conclusion
	// stays on RequestLog so websocket outcome attribution has a single source of truth.
	Attempts []RequestAttempt `gorm:"-" json:"attempts,omitempty"`
}

// RequestAttemptPhase records which websocket failover window an attempt ended in.
// It stays nullable so HTTP attempts do not need to emulate websocket semantics.
type RequestAttemptPhase string

const (
	RequestAttemptPhasePreAccept             RequestAttemptPhase = "pre_accept"
	RequestAttemptPhasePostUpgradePreVisible RequestAttemptPhase = "post_upgrade_pre_visible"
	RequestAttemptPhaseVisible               RequestAttemptPhase = "visible"
)

// RequestAttemptOutcome records the provider-attempt result without duplicating the
// request-level lifecycle summary that lives on RequestLog.
type RequestAttemptOutcome string

const (
	RequestAttemptOutcomeUpstreamHandshakeRejected RequestAttemptOutcome = "upstream_handshake_rejected"
	RequestAttemptOutcomeUpstreamTransportError    RequestAttemptOutcome = "upstream_transport_error"
	RequestAttemptOutcomeUpstreamSemanticError     RequestAttemptOutcome = "upstream_semantic_error"
	RequestAttemptOutcomeVisibleSession            RequestAttemptOutcome = "visible_session"
	RequestAttemptOutcomeUpstreamCompleted         RequestAttemptOutcome = "upstream_completed"
	RequestAttemptOutcomeUpstreamHTTPStatusError   RequestAttemptOutcome = "upstream_http_status_error"
	RequestAttemptOutcomeUpstreamIncomplete        RequestAttemptOutcome = "upstream_incomplete"
	RequestAttemptOutcomeGatewayError              RequestAttemptOutcome = "gateway_error"
)

// RequestAttemptHealthVerdict and RequestAttemptHealthCause persist the health
// policy conclusion separately from transport outcome. A semantic HTTP 200 can
// therefore remain an upstream semantic error while contributing either a
// neutral or failure verdict according to its rule action.
type RequestAttemptHealthVerdict string

const (
	RequestAttemptHealthSuccess RequestAttemptHealthVerdict = "success"
	RequestAttemptHealthFailure RequestAttemptHealthVerdict = "failure"
	RequestAttemptHealthNeutral RequestAttemptHealthVerdict = "neutral"
)

type RequestAttemptHealthCause string

const (
	RequestAttemptHealthCauseNormalCompletion        RequestAttemptHealthCause = "normal_completion"
	RequestAttemptHealthCauseTransportFailure        RequestAttemptHealthCause = "transport_failure"
	RequestAttemptHealthCauseHTTPStatusFailure       RequestAttemptHealthCause = "http_status_failure"
	RequestAttemptHealthCauseSemanticRetryThenSwitch RequestAttemptHealthCause = "semantic_retry_then_switch"
	RequestAttemptHealthCauseSemanticNeutral         RequestAttemptHealthCause = "semantic_neutral"
	RequestAttemptHealthCauseClientCancelled         RequestAttemptHealthCause = "client_cancelled"
	RequestAttemptHealthCauseIncomplete              RequestAttemptHealthCause = "incomplete"
)

// RequestAttemptSwitchMode keeps replacement and failover explicit on each
// provider attempt so observability never has to infer semantics from reasons or
// neighboring rows.
type RequestAttemptSwitchMode string

const (
	RequestAttemptSwitchModeInitial     RequestAttemptSwitchMode = "initial"
	RequestAttemptSwitchModeReplacement RequestAttemptSwitchMode = "replacement"
	RequestAttemptSwitchModeFailover    RequestAttemptSwitchMode = "failover"
)

// RequestAttempt switch reasons stay free-form because most retries reuse shared
// transport/lifecycle causes, but semantic failover needs a stable persisted label
// that distinguishes "provider-scoped error was suppressed" from generic terminal
// causes shown elsewhere in the UI.
const RequestAttemptSwitchReasonProviderScopedSemanticError = "provider_scoped_semantic_error"

// RequestAttempt represents a single provider attempt within a request.
type RequestAttempt struct {
	ID        uint   `gorm:"primaryKey;autoIncrement" json:"id"`
	RequestID string `gorm:"index" json:"request_id"`
	// ProviderID identifies the provider used for this individual attempt only.
	ProviderID string `json:"provider_id"`
	// SemanticsVersion stays explicit on attempt rows so replaced-attempt evidence
	// keeps the same cutover boundary as the final request assessment.
	SemanticsVersion RequestSemanticsVersion `gorm:"type:text;not null;default:normalized_v1;index" json:"semantics_version"`
	Attempt          int                     `json:"attempt"`
	// SwitchMode records how this provider attempt was entered; switch_reason stays
	// focused on why execution left an attempt.
	SwitchMode          RequestAttemptSwitchMode `gorm:"type:text;default:''" json:"switch_mode,omitempty"`
	ProviderAttempt     int                      `json:"provider_attempt,omitempty"`
	ProviderSwitchCount int                      `json:"provider_switch_count,omitempty"`
	// StatusCode is the upstream transport status. ClientTransportStatusCode is
	// populated only when this attempt committed a response to the client.
	StatusCode                int    `json:"status_code"`
	ClientTransportStatusCode *int   `gorm:"default:null" json:"client_transport_status_code,omitempty"`
	Error                     string `json:"error"`
	// Observed attempt axes stay nullable so legacy and pre-dispatch rows do not
	// fabricate transport or health conclusions.
	Phase                      *RequestAttemptPhase         `gorm:"type:text;default:null" json:"phase,omitempty"`
	Outcome                    *RequestAttemptOutcome       `gorm:"type:text;default:null" json:"outcome,omitempty"`
	ResultVisibleToClient      *bool                        `gorm:"default:null" json:"result_visible_to_client,omitempty"`
	HealthVerdict              *RequestAttemptHealthVerdict `gorm:"type:text;default:null" json:"health_verdict,omitempty"`
	HealthCause                *RequestAttemptHealthCause   `gorm:"type:text;default:null" json:"health_cause,omitempty"`
	AttemptEvidenceJSON        *string                      `gorm:"type:text;default:null" json:"attempt_evidence_json"`
	BodySnippet                string                       `json:"body_snippet,omitempty"`     // First ~512 bytes of error response (failover scenarios only)
	ReqBodySnippet             string                       `json:"req_body_snippet,omitempty"` // First ~512 bytes of request body (error attempts only)
	LatencyMs                  int64                        `json:"latency_ms"`
	SwitchReason               string                       `json:"switch_reason,omitempty"` // Reason for switching to the next provider (if any)
	ContinuitySeeded           bool                         `gorm:"default:false" json:"continuity_seeded,omitempty"`
	ContinuityOriginProviderID string                       `gorm:"default:''" json:"continuity_origin_provider_id,omitempty"`
	ContinuitySeedAgeMs        *int64                       `gorm:"default:null" json:"continuity_seed_age_ms,omitempty"`
	CreatedAt                  time.Time                    `json:"created_at"`
}

// LogFilter represents filter and sort parameters for log queries.
type LogFilter struct {
	ProviderID                string // Filter by provider ID
	APIType                   string // Filter by API type (claude/codex/gemini/grok/custom:*)
	SemanticsVersion          RequestSemanticsVersion
	CompletionState           CompletionState
	ServiceOutcome            ServiceOutcome
	ClientAction              ClientAction
	TerminationActor          TerminationActor
	TerminationReason         TerminationReason
	ClientTransportStatusCode *int
	IsSSE                     *bool        // Filter by SSE/regular request (nil = no filter)
	IsWebSocket               *bool        // Filter by WebSocket/regular request (nil = no filter)
	SessionCommitted          *bool        // Filter by known commitment state on WebSocket rows (nil = no filter; NULL rows stay excluded)
	ClientVisible             *bool        // Filter by the explicit visibility boundary on WebSocket rows (nil = no filter; NULL rows stay excluded)
	CommitSource              CommitSource // Filter by explicit commit source on WebSocket rows (empty = no filter)
	UserID                    string       // Filter by user ID
	StartTime                 *time.Time   // Filter by start time (inclusive)
	EndTime                   *time.Time   // Filter by end time (exclusive)
	MinLatency                *int64       // Filter by minimum latency in ms
	MinRetryCount             *int         // Filter by minimum retry count (e.g., 1 for "has retries")
	HasRetries                *bool        // Filter by has retries (true = retry_count > 0, false = retry_count = 0)
	SortBy                    string       // Sort field: "created_at" or "latency_ms"
	SortOrder                 string       // Sort direction: "asc" or "desc"
	Limit                     int          // Maximum number of results
	Offset                    int          // Offset for pagination
}

// HasWebSocketLifecycleFilter centralizes the rule that lifecycle predicates only
// make sense for WebSocket rows, so query builders and test doubles stay aligned.
func (f LogFilter) HasWebSocketLifecycleFilter() bool {
	return f.SessionCommitted != nil ||
		f.ClientVisible != nil ||
		f.CommitSource != ""
}

// StickyKey represents the cache key for sticky session.
type StickyKey struct {
	IP      string
	User    string
	APIType string
	Model   string
}

// StickyEntry is the durable representation of one sticky binding.
// ExpiresAt is an absolute timestamp so a restart cannot accidentally extend
// a binding's lifetime by applying its TTL from the new process start time.
type StickyEntry struct {
	Key        StickyKey
	ProviderID string
	ExpiresAt  time.Time
}

// SelectRequest represents a provider selection request.
type SelectRequest struct {
	ClientIP   string
	User       string
	APIType    string
	Model      string
	StickyMode StickyMode // Sticky session mode pre-loaded from runtime config
	// SwitchMode keeps replacement and failover explicit so selector isolation only
	// runs when the request has actually left visible continuity.
	SwitchMode SwitchMode
	// ProviderSwitchHistory tracks cross-provider movement within the current
	// request chain. It is orthogonal to continuity provenance.
	ProviderSwitchHistory *ProviderSwitchHistory
	// ProviderContinuityContext is request-local state created only after visible
	// continuity has been attached. It carries vendor-isolation inputs for later
	// failover decisions without reusing shared seed storage.
	ProviderContinuityContext *ProviderContinuityContext
	// VisibleContinuitySeedCandidate is an immutable snapshot from the shared
	// continuity-seed store. It marks a request as a continuity candidate without
	// pre-attaching failover semantics.
	VisibleContinuitySeedCandidate *VisibleContinuitySeedCandidate
	// FailoverContext is retained only as a temporary transport field while runtime
	// propagation migrates. Core model helpers must not infer switch semantics from
	// it; SwitchMode, ProviderSwitchHistory, and ProviderContinuityContext are the
	// authoritative contract.
	FailoverContext *FailoverContext
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
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Details map[string]string `json:"details,omitempty"`
}

// LogStats represents aggregated statistics from request logs.
type LogStats struct {
	TotalRequests int64                    // Total number of normalized requests
	AvgLatencyMs  int64                    // Average latency in milliseconds
	OutcomeCounts map[ServiceOutcome]int64 // Request count by normalized service outcome
	ByAPIType     map[string]int64         // Request count by API type
	ByProvider    []ProviderLogStats       // Request statistics by provider
	EarliestLog   time.Time                // Earliest normalized log timestamp (for "all" period)
}

// ProviderLogStats represents log statistics for a single provider.
type ProviderLogStats struct {
	ProviderID    string                   // Provider ID
	Count         int64                    // Total request count
	OutcomeCounts map[ServiceOutcome]int64 // Request count by normalized service outcome
}

// TimeSeriesPoint represents a single data point in a time series.
type TimeSeriesPoint struct {
	Time          time.Time                `json:"time"`
	Requests      int64                    `json:"total_requests"`
	AvgLatencyMs  int64                    `json:"avg_latency_ms"`
	OutcomeCounts map[ServiceOutcome]int64 `json:"outcome_counts"`
}
