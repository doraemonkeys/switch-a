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
  ProviderUsageLimitPolicy,
  BuiltInAPIType,
  ConfigKey,
  ErrorCode,
  ApiErrorDetails,
  ErrorResponse,
  // Provider types
  BackoffPolicy,
  Provider,
  ProviderAPIType,
  ProviderAuthStatus,
  ProviderAuthView,
  ProviderInput,
  APITypeInput,
  ChatGPTLoginStartResponse,
  ChatGPTLoginStatus,
  ChatGPTLoginStatusResponse,
  RoutingPolicy,
  RoutingPolicyInput,
  RoutingPolicyModelMatchType,
  // Group types
  Group,
  GroupInput,
  // Health & Status types
  HealthState,
  ProviderStatus,
  SystemStatus,
  SystemStatusSummary,
  // Log types
  LegacyRequestLog,
  NormalizedRequestLog,
  RequestLog,
  LogsResponse,
  LogFilter,
  ClientAction,
  CommitSource,
  CompletionState,
  OutcomeTimeSeriesPoint,
  ProviderOutcomeStats,
  RequestEvidence,
  SemanticsVersion,
  ServiceOutcome,
  TerminationActor,
  TerminationReason,
  // Stats types
  StatsPeriod,
  StatsGranularity,
  StatsParams,
  ProviderStats,
  TimeRange,
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
  ExportedRoutingPolicy,
  ExportedConfig,
  FullImportScope,
  ImportMode,
  ImportConfigRequest,
  ChangeCount,
  ImportChanges,
  ImportPreviewResponse,
  AppliedCount,
  ImportedCounts,
  ImportResult,
  ImportScope,
  SelectionImportScope,
  SettingsOnlyImportScope,
} from "./types";

// Interface exports (for testing/mocking)
export type { Storage, HttpClient, ApiClientDeps } from "./interfaces";
export { browserStorage, browserHttpClient } from "./interfaces";

// Context exports
export { ApiProvider } from "./ApiContext";
export { useApi } from "./useApi";
