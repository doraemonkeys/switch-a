import { useState, useCallback } from "react";
import { useApi } from "../api";
import type {
  BatchAction,
  BatchProviderRequest,
  BatchProviderResponse,
} from "../api/client";

interface UseProviderBatchResult {
  /** Execute a batch operation */
  batchAction: (
    action: BatchAction,
    ids: string[],
  ) => Promise<BatchProviderResponse>;
  /** Loading state for batch operation */
  loading: boolean;
  /** Error from the last batch operation */
  error: Error | null;
  /** Result from the last batch operation */
  result: BatchProviderResponse | null;
  /** Reset the error and result state */
  reset: () => void;
}

/**
 * Hook for performing batch operations on providers
 * Supports: reset, enable, disable, delete
 */
export function useProviderBatch(): UseProviderBatchResult {
  const api = useApi();
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<Error | null>(null);
  const [result, setResult] = useState<BatchProviderResponse | null>(null);

  const batchAction = useCallback(
    async (
      action: BatchAction,
      ids: string[],
    ): Promise<BatchProviderResponse> => {
      setLoading(true);
      setError(null);
      setResult(null);
      try {
        const request: BatchProviderRequest = { action, ids };
        const response = await api.providers.batch(request);
        setResult(response);
        return response;
      } catch (err) {
        const error =
          err instanceof Error
            ? err
            : new Error("Failed to execute batch operation");
        setError(error);
        throw error;
      } finally {
        setLoading(false);
      }
    },
    [api],
  );

  const reset = useCallback(() => {
    setError(null);
    setResult(null);
  }, []);

  return {
    batchAction,
    loading,
    error,
    result,
    reset,
  };
}
