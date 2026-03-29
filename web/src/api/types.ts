// =============================================================================
// Enum Types - Re-exported from constants.ts for convenience
// =============================================================================

import type {
  Strategy,
  AuthMode,
  ProviderCredentialType,
  ProviderUsageLimitPolicy,
  ErrorCode,
  FailoverScope,
} from "../config/constants";
export type {
  Strategy,
  AuthMode,
  ProviderCredentialType,
  ProviderUsageLimitPolicy,
  ErrorCode,
  FailoverScope,
};

/** Built-in API type values (custom:* pattern handled separately) */
export type BuiltInAPIType = "claude" | "codex" | "gemini";

/** Valid configuration keys */
export type ConfigKey =
  | "auth_mode"
  | "user_header"
  | "trust_proxy_headers"
  | "upstream_connect_timeout"
  | "first_byte_timeout"
  | "upstream_read_timeout"
  | "sse_idle_timeout"
  | "sticky_mode"
  | "sticky_ttl"
  | "websocket_probe_client_model"
  | "circuit_failure"
  | "circuit_window"
  | "circuit_disable"
  | "max_body_size"
  | "global_max_attempts"
  | "log_retention_days"
  | "inter_group_strategy";

/** Response from GET /config API */
export interface ConfigResponse {
  /** Default configuration values (all keys with their defaults) */
  defaults: Record<string, string>;
  /** User-modified configuration values (only modified keys) */
  values: Record<string, string>;
}

/** Standard API error response */
export interface ErrorResponse {
  code: ErrorCode;
  message: string;
}

// =============================================================================
// Provider Types
// =============================================================================

/**
 * BackoffPolicy defines exponential backoff behavior for same-provider retries.
 * Duration values are strings in Go duration format (e.g., "100ms", "5s").
 */
export interface BackoffPolicy {
  /** Initial delay before first retry (e.g., "100ms") */
  initial_delay: string;
  /** Maximum delay cap (e.g., "5s") */
  max_delay: string;
  /** Exponential multiplier (default: 2.0) */
  multiplier?: number;
  /** Enable Equal Jitter mode: delay = calculated_delay/2 + random[0, calculated_delay/2] */
  jitter?: boolean;
}

// ProviderAPIType represents the association between Provider and API types.
// Each entry can override both endpoint and credentials for that API contract.
export interface ProviderAPIType {
  provider_id: string;
  api_type: string;
  base_url: string;
  api_key?: string;
}

export type ProviderAuthStatus = "not_connected" | "active" | "reauth_required";

export interface ProviderAuthView {
  type: ProviderCredentialType;
  status: ProviderAuthStatus;
  reason?: string;
  email?: string;
  account_id?: string;
  plan_type?: string;
  usage?: ProviderUsageSnapshot | null;
  expires_at?: string;
  last_refresh_at?: string;
  last_error?: string;
}

export interface ProviderUsageWindow {
  used_percent: number;
  window_seconds: number;
  reset_at?: string;
}

export interface ProviderUsageSnapshot {
  fetched_at?: string;
  plan_type?: string;
  five_hour?: ProviderUsageWindow | null;
  one_week?: ProviderUsageWindow | null;
}

export interface Provider {
  id: string;
  name: string;
  api_key: string;
  api_types: ProviderAPIType[];
  auth_mode: AuthMode;
  credential_type: ProviderCredentialType;
  usage_limit_policy?: ProviderUsageLimitPolicy;
  usage_limit_policy_explicit?: boolean;
  group_id: string | null;
  weight: number;
  priority: number;
  concurrency: number;
  max_retries: number;
  /** Exponential backoff policy for same-provider retries */
  backoff?: BackoffPolicy;
  /** Vendor identifier for failover isolation. Empty = no isolation, "*" = wildcard */
  vendor: string;
  /** Outbound failover control: where we can failover TO after this provider fails */
  failover_scope: FailoverScope;
  /** Inbound failover control: which sources we accept failover FROM */
  accept_failover: FailoverScope;
  enabled: boolean;
  created_at: string;
  updated_at: string;
  health?: HealthState | null;
  auth?: ProviderAuthView | null;
}

