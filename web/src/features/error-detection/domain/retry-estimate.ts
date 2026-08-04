import type { InternalErrorRuleAction } from "../contracts";
import { MAX_RULE_RETRIES, calculateBackoffBaseDelays } from "./backoff";

export const DEFAULT_PROBE_DURATION_MS = 2_000;

export interface RetryWaitEstimate {
  readonly valid: true;
  readonly effective_same_provider_retries: number;
  readonly switch_attempt_reserved: boolean;
  readonly probe_window_count: number;
  readonly probe_upper_bound_ms: number;
  readonly backoff_base_delays_ms: readonly number[];
  readonly backoff_upper_bound_ms: number;
  readonly wait_upper_bound_ms: number;
}

export interface InvalidRetryWaitEstimate {
  readonly valid: false;
  readonly error: string;
}

export function effectiveSameProviderRetries(
  action: InternalErrorRuleAction,
  globalMaxAttempts: number,
): number {
  if (!Number.isSafeInteger(globalMaxAttempts) || globalMaxAttempts < 0) {
    throw new RangeError("global_max_attempts must be a non-negative integer");
  }
  if (action.type === "passthrough") {
    return 0;
  }
  if (
    !Number.isSafeInteger(action.max_retries) ||
    action.max_retries < 0 ||
    action.max_retries > MAX_RULE_RETRIES
  ) {
    throw new RangeError(
      `max_retries must be between 0 and ${MAX_RULE_RETRIES}`,
    );
  }
  if (globalMaxAttempts === 0) {
    return action.max_retries;
  }

  const attemptsUnavailableToSameProvider =
    action.type === "retry_then_switch" ? 2 : 1;
  return Math.min(
    action.max_retries,
    Math.max(0, globalMaxAttempts - attemptsUnavailableToSameProvider),
  );
}

/**
 * Estimates only delay controlled by this feature. Network and switched-provider
 * time remain excluded because the browser cannot bound either honestly.
 */
export function estimateRetryWaitUpperBound(
  action: InternalErrorRuleAction,
  globalMaxAttempts: number,
  probeDurationMs = DEFAULT_PROBE_DURATION_MS,
): RetryWaitEstimate | InvalidRetryWaitEstimate {
  if (!Number.isFinite(probeDurationMs) || probeDurationMs < 0) {
    return {
      valid: false,
      error: "probe duration must be a non-negative finite number",
    };
  }

  let sameProviderRetries: number;
  try {
    sameProviderRetries = effectiveSameProviderRetries(
      action,
      globalMaxAttempts,
    );
  } catch (error) {
    return {
      valid: false,
      error: error instanceof Error ? error.message : "invalid retry budget",
    };
  }

  const switchAttemptReserved =
    action.type === "retry_then_switch" && globalMaxAttempts > 1;
  const backoff =
    action.type === "passthrough"
      ? {
          valid: true as const,
          effective_multiplier: 1,
          base_delays_ms: [] as readonly number[],
        }
      : calculateBackoffBaseDelays(action.backoff, sameProviderRetries);
  if (!backoff.valid) {
    return backoff;
  }

  const hasRetriableCandidate =
    action.type === "retry_then_switch" ||
    (action.type === "retry_only" && action.max_retries > 0);
  const probeWindowCount = hasRetriableCandidate ? 1 + sameProviderRetries : 0;
  const probeUpperBoundMs = probeWindowCount * probeDurationMs;
  const backoffUpperBoundMs = backoff.base_delays_ms.reduce(
    (total, delay) => total + delay,
    0,
  );

  return {
    valid: true,
    effective_same_provider_retries: sameProviderRetries,
    switch_attempt_reserved: switchAttemptReserved,
    probe_window_count: probeWindowCount,
    probe_upper_bound_ms: probeUpperBoundMs,
    backoff_base_delays_ms: backoff.base_delays_ms,
    backoff_upper_bound_ms: backoffUpperBoundMs,
    wait_upper_bound_ms: probeUpperBoundMs + backoffUpperBoundMs,
  };
}
