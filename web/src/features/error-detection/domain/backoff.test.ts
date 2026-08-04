import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import type { RuleBackoffPolicy } from "../contracts";
import { calculateBackoffBaseDelays } from "./backoff";

interface BackoffFixtureCase {
  readonly name: string;
  readonly max_retries: number;
  readonly backoff: RuleBackoffPolicy;
  readonly valid: boolean;
  readonly base_delays_ms?: readonly number[];
  readonly error?: string;
}

describe("calculateBackoffBaseDelays", () => {
  it("agrees with every Go-backed boundary fixture", () => {
    const fixture = JSON.parse(
      readFileSync(
        resolve(
          process.cwd(),
          "../contracts/internal-error/v1/backoff-policy.json",
        ),
        "utf8",
      ),
    ) as { validation_cases: readonly BackoffFixtureCase[] };

    for (const testCase of fixture.validation_cases) {
      const result = calculateBackoffBaseDelays(
        testCase.backoff,
        testCase.max_retries,
      );
      expect(result.valid, testCase.name).toBe(testCase.valid);
      if (result.valid) {
        expect(result.base_delays_ms, testCase.name).toEqual(
          testCase.base_delays_ms,
        );
      } else {
        expect(result.error, testCase.name).toBe(testCase.error);
      }
    }
  });

  it("rejects malformed durations and non-finite multipliers", () => {
    expect(
      calculateBackoffBaseDelays(
        {
          initial_delay: "later",
          max_delay: "0s",
          multiplier: 2,
          jitter: false,
        },
        1,
      ),
    ).toMatchObject({ valid: false });
    expect(
      calculateBackoffBaseDelays(
        {
          initial_delay: "100ms",
          max_delay: "0s",
          multiplier: Number.POSITIVE_INFINITY,
          jitter: false,
        },
        1,
      ),
    ).toMatchObject({ valid: false });
  });
});
