// API Configuration
export const API_BASE = import.meta.env.VITE_API_BASE ?? "/admin/api";

// Application Version
export const APP_VERSION = "0.1.0";

// Storage Keys
export const STORAGE_KEYS = {
  AUTH_TOKEN: "admin_token",
} as const;

// =============================================================================
// Backend Enums - Keep in sync with internal/admin/constants.go
// =============================================================================

/**
 * Valid strategies for provider/group selection.
 * @see internal/admin/constants.go ValidStrategies
 */
export const STRATEGIES = {
  PRIORITY: "priority",
  RANDOM: "random",
  WEIGHT: "weight",
} as const;

export type Strategy = (typeof STRATEGIES)[keyof typeof STRATEGIES];

export const STRATEGY_OPTIONS = [
  {
    value: STRATEGIES.PRIORITY,
    label: "Priority",
    description:
      "Select providers in order of priority. Lower number = higher priority. Best for primary/backup setups.",
  },
  {
    value: STRATEGIES.RANDOM,
    label: "Random",
    description: "Randomly select from available providers",
  },
  {
    value: STRATEGIES.WEIGHT,
    label: "Weight",
    description:
      "Select based on configured weights (higher weight = more likely)",
  },
] as const;

/**
 * Valid sticky session modes.
 * @see internal/model/model.go StickyMode
 */
export const STICKY_MODES = {
  OFF: "off",
  API_TYPE: "api_type",
  MODEL: "model",
} as const;

export type StickyMode = (typeof STICKY_MODES)[keyof typeof STICKY_MODES];

export const STICKY_MODE_OPTIONS = [
  {
    value: STICKY_MODES.OFF,
    label: "Off",
    description: "No sticky session — each request is independently routed",
  },
  {
    value: STICKY_MODES.API_TYPE,
    label: "API Type",
    description: "Same user + API type always routes to the same provider",
  },
  {
    value: STICKY_MODES.MODEL,
    label: "Model",
    description:
      "Same user + API type + model always routes to the same provider (recommended)",
  },
] as const;

/**
 * Valid authentication modes for providers.
 * @see internal/admin/constants.go ValidAuthModes
 */
export const AUTH_MODES = {
  AUTO: "auto",
  BEARER: "bearer",
  X_API_KEY: "x-api-key",
} as const;

export type AuthMode = (typeof AUTH_MODES)[keyof typeof AUTH_MODES];

export const AUTH_MODE_OPTIONS = [
  {
    value: AUTH_MODES.AUTO,
    label: "Auto",
    description: "Automatically detect auth mode from request",
  },
  {
    value: AUTH_MODES.BEARER,
    label: "Bearer",
    description:
      "Use Bearer token authentication (Authorization: Bearer <key>)",
  },
  {
    value: AUTH_MODES.X_API_KEY,
    label: "X-API-Key",
    description: "Use X-API-Key header authentication",
  },
] as const;

/**
 * Valid provider credential source types.
 * @see internal/admin/constants.go validProviderCredentialTypes
 */
export const PROVIDER_CREDENTIAL_TYPES = {
  API_KEY: "api_key",
  CHATGPT: "chatgpt",
} as const;

export type ProviderCredentialType =
  (typeof PROVIDER_CREDENTIAL_TYPES)[keyof typeof PROVIDER_CREDENTIAL_TYPES];

export const PROVIDER_CREDENTIAL_TYPE_OPTIONS = [
  {
    value: PROVIDER_CREDENTIAL_TYPES.API_KEY,
    label: "API Key",
    description: "Use a static provider API key or API-type key overrides.",
  },
  {
    value: PROVIDER_CREDENTIAL_TYPES.CHATGPT,
    label: "GPT Login",
    description:
      "Sign in with a ChatGPT account locally and proxy Codex through the captured OAuth session.",
  },
] as const;

export const PROVIDER_USAGE_LIMIT_POLICIES = {
  SWITCH_PROVIDER: "switch_provider",
  SUSPEND: "suspend",
} as const;

export type ProviderUsageLimitPolicy =
  (typeof PROVIDER_USAGE_LIMIT_POLICIES)[keyof typeof PROVIDER_USAGE_LIMIT_POLICIES];

export function defaultProviderUsageLimitPolicy(
  credentialType?: ProviderCredentialType,
): ProviderUsageLimitPolicy {
  if (credentialType === PROVIDER_CREDENTIAL_TYPES.CHATGPT) {
    return PROVIDER_USAGE_LIMIT_POLICIES.SUSPEND;
  }
  return PROVIDER_USAGE_LIMIT_POLICIES.SWITCH_PROVIDER;
}

