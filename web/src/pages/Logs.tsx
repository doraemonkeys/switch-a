import { useState } from "react";
import { useLogs, DEFAULT_LIMIT } from "../hooks/useLogs";
import { useProviders } from "../hooks/useProviders";
import { useStats } from "../hooks/useStats";
import { LogFilters, LogDetailModal } from "../components";
import type { RequestLog, StatsResponse } from "../api/types";
import {
  SUCCESS_RATE_THRESHOLDS,
  ERROR_COUNT_THRESHOLDS,
} from "../config/constants";
import { getSuccessBadgeClass } from "../lib/utils";
import { api } from "../api/client";

// Constants
const LOG_TABLE_COLUMNS = 8;
const PROVIDER_ID_PREVIEW_LENGTH = 8;

// Date formatter moved outside component to avoid re-creation on each render
const dateFormatter = new Intl.DateTimeFormat("en-US", {
  month: "short",
  day: "numeric",
  hour: "2-digit",
  minute: "2-digit",
  second: "2-digit",
});

// Type for stat card variants
type StatVariantValue = "success" | "warning" | "danger";
type StatVariant = StatVariantValue | undefined;

// Helper functions for determining stat variants
function getSuccessRateVariant(rate: number | undefined): StatVariant {
  if (rate === undefined) return undefined;
  if (rate >= SUCCESS_RATE_THRESHOLDS.SUCCESS) return "success";
  if (rate >= SUCCESS_RATE_THRESHOLDS.WARNING) return "warning";
  return "danger";
}

function getErrorCountVariant(count: number | undefined): StatVariant {
  if (count === undefined) return undefined;
  if (count === 0) return "success";
  if (count < ERROR_COUNT_THRESHOLDS.WARNING_MAX) return "warning";
  return "danger";
}

function getStatVariantClass(variant: StatVariant): string {
  if (variant === "success") return "text-success";
  if (variant === "warning") return "text-warning";
  if (variant === "danger") return "text-danger";
  return "text-text-primary";
}

// Helper to determine aria-sort value for sortable table headers
type AriaSortValue = "ascending" | "descending" | "none";