/** API type entry with endpoint/auth overrides, matching backend APITypeInput */
export interface APITypeInput {
  api_type: string;
  base_url: string;
  api_key?: string;
}

export interface ProviderInput {
  id?: string;
  name: string;
  api_key: string;
  api_types: APITypeInput[];
  auth_mode?: AuthMode;
  credential_type?: ProviderCredentialType;
  usage_limit_policy?: ProviderUsageLimitPolicy;
  credential_login_id?: string;
  group_id?: string | null;
  weight?: number;
  priority?: number;
  concurrency?: number;
  max_retries?: number;
  /** Exponential backoff policy for same-provider retries */
  backoff?: BackoffPolicy;
  /** Vendor identifier for failover isolation */
  vendor?: string;
  /** Outbound failover control */
  failover_scope?: FailoverScope;
  /** Inbound failover control */
  accept_failover?: FailoverScope;
  enabled?: boolean;
}

export interface ChatGPTLoginStartResponse {
  login_id: string;
  auth_url: string;
}

export type ChatGPTLoginStatus = "pending" | "completed" | "expired";

export interface ChatGPTLoginStatusResponse {
  login_id: string;
  status: ChatGPTLoginStatus;
  auth?: ProviderAuthView | null;
}

export type RoutingPolicyModelMatchType = "exact" | "prefix";

export interface RoutingPolicy {
  id: string;
  api_type: string;
  enabled: boolean;
  model_match_type?: RoutingPolicyModelMatchType | null;
  model_match_value?: string | null;
  target_provider_id: string | null;
  allowed_group_ids: string[];
  allowed_vendors: string[];
  created_at?: string;
  updated_at?: string;
}

export interface RoutingPolicyInput {
  api_type: string;
  enabled: boolean;
  model_match_type?: RoutingPolicyModelMatchType | null;
  model_match_value?: string | null;
  target_provider_id: string | null;
  allowed_group_ids: string[];
  allowed_vendors: string[];
}

// =============================================================================
// Group Types
// =============================================================================

export interface Group {
  id: string;
  name: string;
  strategy: Strategy;
  priority: number;
  weight: number;
  enabled: boolean;
  created_at: string;
  updated_at: string;
  providers?: Provider[];
}

export interface GroupInput {
  id?: string;
  name: string;
  strategy?: Strategy;
  priority?: number;
  weight?: number;
  enabled?: boolean;
}

export interface HealthState {
  provider_id: string;
  available: boolean;
  success_count: number;
  fail_count: number;
  last_success: string | null;
  last_failure: string | null;
  last_error: string | null;
  disabled_until: string | null;
  disabled_reason: string | null;
}

// ProviderStatus represents the status of a single provider
export interface ProviderStatus {
  id: string;
  name: string;
  enabled: boolean;
  current_requests: number;
  health: HealthState | null;
}

// SystemStatus represents the overall system status (raw backend response)
export interface SystemStatus {
  providers: ProviderStatus[];
}

// SystemStatusSummary represents computed summary statistics
export interface SystemStatusSummary {
  providers_total: number;
  providers_healthy: number;
  providers_unhealthy: number;
  requests_today: number;
}

// =============================================================================
// Live Request Monitoring Types
// =============================================================================

/** Represents an active in-flight request being processed */
export interface ActiveRequest {
  request_id: string;
  provider_id: string;
  model: string;
  api_type: string;
  user_id: string;
  client_ip: string;
  is_sse: boolean;
  is_websocket: boolean;
  started_at: string; // ISO timestamp
  bytes_sent?: number;
  bytes_received?: number;
  msgs_sent?: number;
  msgs_received?: number;
  last_activity_at?: number; // Unix ms, 0 = no activity yet
}

export type RequestAttemptSwitchReason =
  | "circuit_breaker_triggered"
  | "max_retries_exhausted"
  | "usage_limit_reached"
  | "permanent_error_401"
  | "permanent_error_402"
  | "permanent_error_403"
  | "provider_scoped_semantic_error"
  | (string & {});

export type RequestAttemptPhase =
  | "pre_accept"
  | "post_upgrade_pre_visible"
  | "visible";

