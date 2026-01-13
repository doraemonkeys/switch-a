import { useState, useMemo, useEffect } from "react";
import { Link, useNavigate } from "react-router-dom";
import { useStatus } from "../hooks/useStatus";
import { useLogs } from "../hooks/useLogs";
import { useLocalStorage } from "../hooks/useLocalStorage";
import { useProviderBatch } from "../hooks/useProviderBatch";
import { useToast } from "../hooks/useToast";
import { ConfirmModal } from "../components";
import { RefreshIntervalSelect } from "../components/RefreshIntervalSelect";
import { DEFAULT_REFRESH_INTERVAL } from "../components/refreshIntervalConstants";
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
  const { batchAction, loading: batchLoading } = useProviderBatch();
  const toast = useToast();

  // Auto-refresh state (persisted to localStorage)
  // 0 = off, otherwise interval in ms
  const [refreshInterval, setRefreshInterval] = useLocalStorage(
    "dashboard:refreshInterval",
    DEFAULT_REFRESH_INTERVAL.dashboard,
  );

  // Batch reset confirm modal state
  const [batchResetConfirm, setBatchResetConfirm] = useState<{
    isOpen: boolean;
    count: number;
  }>({
    isOpen: false,
    count: 0,
  });

  // Auto-refresh effect
  useEffect(() => {
    let intervalId: ReturnType<typeof setInterval>;
    if (refreshInterval > 0) {
      intervalId = setInterval(() => {
        refetchStatus();
        refetchLogs();
      }, refreshInterval);
    }
    return () => {
      if (intervalId) clearInterval(intervalId);
    };
  }, [refreshInterval, refetchStatus, refetchLogs]);

  const handleRefresh = () => {
    refetchStatus();
    refetchLogs();
  };

  const handleBatchResetClick = () => {
    if (!status?.providers) return;

    // Find all unhealthy providers
    const unhealthyIds = status.providers
      .filter((p) => p.enabled && p.health?.available === false)
      .map((p) => p.id);

    if (unhealthyIds.length === 0) return;

    setBatchResetConfirm({ isOpen: true, count: unhealthyIds.length });
  };

  const handleBatchResetConfirm = async () => {
    if (!status?.providers) return;

    const unhealthyIds = status.providers
      .filter((p) => p.enabled && p.health?.available === false)
      .map((p) => p.id);

    try {
      await batchAction("reset", unhealthyIds);
      toast.success(`Successfully reset ${unhealthyIds.length} providers`);
      setBatchResetConfirm({ isOpen: false, count: 0 });
      refetchStatus();
    } catch (err) {
      console.error("Failed to batch reset providers:", err);
      toast.error("Failed to batch reset providers");
    }
  };

  const handleBatchResetCancel = () => {
    setBatchResetConfirm({ isOpen: false, count: 0 });
  };

  const loading = statusLoading || logsLoading;
  const error = statusError || logsError;

  // Calculate stats
  const stats = useMemo(() => {
    if (!status?.providers)
      return {
        total: 0,
        healthy: 0,
        unhealthy: 0,
        disabled: 0,
      };

    const providers = status.providers;
    return {
      total: providers.length,
      healthy: providers.filter(
        (p) => p.enabled && p.health?.available !== false,
      ).length,
      unhealthy: providers.filter(
        (p) => p.enabled && p.health?.available === false,
      ).length,
      disabled: providers.filter((p) => !p.enabled).length,
    };
  }, [status]);

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
          title="Healthy"
          value={stats.healthy.toString()}
          icon="✅"
          subtext="Available"
          variant="success"
          loading={loading && !summary}
          onClick={() => navigate("/providers?status=healthy")}
        />
        <StatCard
          title="Unhealthy"
          value={stats.unhealthy.toString()}
          icon="🔴"
          subtext="Circuit Breaker"
          variant="danger"
          loading={loading && !summary}
          onClick={() => navigate("/providers?status=unhealthy")}
        />
        <StatCard
          title="Disabled"
          value={stats.disabled.toString()}
          icon="⚪"
          subtext="User Disabled"
          variant="secondary"
          loading={loading && !summary}
          onClick={() => navigate("/providers?status=disabled")}
        />
        <StatCard
          title="Total Requests"
          value={totalLogs.toString() || "0"}
          icon="📈"
          subtext="Total logged"
          variant="info"
          loading={loading && totalLogs === 0}
          onClick={() => navigate("/logs")}
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
            <div className="flex items-center gap-2">
              <RefreshIntervalSelect
                value={refreshInterval}
                onChange={setRefreshInterval}
              />
              <span className="badge badge-neutral">
                {status?.providers.length || 0} providers
              </span>
            </div>
          </div>

          <ProviderStatusContent
            status={status}
            loading={loading}
            onBatchReset={handleBatchResetClick}
            batchLoading={batchLoading}
          />
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

      <ConfirmModal
        isOpen={batchResetConfirm.isOpen}
        onClose={handleBatchResetCancel}
        onConfirm={handleBatchResetConfirm}
        title="Reset Unhealthy Providers"
        message={`Are you sure you want to reset ${batchResetConfirm.count} unhealthy providers? This will clear failure counts and enable them immediately.`}
        confirmText="Reset All"
        cancelText="Cancel"
        variant="warning"
        loading={batchLoading}
      />
    </div>
  );
}

