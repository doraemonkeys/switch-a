import { useState, useEffect, useCallback } from "react";
import { useApi } from "../api";
import type { StatsParams, StatsResponse } from "../api/client";

interface UseStatsResult {
  /** Statistics data */
  stats: StatsResponse | null;
  /** Loading state */
  loading: boolean;
  /** Error from the last fetch */
  error: Error | null;
  /** Refetch stats with current params */
  refetch: () => Promise<void>;
  /** Set new params (triggers refetch) */
  setParams: (params: StatsParams) => void;
  /** Current params */
  params: StatsParams;
}

/**
 * Hook for fetching system statistics
 * Supports time period and granularity for time series data
 */
export function useStats(initialParams?: StatsParams): UseStatsResult {
  const api = useApi();
  const [stats, setStats] = useState<StatsResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<Error | null>(null);
  const [params, setParams] = useState<StatsParams>(initialParams ?? {});

  const refetch = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const data = await api.stats.get(params);
      setStats(data);
    } catch (err) {
      setError(err instanceof Error ? err : new Error("Failed to fetch stats"));
    } finally {
      setLoading(false);
    }
  }, [api, params]);

  useEffect(() => {
    refetch();
  }, [refetch]);

  return {
    stats,
    loading,
    error,
    refetch,
    setParams,
    params,
  };
}
