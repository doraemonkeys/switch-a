import { useEffect, useEffectEvent, useRef, useState } from "react";
import {
  useQuery,
  type UseQueryOptions,
  type UseQueryResult,
} from "./useQuery";

const DEFAULT_MAX_POLL_INTERVAL_MS = 60_000;

export interface UsePollingQueryOptions extends UseQueryOptions {
  intervalMs: number;
  enabled?: boolean;
  maxIntervalMs?: number;
}

export interface UsePollingQueryResult<T> extends UseQueryResult<T> {
  isPolling: boolean;
}

/**
 * Keeps one server snapshot current without allowing each page to reinvent
 * visibility handling, stale-response protection, and failure backoff.
 */
export function usePollingQuery<T>(
  fetcher: () => Promise<T>,
  options: UsePollingQueryOptions,
): UsePollingQueryResult<T> {
  const {
    intervalMs,
    enabled = true,
    maxIntervalMs = DEFAULT_MAX_POLL_INTERVAL_MS,
    queryKey,
    errorMessage,
  } = options;
  const [consecutiveFailures, setConsecutiveFailures] = useState(0);
  const activeRequestCount = useRef(0);
  const pollingEnabled = enabled && intervalMs > 0;

  const query = useQuery(
    async () => {
      activeRequestCount.current += 1;
      try {
        const data = await fetcher();
        setConsecutiveFailures(0);
        return data;
      } catch (reason) {
        setConsecutiveFailures((current) => current + 1);
        throw reason;
      } finally {
        activeRequestCount.current -= 1;
      }
    },
    {
      skip: !enabled,
      queryKey,
      errorMessage,
    },
  );

  const effectiveIntervalMs = Math.min(
    intervalMs * Math.pow(2, consecutiveFailures),
    maxIntervalMs,
  );
  const refetch = async (): Promise<void> => {
    // A slow snapshot remains authoritative until it settles. Starting another
    // poll would only invalidate that response and can starve publication when
    // latency is consistently longer than the configured interval.
    if (query.loading || activeRequestCount.current > 0) return;
    await query.refetch();
  };
  const pollNow = useEffectEvent(() => {
    void refetch();
  });

  useEffect(() => {
    if (!pollingEnabled) return;

    let isVisible = document.visibilityState === "visible";
    const handleVisibilityChange = () => {
      isVisible = document.visibilityState === "visible";
      if (isVisible) {
        pollNow();
      }
    };
    const intervalId = window.setInterval(() => {
      if (isVisible) {
        pollNow();
      }
    }, effectiveIntervalMs);

    document.addEventListener("visibilitychange", handleVisibilityChange);
    return () => {
      document.removeEventListener("visibilitychange", handleVisibilityChange);
      window.clearInterval(intervalId);
    };
  }, [effectiveIntervalMs, pollingEnabled]);

  return {
    ...query,
    refetch,
    isPolling: pollingEnabled,
  };
}
