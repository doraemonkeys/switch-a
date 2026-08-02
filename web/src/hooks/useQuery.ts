import { useEffect, useEffectEvent, useRef, useState } from "react";

const DEFAULT_QUERY_ERROR_MESSAGE = "Failed to fetch data";
const DEFAULT_MUTATION_ERROR_MESSAGE = "Operation failed";
const UNRESOLVED_QUERY_KEY = Symbol("unresolved-query-key");

export interface UseQueryResult<T> {
  data: T | null;
  loading: boolean;
  error: Error | null;
  refetch: () => Promise<void>;
  replaceData: (data: T) => void;
  updateData: (updater: (current: T | null) => T | null) => void;
}

export interface UseQueryOptions {
  /** Skip both the initial request and manual refreshes. */
  skip?: boolean;
  /** A semantic key whose changes require a new server snapshot. */
  queryKey?: unknown;
  /** Error message used when a dependency rejects with a non-Error value. */
  errorMessage?: string;
}

interface QueryState<T> {
  data: T | null;
  error: Error | null;
  resolvedKey: unknown;
  refreshing: boolean;
}

function normalizeError(reason: unknown, fallback: string): Error {
  return reason instanceof Error ? reason : new Error(fallback);
}

/**
 * Synchronizes one semantic query key with an external data source.
 *
 * The resolved key is stored with the result so loading can be derived during
 * render. This avoids an extra render whose only purpose is to mirror a key
 * change into loading state, while request IDs prevent stale responses from
 * replacing newer snapshots.
 */
export function useQuery<T>(
  fetcher: () => Promise<T>,
  options: UseQueryOptions = {},
): UseQueryResult<T> {
  const {
    skip = false,
    queryKey,
    errorMessage = DEFAULT_QUERY_ERROR_MESSAGE,
  } = options;
  const [state, setState] = useState<QueryState<T>>({
    data: null,
    error: null,
    resolvedKey: UNRESOLVED_QUERY_KEY,
    refreshing: false,
  });
  const requestSequence = useRef(0);

  const synchronize = useEffectEvent(
    async (requestId: number, requestedKey: unknown) => {
      // Yield before touching React state so the Effect only initiates external
      // synchronization; completions, rather than the Effect body, publish it.
      await Promise.resolve();
      if (requestSequence.current === requestId) {
        setState((current) => ({
          ...current,
          error: null,
          refreshing: true,
        }));
      }
      try {
        const data = await fetcher();
        if (requestSequence.current === requestId) {
          setState({
            data,
            error: null,
            resolvedKey: requestedKey,
            refreshing: false,
          });
        }
      } catch (reason) {
        if (requestSequence.current === requestId) {
          setState((current) => ({
            ...current,
            error: normalizeError(reason, errorMessage),
            resolvedKey: requestedKey,
            refreshing: false,
          }));
        }
      }
    },
  );

  useEffect(() => {
    if (skip) return;

    const requestId = ++requestSequence.current;
    void synchronize(requestId, queryKey);

    return () => {
      // Invalidating the sequence is enough to cancel publication even when an
      // underlying client cannot cancel its network request.
      requestSequence.current += 1;
    };
  }, [queryKey, skip]);

  const refetch = async (): Promise<void> => {
    if (skip) return;

    const requestId = ++requestSequence.current;
    setState((current) => ({ ...current, error: null, refreshing: true }));
    try {
      const data = await fetcher();
      if (requestSequence.current === requestId) {
        setState({
          data,
          error: null,
          resolvedKey: queryKey,
          refreshing: false,
        });
      }
    } catch (reason) {
      if (requestSequence.current === requestId) {
        setState((current) => ({
          ...current,
          error: normalizeError(reason, errorMessage),
          resolvedKey: queryKey,
          refreshing: false,
        }));
      }
    }
  };

  const replaceData = (data: T): void => {
    // A confirmed mutation result is newer than every request already in flight.
    requestSequence.current += 1;
    setState({
      data,
      error: null,
      resolvedKey: queryKey,
      refreshing: false,
    });
  };
  const updateData = (updater: (current: T | null) => T | null): void => {
    // Functional publication makes async mutation reconciliation atomic against
    // status snapshots that arrived while the mutation was awaiting the server.
    requestSequence.current += 1;
    setState((current) => ({
      data: updater(current.data),
      error: null,
      resolvedKey: queryKey,
      refreshing: false,
    }));
  };
  const hasCurrentSnapshot = Object.is(state.resolvedKey, queryKey);

  return {
    data: state.data,
    loading: !skip && (state.refreshing || !hasCurrentSnapshot),
    error: !skip && hasCurrentSnapshot ? state.error : null,
    refetch,
    replaceData,
    updateData,
  };
}

export interface UseMutationResult<TData, TInput> {
  mutate: (input: TInput) => Promise<TData>;
  loading: boolean;
  error: Error | null;
}

/** Generic state handling for mutations initiated by user events. */
export function useMutation<TData, TInput>(
  mutator: (input: TInput) => Promise<TData>,
  options?: { onSuccess?: () => void; errorMessage?: string },
): UseMutationResult<TData, TInput> {
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<Error | null>(null);
  const { onSuccess, errorMessage = DEFAULT_MUTATION_ERROR_MESSAGE } =
    options ?? {};

  const mutate = async (input: TInput): Promise<TData> => {
    setLoading(true);
    setError(null);
    try {
      const result = await mutator(input);
      onSuccess?.();
      return result;
    } catch (reason) {
      const mutationError = normalizeError(reason, errorMessage);
      setError(mutationError);
      throw mutationError;
    } finally {
      setLoading(false);
    }
  };

  return { mutate, loading, error };
}
