import { useApi } from "../api";
import type {
  HealthState,
  SystemStatus,
  SystemStatusSummary,
} from "../api/client";
import { useQuery } from "./useQuery";

interface UseStatusResult {
  status: SystemStatus | null;
  summary: SystemStatusSummary | null;
  loading: boolean;
  error: Error | null;
  refetch: () => Promise<void>;
}

function summarizeStatus(
  status: SystemStatus | null,
): SystemStatusSummary | null {
  if (!status) return null;

  const total = status.providers.length;
  const healthy = status.providers.filter(
    (provider) => provider.enabled && provider.health?.available !== false,
  ).length;

  return {
    providers_total: total,
    providers_healthy: healthy,
    providers_unhealthy: total - healthy,
    // Request volume has a separate lifecycle and cannot be inferred from the
    // provider health snapshot exposed by this endpoint.
    requests_today: 0,
  };
}

export function useStatus(): UseStatusResult {
  const api = useApi();
  const query = useQuery(() => api.status.get(), {
    errorMessage: "Failed to fetch status",
  });

  return {
    status: query.data,
    summary: summarizeStatus(query.data),
    loading: query.loading,
    error: query.error,
    refetch: query.refetch,
  };
}

interface UseHealthStatesResult {
  healthStates: HealthState[];
  loading: boolean;
  error: Error | null;
  refetch: () => Promise<void>;
}

export function useHealthStates(): UseHealthStatesResult {
  const api = useApi();
  const query = useQuery(() => api.status.health(), {
    errorMessage: "Failed to fetch health states",
  });

  return {
    healthStates: query.data ?? [],
    loading: query.loading,
    error: query.error,
    refetch: query.refetch,
  };
}
