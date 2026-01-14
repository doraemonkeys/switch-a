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
 * Valid API types for providers.
 * These must match the types recognized by the proxy router.
 * @see internal/admin/constants.go ValidAPITypes
 */
export const API_TYPES = {
  CLAUDE: "claude",
  CODEX: "codex",
  GEMINI: "gemini",
} as const;

export type APIType = (typeof API_TYPES)[keyof typeof API_TYPES];

/**
 * Prefix for custom API types (e.g., "custom:mytool").
 * @see internal/admin/constants.go CustomAPITypePrefix
 */
export const CUSTOM_API_TYPE_PREFIX = "custom:";

export const API_TYPE_OPTIONS = [
  {
    value: API_TYPES.CLAUDE,
    label: "Claude",
    description: "Anthropic Claude API (/v1/messages, /v1/models)",
  },
  {
    value: API_TYPES.CODEX,
    label: "Codex",
    description: "OpenAI Codex/Responses API (/responses)",
  },
  {
    value: API_TYPES.GEMINI,
    label: "Gemini",
    description: "Google Gemini API (/gemini/*)",
  },
] as const;

/**
 * Check if an API type is valid.
 * Accepts both predefined API types and custom:* pattern.
 */
export function isValidAPIType(type: string): boolean {
  if (Object.values(API_TYPES).includes(type as APIType)) {
    return true;
  }
  return (
    type.startsWith(CUSTOM_API_TYPE_PREFIX) &&
    type.length > CUSTOM_API_TYPE_PREFIX.length
  );
}

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
  STICKY_ENABLED: "sticky_enabled",
  STICKY_TTL: "sticky_ttl",
  CIRCUIT_FAILURE: "circuit_failure",
  CIRCUIT_WINDOW: "circuit_window",
  CIRCUIT_DISABLE: "circuit_disable",
  MAX_BODY_SIZE: "max_body_size",
  GLOBAL_MAX_ATTEMPTS: "global_max_attempts",
  LOG_RETENTION_DAYS: "log_retention_days",
  INTER_GROUP_STRATEGY: "inter_group_strategy",
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
  STICKY_ENABLED: true,
  STICKY_TTL: 300,

  // Circuit Breaker
  CIRCUIT_FAILURE: 3,
  CIRCUIT_WINDOW: 60,
  CIRCUIT_DISABLE: 300,

  // Request Handling
  MAX_BODY_SIZE_MB: 10,
  GLOBAL_MAX_ATTEMPTS: 0, // 0 = unlimited (iterate through all providers)
  PROVIDER_MAX_RETRIES: 0, // 0 = try once, no retry on same provider
  LOG_RETENTION_DAYS: 7,

  // Strategy
  INTER_GROUP_STRATEGY: STRATEGIES.PRIORITY,
  PROVIDER_WEIGHT: 1,
} as const;

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
} as const;

// Recent Logs Display Limit
export const RECENT_LOGS_LIMIT = 5;

// Provider Form Default Values
export const PROVIDER_DEFAULTS = {
  PRIORITY: 0,
  WEIGHT: 1,
  CONCURRENCY: 10,
  MAX_RETRIES: 0,
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
