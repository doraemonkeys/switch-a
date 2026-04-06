import { Link } from "react-router-dom";
import type { SystemStatus, RequestLog, ProviderStatus } from "../api/client";
import {
  getDiagnosticToneClass,
  getLogEvidenceSummary,
  getLogLifecyclePresentation,
} from "../components/logs/diagnostics";
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

// Provider card component for individual provider display
interface ProviderCardProps {
  provider: ProviderStatus;
  onProviderClick?: (provider: ProviderStatus) => void;
}

function ProviderCard({ provider, onProviderClick }: ProviderCardProps) {
  const totalOps =
    (provider.health?.success_count || 0) + (provider.health?.fail_count || 0);
  const successRate =
    totalOps > 0
      ? Math.round(((provider.health?.success_count || 0) / totalOps) * 100)
      : null;

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
  const isCircuitOpen = disabledUntil !== null && disabledUntil > now;
  const timeLeft = isCircuitOpen
    ? Math.ceil((disabledUntil.getTime() - now.getTime()) / 1000)
    : 0;
  const minutes = Math.floor(timeLeft / 60);
  const seconds = timeLeft % 60;
  const timeLeftStr = `${minutes}:${seconds.toString().padStart(2, "0")}`;

  const cardBorderClass = getCardBorderClass(isUnhealthy, isPendingRecovery);
  const footerBorderClass = isUnhealthy ? "border-red-200" : "border-amber-200";

  return (
    <div className={`rounded-lg border transition-colors ${cardBorderClass}`}>
      <div className="p-3 flex items-center justify-between">
        <div className="flex items-center gap-3">
          <div
            className={`w-2 h-2 rounded-full ${statusDotClass[providerStatus]}`}
          />
          <div>
            <span
              onClick={
                onProviderClick ? () => onProviderClick(provider) : undefined
              }
              className={`font-medium text-text-primary ${onProviderClick ? "hover:text-primary cursor-pointer" : ""} transition-colors`}
            >
              {provider.name}
            </span>
            <ProviderSubtext
              provider={provider}
              hasIssue={hasIssue}
              isUnhealthy={isUnhealthy}
            />
          </div>
        </div>
        <div className="flex items-center gap-6">
          <RecoveryInfo
            hasIssue={hasIssue}
            isCircuitOpen={isCircuitOpen}
            timeLeftStr={timeLeftStr}
          />
          <SuccessRateInfo successRate={successRate} hasIssue={hasIssue} />
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
        <ProviderIssueFooter
          provider={provider}
          isUnhealthy={isUnhealthy}
          footerBorderClass={footerBorderClass}
        />
      )}
    </div>
  );
}

function getCardBorderClass(
  isUnhealthy: boolean,
  isPendingRecovery: boolean,
): string {
  if (isUnhealthy) return "border-red-200 bg-danger-light";
  if (isPendingRecovery) return "border-amber-300 bg-warning-light";
  return "border-border bg-bg-secondary/50 hover:bg-bg-secondary";
}

function ProviderSubtext({
  provider,
  hasIssue,
  isUnhealthy,
}: {
  provider: ProviderStatus;
  hasIssue: boolean;
  isUnhealthy: boolean;
}) {
  if (hasIssue && provider.health?.last_error) {
    return (
      <p
        className={`text-xs mt-0.5 max-w-[300px] truncate ${isUnhealthy ? "text-danger" : "text-warning-dark"}`}
        title={provider.health.last_error}
      >
        {provider.health.last_error}
      </p>
    );
  }
  return <p className="text-xs text-text-muted">ID: {provider.id}</p>;
}

function RecoveryInfo({
  hasIssue,
  isCircuitOpen,
  timeLeftStr,
}: {
  hasIssue: boolean;
  isCircuitOpen: boolean;
  timeLeftStr: string;
}) {
  if (!hasIssue) return null;
  return (
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
  );
}

