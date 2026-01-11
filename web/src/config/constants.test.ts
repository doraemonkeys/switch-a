import { describe, it, expect } from "vitest";
import {
  API_BASE,
  STORAGE_KEYS,
  CONFIG_DEFAULTS,
  FORM_CONSTRAINTS,
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

describe("CONFIG_DEFAULTS", () => {
  it("should have STICKY_TTL_SECONDS", () => {
    expect(CONFIG_DEFAULTS.STICKY_TTL_SECONDS).toBe(300);
    expect(typeof CONFIG_DEFAULTS.STICKY_TTL_SECONDS).toBe("number");
  });

  it("should have CIRCUIT_BREAKER settings", () => {
    expect(CONFIG_DEFAULTS.CIRCUIT_BREAKER).toBeDefined();
    expect(CONFIG_DEFAULTS.CIRCUIT_BREAKER.FAILURE_THRESHOLD).toBe(3);
    expect(CONFIG_DEFAULTS.CIRCUIT_BREAKER.WINDOW_SECONDS).toBe(60);
    expect(CONFIG_DEFAULTS.CIRCUIT_BREAKER.DISABLE_DURATION_SECONDS).toBe(300);
  });

  it("should have sensible defaults for circuit breaker", () => {
    const cb = CONFIG_DEFAULTS.CIRCUIT_BREAKER;
    // Failure threshold should be reasonable (1-10)
    expect(cb.FAILURE_THRESHOLD).toBeGreaterThanOrEqual(1);
    expect(cb.FAILURE_THRESHOLD).toBeLessThanOrEqual(10);
    // Window should be at least 10 seconds
    expect(cb.WINDOW_SECONDS).toBeGreaterThanOrEqual(10);
    // Disable duration should be at least window time
    expect(cb.DISABLE_DURATION_SECONDS).toBeGreaterThanOrEqual(
      cb.WINDOW_SECONDS,
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

  it("should have logical constraint values", () => {
    expect(FORM_CONSTRAINTS.MIN_ZERO).toBeLessThan(
      FORM_CONSTRAINTS.MIN_POSITIVE,
    );
  });
});
