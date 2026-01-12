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
  Group,
  GroupInput,
  HealthState,
  SystemStatus,
  LogsResponse,
  LogFilter,
  StatsParams,
  StatsResponse,
} from "./types";

// Re-export types for backward compatibility
export type {
  Provider,
  ProviderAPIType,
  ProviderInput,
  Group,
  GroupInput,
  HealthState,
  ProviderStatus,
  SystemStatus,
  SystemStatusSummary,
  RequestLog,
  LogsResponse,
  LogFilter,
  StatsPeriod,
  StatsGranularity,
  StatsParams,
  ProviderStats,
  ProviderRequestStats,
  TimeRange,
  TimeSeriesPoint,
  StatsResponse,
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
function buildLogsQuery(filter?: LogFilter): string {
  const query = new URLSearchParams();
  if (filter?.limit != null) query.set("limit", String(filter.limit));
  if (filter?.offset != null) query.set("offset", String(filter.offset));
  if (filter?.provider_id) query.set("provider_id", filter.provider_id);
  if (filter?.api_type) query.set("api_type", filter.api_type);
  if (filter?.success != null) query.set("success", String(filter.success));
  if (filter?.user_id) query.set("user_id", filter.user_id);
  if (filter?.start_time) query.set("start_time", filter.start_time);
  if (filter?.end_time) query.set("end_time", filter.end_time);
  if (filter?.min_latency != null)
    query.set("min_latency", String(filter.min_latency));
  if (filter?.sort_by) query.set("sort_by", filter.sort_by);
  if (filter?.sort_order) query.set("sort_order", filter.sort_order);
  return query.toString();
}

// Build query string for stats API
function buildStatsQuery(params?: StatsParams): string {
  const query = new URLSearchParams();
  if (params?.period) query.set("period", params.period);
  if (params?.granularity) query.set("granularity", params.granularity);
  return query.toString();
}

// Request factory with dependency injection
function createRequest(
  deps: ApiClientDeps,
  tokenManager: ReturnType<typeof createTokenManager>
) {
  const { httpClient, baseUrl, onUnauthorized } = deps;

  return async function request<T>(
    endpoint: string,
    options: RequestInit = {}
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
        response.status
      );
    }

    // 204 No Content
    if (response.status === 204) {
      return undefined as T;
    }

    return response.json();
  };
}

// API Client factory with dependency injection
export function createApiClient(deps: ApiClientDeps) {
  const tokenManager = createTokenManager(deps.storage);
  const request = createRequest(deps, tokenManager);

  return {
    // Token management
    setToken: tokenManager.set,
    clearToken: tokenManager.clear,
    getToken: tokenManager.get,

    // Validate a token without storing it or triggering onUnauthorized
    // Returns true if token is valid, false otherwise
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

    // Providers
    providers: {
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
        request<void>(`/providers/${id}`, {
          method: "DELETE",
        }),
      enable: (id: string) =>
        request<Provider>(`/providers/${id}/enable`, {
          method: "POST",
        }),
      disable: (id: string) =>
        request<Provider>(`/providers/${id}/disable`, {
          method: "POST",
        }),
      reset: (id: string) =>
        request<HealthState>(`/providers/${id}/reset`, {
          method: "POST",
        }),
    },

    // Groups
    groups: {
      list: () => request<Group[]>("/groups"),
      get: (id: string) => request<Group>(`/groups/${id}`),
      create: (data: GroupInput) =>
        request<Group>("/groups", {
          method: "POST",
          body: JSON.stringify(data),
        }),
      update: (id: string, data: GroupInput) =>
        request<Group>(`/groups/${id}`, {
          method: "PUT",
          body: JSON.stringify(data),
        }),
      delete: (id: string) =>
        request<void>(`/groups/${id}`, {
          method: "DELETE",
        }),
    },

    // Config
    config: {
      get: () => request<Record<string, string>>("/config"),
      update: (data: Record<string, string>) =>
        request<Record<string, string>>("/config", {
          method: "PUT",
          body: JSON.stringify(data),
        }),
    },

    // Status
    status: {
      get: () => request<SystemStatus>("/status"),
      health: () => request<HealthState[]>("/health"),
    },

    // Logs
    logs: {
      list: (filter?: LogFilter) => {
        const queryStr = buildLogsQuery(filter);
        return request<LogsResponse>(queryStr ? `/logs?${queryStr}` : "/logs");
      },
    },

    // Stats
    stats: {
      get: (params?: StatsParams) => {
        const queryStr = buildStatsQuery(params);
        return request<StatsResponse>(
          queryStr ? `/stats?${queryStr}` : "/stats"
        );
      },
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
