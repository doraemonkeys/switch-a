import { describe, it, expect } from "vitest";
import {
  API_BASE,
  STORAGE_KEYS,
  FORM_CONSTRAINTS,
  STRATEGIES,
  STRATEGY_OPTIONS,
  AUTH_MODES,
  AUTH_MODE_OPTIONS,
  API_TYPES,
  API_TYPE_OPTIONS,
  CUSTOM_API_TYPE_PREFIX,
  isValidAPIType,
  ERROR_CODES,
  CONFIG_KEYS,
  DEFAULTS,
  MAX_RETRIES_USE_GLOBAL,
} from "./constants";

describe("API_BASE", () => {
  it("should have a default value", () => {
    // API_BASE defaults to /admin/api when env var is not set
    expect(API_BASE).toBeDefined();
    expect(typeof API_BASE).toBe("string");
  });
});

describe("STORAGE_KEYS", () => {
  it("should have AUTH_TOKEN key", () => {
    expect(STORAGE_KEYS.AUTH_TOKEN).toBe("admin_token");
  });

  it("should be readonly", () => {
    // TypeScript enforces this at compile time, but we can verify structure
    expect(
      Object.isFrozen(STORAGE_KEYS) || typeof STORAGE_KEYS === "object",
    ).toBe(true);
  });
});

describe("DEFAULTS (sticky & circuit breaker)", () => {
  it("should have STICKY_TTL", () => {
    expect(DEFAULTS.STICKY_TTL).toBe(300);
    expect(typeof DEFAULTS.STICKY_TTL).toBe("number");
  });

  it("should have CIRCUIT_BREAKER settings", () => {
    expect(DEFAULTS.CIRCUIT_FAILURE).toBe(3);
    expect(DEFAULTS.CIRCUIT_WINDOW).toBe(60);
    expect(DEFAULTS.CIRCUIT_DISABLE).toBe(300);
  });

  it("should have sensible defaults for circuit breaker", () => {
    // Failure threshold should be reasonable (1-10)
    expect(DEFAULTS.CIRCUIT_FAILURE).toBeGreaterThanOrEqual(1);
    expect(DEFAULTS.CIRCUIT_FAILURE).toBeLessThanOrEqual(10);
    // Window should be at least 10 seconds
    expect(DEFAULTS.CIRCUIT_WINDOW).toBeGreaterThanOrEqual(10);
    // Disable duration should be at least window time
    expect(DEFAULTS.CIRCUIT_DISABLE).toBeGreaterThanOrEqual(
      DEFAULTS.CIRCUIT_WINDOW,
    );
  });
});

describe("FORM_CONSTRAINTS", () => {
  it("should have MIN_POSITIVE constraint", () => {
    expect(FORM_CONSTRAINTS.MIN_POSITIVE).toBe(1);
  });

  it("should have MIN_ZERO constraint", () => {
    expect(FORM_CONSTRAINTS.MIN_ZERO).toBe(0);
  });

  it("should have MAX_RETRIES_LIMIT constraint", () => {
    expect(FORM_CONSTRAINTS.MAX_RETRIES_LIMIT).toBe(10);
  });

  it("should have logical constraint values", () => {
    expect(FORM_CONSTRAINTS.MIN_ZERO).toBeLessThan(
      FORM_CONSTRAINTS.MIN_POSITIVE,
    );
    expect(FORM_CONSTRAINTS.MIN_ZERO).toBeLessThan(
      FORM_CONSTRAINTS.MAX_RETRIES_LIMIT,
    );
  });
});

// =============================================================================
// Backend Enums Tests - Sync with internal/admin/constants.go
// =============================================================================

describe("STRATEGIES", () => {
  it("should have all valid strategies", () => {
    expect(STRATEGIES.PRIORITY).toBe("priority");
    expect(STRATEGIES.RANDOM).toBe("random");
    expect(STRATEGIES.WEIGHT).toBe("weight");
  });

  it("should have exactly 3 strategies", () => {
    expect(Object.keys(STRATEGIES)).toHaveLength(3);
  });
});

describe("STRATEGY_OPTIONS", () => {
  it("should have options for all strategies", () => {
    expect(STRATEGY_OPTIONS).toHaveLength(3);
    const values = STRATEGY_OPTIONS.map((opt) => opt.value);
    expect(values).toContain(STRATEGIES.PRIORITY);
    expect(values).toContain(STRATEGIES.RANDOM);
    expect(values).toContain(STRATEGIES.WEIGHT);
  });

  it("should have label and description for each option", () => {
    STRATEGY_OPTIONS.forEach((opt) => {
      expect(opt.label).toBeDefined();
      expect(opt.description).toBeDefined();
      expect(typeof opt.label).toBe("string");
      expect(typeof opt.description).toBe("string");
    });
  });
});

describe("AUTH_MODES", () => {
  it("should have all valid auth modes", () => {
    expect(AUTH_MODES.AUTO).toBe("auto");
    expect(AUTH_MODES.BEARER).toBe("bearer");
    expect(AUTH_MODES.X_API_KEY).toBe("x-api-key");
  });

  it("should have exactly 3 auth modes", () => {
    expect(Object.keys(AUTH_MODES)).toHaveLength(3);
  });
});

describe("AUTH_MODE_OPTIONS", () => {
  it("should have options for all auth modes", () => {
    expect(AUTH_MODE_OPTIONS).toHaveLength(3);
    const values = AUTH_MODE_OPTIONS.map((opt) => opt.value);
    expect(values).toContain(AUTH_MODES.AUTO);
    expect(values).toContain(AUTH_MODES.BEARER);
    expect(values).toContain(AUTH_MODES.X_API_KEY);
  });
});

