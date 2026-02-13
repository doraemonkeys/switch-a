// Client exports
export {
  api,
  setToken,
  clearToken,
  ApiError,
  createApiClient,
  createTokenManager,
} from "./client";
export type { ApiClient } from "./client";

// Type exports
export type {
  // Enum types (synced with backend)
  Strategy,
  AuthMode,
  FailoverScope,
  BuiltInAPIType,
  ConfigKey,
  ErrorCode,
  ErrorResponse,
  // Provider types
  BackoffPolicy,
  Provider,
  ProviderAPIType,
  ProviderInput,
  APITypeInput,
  // Group types
  Group,
  GroupInput,
  // Health & Status types
  HealthState,
  ProviderStatus,
  SystemStatus,
  SystemStatusSummary,
  // Log types
  RequestLog,
  LogsResponse,
  LogFilter,
  // Stats types
  StatsPeriod,
  StatsGranularity,
  StatsParams,
  ProviderStats,
  ProviderRequestStats,
  TimeRange,
  TimeSeriesPoint,
  StatsResponse,
  // Batch operation types
  BatchAction,
  BatchProviderRequest,
  BatchProviderResult,
  BatchProviderResponse,
  // Config export/import types
  ExportedAPIType,
  ExportedProvider,
  ExportedGroup,
  ExportedConfig,
  ImportConfigRequest,
  ChangeCount,
  ImportChanges,
  ImportPreviewResponse,
  AppliedCount,
  ImportedCounts,
  ImportResult,
} from "./types";

// Interface exports (for testing/mocking)
export type { Storage, HttpClient, ApiClientDeps } from "./interfaces";
export { browserStorage, browserHttpClient } from "./interfaces";

// Context exports
export { ApiProvider } from "./ApiContext";
export { useApi } from "./useApi";
