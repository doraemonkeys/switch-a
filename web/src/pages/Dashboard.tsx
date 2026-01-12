import { Link, useNavigate } from "react-router-dom";
import { useStatus } from "../hooks/useStatus";
import { useLogs } from "../hooks/useLogs";
import { useLocalStorage } from "../hooks/useLocalStorage";
import { useMemo, useEffect } from "react";
import type { SystemStatus, RequestLog } from "../api/client";
import {
  getProviderStatus,
  statusDotClass,
  statusBadgeClass,
  statusLabel,
} from "./providers/types";

// Dashboard configuration constants
const SUCCESS_RATE_EXCELLENT = 98;
const SUCCESS_RATE_ACCEPTABLE = 90;
const AUTO_REFRESH_INTERVAL_MS = 5000;

function getSuccessRateClass(successRate: number): string {
  if (successRate >= SUCCESS_RATE_EXCELLENT) return "text-success";
  if (successRate >= SUCCESS_RATE_ACCEPTABLE) return "text-warning";
  return "text-danger";
}

export function Dashboard() {
  const {
    status,
    summary,
    loading: statusLoading,
    error: statusError,
    refetch: refetchStatus,
  } = useStatus();
  const {
    logs,
    total: totalLogs,
    loading: logsLoading,
    error: logsError,
    refetch: refetchLogs,
  } = useLogs({
    limit: 50,
  });
  const navigate = useNavigate();

  // Auto-refresh state (persisted to localStorage)
  const [autoRefresh, setAutoRefresh] = useLocalStorage(
    "dashboard:autoRefresh",
    false,
  );

  // Auto-refresh effect
  useEffect(() => {
    let intervalId: ReturnType<typeof setInterval>;
    if (autoRefresh) {
      intervalId = setInterval(() => {
        refetchStatus();
        refetchLogs();
      }, AUTO_REFRESH_INTERVAL_MS);
    }
    return () => {
      if (intervalId) clearInterval(intervalId);
    };
  }, [autoRefresh, refetchStatus, refetchLogs]);

  const handleRefresh = () => {
    refetchStatus();
    refetchLogs();
  };

  const loading = statusLoading || logsLoading;
  const error = statusError || logsError;

  // Calculate trends (mock for now as we don't have historical data)
  const trends = {
    providers: { value: 0, label: "configured" },
    healthy: { value: 0, label: "available" },
    unhealthy: { value: 0, label: "circuit breaker" },
    requests: { value: 0, label: "total logged" },
  };

  // Filter for recent errors
  const recentErrors = useMemo(() => {
    return logs.filter((log) => !log.success).slice(0, 5);
  }, [logs]);

  return (
    <div className="space-y-6">
      {/* Page Header */}
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-2xl font-bold text-text-primary">Dashboard</h2>
          <p className="text-text-secondary mt-1">系统状态总览</p>
        </div>
        <div className="flex items-center gap-3">
          <div className="flex items-center gap-2 mr-2">
            <span className="text-sm text-text-secondary">Auto-refresh</span>
            <button
              onClick={() => setAutoRefresh(!autoRefresh)}
              className={`relative inline-flex h-6 w-11 items-center rounded-full transition-colors cursor-pointer focus:outline-none focus:ring-2 focus:ring-primary focus:ring-offset-2 ${
                autoRefresh ? "bg-primary" : "bg-gray-200"
              }`}
            >
              <span
                className={`${
                  autoRefresh ? "translate-x-6" : "translate-x-1"
                } inline-block h-4 w-4 transform rounded-full bg-white transition-transform`}
              />
            </button>
          </div>
          <button
            onClick={handleRefresh}
            disabled={loading}
            className="btn btn-secondary btn-sm"
          >
            <span className={loading ? "animate-spin" : ""}>🔄</span>
            Refresh
          </button>
        </div>
      </div>

      {/* Error Banner */}
      {error && (
        <div className="bg-danger-light/10 border border-danger-light text-danger-dark px-4 py-3 rounded-lg flex items-center gap-3">
          <span className="text-xl">⚠️</span>
          <div>
            <p className="font-medium">Failed to load dashboard data</p>
            <p className="text-sm opacity-90">{error.message}</p>
          </div>
        </div>
      )}

      {/* Stats Grid */}
      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-4">
        <StatCard
          title="Total Providers"
          value={summary?.providers_total.toString() || "0"}
          icon="🔌"
          trend={trends.providers}
          variant="primary"
          loading={loading && !summary}
        />
        <StatCard
          title="Healthy"
          value={summary?.providers_healthy.toString() || "0"}
          icon="✅"
          trend={trends.healthy}
          variant="success"
          loading={loading && !summary}
        />
        <StatCard
          title="Unhealthy"
          value={summary?.providers_unhealthy.toString() || "0"}
          icon="⚠️"
          trend={trends.unhealthy}
          variant="danger"
          loading={loading && !summary}
        />
        <StatCard
          title="Total Requests"
          value={totalLogs.toString() || "0"}
          icon="📈"
          trend={trends.requests}
          variant="info"
          loading={loading && totalLogs === 0}
        />
      </div>

      {/* Main Content Grid */}
      <div className="grid grid-cols-1 lg:grid-cols-3 gap-6">
        {/* Providers Status - Takes 2 columns */}
        <div className="lg:col-span-2 card">
          <div className="flex items-center justify-between mb-4">
            <h3 className="text-lg font-semibold text-text-primary">
              Provider Status
            </h3>
            <span className="badge badge-neutral">
              {status?.providers.length || 0} providers
            </span>
          </div>

          <ProviderStatusContent status={status} loading={loading} />
        </div>

        {/* Quick Actions */}
        <div className="card">
          <h3 className="text-lg font-semibold text-text-primary mb-4">
            Quick Actions
          </h3>
          <div className="space-y-2">
            <QuickActionButton
              icon="➕"
              label="Add Provider"
              onClick={() => navigate("/providers")}
            />
            <QuickActionButton
              icon="📁"
              label="Create Group"
              onClick={() => navigate("/groups")}
            />
            <QuickActionButton
              icon="⚙️"
              label="Edit Config"
              onClick={() => navigate("/config")}
            />
            <QuickActionButton
              icon="📋"
              label="View Logs"
              onClick={() => navigate("/logs")}
            />
          </div>
        </div>
      </div>

      {/* Recent Errors */}
      <div className="card">
        <div className="flex items-center justify-between mb-4">
          <h3 className="text-lg font-semibold text-text-primary">
            Recent Errors
          </h3>
          <Link
            to="/logs"
            className="text-sm text-primary hover:text-primary-hover font-medium"
          >
            View All →
          </Link>
        </div>
        <RecentErrorsContent errors={recentErrors} loading={loading} />
      </div>
    </div>
  );
}

