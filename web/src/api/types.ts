// =============================================================================
// Enum Types (synced with internal/admin/constants.go)
// =============================================================================

/** Valid strategy values for group selection */
export type Strategy = "priority" | "random" | "weight";

/** Valid authentication mode values */
export type AuthMode = "auto" | "bearer" | "x-api-key";

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
  | "sticky_enabled"
  | "sticky_ttl"
  | "circuit_failure"
  | "circuit_window"
  | "circuit_disable"
  | "max_body_size"
  | "max_retries"
  | "log_retention_days"
  | "inter_group_strategy";

/** Response from GET /config API */
export interface ConfigResponse {
  /** Default configuration values (all keys with their defaults) */
  defaults: Record<string, string>;
  /** User-modified configuration values (only modified keys) */
  values: Record<string, string>;
}

/** API error codes */
export type ErrorCode =
  | "VALIDATION_ERROR"
  | "INTERNAL_ERROR"
  | "NOT_FOUND"
  | "CONFLICT"
  | "UNAUTHORIZED";

/** Standard API error response */
export interface ErrorResponse {
  code: ErrorCode;
  message: string;
}

// =============================================================================
// Provider Types
// =============================================================================

// ProviderAPIType represents the association between Provider and API types
export interface ProviderAPIType {
  provider_id: string;
  api_type: string;
}

export interface Provider {
  id: string;
  name: string;
  base_url: string;
  api_key: string;
  api_types: ProviderAPIType[];
  auth_mode: AuthMode;
  group_id: string | null;
  weight: number;
  priority: number;
  concurrency: number;
  max_retries: number;
  enabled: boolean;
  created_at: string;
  updated_at: string;
  health?: HealthState | null;
}

export interface ProviderInput {
  id?: string;
  name: string;
  base_url: string;
  api_key: string;
  api_types: string[];
  auth_mode?: AuthMode;
  group_id?: string | null;
  weight?: number;
  priority?: number;
  concurrency?: number;
  max_retries?: number;
  enabled?: boolean;
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

export interface RequestLog {
  id: number;
  provider_id: string;
  api_type: string;
  model: string;
  client_ip: string;
  user_id: string;
  status_code: number;
  latency_ms: number;
  success: boolean;
  error_msg: string | null;
  created_at: string;
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
  /** Filter by user ID */
  user_id?: string;
  /** Filter by start time (RFC3339 format) */
  start_time?: string;
  /** Filter by end time (RFC3339 format) */
  end_time?: string;
  /** Filter by minimum latency in ms */
  min_latency?: number;
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

/** ExportedProvider represents a provider in the export format */
export interface ExportedProvider {
  id: string;
  name: string;
  base_url: string;
  api_key: string;
  api_types: string[];
  auth_mode: AuthMode;
  group_id?: string | null;
  weight: number;
  priority: number;
  concurrency: number;
  max_retries: number;
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

/** ExportedConfig represents the full exported configuration */
export interface ExportedConfig {
  version: string;
  exported_at: string;
  providers: ExportedProvider[];
  groups: ExportedGroup[];
  settings: Partial<Record<ConfigKey, string>>;
}

/** ImportConfigRequest represents the request body for config import */
export interface ImportConfigRequest {
  version?: string;
  providers: ExportedProvider[];
  groups: ExportedGroup[];
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
  settings: AppliedCount;
}

/** ImportResult represents the result of an actual import */
export interface ImportResult {
  success: boolean;
  applied: ImportedCounts;
}