export const PROVIDER_USAGE_LIMIT_POLICY_OPTIONS = [
  {
    value: PROVIDER_USAGE_LIMIT_POLICIES.SWITCH_PROVIDER,
    label: "Switch Provider",
    description:
      "Record the usage-limit error and route the request to another provider without opening a timed suspension window.",
  },
  {
    value: PROVIDER_USAGE_LIMIT_POLICIES.SUSPEND,
    label: "Suspend Until Reset",
    description:
      "Open the circuit until the upstream reset window, then recover through the normal health flow.",
  },
] as const;

/**
 * Failover scope values for vendor isolation.
 * Controls which providers can be used as failover targets.
 * @see internal/model/model.go Scope
 */
export const FAILOVER_SCOPES = {
  NONE: "none",
  VENDOR: "vendor",
  ANY: "any",
} as const;

export type FailoverScope =
  (typeof FAILOVER_SCOPES)[keyof typeof FAILOVER_SCOPES];

/** Vendor wildcard value that matches any vendor */
export const VENDOR_WILDCARD = "*";

export const FAILOVER_SCOPE_OPTIONS = [
  {
    value: FAILOVER_SCOPES.ANY,
    label: "Any",
    description:
      "Allow true failover to/from any provider after client-visible continuity exists (default)",
    icon: "🌐",
  },
  {
    value: FAILOVER_SCOPES.VENDOR,
    label: "Same Vendor",
    description:
      "Only allow true failover within the same vendor group; pre-visible replacement still proceeds",
    icon: "🔗",
  },
  {
    value: FAILOVER_SCOPES.NONE,
    label: "None",
    description:
      "Do not allow true failover; pre-visible replacement still proceeds",
    icon: "🚫",
  },
] as const;

/**
 * Common vendor names for quick selection in forms.
 * These represent well-known AI API providers.
 */
export const COMMON_VENDORS = [
  "yescode",
  "openrouter",
  "openai",
  "anthropic",
  "xai",
  "deepseek",
] as const;

/** Fixed upstream base URL used by GPT-login-backed Codex providers. */
export const CHATGPT_CODEX_BASE_URL = "https://chatgpt.com/backend-api/codex";

/**
 * Error codes returned by the backend API.
 * @see internal/admin/constants.go ErrCode*
 */
export const ERROR_CODES = {
  VALIDATION: "VALIDATION_ERROR",
  INTERNAL: "INTERNAL_ERROR",
  NOT_FOUND: "NOT_FOUND",
  CONFLICT: "CONFLICT",
  UNAUTHORIZED: "UNAUTHORIZED",
  PROVIDER_AUTH_REQUIRED: "PROVIDER_AUTH_REQUIRED",
  PRECONDITION_REQUIRED: "PRECONDITION_REQUIRED",
  REVISION_MISMATCH: "REVISION_MISMATCH",
  REQUEST_TOO_LARGE: "REQUEST_TOO_LARGE",
} as const;

export type ErrorCode = (typeof ERROR_CODES)[keyof typeof ERROR_CODES];

/**
 * Valid runtime configuration keys.
 * @see internal/admin/constants.go ValidConfigKeys
 */
export const CONFIG_KEYS = {
  AUTH_MODE: "auth_mode",
  USER_HEADER: "user_header",
  TRUST_PROXY_HEADERS: "trust_proxy_headers",
  UPSTREAM_CONNECT_TIMEOUT: "upstream_connect_timeout",
  FIRST_BYTE_TIMEOUT: "first_byte_timeout",
  UPSTREAM_READ_TIMEOUT: "upstream_read_timeout",
  SSE_IDLE_TIMEOUT: "sse_idle_timeout",
  STICKY_MODE: "sticky_mode",
  STICKY_TTL: "sticky_ttl",
  WEBSOCKET_PROBE_CLIENT_MODEL: "websocket_probe_client_model",
  CIRCUIT_FAILURE: "circuit_failure",
  CIRCUIT_WINDOW: "circuit_window",
  CIRCUIT_DISABLE: "circuit_disable",
  MAX_BODY_SIZE: "max_body_size",
  GLOBAL_MAX_ATTEMPTS: "global_max_attempts",
  LOG_RETENTION_DAYS: "log_retention_days",
  INTER_GROUP_STRATEGY: "inter_group_strategy",
  CODEX_UPSTREAM_HEADER_HYGIENE: "codex_upstream_header_hygiene_enabled",
  CODEX_WEBSOCKET_SUBPROTOCOL: "codex_websocket_subprotocol_enabled",
  CODEX_CONTINUITY: "codex_continuity_enabled",
  CODEX_PROVIDER_COOKIE_JAR: "codex_provider_cookie_jar_enabled",
} as const;

