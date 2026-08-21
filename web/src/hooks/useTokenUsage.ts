import { useApi } from "../api";
import type { TokenUsageParams, TokenUsageResponse } from "../api/types";
import { useQuery } from "./useQuery";

const EMPTY_TOKEN_USAGE_PARAMS: TokenUsageParams = {};

export interface UseTokenUsageResult {
  data: TokenUsageResponse | null;
  loading: boolean;
  error: Error | null;
  refetch: () => Promise<void>;
}

/** Fetches the token report identified by the controlled analytics window. */
export function useTokenUsage(
  params: TokenUsageParams = EMPTY_TOKEN_USAGE_PARAMS,
): UseTokenUsageResult {
  const api = useApi();
  const queryKey = JSON.stringify([
    params.period,
    params.granularity,
    params.as_of,
    params.provider_id,
    params.model,
    params.api_type,
  ]);
  const query = useQuery(() => api.tokenUsage.get(params), {
    queryKey,
    errorMessage: "Failed to fetch token usage analytics",
  });

  return {
    data: query.data,
    loading: query.loading,
    error: query.error,
    refetch: query.refetch,
  };
}