export type RequestAttemptOutcome =
  | "upstream_handshake_rejected"
  | "upstream_transport_error"
  | "upstream_semantic_error"
  | "visible_session";

/**
 * Represents a single provider attempt within a request.
 *
 * WebSocket lifecycle attribution stays on RequestLog. Attempt rows only carry
 * provider-scoped dial / handshake / error detail for the UI timeline.
 */
export interface RequestAttempt {
  id: number;
  request_id: string;
  provider_id: string;
  /** Backend ordinal as recorded; UIs should normalize display instead of assuming a base. */
  attempt: number;
  status_code: number;
  error: string;
  phase?: RequestAttemptPhase | null;
  outcome?: RequestAttemptOutcome | null;
  result_visible_to_client?: boolean | null;
  body_snippet?: string; // First ~512 bytes of error response
  req_body_snippet?: string; // First ~512 bytes of request body (error attempts only)
  latency_ms: number;
  switch_reason?: RequestAttemptSwitchReason; // Reason for switching to next provider (if any)
  created_at: string; // ISO timestamp
}

/** Response for active requests API */
export interface ActiveRequestsResponse {
  requests: ActiveRequest[];
  count: number;
}

// =============================================================================
// Token Usage Types
// =============================================================================

/**
 * Extra usage details from API response (parsed JSON object).
 * Supports known fields and allows extension for unknown provider-specific fields.
 */
export interface UsageDetails {
  /** Service tier (e.g., "default", "flex") */
  service_tier?: string;
  /** Claude: ephemeral 1-hour input tokens */
  ephemeral_1h_input_tokens?: number;
  /** Claude: ephemeral 5-minute input tokens */
  ephemeral_5m_input_tokens?: number;
  /** OpenAI o1 series: reasoning tokens */
  reasoning_tokens?: number;
  /** OpenAI: cached tokens (GPT-4 Turbo+) */
  cached_tokens?: number;
  /** Allow extension for unknown provider-specific fields */
  [key: string]: unknown;
}

// =============================================================================
// Request Log Types
// =============================================================================

export type TerminalCause =
  | "unknown"
  | "provider_unavailable"
  | "provider_configuration_error"
  | "clean_close"
  | "client_disconnect"
  | "upstream_transport_error"
  | "upstream_semantic_error"
  | "upstream_handshake_rejected"
  | "client_upgrade_rejected"
  | "internal_error";

export type CommitSource = "semantic_event" | "upstream_message" | "unknown";

export type RecoveryAction =
  | "none"
  | "transparent_retry"
  | "reconnect_required";

export type WebSocketProbeOutcome =
  | "unknown"
  | "bypassed"
  | "demand_resolution_failed"
  | "unsupported"
  | "observed_usable_model"
  | "completed_without_usable_model"
  | "transport_failed";

export interface RequestLog {
  id: number;
  request_id: string;
  /** Final provider attribution for the request lifecycle, including WebSocket sessions. */
  provider_id: string;
  api_type: string;
  model: string;
  client_ip: string;
  user_id: string;
  status_code: number;
  latency_ms: number;
  success: boolean;
  is_sse: boolean;
  is_websocket: boolean;
  retry_count: number;
  is_sticky: boolean;
  error_msg: string | null;
  created_at: string;
  // Phase 1 diagnostic fields (P0)
  request_path?: string; // Relative path like /v1/messages
  request_method?: string; // HTTP method: GET/POST/PUT/DELETE
  user_agent?: string; // Client User-Agent (truncated to 512 chars)
  request_id_header?: string; // Client's X-Request-ID for tracing
  // Phase 2 diagnostic fields (P1)
  first_token_ms?: number | null; // Time to first token for SSE requests (ms), null for non-SSE
  // Phase 3 transfer statistics (P2)
  request_bytes?: number; // Request body size in bytes
  response_bytes?: number; // Response body size in bytes
  content_type?: string; // Request Content-Type header
  // Phase 4 token usage statistics (P3)
  // Uses null to distinguish "unknown/parse failed" from explicit 0
  prompt_tokens?: number | null; // Input tokens (OpenAI: prompt_tokens, Claude: input_tokens)
  completion_tokens?: number | null; // Output tokens (OpenAI: completion_tokens, Claude: output_tokens)
  total_tokens?: number | null; // Total tokens
  cache_read_input_tokens?: number | null; // Claude: tokens read from cache (billed at 10%)
  cache_creation_input_tokens?: number | null; // Claude: tokens written to cache (billed at 125%)
  usage_details?: UsageDetails | null; // Parsed usage details (service_tier, etc.)
  // WebSocket lifecycle semantics (nullable outside the WebSocket lifecycle domain)
  sticky_written?: boolean | null;
  session_committed?: boolean | null;
  client_visible?: boolean | null;
  probe_outcome?: WebSocketProbeOutcome | null;
  terminal_cause?: TerminalCause | null;
  commit_source?: CommitSource | null;
  recovery_action?: RecoveryAction | null;
  // Provider-attempt records only. RequestLog remains the session lifecycle summary.
  attempts?: RequestAttempt[];
}

