import { parseGoDurationMilliseconds } from "@/lib/utils";
import type { RuleBackoffPolicy } from "../contracts";

export const MAX_RULE_RETRIES = 10;
export const DEFAULT_RULE_BACKOFF_MULTIPLIER = 2;

// Go caps an uncapped time.Duration before converting the floating-point delay
// back to int64 nanoseconds. Mirroring that ceiling avoids optimistic UI sums.
const MAX_GO_DURATION_MILLISECONDS = 9_223_372_036_854.775;

export type BackoffCalculation =
  | {
      readonly valid: true;
      readonly effective_multiplier: number;
      readonly base_delays_ms: readonly number[];
    }
  | { readonly valid: false; readonly error: string };

function parseSignedGoDurationMilliseconds(value: string): number | null {
  const duration = value.trim();
  let sign = 1;
  let unsigned = duration;
  if (duration.startsWith("-")) {
    sign = -1;
    unsigned = duration.slice(1);
  } else if (duration.startsWith("+")) {
    unsigned = duration.slice(1);
  }
  const milliseconds = parseGoDurationMilliseconds(unsigned);
  return milliseconds === null ? null : sign * milliseconds;
}

/**
 * Produces the same unjittered bases as model.BackoffPolicy.DelayForRetry.
 * Jitter never exceeds the base, so these values are also the wait upper bound.
 */
export function calculateBackoffBaseDelays(
  backoff: RuleBackoffPolicy,
  maxRetries: number,
): BackoffCalculation {
  if (
    !Number.isSafeInteger(maxRetries) ||
    maxRetries < 0 ||
    maxRetries > MAX_RULE_RETRIES
  ) {
    return {
      valid: false,
      error: `max_retries must be between 0 and ${MAX_RULE_RETRIES}`,
    };
  }

  const initialDelayMs = parseSignedGoDurationMilliseconds(
    backoff.initial_delay,
  );
  if (initialDelayMs === null) {
    return {
      valid: false,
      error: "initial_delay must be a valid Go duration",
    };
  }
  if (initialDelayMs < 0) {
    return { valid: false, error: "initial_delay must be non-negative" };
  }

  const maxDelayMs = parseSignedGoDurationMilliseconds(backoff.max_delay);
  if (maxDelayMs === null) {
    return { valid: false, error: "max_delay must be a valid Go duration" };
  }
  if (maxDelayMs > 0 && initialDelayMs > maxDelayMs) {
    return {
      valid: false,
      error: "initial_delay cannot exceed max_delay",
    };
  }

  if (
    !Number.isFinite(backoff.multiplier) ||
    (backoff.multiplier < 1 && backoff.multiplier !== 0)
  ) {
    return {
      valid: false,
      error: "multiplier must be >= 1.0 (or 0 for default)",
    };
  }

  const effectiveMultiplier =
    backoff.multiplier === 0
      ? DEFAULT_RULE_BACKOFF_MULTIPLIER
      : backoff.multiplier;
  const delayCeiling =
    maxDelayMs > 0 ? maxDelayMs : MAX_GO_DURATION_MILLISECONDS;
  const baseDelays = Array.from({ length: maxRetries }, (_, retryIndex) => {
    if (initialDelayMs === 0) {
      return 0;
    }
    const delay = initialDelayMs * effectiveMultiplier ** retryIndex;
    return Number.isFinite(delay)
      ? Math.min(delay, delayCeiling)
      : delayCeiling;
  });

  return {
    valid: true,
    effective_multiplier: effectiveMultiplier,
    base_delays_ms: Object.freeze(baseDelays),
  };
}
