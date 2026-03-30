import { API_BASE, STORAGE_KEYS } from "../config";
import {
  type ApiClientDeps,
  type Storage,
  browserStorage,
  browserHttpClient,
} from "./interfaces";
import type {
  Provider,
  ProviderInput,
  RoutingPolicy,
  RoutingPolicyInput,
  Group,
  GroupInput,
  HealthState,
  SystemStatus,
  LogsResponse,
  LogFilter,
  StatsParams,
  StatsResponse,
  BatchProviderRequest,
  BatchProviderResponse,
  ExportedConfig,
  ImportConfigRequest,
  ImportPreviewResponse,
  ImportResult,
  ConfigResponse,
  ActiveRequestsResponse,
  RequestLog,
  ChatGPTLoginStartResponse,
  ChatGPTLoginStatusResponse,
} from "./types";

// Re-export types for consumers
export type {
  BackoffPolicy,
  Provider,
  ProviderAuthStatus,
  ProviderAuthView,
  ProviderInput,
  RoutingPolicy,
  RoutingPolicyInput,
  RoutingPolicyModelMatchType,
  Group,
  GroupInput,
  HealthState,
  ProviderStatus,
  SystemStatus,
  SystemStatusSummary,
  RequestLog,
  LogsResponse,
  LogFilter,
  StatsParams,
  StatsResponse,
  BatchAction,
  BatchProviderRequest,
  BatchProviderResponse,
  ExportedRoutingPolicy,
  ExportedConfig,
  FullImportScope,
  ImportMode,
  ImportConfigRequest,
  ImportPreviewResponse,
  ImportResult,
  ImportScope,
  SelectionImportScope,
  SettingsOnlyImportScope,
  ConfigResponse,
  ActiveRequest,
  ActiveRequestsResponse,
  ChatGPTLoginStartResponse,
  ChatGPTLoginStatusResponse,
  RecoveryAction,
  RequestAttemptOutcome,
  RequestAttemptPhase,
  RequestAttempt,
} from "./types";

// API Error type
export class ApiError extends Error {
  code: string;
  status: number;

  constructor(code: string, message: string, status: number) {
    super(message);
    this.name = "ApiError";
    this.code = code;
    this.status = status;
  }
}

// Token management factory
export function createTokenManager(storage: Storage) {
  return {
    get: (): string | null => storage.getItem(STORAGE_KEYS.AUTH_TOKEN),
    set: (token: string): void =>
      storage.setItem(STORAGE_KEYS.AUTH_TOKEN, token),
    clear: (): void => storage.removeItem(STORAGE_KEYS.AUTH_TOKEN),
  };
}

// Build query string for logs API
function appendLogQueryParam(
  query: URLSearchParams,
  key: string,
  value: string | number | boolean | undefined | null,
): void {
  if (value === undefined || value === null || value === "") {
    return;
  }
  query.set(key, String(value));
}

function buildLogsQuery(filter?: LogFilter): string {
  const query = new URLSearchParams();
  if (!filter) {
    return query.toString();
  }

  appendLogQueryParam(query, "limit", filter.limit);
  appendLogQueryParam(query, "offset", filter.offset);
  appendLogQueryParam(query, "provider_id", filter.provider_id);
  appendLogQueryParam(query, "api_type", filter.api_type);
  appendLogQueryParam(query, "success", filter.success);
  appendLogQueryParam(query, "is_sse", filter.is_sse);
  appendLogQueryParam(query, "is_websocket", filter.is_websocket);
  appendLogQueryParam(query, "user_id", filter.user_id);
  appendLogQueryParam(query, "start_time", filter.start_time);
  appendLogQueryParam(query, "end_time", filter.end_time);
  appendLogQueryParam(query, "min_latency", filter.min_latency);
  appendLogQueryParam(query, "min_retry_count", filter.min_retry_count);
  appendLogQueryParam(query, "has_retries", filter.has_retries);
  appendLogQueryParam(query, "session_committed", filter.session_committed);
  appendLogQueryParam(query, "client_visible", filter.client_visible);
  appendLogQueryParam(query, "sticky_written", filter.sticky_written);
  appendLogQueryParam(query, "probe_outcome", filter.probe_outcome);
  appendLogQueryParam(query, "terminal_cause", filter.terminal_cause);
  appendLogQueryParam(query, "commit_source", filter.commit_source);
  appendLogQueryParam(query, "recovery_action", filter.recovery_action);
  appendLogQueryParam(query, "sort_by", filter.sort_by);
  appendLogQueryParam(query, "sort_order", filter.sort_order);
  return query.toString();
}

