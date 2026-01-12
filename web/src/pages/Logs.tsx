import { useMemo } from "react";
import { useLogs, DEFAULT_LIMIT } from "../hooks/useLogs";
import { useProviders } from "../hooks/useProviders";

// Constants
const LOG_TABLE_COLUMNS = 7;
const PROVIDER_ID_PREVIEW_LENGTH = 8;

// Date formatter moved outside component to avoid re-creation on each render
const dateFormatter = new Intl.DateTimeFormat("en-US", {
  month: "short",
  day: "numeric",
  hour: "2-digit",
  minute: "2-digit",
  second: "2-digit",
});

export function Logs() {
  const limit = DEFAULT_LIMIT;
  const { logs, total, loading, error, refetch, setParams, params } = useLogs({
    limit,
    offset: 0,
  });
  const { providers } = useProviders();

  // Calculate pagination values
  const currentPage = Math.floor((params.offset || 0) / limit) + 1;
  const totalPages = Math.ceil(total / limit);
  const startResult = total === 0 ? 0 : (params.offset || 0) + 1;
  const endResult = Math.min((params.offset || 0) + limit, total);

  // Memoize provider name lookup
  const providerNames = useMemo(() => {
    const map = new Map<string, string>();
    providers.forEach((p) => map.set(p.id, p.name));
    return map;
  }, [providers]);

  const handlePageChange = (newPage: number) => {
    if (newPage < 1 || newPage > totalPages) return;
    setParams({ ...params, offset: (newPage - 1) * limit });
  };

  const formatTime = (dateStr: string) =>
    dateFormatter.format(new Date(dateStr));

  const renderTableRows = () => {
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
                <span className="text-4xl">📋</span>
              </div>
              <p className="font-medium text-text-primary mb-1">
                No logs recorded yet
              </p>
              <p className="text-sm text-text-muted">
                Requests proxied through the gateway will appear here.
              </p>
            </div>
          </td>
        </tr>
      );
    }

    return logs.map((log) => (
      <tr key={log.id} className="hover:bg-bg-tertiary/50 transition-colors">
        <td className="px-6 py-4 whitespace-nowrap text-sm text-text-secondary">
          {formatTime(log.created_at)}
        </td>
        <td className="px-6 py-4 whitespace-nowrap text-sm font-medium text-text-primary">
          {providerNames.get(log.provider_id) || (
            <span className="text-text-muted font-mono text-xs">
              {log.provider_id.substring(0, PROVIDER_ID_PREVIEW_LENGTH)}...
            </span>
          )}
        </td>
        <td className="px-6 py-4 whitespace-nowrap text-sm text-text-secondary">
          <span className="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-blue-100 text-blue-800 dark:bg-blue-900/30 dark:text-blue-300 uppercase">
            {log.api_type}
          </span>
        </td>
        <td className="px-6 py-4 whitespace-nowrap text-sm text-text-primary font-mono">
          {log.model}
        </td>
        <td className="px-6 py-4 whitespace-nowrap text-sm">
          <span
            className={`inline-flex items-center gap-1.5 px-2.5 py-0.5 rounded-full text-xs font-medium ${
              log.success
                ? "bg-green-100 text-green-800 dark:bg-green-900/30 dark:text-green-300"
                : "bg-red-100 text-red-800 dark:bg-red-900/30 dark:text-red-300"
            }`}
          >
            {log.success ? "✅" : "❌"}
            {log.status_code}
          </span>
        </td>
        <td className="px-6 py-4 whitespace-nowrap text-sm text-text-secondary">
          {log.latency_ms}ms
        </td>
        <td className="px-6 py-4 whitespace-nowrap text-sm text-text-secondary">
          <div className="flex flex-col">
            <span className="text-xs font-mono">{log.client_ip}</span>
            {log.user_id && (
              <span className="text-xs text-text-muted">
                User: {log.user_id}
              </span>
            )}
          </div>
        </td>
      </tr>
    ));
  };

  return (
    <div className="space-y-6">
      {/* Page Header */}
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-2xl font-bold text-text-primary">Request Logs</h2>
          <p className="text-text-secondary mt-1">View request history</p>
        </div>
        <button
          onClick={() => refetch()}
          className="btn btn-secondary btn-sm"
          disabled={loading}
          title="Refresh logs"
        >
          <span className={`${loading ? "animate-spin" : ""}`}>🔄</span>
          Refresh
        </button>
      </div>

      {/* Error State */}
      {error && (
        <div className="bg-red-50 dark:bg-red-900/20 text-red-600 dark:text-red-400 p-4 rounded-lg flex items-center gap-2">
          <span>⚠️</span>
          <p>{error.message}</p>
        </div>
      )}

      {/* future: Add filter bar when backend support is available */}

      {/* Logs Table */}
      <div className="card overflow-hidden p-0">
        <div className="overflow-x-auto">
          <table className="w-full">
            <thead className="table-header">
              <tr>
                <th className="table-cell text-left text-xs font-medium text-text-secondary uppercase tracking-wider">
                  Time
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
                  Latency
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
              {renderTableRows()}
            </tbody>
          </table>
        </div>
      </div>

      {/* Pagination */}
      {total > 0 && (
        <div className="flex items-center justify-between">
          <p className="text-sm text-text-secondary">
            Showing{" "}
            <span className="font-medium text-text-primary">{startResult}</span>{" "}
            to{" "}
            <span className="font-medium text-text-primary">{endResult}</span>{" "}
            of <span className="font-medium text-text-primary">{total}</span>{" "}
            results
          </p>
          <div className="flex items-center gap-2">
            <button
              onClick={() => handlePageChange(currentPage - 1)}
              disabled={currentPage === 1 || loading}
              className="btn btn-secondary btn-sm disabled:opacity-50 disabled:cursor-not-allowed"
            >
              ← Previous
            </button>
            <div className="flex items-center gap-1">
              <span className="px-3 py-1 bg-primary text-white text-sm font-medium rounded-lg">
                Page {currentPage} of {totalPages}
              </span>
            </div>
            <button
              onClick={() => handlePageChange(currentPage + 1)}
              disabled={currentPage === totalPages || loading}
              className="btn btn-secondary btn-sm disabled:opacity-50 disabled:cursor-not-allowed"
            >
              Next →
            </button>
          </div>
        </div>
      )}

      {/* Log Stats - Only show supported stats */}
      <div className="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-4 gap-4">
        <LogStatCard
          label="Total Logs"
          value={total.toLocaleString()}
          icon="📊"
        />
        {/*
          These stats require backend aggregation which is not yet implemented.
          <LogStatCard label="Success Rate" value="-" icon="✅" />
          <LogStatCard label="Avg Latency" value="-" icon="⚡" />
          <LogStatCard label="Errors Today" value="-" icon="⚠️" />
        */}
      </div>
    </div>
  );
}

interface LogStatCardProps {
  label: string;
  value: string;
  icon: string;
}

function LogStatCard({ label, value, icon }: LogStatCardProps) {
  return (
    <div className="card py-4">
      <div className="flex items-center gap-3">
        <span className="text-xl">{icon}</span>
        <div>
          <p className="text-xs text-text-muted">{label}</p>
          <p className="text-lg font-bold text-text-primary">{value}</p>
        </div>
      </div>
    </div>
  );
}