interface ProviderStatusContentProps {
  status: SystemStatus | null;
  loading: boolean;
}

function ProviderStatusContent({
  status,
  loading,
}: ProviderStatusContentProps) {
  if (loading && !status) {
    return (
      <div className="space-y-3">
        {[1, 2, 3].map((i) => (
          <div
            key={i}
            className="h-20 rounded-lg border border-border bg-gray-50 animate-pulse"
          />
        ))}
      </div>
    );
  }

  if (status?.providers && status.providers.length > 0) {
    return (
      <div className="space-y-3">
        {status.providers.map((provider) => {
          const totalOps =
            (provider.health?.success_count || 0) +
            (provider.health?.fail_count || 0);
          const successRate =
            totalOps > 0
              ? Math.round(
                  ((provider.health?.success_count || 0) / totalOps) * 100,
                )
              : null;

          return (
            <div
              key={provider.id}
              className="flex items-center justify-between p-3 rounded-lg border border-border bg-bg-secondary/50 hover:bg-bg-secondary transition-colors"
            >
              <div className="flex items-center gap-3">
                <div
                  className={`w-2 h-2 rounded-full ${
                    statusDotClass[
                      getProviderStatus(
                        provider.enabled,
                        provider.health?.available,
                      )
                    ]
                  }`}
                />
                <div>
                  <p className="font-medium text-text-primary">
                    {provider.name}
                  </p>
                  <p className="text-xs text-text-muted">ID: {provider.id}</p>
                </div>
              </div>
              <div className="flex items-center gap-6">
                {successRate !== null && (
                  <div className="text-right hidden sm:block">
                    <p className="text-xs text-text-muted">Success Rate</p>
                    <p
                      className={`text-sm font-medium ${getSuccessRateClass(successRate)}`}
                    >
                      {successRate}%
                    </p>
                  </div>
                )}
                <div className="text-right">
                  <p className="text-xs text-text-muted">Requests</p>
                  <p className="text-sm font-medium text-text-primary">
                    {provider.current_requests}
                  </p>
                </div>
                <div
                  className={`px-2 py-1 rounded text-xs font-medium ${
                    statusBadgeClass[
                      getProviderStatus(
                        provider.enabled,
                        provider.health?.available,
                      )
                    ]
                  }`}
                >
                  {
                    statusLabel[
                      getProviderStatus(
                        provider.enabled,
                        provider.health?.available,
                      )
                    ]
                  }
                </div>
              </div>
            </div>
          );
        })}
      </div>
    );
  }

  return (
    <div className="empty-state">
      <div className="w-16 h-16 mx-auto mb-4 bg-bg-tertiary rounded-full flex items-center justify-center">
        <span className="text-3xl">🔌</span>
      </div>
      <p className="font-medium text-text-primary mb-1">
        No providers configured
      </p>
      <p className="text-sm text-text-muted">
        Go to Providers page to add your first provider.
      </p>
      <Link to="/providers" className="btn btn-primary btn-sm mt-4">
        + Add Provider
      </Link>
    </div>
  );
}

