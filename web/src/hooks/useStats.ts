import { useState } from "react";
import { useApi } from "../api";
import type { StatsParams, StatsResponse } from "../api/client";
import { useQuery } from "./useQuery";

interface UseStatsResult {
  stats: StatsResponse | null;
  loading: boolean;
  error: Error | null;
  refetch: () => Promise<void>;
  setParams: (params: StatsParams) => void;
  params: StatsParams;
}

/** Fetches the server snapshot identified by the current statistics params. */
export function useStats(initialParams?: StatsParams): UseStatsResult {
  const api = useApi();
  const [params, setParams] = useState<StatsParams>(initialParams ?? {});
  const query = useQuery(() => api.stats.get(params), {
    queryKey: params,
    errorMessage: "Failed to fetch stats",
  });

  return {
    stats: query.data,
    loading: query.loading,
    error: query.error,
    refetch: query.refetch,
    setParams,
    params,
  };
}
