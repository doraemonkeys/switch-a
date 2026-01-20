import type { RequestLog } from "../../api/types";
import { getSuccessBadgeClass } from "../../lib/utils";
import { getAriaSortValue } from "./utils";
import {
  LOG_TABLE_COLUMNS,
  PROVIDER_ID_PREVIEW_LENGTH,
  dateFormatter,
} from "./constants";
import { InfoTooltip } from "./InfoTooltip";

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

export function LogsTable({
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