// LogsResponse represents the paginated logs response from the backend
export interface LogsResponse {
  logs: RequestLog[];
  total: number;
  limit: number;
  offset: number;
  sort_by: string;
  sort_order: string;
}

// LogFilter represents filter parameters for log queries
export interface LogFilter {
  /** Max results (default: 100, max: 1000) */
  limit?: number;
  /** Pagination offset (default: 0) */
  offset?: number;
  /** Filter by provider ID */
  provider_id?: string;
  /** Filter by API type (claude/codex/gemini/custom:*) */
  api_type?: string;
  /** Filter by success/failure */
  success?: boolean;
  /** Filter by SSE/regular request */
  is_sse?: boolean;
  /** Filter by WebSocket connection */
  is_websocket?: boolean;
  /** Filter by user ID */
  user_id?: string;
  /** Filter by start time (RFC3339 format) */
  start_time?: string;
  /** Filter by end time (RFC3339 format) */
  end_time?: string;
  /** Filter by minimum latency in ms */
  min_latency?: number;
  /** Filter by minimum retry count */
  min_retry_count?: number;
  /** Filter by has retries (true = retry_count > 0, false = retry_count = 0) */
  has_retries?: boolean;
  /** Filter by whether a WebSocket session committed upstream service */
  session_committed?: boolean;
  /** Filter by whether a WebSocket session crossed the client-visible boundary */
  client_visible?: boolean;
  /** Filter by whether a WebSocket session wrote sticky affinity */
  sticky_written?: boolean;
  /** Filter by WebSocket probe outcome */
  probe_outcome?: WebSocketProbeOutcome;
  /** Filter by WebSocket terminal cause */
  terminal_cause?: TerminalCause;
  /** Filter by WebSocket commit source */
  commit_source?: CommitSource;
  /** Filter by session-level recovery action */
  recovery_action?: RecoveryAction;
  /** Sort field (created_at/latency_ms, default: created_at) */
  sort_by?: "created_at" | "latency_ms";
  /** Sort direction (asc/desc, default: desc) */
  sort_order?: "asc" | "desc";
}

// Batch operations types

/** Valid batch action values */
export type BatchAction = "reset" | "enable" | "disable" | "delete";

/** Request for batch provider operations */
export interface BatchProviderRequest {
  action: BatchAction;
  ids: string[];
}

/** Result of a single provider operation in a batch */
export interface BatchProviderResult {
  id: string;
  success: boolean;
  error?: string;
}

/** Response for batch provider operations */
export interface BatchProviderResponse {
  success: boolean;
  affected: number;
  results: BatchProviderResult[];
}

// Stats API types

/** Valid period values for stats API */
export type StatsPeriod = "24h" | "7d" | "30d" | "all";

/** Valid granularity values for stats API */
export type StatsGranularity = "5m" | "15m" | "1h" | "6h" | "1d";

// StatsParams represents query parameters for stats API
export interface StatsParams {
  /** Statistics time range (default: 24h) */
  period?: StatsPeriod;
  /** Time bucket size for time series (optional) */
  granularity?: StatsGranularity;
}