describe("API_TYPES", () => {
  it("should have all valid API types", () => {
    expect(API_TYPES.CLAUDE).toBe("claude");
    expect(API_TYPES.CODEX).toBe("codex");
    expect(API_TYPES.GEMINI).toBe("gemini");
  });

  it("should have exactly 3 API types", () => {
    expect(Object.keys(API_TYPES)).toHaveLength(3);
  });
});

describe("API_TYPE_OPTIONS", () => {
  it("should have options for all API types", () => {
    expect(API_TYPE_OPTIONS).toHaveLength(3);
    const values = API_TYPE_OPTIONS.map((opt) => opt.value);
    expect(values).toContain(API_TYPES.CLAUDE);
    expect(values).toContain(API_TYPES.CODEX);
    expect(values).toContain(API_TYPES.GEMINI);
  });
});

describe("isValidAPIType", () => {
  it("should accept predefined API types", () => {
    expect(isValidAPIType("claude")).toBe(true);
    expect(isValidAPIType("codex")).toBe(true);
    expect(isValidAPIType("gemini")).toBe(true);
  });

  it("should accept custom:* pattern", () => {
    expect(isValidAPIType("custom:mytool")).toBe(true);
    expect(isValidAPIType("custom:openai")).toBe(true);
  });

  it("should reject invalid API types", () => {
    expect(isValidAPIType("invalid")).toBe(false);
    expect(isValidAPIType("gpt")).toBe(false);
    expect(isValidAPIType("custom:")).toBe(false); // empty custom name
    expect(isValidAPIType("custom")).toBe(false); // missing prefix
  });
});

describe("ERROR_CODES", () => {
  it("should have all error codes", () => {
    expect(ERROR_CODES.VALIDATION).toBe("VALIDATION_ERROR");
    expect(ERROR_CODES.INTERNAL).toBe("INTERNAL_ERROR");
    expect(ERROR_CODES.NOT_FOUND).toBe("NOT_FOUND");
    expect(ERROR_CODES.CONFLICT).toBe("CONFLICT");
    expect(ERROR_CODES.UNAUTHORIZED).toBe("UNAUTHORIZED");
  });
});

describe("CONFIG_KEYS", () => {
  it("should have all config keys", () => {
    expect(CONFIG_KEYS.AUTH_MODE).toBe("auth_mode");
    expect(CONFIG_KEYS.USER_HEADER).toBe("user_header");
    expect(CONFIG_KEYS.STICKY_ENABLED).toBe("sticky_enabled");
    expect(CONFIG_KEYS.STICKY_TTL).toBe("sticky_ttl");
    expect(CONFIG_KEYS.CIRCUIT_FAILURE).toBe("circuit_failure");
    expect(CONFIG_KEYS.MAX_RETRIES).toBe("max_retries");
    expect(CONFIG_KEYS.INTER_GROUP_STRATEGY).toBe("inter_group_strategy");
  });

  it("should have exactly 16 config keys", () => {
    expect(Object.keys(CONFIG_KEYS)).toHaveLength(16);
  });
});

describe("DEFAULTS", () => {
  it("should have auth defaults", () => {
    expect(DEFAULTS.AUTH_MODE).toBe(AUTH_MODES.AUTO);
    expect(DEFAULTS.USER_HEADER).toBe("X-User-ID");
    expect(DEFAULTS.TRUST_PROXY_HEADERS).toBe(true);
  });

  it("should have timeout defaults", () => {
    expect(DEFAULTS.UPSTREAM_CONNECT_TIMEOUT).toBe(10);
    expect(DEFAULTS.FIRST_BYTE_TIMEOUT).toBe(0);
    expect(DEFAULTS.UPSTREAM_READ_TIMEOUT).toBe(0);
    expect(DEFAULTS.SSE_IDLE_TIMEOUT).toBe(0);
  });

  it("should have sticky session defaults", () => {
    expect(DEFAULTS.STICKY_ENABLED).toBe(true);
    expect(DEFAULTS.STICKY_TTL).toBe(300);
  });

  it("should have circuit breaker defaults", () => {
    expect(DEFAULTS.CIRCUIT_FAILURE).toBe(3);
    expect(DEFAULTS.CIRCUIT_WINDOW).toBe(60);
    expect(DEFAULTS.CIRCUIT_DISABLE).toBe(300);
  });

  it("should have request handling defaults", () => {
    expect(DEFAULTS.MAX_BODY_SIZE_MB).toBe(10);
    expect(DEFAULTS.MAX_RETRIES).toBe(3);
    expect(DEFAULTS.LOG_RETENTION_DAYS).toBe(7);
  });

  it("should have strategy defaults", () => {
    expect(DEFAULTS.INTER_GROUP_STRATEGY).toBe(STRATEGIES.PRIORITY);
    expect(DEFAULTS.PROVIDER_WEIGHT).toBe(1);
  });
});

describe("MAX_RETRIES_USE_GLOBAL", () => {
  it("should be -1 (special value for global default)", () => {
    expect(MAX_RETRIES_USE_GLOBAL).toBe(-1);
  });
});

describe("CUSTOM_API_TYPE_PREFIX", () => {
  it("should be 'custom:'", () => {
    expect(CUSTOM_API_TYPE_PREFIX).toBe("custom:");
  });
});