function getAriaSortValue(
  field: string,
  currentSortBy: string,
  currentSortOrder: string,
): AriaSortValue {
  if (currentSortBy !== field) return "none";
  return currentSortOrder === "asc" ? "ascending" : "descending";
}

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
  const { stats, loading: statsLoading } = useStats({ period: "24h" });

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
  const hasActiveFilters = !!(
    filter.provider_id ||
    filter.api_type ||
    filter.success !== undefined ||
    filter.is_sse !== undefined ||
    filter.start_time ||
    filter.end_time ||
    filter.has_retries !== undefined ||
    filter.min_retry_count !== undefined
  );

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
    updateFilter({
      provider_id: undefined,
      api_type: undefined,
      success: undefined,
      is_sse: undefined,
      start_time: undefined,
      end_time: undefined,
      min_latency: undefined,
      user_id: undefined,
      has_retries: undefined,
      min_retry_count: undefined,
    });
  };

  return (
    <div className="space-y-6">
      <LogsHeader loading={loading} onRefresh={refetch} />

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

      <LogStatsGrid
        total={total}
        stats={stats}
        loading={loading}
        statsLoading={statsLoading}
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

// Sub-components

function InfoTooltip({ text }: { text: string }) {
  return (
    <span className="relative group cursor-help text-text-muted">
      <span className="text-xs">ℹ️</span>
      <span className="invisible group-hover:visible absolute left-1/2 -translate-x-1/2 bottom-full mb-2 w-64 p-2 text-xs text-white bg-gray-900 rounded-lg shadow-lg z-50 whitespace-normal">
        {text}
        <span className="absolute left-1/2 -translate-x-1/2 top-full border-4 border-transparent border-t-gray-900" />
      </span>
    </span>
  );
}

interface LogsHeaderProps {
  loading: boolean;
  onRefresh: () => void;
}

function LogsHeader({ loading, onRefresh }: LogsHeaderProps) {
  return (
    <div className="flex items-center justify-between">
      <div>
        <h2 className="text-2xl font-bold text-text-primary">Request Logs</h2>
        <p className="text-text-secondary mt-1">
          View and filter request history
        </p>
      </div>
      <button
        onClick={onRefresh}
        className="btn btn-secondary btn-sm"
        disabled={loading}
        title="Refresh logs"
      >
        <span className={loading ? "animate-spin" : ""}>🔄</span>
        Refresh
      </button>
    </div>
  );
}

function ErrorBanner({ message }: { message: string }) {
  return (
    <div className="bg-red-50 dark:bg-red-900/20 text-red-600 dark:text-red-400 p-4 rounded-lg flex items-center gap-2">
      <span>⚠️</span>
      <p>{message}</p>
    </div>
  );
}

interface LogsTableProps {
  logs: RequestLog[];
  loading: boolean;
  sortBy: string;
  sortOrder: string;
  hasActiveFilters: boolean;
  providerNames: Map<string, string>;
  onSort: (field: "created_at" | "latency_ms") => void;
  onSelectLog: (log: RequestLog) => void;
  onClearFilters: () => void;
}

function LogsTable({
  logs,
  loading,
  sortBy,
  sortOrder,
  hasActiveFilters,
  providerNames,
  onSort,
  onSelectLog,
  onClearFilters,
}: LogsTableProps) {
  const renderSortIcon = (field: string) => {
    if (sortBy !== field) {
      return <span className="text-text-muted ml-1">↕</span>;
    }
    return (
      <span className="text-primary ml-1">
        {sortOrder === "asc" ? "↑" : "↓"}
      </span>
    );
  };

  return (
    <div className="card overflow-hidden p-0">
      <div className="overflow-x-auto">
        <table className="w-full">
          <thead className="table-header">
            <tr>
              <th
                onClick={() => onSort("created_at")}
                aria-sort={getAriaSortValue("created_at", sortBy, sortOrder)}
                className="table-cell text-left text-xs font-medium text-text-secondary uppercase tracking-wider cursor-pointer hover:text-text-primary transition-colors"
              >
                <span className="inline-flex items-center">
                  Time
                  {renderSortIcon("created_at")}
                </span>
              </th>
              <th className="table-cell text-left text-xs font-medium text-text-secondary uppercase tracking-wider">
                Provider
              </th>
              <th className="table-cell text-left text-xs font-medium text-text-secondary uppercase tracking-wider">
                API Type
              </th>
              <th className="table-cell text-left text-xs font-medium text-text-secondary uppercase tracking-wider">
                Model
              </th>
              <th className="table-cell text-left text-xs font-medium text-text-secondary uppercase tracking-wider">
                Status
              </th>
              <th className="table-cell text-left text-xs font-medium text-text-secondary uppercase tracking-wider">
                <span className="inline-flex items-center gap-1">
                  Retries
                  <InfoTooltip text="Retry count shows the number of additional attempts after the initial request. A request with max_retries=2 can have up to 3 attempts total (1 initial + 2 retries). Circuit breaker or permanent errors (401/402/403) may interrupt retries early." />
                </span>
              </th>
              <th
                onClick={() => onSort("latency_ms")}
                aria-sort={getAriaSortValue("latency_ms", sortBy, sortOrder)}
                className="table-cell text-left text-xs font-medium text-text-secondary uppercase tracking-wider cursor-pointer hover:text-text-primary transition-colors"
              >
                <span className="inline-flex items-center">
                  Latency
                  {renderSortIcon("latency_ms")}
                </span>
              </th>
              <th className="table-cell text-left text-xs font-medium text-text-secondary uppercase tracking-wider">
                Client
              </th>
            </tr>
          </thead>
          <tbody
            className={`divide-y divide-border-light transition-opacity duration-200 ${
              loading && logs.length > 0 ? "opacity-50" : ""
            }`}
          >
            <LogTableBody
              logs={logs}
              loading={loading}
              hasActiveFilters={hasActiveFilters}
              providerNames={providerNames}
              onSelectLog={onSelectLog}
              onClearFilters={onClearFilters}
            />
          </tbody>
        </table>
      </div>
    </div>
  );
}

interface LogTableBodyProps {
  logs: RequestLog[];
  loading: boolean;
  hasActiveFilters: boolean;
  providerNames: Map<string, string>;
  onSelectLog: (log: RequestLog) => void;
  onClearFilters: () => void;
}

function LogTableBody({
  logs,
  loading,
  hasActiveFilters,
  providerNames,
  onSelectLog,
  onClearFilters,
}: LogTableBodyProps) {
  if (loading && logs.length === 0) {
    return (
      <tr>
        <td colSpan={LOG_TABLE_COLUMNS} className="px-4 py-8 text-center">
          <div className="inline-block animate-spin rounded-full h-8 w-8 border-b-2 border-primary"></div>
          <p className="mt-2 text-text-secondary">Loading logs...</p>
        </td>
      </tr>
    );
  }

  if (logs.length === 0) {
    return (
      <tr>
        <td colSpan={LOG_TABLE_COLUMNS} className="px-4 py-16">
          <div className="empty-state">
            <div className="w-20 h-20 mx-auto mb-4 bg-bg-tertiary rounded-2xl flex items-center justify-center">
              <span className="text-4xl">{hasActiveFilters ? "🔍" : "📋"}</span>
            </div>
            <p className="font-medium text-text-primary mb-1">
              {hasActiveFilters
                ? "No logs match your filters"
                : "No logs recorded yet"}
            </p>
            <p className="text-sm text-text-muted">
              {hasActiveFilters
                ? "Try adjusting your filter criteria."
                : "Requests proxied through the gateway will appear here."}
            </p>
            {hasActiveFilters && (
              <button
                onClick={onClearFilters}
                className="btn btn-secondary btn-sm mt-4"
              >
                Clear Filters
              </button>
            )}
          </div>
        </td>
      </tr>
    );
  }

  return (
    <>
      {logs.map((log) => (
        <LogTableRow
          key={log.id}
          log={log}
          providerName={providerNames.get(log.provider_id)}
          onClick={() => onSelectLog(log)}
        />
      ))}
    </>
  );
}

interface LogTableRowProps {
  log: RequestLog;
  providerName: string | undefined;
  onClick: () => void;
}

function LogTableRow({ log, providerName, onClick }: LogTableRowProps) {
  const formatTime = (dateStr: string) =>
    dateFormatter.format(new Date(dateStr));

  return (
    <tr
      onClick={onClick}
      onKeyDown={(e) => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          onClick();
        }
      }}
      tabIndex={0}
      role="button"
      aria-label={`View details for ${log.model} request at ${formatTime(log.created_at)}`}
      className="hover:bg-bg-tertiary/50 transition-colors cursor-pointer"
    >
      <td className="px-6 py-4 whitespace-nowrap text-sm text-text-secondary">
        {formatTime(log.created_at)}
      </td>
      <td className="px-6 py-4 whitespace-nowrap text-sm font-medium text-text-primary">
        {providerName || (
          <span className="text-text-muted font-mono text-xs">
            {log.provider_id.substring(0, PROVIDER_ID_PREVIEW_LENGTH)}...
          </span>
        )}
      </td>
      <td className="px-6 py-4 whitespace-nowrap text-sm text-text-secondary">
        <div className="flex items-center gap-1.5">
          <span className="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-blue-100 text-blue-800 dark:bg-blue-900/30 dark:text-blue-300 uppercase">
            {log.api_type}
          </span>
          {log.is_sse && (
            <span
              className="inline-flex items-center px-1.5 py-0.5 rounded text-xs font-medium bg-purple-100 text-purple-800 dark:bg-purple-900/30 dark:text-purple-300"
              title="Server-Sent Events (streaming)"
            >
              SSE
            </span>
          )}
        </div>
      </td>
      <td className="px-6 py-4 whitespace-nowrap text-sm text-text-primary font-mono">
        {log.model}
      </td>
      <td className="px-6 py-4 whitespace-nowrap text-sm">
        <span
          className={`inline-flex items-center gap-1.5 px-2.5 py-0.5 rounded-full text-xs font-medium ${getSuccessBadgeClass(log.success)}`}
        >
          {log.success ? "✅" : "❌"}
          {log.status_code}
        </span>
      </td>
      <td className="px-6 py-4 whitespace-nowrap text-sm">
        {log.retry_count > 0 ? (
          <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs font-medium bg-amber-100 text-amber-800 dark:bg-amber-900/30 dark:text-amber-300">
            🔄 {log.retry_count}
          </span>
        ) : (
          <span className="text-text-muted">-</span>
        )}
      </td>
      <td className="px-6 py-4 whitespace-nowrap text-sm text-text-secondary">
        {log.latency_ms}ms
      </td>
      <td className="px-6 py-4 whitespace-nowrap text-sm text-text-secondary">
        <div className="flex flex-col">
          <span className="text-xs font-mono">{log.client_ip}</span>
          {log.user_id && (
            <span className="text-xs text-text-muted">User: {log.user_id}</span>
          )}
        </div>
      </td>
    </tr>
  );
}

