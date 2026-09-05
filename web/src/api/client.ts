import { API_BASE, STORAGE_KEYS } from "../config";
import { createDebugCaptureApi } from "./debug-capture";
import { createClientDisguiseApi } from "./client-disguise/client";
import {
  type ApiClientDeps,
  type Storage,
  browserStorage,
  browserHttpClient,
} from "./interfaces";
import {
  parseLogsResponse,
  parseRequestLog,
  parseStatsResponse,
} from "./contracts";
import { parseTokenUsageResponse } from "./token-usage-decoders";
import { parseAPICatalog } from "./api-catalog";
import {
  createCredentialSessionsApi,
  createProvidersApi,
} from "./provider-client";
import {
  createErrorDetectionApi,
  type APIErrorDecoder,
  type AuthenticatedResponseRequest,
} from "./error-detection";
import type {
  RoutingPolicy,
  RoutingPolicyInput,
  Group,
  GroupInput,
  HealthState,
  SystemStatus,
  LogFilter,
  StatsParams,
  TokenUsageParams,
  ExportedConfig,
  ImportConfigRequest,
  ImportPreviewResponse,
  ImportResult,
  ConfigResponse,
  ActiveRequestsResponse,
  ApiErrorDetails,
} from "./types";
import type {
  ProviderImportCommitRequest,
  ProviderImportCommitResult,
  ProviderImportPreview,
} from "./provider-import-types";

// Re-export types for consumers
export type {
  BackoffPolicy,
  ClientAction,
  Provider,
  ProviderAuthStatus,
  ProviderAuthView,
  ProviderCredentialSession,
  CredentialSession,
  CredentialSessionKind,
  CredentialSessionAuthState,
  CreateCredentialSessionInput,
  ReauthenticateCredentialSessionInput,
  UpdateCredentialSessionInput,
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
  OutcomeTimeSeriesPoint,
  ProviderOutcomeStats,
  SelectionImportScope,
  ServiceOutcome,
  SettingsOnlyImportScope,
  SemanticsVersion,
  ConfigResponse,
  ActiveRequest,
  ActiveRequestsResponse,
  ChatGPTLoginStartResponse,
  ChatGPTLoginStatusResponse,
  CodexAuthDocument,
  CompletionState,
  LegacyRequestLog,
  NormalizedRequestLog,
  RequestAttemptOutcome,
  RequestAttemptPhase,
  RequestAttempt,
  RequestEvidence,
  TerminationActor,
  TerminationReason,
  TokenBreakdownDTO,
  TokenSummaryDTO,
  TokenBucketDTO,
  TokenProviderRankDTO,
  TokenModelRankDTO,
  TokenTimeRangeDTO,
  TokenCoverageDTO,
  TokenDataQualityDTO,
  TokenUsageResponse,
  TokenUsageParams,
} from "./types";
export type {
  ProviderImportCandidate,
  ProviderImportCandidateStatus,
  ProviderImportConflictDetail,
  ProviderImportConflictKind,
  ProviderImportCommitAction,
  ProviderImportCommitItem,
  ProviderImportCreateCommitItem,
  ProviderImportCommitOutcome,
  ProviderImportCommitRequest,
  ProviderImportCommitResult,
  ProviderImportCommitResultItem,
  ProviderImportCommitSummary,
  ProviderImportIssue,
  ProviderImportMappingNote,
  ProviderImportPreview,
  ProviderImportSummary,
  ProviderImportUpdateCommitItem,
} from "./provider-import-types";

// API Error type
export class ApiError extends Error {
  code: string;
  status: number;
  details?: ApiErrorDetails;

