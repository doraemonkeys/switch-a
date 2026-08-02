import { useApi } from "../api";
import type { ActiveRequest } from "../api/client";
import { usePollingQuery } from "./usePollingQuery";

const DEFAULT_POLL_INTERVAL_MS = 5_000;

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

/** Tracks active requests through the shared visibility-aware polling policy. */
export function useLiveRequests(
  options: UseLiveRequestsOptions = {},
): UseLiveRequestsResult {
  const { pollInterval = DEFAULT_POLL_INTERVAL_MS, enabled = true } = options;
  const api = useApi();
  const query = usePollingQuery(() => api.requests.active(), {
    intervalMs: pollInterval,
    enabled,
    errorMessage: "Failed to fetch active requests",
  });

  return {
    requests: query.data?.requests ?? [],
    count: query.data?.count ?? 0,
    loading: query.loading,
    error: query.error,
    refetch: query.refetch,
    isPolling: query.isPolling,
  };
}
