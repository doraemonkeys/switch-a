import { useState, useEffect, useRef } from "react";
import { useApi } from "../api";
import type { ActiveRequest } from "../api/client";

/** Default polling interval in milliseconds (5 seconds) */
const DEFAULT_POLL_INTERVAL = 5000;

/** Maximum polling interval during backoff (1 minute) */
const MAX_BACKOFF_INTERVAL = 60000;

interface UseLiveRequestsOptions {
  /** Polling interval in milliseconds (default: 5000) */
  pollInterval?: number;
  /** Whether to enable polling (default: true) */
  enabled?: boolean;
}

interface UseLiveRequestsResult {
  /** List of currently active requests */
  requests: ActiveRequest[];
  /** Total count of active requests */
  count: number;
  /** Whether data is currently being fetched */
  loading: boolean;
  /** Error if fetch failed */
  error: Error | null;
  /** Manually trigger a refetch */
  refetch: () => Promise<void>;
  /** Whether polling is currently active */
  isPolling: boolean;
}

/**
 * Hook for fetching live/active requests with automatic polling.
 *
 * Features:
 * - Automatic polling at configurable intervals (default 5s)
 * - Pauses polling when tab is not visible (visibility API)
 * - Resumes polling immediately when tab becomes visible
 * - Manual refetch capability
 */
export function useLiveRequests(
  options: UseLiveRequestsOptions = {},
): UseLiveRequestsResult {
  const { pollInterval = DEFAULT_POLL_INTERVAL, enabled = true } = options;

  const api = useApi();
  const [requests, setRequests] = useState<ActiveRequest[]>([]);
  const [count, setCount] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<Error | null>(null);
  const [isPolling, setIsPolling] = useState(false);
  const [consecutiveErrors, setConsecutiveErrors] = useState(0);

  // Track if tab is visible
  const isVisibleRef = useRef(true);
  // Store refetch in ref to avoid stale closure issues in effects
  const refetchRef = useRef<(() => Promise<void>) | undefined>(undefined);

  const refetch = async () => {
    setLoading(true);
    setError(null);
    try {
      const response = await api.requests.active();
      setRequests(response.requests);
      setCount(response.count);
      setConsecutiveErrors(0); // Reset error count on success
    } catch (err) {
      setError(
        err instanceof Error
          ? err
          : new Error("Failed to fetch active requests"),
      );
      setConsecutiveErrors((prev) => prev + 1);
    } finally {
      setLoading(false);
    }
  };

  // Keep refetch ref up-to-date
  refetchRef.current = refetch;

  // Initial fetch
  useEffect(() => {
    if (enabled) {
      refetchRef.current?.();
    }
  }, [enabled]);

  // Calculate effective polling interval with exponential backoff on errors
  const effectiveInterval = Math.min(
    pollInterval * Math.pow(2, consecutiveErrors),
    MAX_BACKOFF_INTERVAL,
  );

  // Polling with visibility detection
  useEffect(() => {
    if (!enabled) {
      setIsPolling(false);
      return;
    }

    // Handle visibility change
    const handleVisibilityChange = () => {
      isVisibleRef.current = document.visibilityState === "visible";
      // Immediately refetch when becoming visible
      if (isVisibleRef.current) {
        refetchRef.current?.();
      }
    };

    document.addEventListener("visibilitychange", handleVisibilityChange);
    isVisibleRef.current = document.visibilityState === "visible";

    // Set up polling interval with backoff applied
    setIsPolling(true);
    const intervalId = setInterval(() => {
      // Only poll if tab is visible
      if (isVisibleRef.current) {
        refetchRef.current?.();
      }
    }, effectiveInterval);

    return () => {
      document.removeEventListener("visibilitychange", handleVisibilityChange);
      clearInterval(intervalId);
      setIsPolling(false);
    };
  }, [enabled, effectiveInterval]);

  return {
    requests,
    count,
    loading,
    error,
    refetch,
    isPolling,
  };
}
