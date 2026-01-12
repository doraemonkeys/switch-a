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
}