function SuccessRateInfo({
  successRate,
  hasIssue,
}: {
  successRate: number | null;
  hasIssue: boolean;
}) {
  if (successRate === null || hasIssue) return null;
  return (
    <div className="text-right hidden sm:block">
      <p className="text-xs text-text-muted">Success Rate</p>
      <p className={`text-sm font-medium ${getSuccessRateClass(successRate)}`}>
        {successRate}%
      </p>
    </div>
  );
}

function ProviderIssueFooter({
  provider,
  isUnhealthy,
  footerBorderClass,
}: {
  provider: ProviderStatus;
  isUnhealthy: boolean;
  footerBorderClass: string;
}) {
  return (
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
  );
}

export interface ProviderStatusContentProps {
  status: SystemStatus | null;
  loading: boolean;
  onBatchReset: () => void;
  batchLoading: boolean;
  onProviderClick?: (provider: ProviderStatus) => void;
}

export function ProviderStatusContent({
  status,
  loading,
  onBatchReset,
  batchLoading,
  onProviderClick,
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
        {status.providers.map((provider) => (
          <ProviderCard
            key={provider.id}
            provider={provider}
            onProviderClick={onProviderClick}
          />
        ))}
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

export interface RecentErrorsContentProps {
  errors: RequestLog[];
  loading: boolean;
}

export function RecentErrorsContent({
  errors,
  loading,
}: RecentErrorsContentProps) {
  if (errors.length > 0) {
    return (
      <div className="space-y-3">
        {errors.map((log) => (
          <RecentErrorItem key={log.id} log={log} />
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

function RecentErrorItem({ log }: { log: RequestLog }) {
  const lifecycle = getLogLifecyclePresentation(log);
  const evidenceSummary = getLogEvidenceSummary(log);

  return (
    <div className="p-3 rounded-lg border border-danger-light bg-danger-light/10">
      <div className="flex items-start justify-between gap-4">
        <div>
          <div className="flex flex-wrap items-center gap-2 mb-1">
            <span className="font-medium text-text-primary">{log.model}</span>
            <span
              className={`px-1.5 py-0.5 rounded text-xs font-medium ${getDiagnosticToneClass(lifecycle.outcomeTone)}`}
            >
              {lifecycle.shortOutcomeLabel}
            </span>
            {log.client_transport_status_code != null && (
              <span className="px-1.5 py-0.5 rounded text-xs font-medium bg-white/70 text-text-secondary border border-border-light">
                {lifecycle.transportStatusLabel}
              </span>
            )}
          </div>
          <p className="text-sm text-text-secondary">
            {evidenceSummary ||
              lifecycle.terminationReasonLabel ||
              lifecycle.outcomeLabel}
          </p>
        </div>
        <span className="text-xs text-text-muted">
          {new Date(log.created_at).toLocaleString()}
        </span>
      </div>
    </div>
  );
}

export interface StatCardProps {
  title: string;
  value: string;
  icon: string;
  subtext: string;
  variant: "primary" | "success" | "danger" | "info" | "secondary";
  loading?: boolean;
  onClick?: () => void;
}

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
  danger: { bg: "bg-danger-light", icon: "text-red-600", accent: "bg-danger" },
  info: { bg: "bg-info-light", icon: "text-blue-600", accent: "bg-info" },
  secondary: {
    bg: "bg-gray-100",
    icon: "text-gray-600",
    accent: "bg-gray-400",
  },
};

export function StatCard({
  title,
  value,
  icon,
  subtext,
  variant,
  loading,
  onClick,
}: StatCardProps) {
  const styles = variantStyles[variant];

  return (
    <div
      className={`card relative overflow-hidden transition-transform hover:-translate-y-1 ${onClick ? "cursor-pointer" : ""}`}
      onClick={onClick}
    >
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

export interface QuickActionButtonProps {
  icon: string;
  label: string;
  onClick: () => void;
}

export function QuickActionButton({
  icon,
  label,
  onClick,
}: QuickActionButtonProps) {
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
