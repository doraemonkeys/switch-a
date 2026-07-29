import { ApiError, useApi } from "../api";
import type { RoutingPolicy, RoutingPolicyInput } from "../api";
import { useQuery } from "./useQuery";

const FEATURE_UNAVAILABLE_MESSAGE =
  "Routing policy management is not available on this server build yet.";

interface RoutingPolicySnapshot {
  policies: RoutingPolicy[];
  available: boolean;
}

interface UseRoutingPoliciesResult {
  policies: RoutingPolicy[];
  loading: boolean;
  error: Error | null;
  available: boolean;
  refetch: () => Promise<void>;
  createPolicy: (data: RoutingPolicyInput) => Promise<RoutingPolicy>;
  updatePolicy: (
    id: string,
    data: RoutingPolicyInput,
  ) => Promise<RoutingPolicy>;
  deletePolicy: (id: string) => Promise<void>;
}

function isFeatureUnavailableError(error: unknown): boolean {
  return (
    error instanceof ApiError &&
    (error.status === 404 || error.status === 501 || error.status === 405)
  );
}

export function useRoutingPolicies(): UseRoutingPoliciesResult {
  const api = useApi();
  const query = useQuery<RoutingPolicySnapshot>(
    async () => {
      try {
        return {
          policies: await api.routingPolicies.list(),
          available: true,
        };
      } catch (error) {
        if (isFeatureUnavailableError(error)) {
          return { policies: [], available: false };
        }
        throw error;
      }
    },
    {
      errorMessage: "Failed to fetch routing policies",
    },
  );
  const available = query.data?.available ?? true;

  const assertAvailable = () => {
    if (!available) {
      throw new Error(FEATURE_UNAVAILABLE_MESSAGE);
    }
  };

  const createPolicy = async (
    data: RoutingPolicyInput,
  ): Promise<RoutingPolicy> => {
    assertAvailable();
    const policy = await api.routingPolicies.create(data);
    await query.refetch();
    return policy;
  };

  const updatePolicy = async (
    id: string,
    data: RoutingPolicyInput,
  ): Promise<RoutingPolicy> => {
    assertAvailable();
    const policy = await api.routingPolicies.update(id, data);
    await query.refetch();
    return policy;
  };

  const deletePolicy = async (id: string): Promise<void> => {
    assertAvailable();
    await api.routingPolicies.delete(id);
    await query.refetch();
  };

  return {
    policies: query.data?.policies ?? [],
    loading: query.loading,
    error: query.error,
    available,
    refetch: query.refetch,
    createPolicy,
    updatePolicy,
    deletePolicy,
  };
}