interface RecentErrorsContentProps {
  errors: RequestLog[];
  loading: boolean;
}

function RecentErrorsContent({ errors, loading }: RecentErrorsContentProps) {
  if (errors.length > 0) {
    return (
      <div className="space-y-3">
        {errors.map((log) => (
          <div
            key={log.id}
            className="p-3 rounded-lg border border-danger-light bg-danger-light/10"
          >
            <div className="flex items-start justify-between">
              <div>
                <div className="flex items-center gap-2 mb-1">
                  <span className="font-medium text-text-primary">
                    {log.model}
                  </span>
                  <span className="px-1.5 py-0.5 rounded text-xs font-medium bg-danger-light text-danger-dark">
                    {log.status_code}
                  </span>
                </div>
                <p className="text-sm text-text-secondary">
                  {log.error_msg || "Unknown error"}
                </p>
              </div>
              <span className="text-xs text-text-muted">
                {new Date(log.created_at).toLocaleString()}
              </span>
            </div>
          </div>
        ))}
      </div>
    );
  }

  if (loading) {
    return (
      <div className="space-y-3">
        {[1, 2].map((i) => (
          <div
            key={i}
            className="h-20 rounded-lg border border-border bg-gray-50 animate-pulse"
          />
        ))}
      </div>
    );
  }

  return (
    <div className="empty-state py-8">
      <div className="w-12 h-12 mx-auto mb-3 bg-success-light rounded-full flex items-center justify-center">
        <span className="text-2xl">✨</span>
      </div>
      <p className="text-sm">
        No recent errors. Everything is running smoothly!
      </p>
    </div>
  );
}

interface StatCardProps {
  title: string;
  value: string;
  icon: string;
  trend: { value: number; label: string };
  variant: "primary" | "success" | "danger" | "info";
  loading?: boolean;
}

function StatCard({
  title,
  value,
  icon,
  trend,
  variant,
  loading,
}: StatCardProps) {
  const variantStyles = {
    primary: {
      bg: "bg-primary-light",
      icon: "text-primary",
      accent: "bg-primary",
    },
    success: {
      bg: "bg-success-light",
      icon: "text-emerald-600",
      accent: "bg-success",
    },
    danger: {
      bg: "bg-danger-light",
      icon: "text-red-600",
      accent: "bg-danger",
    },
    info: {
      bg: "bg-info-light",
      icon: "text-blue-600",
      accent: "bg-info",
    },
  };

  const styles = variantStyles[variant];

  return (
    <div className="card relative overflow-hidden">
      {/* Accent bar */}
      <div className={`absolute top-0 left-0 w-full h-1 ${styles.accent}`} />

      <div className="flex items-start justify-between">
        <div className="w-full">
          <p className="text-sm font-medium text-text-secondary">{title}</p>
          {loading ? (
            <div className="h-9 w-16 bg-gray-200 rounded animate-pulse mt-1" />
          ) : (
            <p className="text-3xl font-bold text-text-primary mt-1">{value}</p>
          )}
          <p className="text-xs text-text-muted mt-2">{trend.label}</p>
        </div>
        <div
          className={`w-12 h-12 rounded-xl flex items-center justify-center ${styles.bg}`}
        >
          <span className={`text-xl ${styles.icon}`}>{icon}</span>
        </div>
      </div>
    </div>
  );
}

interface QuickActionButtonProps {
  icon: string;
  label: string;
  onClick: () => void;
}

function QuickActionButton({ icon, label, onClick }: QuickActionButtonProps) {
  return (
    <button
      onClick={onClick}
      className="w-full flex items-center gap-3 px-4 py-3 rounded-lg border border-border cursor-pointer
                       hover:bg-bg-hover hover:border-primary/30 transition-all duration-200 group text-left"
    >
      <span className="text-lg group-hover:scale-110 transition-transform">
        {icon}
      </span>
      <span className="text-sm font-medium text-text-primary">{label}</span>
      <span className="ml-auto text-text-muted group-hover:text-primary transition-colors">
        →
      </span>
    </button>
  );
}