// Build query string for stats API
function buildStatsQuery(params?: StatsParams): string {
  const query = new URLSearchParams();
  if (params?.period) query.set("period", params.period);
  if (params?.granularity) query.set("granularity", params.granularity);
  return query.toString();
}

type RoutingPolicyResponse = Pick<RoutingPolicy, "id" | "api_type"> &
  Partial<RoutingPolicy>;

function normalizeStringList(values?: string[] | null): string[] {
  return Array.from(
    new Set(
      (values ?? [])
        .map((value) => value.trim())
        .filter((value) => value !== ""),
    ),
  );
}

function normalizeRoutingPolicy(policy: RoutingPolicyResponse): RoutingPolicy {
  const targetProviderId = policy.target_provider_id ?? null;

  return {
    ...policy,
    // Older backend builds may not emit the refactored lifecycle fields yet.
    // Normalizing here lets the admin UI speak the new resource shape without
    // scattering compatibility defaults through every caller.
    enabled: policy.enabled ?? true,
    model_match_type: policy.model_match_type ?? null,
    model_match_value: policy.model_match_value ?? null,
    target_provider_id: targetProviderId,
    allowed_group_ids: targetProviderId
      ? []
      : normalizeStringList(policy.allowed_group_ids),
    allowed_vendors: targetProviderId
      ? []
      : normalizeStringList(policy.allowed_vendors),
  };
}

// Request factory with dependency injection
function createRequest(
  deps: ApiClientDeps,
  tokenManager: ReturnType<typeof createTokenManager>,
) {
  const { httpClient, baseUrl, onUnauthorized } = deps;

  return async function request<T>(
    endpoint: string,
    options: RequestInit = {},
  ): Promise<T> {
    const token = tokenManager.get();
    const headers: Record<string, string> = {
      "Content-Type": "application/json",
      ...(options.headers as Record<string, string>),
    };

    if (token) {
      headers["Authorization"] = `Bearer ${token}`;
    }

    const response = await httpClient.fetch(`${baseUrl}${endpoint}`, {
      ...options,
      headers,
    });

    if (!response.ok) {
      const data = await response.json().catch(() => ({}));

      // Handle 401 Unauthorized - clear token and redirect to login
      if (response.status === 401) {
        tokenManager.clear();
        onUnauthorized?.();
      }

      throw new ApiError(
        data.code || "UNKNOWN_ERROR",
        data.message || response.statusText,
        response.status,
      );
    }

    // 204 No Content
    if (response.status === 204) {
      return undefined as T;
    }

    return response.json();
  };
}

// Type for authenticated request function (handles token injection)
type AuthenticatedRequestFn = <T>(
  endpoint: string,
  options?: RequestInit,
) => Promise<T>;

// =============================================================================
// API Module Organization Standard:
// - Factory functions: APIs with ≥5 methods (providers, groups)
// - Inline definitions: APIs with <5 methods (config, status, logs, stats)
// =============================================================================

// Create providers API object
function createProvidersApi(request: AuthenticatedRequestFn) {
  return {
    list: () => request<Provider[]>("/providers"),
    get: (id: string) => request<Provider>(`/providers/${id}`),
    create: (data: ProviderInput) =>
      request<Provider>("/providers", {
        method: "POST",
        body: JSON.stringify(data),
      }),
    update: (id: string, data: ProviderInput) =>
      request<Provider>(`/providers/${id}`, {
        method: "PUT",
        body: JSON.stringify(data),
      }),
    delete: (id: string) =>
      request<void>(`/providers/${id}`, { method: "DELETE" }),
    enable: (id: string) =>
      request<Provider>(`/providers/${id}/enable`, { method: "POST" }),
    disable: (id: string) =>
      request<Provider>(`/providers/${id}/disable`, { method: "POST" }),
    reset: (id: string) =>
      request<HealthState>(`/providers/${id}/reset`, { method: "POST" }),
    startChatGPTLogin: () =>
      request<ChatGPTLoginStartResponse>("/provider-auth/chatgpt/start", {
        method: "POST",
      }),
    getChatGPTLoginStatus: (loginId: string) =>
      request<ChatGPTLoginStatusResponse>(
        `/provider-auth/chatgpt/sessions/${encodeURIComponent(loginId)}`,
      ),
    refreshCredential: (id: string) =>
      request<void>(`/providers/${id}/refresh-credential`, { method: "POST" }),
    refreshUsage: (id: string) =>
      request<void>(`/providers/${id}/refresh-usage`, { method: "POST" }),
    batch: (data: BatchProviderRequest) =>
      request<BatchProviderResponse>("/providers/batch", {
        method: "POST",
        body: JSON.stringify(data),
      }),
  };
}