interface LogStatsGridProps {
  total: number;
  stats: StatsResponse | null;
  loading: boolean;
  statsLoading: boolean;
}

function LogStatsGrid({
  total,
  stats,
  loading,
  statsLoading,
}: LogStatsGridProps) {
  const successRateValue =
    stats?.success_rate !== undefined
      ? `${(stats.success_rate * 100).toFixed(1)}%`
      : "-";

  const avgLatencyValue =
    stats?.avg_latency_ms !== undefined ? `${stats.avg_latency_ms}ms` : "-";

  const errorCountValue = stats?.fail_count?.toLocaleString() ?? "-";

  return (
    <div className="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-4 gap-4">
      <LogStatCard
        label="Total Logs"
        value={total.toLocaleString()}
        icon="📊"
        loading={loading && total === 0}
      />
      <LogStatCard
        label="Success Rate (24h)"
        value={successRateValue}
        icon="✅"
        loading={statsLoading}
        variant={getSuccessRateVariant(stats?.success_rate)}
      />
      <LogStatCard
        label="Avg Latency (24h)"
        value={avgLatencyValue}
        icon="⚡"
        loading={statsLoading}
      />
      <LogStatCard
        label="Errors (24h)"
        value={errorCountValue}
        icon="⚠️"
        loading={statsLoading}
        variant={getErrorCountVariant(stats?.fail_count)}
      />
    </div>
  );
}