interface ProviderStatusContentProps {
  status: SystemStatus | null;
  loading: boolean;
  onBatchReset: () => void;
  batchLoading: boolean;
}

function ProviderStatusContent({
  status,
  loading,
  onBatchReset,
  batchLoading,
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
    const hasUnhealthy = status.providers.some(
      (p) => p.enabled && p.health?.available === false,
    );

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

          // Get the proper status using the shared utility
          const providerStatus = getProviderStatus(
            provider.enabled,
            provider.health?.available,
            provider.health?.disabled_until,
          );

          const isUnhealthy = providerStatus === "unhealthy";
          const isPendingRecovery = providerStatus === "pending-recovery";
          const hasIssue = isUnhealthy || isPendingRecovery;

          const disabledUntil = provider.health?.disabled_until
            ? new Date(provider.health.disabled_until)
            : null;
          const now = new Date();
          const isCircuitOpen = disabledUntil && disabledUntil > now;
          const timeLeft = isCircuitOpen
            ? Math.ceil((disabledUntil.getTime() - now.getTime()) / 1000)
            : 0;

          const minutes = Math.floor(timeLeft / 60);
          const seconds = timeLeft % 60;
          const timeLeftStr = `${minutes}:${seconds.toString().padStart(2, "0")}`;

          // Determine card styling based on status
          let cardBorderClass =
            "border-border bg-bg-secondary/50 hover:bg-bg-secondary";
          if (isUnhealthy) {
            cardBorderClass = "border-red-200 bg-danger-light";
          } else if (isPendingRecovery) {
            cardBorderClass = "border-amber-300 bg-warning-light";
          }

          const footerBorderClass = isUnhealthy
            ? "border-red-200"
            : "border-amber-200";

          return (
            <div
              key={provider.id}
              className={`rounded-lg border transition-colors ${cardBorderClass}`}
            >
              <div className="p-3 flex items-center justify-between">
                <div className="flex items-center gap-3">
                  <div
                    className={`w-2 h-2 rounded-full ${statusDotClass[providerStatus]}`}
                  />
                  <div>
                    <Link
                      to={`/providers?search=${provider.id}`}
                      className="font-medium text-text-primary hover:text-primary transition-colors"
                    >
                      {provider.name}
                    </Link>
                    {hasIssue && provider.health?.last_error ? (
                      <p
                        className={`text-xs mt-0.5 max-w-[300px] truncate ${
                          isUnhealthy ? "text-danger" : "text-warning-dark"
                        }`}
                        title={provider.health.last_error}
                      >
                        {provider.health.last_error}
                      </p>
                    ) : (
                      <p className="text-xs text-text-muted">
                        ID: {provider.id}
                      </p>
                    )}
                  </div>
                </div>
                <div className="flex items-center gap-6">
                  {hasIssue && (
                    <div className="text-right hidden sm:block">
                      <p className="text-xs text-text-muted">Recovery</p>
                      {isCircuitOpen ? (
                        <p className="text-sm font-medium text-danger font-mono">
                          ⏱️ {timeLeftStr}
                        </p>
                      ) : (
                        <p className="text-sm font-medium text-warning-dark flex items-center justify-end gap-1">
                          <span className="inline-block w-1.5 h-1.5 bg-warning rounded-full animate-pulse" />
                          Probing
                        </p>
                      )}
                    </div>
                  )}
                  {successRate !== null && !hasIssue && (
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
                    className={`px-2 py-1 rounded text-xs font-medium ${statusBadgeClass[providerStatus]}`}
                  >
                    {statusLabel[providerStatus]}
                  </div>
                </div>
              </div>
              {hasIssue && (
                <div
                  className={`px-3 pb-2 flex items-center gap-4 text-xs text-text-secondary border-t ${footerBorderClass} pt-2 mx-3 mt-1`}
                >
                  <span>
                    Failures:{" "}
                    <span
                      className={`font-medium ${isUnhealthy ? "text-danger" : "text-warning-dark"}`}
                    >
                      {provider.health?.fail_count || 0}
                    </span>
                  </span>
                  {provider.health?.disabled_reason && (
                    <span
                      className="truncate max-w-[200px]"
                      title={provider.health.disabled_reason}
                    >
                      Reason: {provider.health.disabled_reason}
                    </span>
                  )}
                </div>
              )}
            </div>
          );
        })}

        {hasUnhealthy && (
          <div className="mt-4 pt-2 border-t border-border flex justify-end">
            <button
              onClick={onBatchReset}
              disabled={batchLoading}
              className="btn btn-sm text-danger hover:bg-danger-light/10 border-danger-light"
            >
              {batchLoading ? "Resetting..." : "🔄 Batch Reset Unhealthy"}
            </button>
          </div>
        )}
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
  subtext: string;
  variant: "primary" | "success" | "danger" | "info" | "secondary";
  loading?: boolean;
  onClick?: () => void;
}

function StatCard({
  title,
  value,
  icon,
  subtext,
  variant,
  loading,
  onClick,
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
    secondary: {
      bg: "bg-gray-100",
      icon: "text-gray-600",
      accent: "bg-gray-400",
    },
  };

  const styles = variantStyles[variant];

  return (
    <div
      className={`card relative overflow-hidden transition-transform hover:-translate-y-1 ${onClick ? "cursor-pointer" : ""}`}
      onClick={onClick}
    >
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
          <p className="text-xs text-text-muted mt-2">{subtext}</p>
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
