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
  auth_mode: string;
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
  auth_mode?: string;
  group_id?: string | null;
  weight?: number;
  priority?: number;
  concurrency?: number;
  max_retries?: number;
  enabled?: boolean;
}

export interface Group {
  id: string;
  name: string;
  strategy: string;
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
  strategy?: string;
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