  constructor(
    code: string,
    message: string,
    status: number,
    details?: ApiErrorDetails,
  ) {
    super(message);
    this.name = "ApiError";
    this.code = code;
    this.status = status;
    this.details = details;
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
  appendLogQueryParam(query, "semantics_version", filter.semantics_version);
  appendLogQueryParam(query, "completion_state", filter.completion_state);
  appendLogQueryParam(query, "service_outcome", filter.service_outcome);
  appendLogQueryParam(query, "client_action", filter.client_action);
  appendLogQueryParam(query, "termination_actor", filter.termination_actor);
  appendLogQueryParam(query, "termination_reason", filter.termination_reason);
  appendLogQueryParam(
    query,
    "client_transport_status_code",
    filter.client_transport_status_code,
  );
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
  appendLogQueryParam(query, "commit_source", filter.commit_source);
  appendLogQueryParam(query, "sort_by", filter.sort_by);
  appendLogQueryParam(query, "sort_order", filter.sort_order);
  return query.toString();
}

// Build query string for stats API
function buildStatsQuery(params?: StatsParams): string {
  const query = new URLSearchParams();
  if (params?.period) query.set("period", params.period);
  if (params?.granularity) query.set("granularity", params.granularity);
  if (params?.as_of) query.set("as_of", params.as_of);
  return query.toString();
}

// Build query string for token-usage API
function buildTokenUsageQuery(params?: TokenUsageParams): string {
  const query = new URLSearchParams();
  if (params?.period) query.set("period", params.period);
  if (params?.granularity) query.set("granularity", params.granularity);
  if (params?.as_of) query.set("as_of", params.as_of);
  if (params?.provider_id) query.set("provider_id", params.provider_id);
  if (params?.model) query.set("model", params.model);
  if (params?.api_type) query.set("api_type", params.api_type);
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

function readLooseAPIError(value: unknown): {
  code?: string;
  message?: string;
  details?: ApiErrorDetails;
} {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return {};
  }
  const record = value as Record<string, unknown>;
  return {
    code: typeof record.code === "string" ? record.code : undefined,
    message: typeof record.message === "string" ? record.message : undefined,
    details:
      typeof record.details === "object" &&
      record.details !== null &&
      !Array.isArray(record.details)
        ? (record.details as ApiErrorDetails)
        : undefined,
  };
}

// Keeping response metadata available is required for revision ETags; ordinary
// JSON callers still use the narrower wrapper below.
function createResponseRequest(
  deps: ApiClientDeps,
  tokenManager: ReturnType<typeof createTokenManager>,
): AuthenticatedResponseRequest {
  const { httpClient, baseUrl, onUnauthorized } = deps;

  return async function requestResponse(
    endpoint: string,
    options: RequestInit = {},
    errorDecoder?: APIErrorDecoder,
  ): Promise<Response> {
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
      const payload: unknown = await response.json().catch(() => undefined);
      let data = readLooseAPIError(payload);
      if (errorDecoder) {
        try {
          data = errorDecoder(payload);
        } catch {
          // A malformed error body is untrusted; retain status context without
          // surfacing unchecked server fields as a typed conflict.
          data = {};
        }
      }

      // Handle 401 Unauthorized - clear token and redirect to login
      if (response.status === 401) {
        tokenManager.clear();
        onUnauthorized?.();
      }

      throw new ApiError(
        data.code || "UNKNOWN_ERROR",
        data.message || response.statusText,
        response.status,
        data.details,
      );
    }

    return response;
  };
}

function createRequest(requestResponse: AuthenticatedResponseRequest) {
  return async function request<T>(
    endpoint: string,
    options: RequestInit = {},
  ): Promise<T> {
    const response = await requestResponse(endpoint, options);
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
  const requestResponse = createResponseRequest(deps, tokenManager);
  const request = createRequest(requestResponse);

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
    apiCatalog: {
      get: async () => parseAPICatalog(await request<unknown>("/api-catalog")),
    },
    errorDetection: createErrorDetectionApi(requestResponse),
    clientDisguise: createClientDisguiseApi(request),
    providers: createProvidersApi(request),
    credentialSessions: createCredentialSessionsApi(request),
    providerImports: {
      preview: (sourceJson: string) =>
        request<ProviderImportPreview>("/provider-imports", {
          method: "POST",
          body: sourceJson,
        }),
      commit: (importId: string, data: ProviderImportCommitRequest) =>
        request<ProviderImportCommitResult>(
          `/provider-imports/${encodeURIComponent(importId)}/commit`,
          {
            method: "POST",
            body: JSON.stringify(data),
          },
        ),
      discard: (importId: string) =>
        request<void>(`/provider-imports/${encodeURIComponent(importId)}`, {
          method: "DELETE",
        }),
    },
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
      importPreview: async (data: ImportConfigRequest) => {
        const preview = await request<ImportPreviewResponse>(
          "/config/import?dry_run=true",
          {
            method: "POST",
            body: JSON.stringify(data),
          },
        );
        return {
          ...preview,
          warnings: preview.warnings ?? [],
          credential_reauthentication_requirements:
            preview.credential_reauthentication_requirements ?? [],
        };
      },
      import: (
        data: ImportConfigRequest,
        ruleSetETag: ImportPreviewResponse["rule_set_etag"],
      ) => {
        const headers =
          data.import_scope.mode === "settings_only"
            ? undefined
            : { "If-Match": ruleSetETag };
        return request<ImportResult>("/config/import", {
          method: "POST",
          ...(headers ? { headers } : {}),
          body: JSON.stringify(data),
        }).then((result) => ({
          ...result,
          credential_reauthentication_requirements:
            result.credential_reauthentication_requirements ?? [],
        }));
      },
    },
    status: {
      get: () => request<SystemStatus>("/status"),
      health: () => request<HealthState[]>("/health"),
    },
    logs: {
      list: async (filter?: LogFilter) => {
        const queryStr = buildLogsQuery(filter);
        return parseLogsResponse(
          await request<unknown>(queryStr ? `/logs?${queryStr}` : "/logs"),
        );
      },
      get: async (id: number) =>
        parseRequestLog(await request<unknown>(`/logs/${id}`), `logs/${id}`),
    },
    stats: {
      get: async (params?: StatsParams) => {
        const queryStr = buildStatsQuery(params);
        return parseStatsResponse(
          await request<unknown>(queryStr ? `/stats?${queryStr}` : "/stats"),
        );
      },
    },
    tokenUsage: {
      get: async (params?: TokenUsageParams) => {
        const queryStr = buildTokenUsageQuery(params);
        return parseTokenUsageResponse(
          await request<unknown>(
            queryStr ? `/token-usage?${queryStr}` : "/token-usage",
          ),
        );
      },
    },
    requests: {
      active: () => request<ActiveRequestsResponse>("/requests/active"),
    },
    debugCapture: createDebugCaptureApi(request),
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