export type ConfigKey = (typeof CONFIG_KEYS)[keyof typeof CONFIG_KEYS];

// =============================================================================
// Default Values - Keep in sync with internal/defaults/defaults.go
// =============================================================================

/**
 * Default configuration values from backend.
 * @see internal/defaults/defaults.go
 */
export const DEFAULTS = {
  // Authentication
  AUTH_MODE: AUTH_MODES.AUTO,
  USER_HEADER: "X-User-ID",
  TRUST_PROXY_HEADERS: true,

  // Timeouts (in seconds)
  UPSTREAM_CONNECT_TIMEOUT: 10,
  FIRST_BYTE_TIMEOUT: 0, // 0 = no timeout
  UPSTREAM_READ_TIMEOUT: 0, // 0 = no timeout
  SSE_IDLE_TIMEOUT: 0, // 0 = no timeout

  // Sticky Session
  STICKY_MODE: STICKY_MODES.MODEL,
  STICKY_TTL: 300,
  WEBSOCKET_PROBE_CLIENT_MODEL: true,

  // Circuit Breaker
  CIRCUIT_FAILURE: 3,
  CIRCUIT_WINDOW: 60,
  CIRCUIT_DISABLE: 300,

  // Request Handling
  MAX_BODY_SIZE_MB: 10,
  GLOBAL_MAX_ATTEMPTS: 0, // 0 = unlimited (iterate through all providers)
  PROVIDER_MAX_RETRIES: 0, // 0 = try once, no retry on same provider
  LOG_RETENTION_DAYS: 7,

  // Backoff Policy (for same-provider retries)
  BACKOFF_INITIAL_DELAY: "100ms",
  BACKOFF_MAX_DELAY: "5s",
  BACKOFF_MULTIPLIER: 2.0,
  BACKOFF_JITTER: false,

  // Strategy
  INTER_GROUP_STRATEGY: STRATEGIES.PRIORITY,
  PROVIDER_WEIGHT: 1,

  // Codex protocol rollout features
  CODEX_UPSTREAM_HEADER_HYGIENE: false,
  CODEX_WEBSOCKET_SUBPROTOCOL: false,
  CODEX_CONTINUITY: false,
  CODEX_PROVIDER_COOKIE_JAR: false,
} as const;

export const CODEX_FEATURE_KEYS = [
  CONFIG_KEYS.CODEX_UPSTREAM_HEADER_HYGIENE,
  CONFIG_KEYS.CODEX_WEBSOCKET_SUBPROTOCOL,
  CONFIG_KEYS.CODEX_CONTINUITY,
  CONFIG_KEYS.CODEX_PROVIDER_COOKIE_JAR,
] as const;

export type CodexFeatureKey = (typeof CODEX_FEATURE_KEYS)[number];

interface CodexFeatureDefinition {
  key: CodexFeatureKey;
  label: string;
  description: string;
  defaultValue: boolean;
  requires: readonly CodexFeatureKey[];
}

// This is the UI projection of internal/codex/startup's registry. Rendering and
// dependency affordances consume this one collection so the form cannot expose
// session identity separately from continuity or couple Cookie to continuity.
export const CODEX_FEATURES = [
  {
    key: CONFIG_KEYS.CODEX_UPSTREAM_HEADER_HYGIENE,
    label: "Upstream Header Hygiene",
    description:
      "Rebuild each upstream attempt without client or previous-attempt authentication and account headers.",
    defaultValue: DEFAULTS.CODEX_UPSTREAM_HEADER_HYGIENE,
    requires: [],
  },
  {
    key: CONFIG_KEYS.CODEX_WEBSOCKET_SUBPROTOCOL,
    label: "WebSocket Subprotocol",
    description:
      "Negotiate one matching WebSocket subprotocol across the downstream and upstream connections.",
    defaultValue: DEFAULTS.CODEX_WEBSOCKET_SUBPROTOCOL,
    requires: [],
  },
  {
    key: CONFIG_KEYS.CODEX_CONTINUITY,
    label: "Continuity and Session Identity",
    description:
      "Keep Codex state, response references, and session identity on the same verified security scope.",
    defaultValue: DEFAULTS.CODEX_CONTINUITY,
    requires: [CONFIG_KEYS.CODEX_UPSTREAM_HEADER_HYGIENE],
  },
  {
    key: CONFIG_KEYS.CODEX_PROVIDER_COOKIE_JAR,
    label: "Provider Cookie Jar",
    description:
      "Persist upstream cookies in an isolated server-side jar; this remains independent of continuity.",
    defaultValue: DEFAULTS.CODEX_PROVIDER_COOKIE_JAR,
    requires: [CONFIG_KEYS.CODEX_UPSTREAM_HEADER_HYGIENE],
  },
] as const satisfies readonly CodexFeatureDefinition[];

