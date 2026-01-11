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
  Provider,
  ProviderInput,
  Group,
  GroupInput,
  HealthState,
  SystemStatus,
  RequestLog,
} from "./client";

// Interface exports (for testing/mocking)
export type { Storage, HttpClient, ApiClientDeps } from "./interfaces";
export { browserStorage, browserHttpClient } from "./interfaces";

// Context exports
export { ApiProvider } from "./ApiContext";
export { useApi } from "./useApi";
