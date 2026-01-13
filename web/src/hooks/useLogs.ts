import { useState, useEffect, useCallback } from "react";
import { useApi } from "../api";
import type { RequestLog, LogFilter, LogsResponse } from "../api/client";

interface UseLogsResult {
  logs: RequestLog[];
  total: number;
  loading: boolean;
  error: Error | null;
  refetch: () => Promise<void>;
  /** @deprecated Use setFilter instead */
  setParams: (filter: LogFilter) => void;
  setFilter: (filter: LogFilter) => void;
  updateFilter: (filter: Partial<LogFilter>) => void;
  /** @deprecated Use filter instead */
  params: LogFilter;
  filter: LogFilter;
  /** Sort field and direction from the response */
  sortBy: string;
  sortOrder: string;
}

export const DEFAULT_LIMIT = 20;

export function useLogs(initialFilter?: LogFilter): UseLogsResult {
  const api = useApi();
  const [logs, setLogs] = useState<RequestLog[]>([]);
  const [total, setTotal] = useState(0);
  const [sortBy, setSortBy] = useState("created_at");
  const [sortOrder, setSortOrder] = useState("desc");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<Error | null>(null);
  const [filter, setFilter] = useState<LogFilter>({
    limit: initialFilter?.limit ?? DEFAULT_LIMIT,
    offset: initialFilter?.offset ?? 0,
    ...initialFilter,
  });

  const refetch = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const response: LogsResponse = await api.logs.list(filter);
      setLogs(response.logs);
      setTotal(response.total);
      setSortBy(response.sort_by);
      setSortOrder(response.sort_order);
    } catch (err) {
      setError(err instanceof Error ? err : new Error("Failed to fetch logs"));
    } finally {
      setLoading(false);
    }
  }, [api, filter]);

  useEffect(() => {
    refetch();
  }, [refetch]);

  // Partial update helper for convenience
  // Automatically resets offset to 0 when filter criteria (not pagination) changes
  const updateFilter = useCallback((partial: Partial<LogFilter>) => {
    setFilter((prev) => {
      // Check if this is a pagination-only update
      const isPaginationOnly =
        Object.keys(partial).length === 1 &&
        (partial.offset !== undefined || partial.limit !== undefined);

      // Reset offset to 0 when filter criteria changes (not pagination)
      const newFilter = { ...prev, ...partial };
      if (!isPaginationOnly && partial.offset === undefined) {
        newFilter.offset = 0;
      }

      return newFilter;
    });
  }, []);

  return {
    logs,
    total,
    loading,
    error,
    refetch,
    // Backward compatible aliases
    setParams: setFilter,
    params: filter,
    // New API
    setFilter,
    updateFilter,
    filter,
    sortBy,
    sortOrder,
  };
}
