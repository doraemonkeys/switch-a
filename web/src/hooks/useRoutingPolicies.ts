import { useState, useEffect, useCallback } from "react";
import { ApiError, useApi } from "../api";
import type { RoutingPolicy, RoutingPolicyInput } from "../api";

const FEATURE_UNAVAILABLE_MESSAGE =
  "Routing policy management is not available on this server build yet.";

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
  const [policies, setPolicies] = useState<RoutingPolicy[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<Error | null>(null);
  const [available, setAvailable] = useState(true);

  const refetch = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const data = await api.routingPolicies.list();
      setPolicies(data);
      setAvailable(true);
    } catch (err) {
      if (isFeatureUnavailableError(err)) {
        setPolicies([]);
        setAvailable(false);
      } else {
        setError(
          err instanceof Error
            ? err
            : new Error("Failed to fetch routing policies"),
        );
      }
    } finally {
      setLoading(false);
    }
  }, [api]);

  useEffect(() => {
    void refetch();
  }, [refetch]);

  const createPolicy = useCallback(
    async (data: RoutingPolicyInput): Promise<RoutingPolicy> => {
      if (!available) {
        throw new Error(FEATURE_UNAVAILABLE_MESSAGE);
      }
      const policy = await api.routingPolicies.create(data);
      await refetch();
      return policy;
    },
    [api, available, refetch],
  );

  const updatePolicy = useCallback(
    async (id: string, data: RoutingPolicyInput): Promise<RoutingPolicy> => {
      if (!available) {
        throw new Error(FEATURE_UNAVAILABLE_MESSAGE);
      }
      const policy = await api.routingPolicies.update(id, data);
      await refetch();
      return policy;
    },
    [api, available, refetch],
  );

  const deletePolicy = useCallback(
    async (id: string): Promise<void> => {
      if (!available) {
        throw new Error(FEATURE_UNAVAILABLE_MESSAGE);
      }
      await api.routingPolicies.delete(id);
      await refetch();
    },
    [api, available, refetch],
  );

  return {
    policies,
    loading,
    error,
    available,
    refetch,
    createPolicy,
    updatePolicy,
    deletePolicy,
  };
}
