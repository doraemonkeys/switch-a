import { useState, useEffect, useCallback, useRef } from "react";

export interface UseQueryResult<T> {
  data: T | null;
  loading: boolean;
  error: Error | null;
  refetch: () => Promise<void>;
}

export interface UseQueryOptions {
  /** Skip initial fetch */
  skip?: boolean;
  /** Error message prefix */
  errorMessage?: string;
}

/**
 * Generic hook for handling async data fetching with loading/error states.
 * Reduces boilerplate across useProviders, useStatus, useLogs, etc.
 */
export function useQuery<T>(
  fetcher: () => Promise<T>,
  deps: React.DependencyList = [],
  options: UseQueryOptions = {},
): UseQueryResult<T> {
  const { skip = false, errorMessage = "Failed to fetch data" } = options;

  const [data, setData] = useState<T | null>(null);
  const [loading, setLoading] = useState(!skip);
  const [error, setError] = useState<Error | null>(null);

  // Use ref to track if component is mounted
  const mountedRef = useRef(true);

  const refetch = useCallback(async () => {
    if (skip) return;

    setLoading(true);
    setError(null);
    try {
      const result = await fetcher();
      if (mountedRef.current) {
        setData(result);
      }
    } catch (err) {
      if (mountedRef.current) {
        setError(err instanceof Error ? err : new Error(errorMessage));
      }
    } finally {
      if (mountedRef.current) {
        setLoading(false);
      }
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [skip, errorMessage, ...deps]);

  useEffect(() => {
    mountedRef.current = true;
    refetch();
    return () => {
      mountedRef.current = false;
    };
  }, [refetch]);

  return { data, loading, error, refetch };
}

export interface UseMutationResult<TData, TInput> {
  mutate: (input: TInput) => Promise<TData>;
  loading: boolean;
  error: Error | null;
}

/**
 * Generic hook for handling async mutations with loading/error states.
 */
export function useMutation<TData, TInput>(
  mutator: (input: TInput) => Promise<TData>,
  options?: { onSuccess?: () => void; errorMessage?: string },
): UseMutationResult<TData, TInput> {
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<Error | null>(null);
  const { onSuccess, errorMessage = "Operation failed" } = options ?? {};

  const mutate = useCallback(
    async (input: TInput): Promise<TData> => {
      setLoading(true);
      setError(null);
      try {
        const result = await mutator(input);
        onSuccess?.();
        return result;
      } catch (err) {
        const error = err instanceof Error ? err : new Error(errorMessage);
        setError(error);
        throw error;
      } finally {
        setLoading(false);
      }
    },
    [mutator, onSuccess, errorMessage],
  );

  return { mutate, loading, error };
}