/**
 * Default provider max retries value.
 * 0 = try once, no retry on same provider before switching to next.
 * @see internal/defaults/defaults.go ProviderMaxRetries
 */
export const DEFAULT_PROVIDER_MAX_RETRIES = 0;

// =============================================================================
// Deprecated - Use DEFAULTS instead
// =============================================================================

/** @deprecated Use DEFAULTS instead */
export const CONFIG_DEFAULTS = {
  STICKY_TTL_SECONDS: DEFAULTS.STICKY_TTL,
  CIRCUIT_BREAKER: {
    FAILURE_THRESHOLD: DEFAULTS.CIRCUIT_FAILURE,
    WINDOW_SECONDS: DEFAULTS.CIRCUIT_WINDOW,
    DISABLE_DURATION_SECONDS: DEFAULTS.CIRCUIT_DISABLE,
  },
} as const;

// Form Constraints
export const FORM_CONSTRAINTS = {
  MIN_POSITIVE: 1,
  MIN_ZERO: 0,
  MAX_PROVIDER_RETRIES: 10, // Max value for provider-level max_retries
  MAX_GLOBAL_ATTEMPTS: 20, // Max value for global_max_attempts
  // Backoff policy constraints
  BACKOFF_MAX_MULTIPLIER: 10, // Max exponential multiplier
} as const;

// Recent Logs Display Limit
export const RECENT_LOGS_LIMIT = 5;

// Provider data defaults/fallbacks. Keep these aligned with backend persisted
// defaults; add-form defaults that intentionally differ belong below.
export const PROVIDER_DEFAULTS = {
  PRIORITY: 0,
  WEIGHT: 1,
  CONCURRENCY: 10,
  MAX_RETRIES: 0,
  /** Default backoff policy for same-provider retries */
  BACKOFF: {
    INITIAL_DELAY: DEFAULTS.BACKOFF_INITIAL_DELAY,
    MAX_DELAY: DEFAULTS.BACKOFF_MAX_DELAY,
    MULTIPLIER: DEFAULTS.BACKOFF_MULTIPLIER,
    JITTER: DEFAULTS.BACKOFF_JITTER,
  },
} as const;

export const PROVIDER_UNLIMITED_BACKOFF_MAX_DELAY = "0s";

// Defaults used only when creating a provider through the frontend form.
export const ADD_PROVIDER_DEFAULTS = {
  PRIORITY: PROVIDER_DEFAULTS.PRIORITY,
  WEIGHT: PROVIDER_DEFAULTS.WEIGHT,
  CONCURRENCY: PROVIDER_DEFAULTS.CONCURRENCY,
  MAX_RETRIES: 3,
  BACKOFF: {
    INITIAL_DELAY: "1s",
    // The backend models an uncapped max delay as a zero duration, and Go
    // duration JSON represents that zero value as "0s".
    MAX_DELAY: PROVIDER_UNLIMITED_BACKOFF_MAX_DELAY,
    MULTIPLIER: 3.0,
    JITTER: true,
  },
} as const;

// =============================================================================
// Stats Display Thresholds
// =============================================================================

/**
 * Thresholds for displaying success rate indicators.
 * Values are decimal rates (0.0 - 1.0).
 *
 * Rationale: 0.95 represents excellent health (95% SLA standard),
 * 0.80 represents degraded service requiring attention.
 */
export const SUCCESS_RATE_THRESHOLDS = {
  /** Success rate >= this value shows "success" variant */
  SUCCESS: 0.95,
  /** Success rate >= this value (but < SUCCESS) shows "warning" variant */
  WARNING: 0.8,
  // Below WARNING threshold shows "danger" variant
} as const;

/**
 * Thresholds for displaying error count indicators.
 *
 * Rationale: 10 errors is the threshold for concern - below this count
 * individual errors may be transient.
 */
export const ERROR_COUNT_THRESHOLDS = {
  /** Error count < this value (and > 0) shows "warning" variant */
  WARNING_MAX: 10,
  // count === 0 shows "success", count >= WARNING_MAX shows "danger"
} as const;
