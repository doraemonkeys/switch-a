import { useEffect, useEffectEvent, useState } from "react";
import { useApi } from "../api";
import type { ActiveRequest } from "../api/client";
import { useQuery } from "./useQuery";

const DEFAULT_POLL_INTERVAL = 5_000;
const MAX_BACKOFF_INTERVAL = 60_000;

interface UseLiveRequestsOptions {
  pollInterval?: number;
  enabled?: boolean;
}

interface UseLiveRequestsResult {
  requests: ActiveRequest[];
  count: number;
  loading: boolean;
  error: Error | null;
  refetch: () => Promise<void>;
  isPolling: boolean;
}

/**
 * Tracks active requests while respecting tab visibility and backing off after
 * failures. Query publication and polling lifecycle remain separate so timer
 * changes never create a second initial request.
 */
export function useLiveRequests(
  options: UseLiveRequestsOptions = {},
): UseLiveRequestsResult {
  const { pollInterval = DEFAULT_POLL_INTERVAL, enabled = true } = options;
  const api = useApi();
  const [consecutiveErrors, setConsecutiveErrors] = useState(0);

  const query = useQuery(
    async () => {
      try {
        const response = await api.requests.active();
        setConsecutiveErrors(0);
        return response;
      } catch (error) {
        setConsecutiveErrors((current) => current + 1);
        throw error;
      }
    },
    {
      skip: !enabled,
      errorMessage: "Failed to fetch active requests",
    },
  );

  const effectiveInterval = Math.min(
    pollInterval * Math.pow(2, consecutiveErrors),
    MAX_BACKOFF_INTERVAL,
  );
  const pollNow = useEffectEvent(() => {
    query.refetch();
  });

  useEffect(() => {
    if (!enabled) return;

    let isVisible = document.visibilityState === "visible";
    const handleVisibilityChange = () => {
      isVisible = document.visibilityState === "visible";
      if (isVisible) pollNow();
    };
    const intervalId = window.setInterval(() => {
      if (isVisible) pollNow();
    }, effectiveInterval);

    document.addEventListener("visibilitychange", handleVisibilityChange);
    return () => {
      document.removeEventListener("visibilitychange", handleVisibilityChange);
      window.clearInterval(intervalId);
    };
  }, [effectiveInterval, enabled]);

  return {
    requests: query.data?.requests ?? [],
    count: query.data?.count ?? 0,
    loading: query.loading,
    error: query.error,
    refetch: query.refetch,
    isPolling: enabled,
  };
}