interface PaginationProps {
  currentPage: number;
  totalPages: number;
  startResult: number;
  endResult: number;
  total: number;
  loading: boolean;
  onPageChange: (page: number) => void;
}

function Pagination({
  currentPage,
  totalPages,
  startResult,
  endResult,
  total,
  loading,
  onPageChange,
}: PaginationProps) {
  const [jumpPage, setJumpPage] = useState("");

  const handleJumpSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    const page = parseInt(jumpPage, 10);
    if (!isNaN(page) && page >= 1 && page <= totalPages) {
      onPageChange(page);
      setJumpPage("");
    }
  };

  const pageNumbers = (() => {
    const pages: (number | "...")[] = [];
    const showPages = 5;

    if (totalPages <= showPages + 2) {
      for (let i = 1; i <= totalPages; i++) {
        pages.push(i);
      }
    } else {
      pages.push(1);
      let start = Math.max(2, currentPage - 1);
      let end = Math.min(totalPages - 1, currentPage + 1);

      if (currentPage <= 3) {
        end = Math.min(showPages - 1, totalPages - 1);
      }
      if (currentPage >= totalPages - 2) {
        start = Math.max(2, totalPages - showPages + 2);
      }
      if (start > 2) {
        pages.push("...");
      }
      for (let i = start; i <= end; i++) {
        pages.push(i);
      }
      if (end < totalPages - 1) {
        pages.push("...");
      }
      pages.push(totalPages);
    }
    return pages;
  })();

  return (
    <div className="flex flex-col sm:flex-row items-center justify-between gap-4">
      <p className="text-sm text-text-secondary">
        Showing{" "}
        <span className="font-medium text-text-primary">{startResult}</span> to{" "}
        <span className="font-medium text-text-primary">{endResult}</span> of{" "}
        <span className="font-medium text-text-primary">{total}</span> results
      </p>

      <div className="flex items-center gap-2">
        <button
          onClick={() => onPageChange(1)}
          disabled={currentPage === 1 || loading}
          className="btn btn-secondary btn-sm disabled:opacity-50 disabled:cursor-not-allowed"
          title="First page"
        >
          ««
        </button>
        <button
          onClick={() => onPageChange(currentPage - 1)}
          disabled={currentPage === 1 || loading}
          className="btn btn-secondary btn-sm disabled:opacity-50 disabled:cursor-not-allowed"
        >
          ←
        </button>

        <div className="hidden sm:flex items-center gap-1">
          {pageNumbers.map((page, index) =>
            page === "..." ? (
              <span
                key={`ellipsis-after-${pageNumbers[index - 1]}`}
                className="px-2 text-text-muted"
              >
                ...
              </span>
            ) : (
              <button
                key={page}
                onClick={() => onPageChange(page)}
                disabled={loading}
                className={`px-3 py-1 text-sm font-medium rounded-lg transition-colors ${
                  page === currentPage
                    ? "bg-primary text-white"
                    : "text-text-secondary hover:bg-bg-tertiary"
                }`}
              >
                {page}
              </button>
            ),
          )}
        </div>

        <span className="sm:hidden px-3 py-1 bg-primary text-white text-sm font-medium rounded-lg">
          {currentPage} / {totalPages}
        </span>

        <button
          onClick={() => onPageChange(currentPage + 1)}
          disabled={currentPage === totalPages || loading}
          className="btn btn-secondary btn-sm disabled:opacity-50 disabled:cursor-not-allowed"
        >
          →
        </button>
        <button
          onClick={() => onPageChange(totalPages)}
          disabled={currentPage === totalPages || loading}
          className="btn btn-secondary btn-sm disabled:opacity-50 disabled:cursor-not-allowed"
          title="Last page"
        >
          »»
        </button>

        {totalPages > 5 && (
          <form
            onSubmit={handleJumpSubmit}
            className="hidden md:flex items-center gap-2 ml-2"
          >
            <label htmlFor="go-to-page" className="text-sm text-text-muted">
              Go to
            </label>
            <input
              id="go-to-page"
              type="number"
              min={1}
              max={totalPages}
              value={jumpPage}
              onChange={(e) => setJumpPage(e.target.value)}
              placeholder="#"
              className="input input-sm w-16 text-center"
            />
          </form>
        )}
      </div>
    </div>
  );
}

interface LogStatCardProps {
  label: string;
  value: string;
  icon: string;
  loading?: boolean;
  variant?: StatVariantValue;
}

function LogStatCard({
  label,
  value,
  icon,
  loading,
  variant,
}: LogStatCardProps) {
  const variantClass = getStatVariantClass(variant);

  return (
    <div className="card py-4">
      <div className="flex items-center gap-3">
        <span className="text-xl">{icon}</span>
        <div>
          <p className="text-xs text-text-muted">{label}</p>
          {loading ? (
            <div className="h-7 w-16 bg-gray-200 rounded animate-pulse mt-1" />
          ) : (
            <p className={`text-lg font-bold ${variantClass}`}>{value}</p>
          )}
        </div>
      </div>
    </div>
  );
}
