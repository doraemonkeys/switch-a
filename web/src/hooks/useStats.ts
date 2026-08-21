import { useApi } from "../api";
import type { StatsParams, StatsResponse } from "../api/client";
import { useQuery } from "./useQuery";

const EMPTY_STATS_PARAMS: StatsParams = {};

interface UseStatsResult {
  stats: StatsResponse | null;
  loading: boolean;
  error: Error | null;
  refetch: () => Promise<void>;
}

/** Fetches the server snapshot identified by the controlled statistics params. */
export function useStats(
  params: StatsParams = EMPTY_STATS_PARAMS,
): UseStatsResult {
  const api = useApi();
  const queryKey = JSON.stringify([
    params.period,
    params.granularity,
    params.as_of,
  ]);
  const query = useQuery(() => api.stats.get(params), {
    queryKey,
    errorMessage: "Failed to fetch stats",
  });

  return {
    stats: query.data,
    loading: query.loading,
    error: query.error,
    refetch: query.refetch,
  };
}
