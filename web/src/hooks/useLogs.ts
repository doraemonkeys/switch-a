import { useState } from "react";
import { useApi } from "../api";
import type { LogFilter, RequestLog } from "../api/client";
import { useQuery } from "./useQuery";

interface UseLogsResult {
  logs: RequestLog[];
  total: number;
  loading: boolean;
  error: Error | null;
  refetch: () => Promise<void>;
  setFilter: (filter: LogFilter) => void;
  updateFilter: (filter: Partial<LogFilter>) => void;
  filter: LogFilter;
  /** Sort field and direction from the response */
  sortBy: string;
  sortOrder: string;
}

export const DEFAULT_LIMIT = 20;
const DEFAULT_SORT_FIELD = "created_at";
const DEFAULT_SORT_ORDER = "desc";

export function useLogs(initialFilter?: LogFilter): UseLogsResult {
  const api = useApi();
  const [filter, setFilter] = useState<LogFilter>({
    limit: initialFilter?.limit ?? DEFAULT_LIMIT,
    offset: initialFilter?.offset ?? 0,
    ...initialFilter,
  });
  const query = useQuery(() => api.logs.list(filter), {
    queryKey: filter,
    errorMessage: "Failed to fetch logs",
  });

  const updateFilter = (partial: Partial<LogFilter>) => {
    setFilter((current) => {
      const isPaginationOnly =
        Object.keys(partial).length === 1 &&
        (partial.offset !== undefined || partial.limit !== undefined);
      const next = { ...current, ...partial };

      // A changed search criterion starts a new result set; retaining an old
      // offset would make the first page of that result set appear empty.
      if (!isPaginationOnly && partial.offset === undefined) {
        next.offset = 0;
      }
      return next;
    });
  };

  return {
    logs: query.data?.logs ?? [],
    total: query.data?.total ?? 0,
    loading: query.loading,
    error: query.error,
    refetch: query.refetch,
    setFilter,
    updateFilter,
    filter,
    sortBy: query.data?.sort_by ?? DEFAULT_SORT_FIELD,
    sortOrder: query.data?.sort_order ?? DEFAULT_SORT_ORDER,
  };
}
