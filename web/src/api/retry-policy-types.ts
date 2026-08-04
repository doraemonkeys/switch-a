/**
 * Duration strings intentionally mirror Go duration syntax so retry policies
 * round-trip without introducing a second unit or conversion convention.
 */
export interface BackoffPolicy {
  initial_delay: string;
  max_delay: string;
  multiplier?: number;
  jitter?: boolean;
}