// Create groups API object
function createGroupsApi(request: AuthenticatedRequestFn) {
  return {
    list: () => request<Group[]>("/groups"),
    get: (id: string) => request<Group>(`/groups/${id}`),
    create: (data: GroupInput) =>
      request<Group>("/groups", { method: "POST", body: JSON.stringify(data) }),
    update: (id: string, data: GroupInput) =>
      request<Group>(`/groups/${id}`, {
        method: "PUT",
        body: JSON.stringify(data),
      }),
    delete: (id: string) =>
      request<void>(`/groups/${id}`, { method: "DELETE" }),
    enable: (id: string) =>
      request<Group>(`/groups/${id}/enable`, { method: "POST" }),
    disable: (id: string) =>
      request<Group>(`/groups/${id}/disable`, { method: "POST" }),
  };
}

function createRoutingPoliciesApi(request: AuthenticatedRequestFn) {
  return {
    list: async () =>
      (await request<RoutingPolicyResponse[]>("/routing-policies")).map(
        normalizeRoutingPolicy,
      ),
    get: async (id: string) =>
      normalizeRoutingPolicy(
        await request<RoutingPolicyResponse>(`/routing-policies/${id}`),
      ),
    create: async (data: RoutingPolicyInput) =>
      normalizeRoutingPolicy(
        await request<RoutingPolicyResponse>("/routing-policies", {
          method: "POST",
          body: JSON.stringify(data),
        }),
      ),
    update: async (id: string, data: RoutingPolicyInput) =>
      normalizeRoutingPolicy(
        await request<RoutingPolicyResponse>(`/routing-policies/${id}`, {
          method: "PUT",
          body: JSON.stringify(data),
        }),
      ),
    delete: (id: string) =>
      request<void>(`/routing-policies/${id}`, { method: "DELETE" }),
  };
}

// API Client factory with dependency injection
export function createApiClient(deps: ApiClientDeps) {
  const tokenManager = createTokenManager(deps.storage);
  const request = createRequest(deps, tokenManager);

  return {
    setToken: tokenManager.set,
    clearToken: tokenManager.clear,
    getToken: tokenManager.get,
    validateToken: async (token: string): Promise<boolean> => {
      const { httpClient, baseUrl } = deps;
      try {
        const response = await httpClient.fetch(`${baseUrl}/status`, {
          headers: {
            "Content-Type": "application/json",
            Authorization: `Bearer ${token}`,
          },
        });
        return response.ok;
      } catch {
        return false;
      }
    },
    providers: createProvidersApi(request),
    routingPolicies: createRoutingPoliciesApi(request),
    groups: createGroupsApi(request),
    config: {
      get: () => request<ConfigResponse>("/config"),
      update: (data: Record<string, string>) =>
        request<ConfigResponse>("/config", {
          method: "PUT",
          body: JSON.stringify(data),
        }),
      export: () => request<ExportedConfig>("/config/export"),
      importPreview: (data: ImportConfigRequest) =>
        request<ImportPreviewResponse>("/config/import?dry_run=true", {
          method: "POST",
          body: JSON.stringify(data),
        }),
      import: (data: ImportConfigRequest) =>
        request<ImportResult>("/config/import", {
          method: "POST",
          body: JSON.stringify(data),
        }),
    },
    status: {
      get: () => request<SystemStatus>("/status"),
      health: () => request<HealthState[]>("/health"),
    },
    logs: {
      list: (filter?: LogFilter) => {
        const queryStr = buildLogsQuery(filter);
        return request<LogsResponse>(queryStr ? `/logs?${queryStr}` : "/logs");
      },
      get: (id: number) => request<RequestLog>(`/logs/${id}`),
    },
    stats: {
      get: (params?: StatsParams) => {
        const queryStr = buildStatsQuery(params);
        return request<StatsResponse>(
          queryStr ? `/stats?${queryStr}` : "/stats",
        );
      },
    },
    requests: {
      active: () => request<ActiveRequestsResponse>("/requests/active"),
    },
  };
}

// Type for the API client instance
export type ApiClient = ReturnType<typeof createApiClient>;

// Default browser handler for 401 Unauthorized - redirects to login page
function browserUnauthorizedHandler(): void {
  // Use window.location to redirect to login, preserving current path for redirect back
  const currentPath = window.location.pathname + window.location.search;
  const loginUrl = `/admin/login?from=${encodeURIComponent(currentPath)}`;
  window.location.href = loginUrl;
}

// Default instance for convenience (browser environment)
const defaultDeps: ApiClientDeps = {
  storage: browserStorage,
  httpClient: browserHttpClient,
  baseUrl: API_BASE,
  onUnauthorized: browserUnauthorizedHandler,
};

export const api = createApiClient(defaultDeps);
export const setToken = api.setToken;
export const clearToken = api.clearToken;
export const getToken = api.getToken;
