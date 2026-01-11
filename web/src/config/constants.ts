// API Configuration
export const API_BASE = import.meta.env.VITE_API_BASE ?? "/admin/api";

// Storage Keys
export const STORAGE_KEYS = {
  AUTH_TOKEN: "admin_token",
} as const;

// Default Config Values
export const CONFIG_DEFAULTS = {
  // Sticky Session
  STICKY_TTL_SECONDS: 300,

  // Circuit Breaker
  CIRCUIT_BREAKER: {
    FAILURE_THRESHOLD: 3,
    WINDOW_SECONDS: 60,
    DISABLE_DURATION_SECONDS: 300,
  },
} as const;

// Form Constraints
export const FORM_CONSTRAINTS = {
  MIN_POSITIVE: 1,
  MIN_ZERO: 0,
} as const;
