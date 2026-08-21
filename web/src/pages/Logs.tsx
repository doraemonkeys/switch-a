import { useState } from "react";
import { useLogs, DEFAULT_LIMIT } from "../hooks/useLogs";
import { useProviders } from "../hooks/useProviders";
import { useStats } from "../hooks/useStats";
import { useTokenUsage } from "../hooks/useTokenUsage";
import {
  LogFilters,
  LogDetailModal,
  TokenUsageAnalyticsPanel,
} from "../components";
import type { RequestLog } from "../api/types";
import { useAnalyticsWindow } from "../features/analytics-window/useAnalyticsWindow";
import { api } from "../api/client";
import {
  LogsHeader,
  LogsTable,
  LogStatsGrid,
  Pagination,
  ErrorBanner,
} from "../components/logs";
import {
  createClearedLogFilterPatch,
  isLogFilterActive,
} from "../components/logs/filtering";

export function Logs() {
  const limit = DEFAULT_LIMIT;
  const {
    logs,
    total,
    loading,
    error,
    refetch,
    updateFilter,
    filter,
    sortBy,
    sortOrder,
  } = useLogs({ limit, offset: 0 });
  const { providers } = useProviders();

  const { window: analyticsWindow, applyIntent: applyAnalyticsWindowIntent } =
    useAnalyticsWindow();
  const { stats, loading: statsLoading } = useStats(analyticsWindow);
  const {
    data: tokenUsage,
    loading: tokenUsageLoading,
    error: tokenUsageError,
  } = useTokenUsage(analyticsWindow);

  // Selected log for detail modal (fetched with attempts)
  const [selectedLog, setSelectedLog] = useState<RequestLog | null>(null);

  // Handler to fetch full log details (with attempts) when clicking a log
  const handleSelectLog = async (log: RequestLog) => {
    try {
      const fullLog = await api.logs.get(log.id);
      setSelectedLog(fullLog);
    } catch {
      // Fallback to partial log if fetch fails
      setSelectedLog(log);
    }
  };

  const handleGlobalRefresh = async () => {
    applyAnalyticsWindowIntent({ type: "refresh-requested" });
    await refetch();
  };

  // Calculate pagination values
  const currentPage = Math.floor((filter.offset || 0) / limit) + 1;
  const totalPages = Math.max(1, Math.ceil(total / limit));
  const startResult = total === 0 ? 0 : (filter.offset || 0) + 1;
  const endResult = Math.min((filter.offset || 0) + limit, total);

  // Provider name lookup
  const providerNames = (() => {
    const map = new Map<string, string>();
    providers.forEach((p) => map.set(p.id, p.name));
    return map;
  })();

  // Check if any filter is active
  const hasActiveFilters = isLogFilterActive(filter);

  const handlePageChange = (newPage: number) => {
    if (newPage < 1 || newPage > totalPages) return;
    updateFilter({ offset: (newPage - 1) * limit });
  };

  const handleSort = (field: "created_at" | "latency_ms") => {
    if (sortBy === field) {
      updateFilter({ sort_order: sortOrder === "asc" ? "desc" : "asc" });
    } else {
      updateFilter({ sort_by: field, sort_order: "desc" });
    }
  };

  const handleClearFilters = () => {
    updateFilter(createClearedLogFilterPatch());
  };

  return (
    <div className="space-y-6">
      <LogsHeader loading={loading} onRefresh={handleGlobalRefresh} />

      {error && <ErrorBanner message={error.message} />}

      <LogFilters
        filter={filter}
        onFilterChange={updateFilter}
        providers={providers}
        onClear={handleClearFilters}
      />

      <LogsTable
        logs={logs}
        loading={loading}
        sortBy={sortBy}
        sortOrder={sortOrder}
        hasActiveFilters={hasActiveFilters}
        providerNames={providerNames}
        onSort={handleSort}
        onSelectLog={handleSelectLog}
        onClearFilters={handleClearFilters}
      />

      {total > 0 && (
        <Pagination
          currentPage={currentPage}
          totalPages={totalPages}
          startResult={startResult}
          endResult={endResult}
          total={total}
          loading={loading}
          onPageChange={handlePageChange}
        />
      )}

      {/* Token Usage Analytics Panel */}
      <TokenUsageAnalyticsPanel
        data={tokenUsage}
        loading={tokenUsageLoading}
        error={tokenUsageError}
        window={analyticsWindow}
        onWindowIntent={applyAnalyticsWindowIntent}
        hasActiveFilters={hasActiveFilters}
      />

      {/* Normalized Outcome Stats Grid */}
      <LogStatsGrid
        stats={stats}
        statsLoading={statsLoading}
        window={analyticsWindow}
        onWindowIntent={applyAnalyticsWindowIntent}
        hasActiveFilters={hasActiveFilters}
      />

      <LogDetailModal
        log={selectedLog}
        providerName={
          selectedLog ? providerNames.get(selectedLog.provider_id) || "" : ""
        }
        providerNames={providerNames}
        onClose={() => setSelectedLog(null)}
      />
    </div>
  );
}
