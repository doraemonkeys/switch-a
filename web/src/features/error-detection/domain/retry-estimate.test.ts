import { describe, expect, it } from "vitest";
import type { InternalErrorRuleAction } from "../contracts";
import {
  effectiveSameProviderRetries,
  estimateRetryWaitUpperBound,
} from "./retry-estimate";

const BACKOFF = {
  initial_delay: "250ms",
  max_delay: "2s",
  multiplier: 2,
  jitter: true,
} as const;

function retryAction(
  type: "retry_only" | "retry_then_switch",
  maxRetries: number,
): InternalErrorRuleAction {
  return { type, max_retries: maxRetries, backoff: BACKOFF };
}

describe("effectiveSameProviderRetries", () => {
  it.each([
    ["retry_only", 3, 0, 3],
    ["retry_only", 3, 1, 0],
    ["retry_only", 3, 3, 2],
    ["retry_then_switch", 3, 0, 3],
    ["retry_then_switch", 3, 1, 0],
    ["retry_then_switch", 3, 2, 0],
    ["retry_then_switch", 3, 4, 2],
  ] as const)(
    "%s with max_retries=%i and global_max_attempts=%i yields %i same retries",
    (type, maxRetries, globalMaxAttempts, expected) => {
      expect(
        effectiveSameProviderRetries(
          retryAction(type, maxRetries),
          globalMaxAttempts,
        ),
      ).toBe(expected);
    },
  );

  it("rejects an invalid global attempt limit", () => {
    expect(() =>
      effectiveSameProviderRetries(retryAction("retry_only", 1), -1),
    ).toThrow("global_max_attempts must be a non-negative integer");
  });

  it("rejects a retry action outside the frozen rule limit", () => {
    expect(() =>
      effectiveSameProviderRetries(retryAction("retry_only", 11), 0),
    ).toThrow("max_retries must be between 0 and 10");
  });
});

describe("estimateRetryWaitUpperBound", () => {
  it("adds probe windows and unjittered backoff while reserving the switch", () => {
    expect(
      estimateRetryWaitUpperBound(
        retryAction("retry_then_switch", 3),
        4,
        2_000,
      ),
    ).toEqual({
      valid: true,
      effective_same_provider_retries: 2,
      switch_attempt_reserved: true,
      probe_window_count: 3,
      probe_upper_bound_ms: 6_000,
      backoff_base_delays_ms: [250, 500],
      backoff_upper_bound_ms: 750,
      wait_upper_bound_ms: 6_750,
    });
  });

  it("does not invent a finite reservation for unlimited attempts", () => {
    const result = estimateRetryWaitUpperBound(
      retryAction("retry_then_switch", 1),
      0,
    );
    expect(result).toMatchObject({
      valid: true,
      effective_same_provider_retries: 1,
      switch_attempt_reserved: false,
    });
  });

  it.each([[{ type: "passthrough" } as const], [retryAction("retry_only", 0)]])(
    "adds no probe delay for observer-only action %#",
    (action) => {
      expect(estimateRetryWaitUpperBound(action, 4)).toMatchObject({
        valid: true,
        probe_window_count: 0,
        probe_upper_bound_ms: 0,
        wait_upper_bound_ms: 0,
      });
    },
  );

  it("returns a safe invalid state for unusable draft timing", () => {
    expect(
      estimateRetryWaitUpperBound(
        {
          type: "retry_only",
          max_retries: 1,
          backoff: { ...BACKOFF, multiplier: 0.5 },
        },
        3,
      ),
    ).toEqual({
      valid: false,
      error: "multiplier must be >= 1.0 (or 0 for default)",
    });
  });
});
