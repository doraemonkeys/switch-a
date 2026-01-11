import { Link, useNavigate } from "react-router-dom";
import { useStatus } from "../hooks/useStatus";
import { useLogs } from "../hooks/useLogs";
import { useMemo } from "react";
import {
  getProviderStatus,
  statusDotClass,
  statusBadgeClass,
  statusLabel,
} from "./providers/types";

export function Dashboard() {
  const {
    status,
    summary,
    loading: statusLoading,
    refetch: refetchStatus,
  } = useStatus();
  const {
    logs,
    total: totalLogs,
    loading: logsLoading,
    refetch: refetchLogs,
  } = useLogs({
    limit: 50,
  });
  const navigate = useNavigate();

  const handleRefresh = () => {
    refetchStatus();
    refetchLogs();
  };

  const loading = statusLoading || logsLoading;

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
        <button
          onClick={handleRefresh}
          disabled={loading}
          className="btn btn-secondary btn-sm"
        >
          <span className={loading ? "animate-spin" : ""}>🔄</span>
          Refresh
        </button>
      </div>

      {/* Stats Grid */}
      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-4">
        <StatCard
          title="Total Providers"
          value={summary?.providers_total.toString() || "0"}
          icon="🔌"
          trend={trends.providers}
          variant="primary"
        />
        <StatCard
          title="Healthy"
          value={summary?.providers_healthy.toString() || "0"}
          icon="✅"
          trend={trends.healthy}
          variant="success"
        />
        <StatCard
          title="Unhealthy"
          value={summary?.providers_unhealthy.toString() || "0"}
          icon="⚠️"
          trend={trends.unhealthy}
          variant="danger"
        />
        <StatCard
          title="Total Requests"
          value={totalLogs.toString() || "0"}
          icon="📈"
          trend={trends.requests}
          variant="info"
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

          {status?.providers && status.providers.length > 0 ? (
            <div className="space-y-3">
              {status.providers.map((provider) => (
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
                      <p className="text-xs text-text-muted">
                        ID: {provider.id}
                      </p>
                    </div>
                  </div>
                  <div className="flex items-center gap-4">
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
              ))}
            </div>
          ) : (
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
          )}
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
        {recentErrors.length > 0 ? (
          <div className="space-y-3">
            {recentErrors.map((log) => (
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
        ) : (
          <div className="empty-state py-8">
            <div className="w-12 h-12 mx-auto mb-3 bg-success-light rounded-full flex items-center justify-center">
              <span className="text-2xl">✨</span>
            </div>
            <p className="text-sm">
              No recent errors. Everything is running smoothly!
            </p>
          </div>
        )}
      </div>
    </div>
  );
}

interface StatCardProps {
  title: string;
  value: string;
  icon: string;
  trend: { value: number; label: string };
  variant: "primary" | "success" | "danger" | "info";
}

function StatCard({ title, value, icon, trend, variant }: StatCardProps) {
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
        <div>
          <p className="text-sm font-medium text-text-secondary">{title}</p>
          <p className="text-3xl font-bold text-text-primary mt-1">{value}</p>
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
      className="w-full flex items-center gap-3 px-4 py-3 rounded-lg border border-border 
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