// ProviderStats represents provider health statistics
export interface ProviderStats {
  total: number;
  healthy: number;
  unhealthy: number;
  disabled: number;
}

// ProviderRequestStats represents request statistics for a single provider
export interface ProviderRequestStats {
  id: string;
  name: string;
  count: number;
  success_rate: number;
}

// TimeRange represents the time range for statistics
export interface TimeRange {
  start: string;
  end: string;
}

// TimeSeriesPoint represents a single data point in a time series
export interface TimeSeriesPoint {
  time: string;
  requests: number;
  success_count: number;
  fail_count: number;
  success_rate: number;
  avg_latency_ms: number;
}

// StatsResponse represents the response for the stats API
export interface StatsResponse {
  total_requests: number;
  success_count: number;
  fail_count: number;
  success_rate: number;
  avg_latency_ms: number;
  providers: ProviderStats;
  requests_by_api_type: Record<string, number>;
  requests_by_provider: ProviderRequestStats[];
  time_range: TimeRange;
  timeseries?: TimeSeriesPoint[];
}

// Config Export/Import types

/** ExportedAPIType represents an API type with its base URL in the export format */
export interface ExportedAPIType {
  api_type: string;
  base_url: string;
  api_key?: string;
}

/** ExportedProvider represents a provider in the export format */
export interface ExportedProvider {
  id: string;
  name: string;
  api_key: string;
  api_types: ExportedAPIType[];
  auth_mode: AuthMode;
  credential_type?: ProviderCredentialType;
  usage_limit_policy?: ProviderUsageLimitPolicy;
  group_id?: string | null;
  weight: number;
  priority: number;
  concurrency: number;
  max_retries: number;
  /** Exponential backoff policy for same-provider retries */
  backoff?: BackoffPolicy;
  vendor?: string;
  failover_scope?: FailoverScope;
  accept_failover?: FailoverScope;
  enabled: boolean;
}

/** ExportedGroup represents a group in the export format */
export interface ExportedGroup {
  id: string;
  name: string;
  strategy: Strategy;
  priority: number;
  weight: number;
  enabled: boolean;
}

/** ExportedRoutingPolicy represents routing policy behavior in the export format */
export interface ExportedRoutingPolicy {
  api_type: string;
  enabled: boolean;
  model_match_type?: RoutingPolicyModelMatchType | null;
  model_match_value?: string | null;
  target_provider_id?: string | null;
  allowed_group_ids: string[];
  allowed_vendors: string[];
}

/** ExportedConfig represents the full exported configuration */
export interface ExportedConfig {
  version: string;
  exported_at: string;
  providers: ExportedProvider[];
  groups: ExportedGroup[];
  routing_policies: ExportedRoutingPolicy[];
  settings: Partial<Record<ConfigKey, string>>;
}

/** ImportConfigRequest represents the request body for config import */
export interface ImportConfigRequest {
  version?: string;
  providers: ExportedProvider[];
  groups: ExportedGroup[];
  routing_policies: ExportedRoutingPolicy[];
  settings: Partial<Record<ConfigKey, string>>;
}

/** ChangeCount represents add/update/delete counts */
export interface ChangeCount {
  add: number;
  update: number;
  delete: number;
}

/** ImportChanges represents the changes that will be applied during import */
export interface ImportChanges {
  providers: ChangeCount;
  groups: ChangeCount;
  routing_policies: ChangeCount;
  settings: ChangeCount;
}

/** ImportPreviewResponse is the response for dry_run=true */
export interface ImportPreviewResponse {
  dry_run: boolean;
  changes: ImportChanges;
  warnings: string[];
}

/** AppliedCount represents added/updated counts for applied changes */
export interface AppliedCount {
  added: number;
  updated: number;
}

/** ImportedCounts represents the counts of successfully imported items */
export interface ImportedCounts {
  providers: AppliedCount;
  groups: AppliedCount;
  routing_policies: AppliedCount;
  settings: AppliedCount;
}

/** ImportResult represents the result of an actual import */
export interface ImportResult {
  success: boolean;
  applied: ImportedCounts;
}
