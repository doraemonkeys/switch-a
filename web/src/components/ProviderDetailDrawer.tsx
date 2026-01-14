import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import type { Provider, RequestLog } from "../api/types";
import { useApi } from "../api";
import {
  getProviderStatus,
  statusDotClass,
  statusBadgeClass,
  statusLabel,
  type ProviderStatusType,
} from "../pages/providers/types";
import { stringToColor, getSuccessBadgeClass } from "../lib/utils";
import { DetailSection, DetailRow } from "./DrawerSection";
import { RecoveryTimer } from "./RecoveryTimer";
import { CloseIcon } from "./icons/CloseIcon";
import { RECENT_LOGS_LIMIT } from "../config/constants";

interface ProviderDetailDrawerProps {
  provider: Provider | null;
  onClose: () => void;
  onEdit: (provider: Provider) => void;
  onDelete: (provider: Provider) => void;
  onToggle: (provider: Provider) => void;
  onReset: (provider: Provider) => void;
  getGroupName: (groupId: string | null) => string;
}

// Recent log item component
function RecentLogItem({ log }: { log: RequestLog }) {
  const formattedTime = new Date(log.created_at).toLocaleTimeString("en-US", {
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });

  const renderResult = () => {
    if (log.success) {
      return <span className="font-mono">{log.latency_ms}ms</span>;
    }
    return (
      <span
        className="text-danger truncate max-w-[100px]"
        title={log.error_msg || ""}
      >
        {log.error_msg || `${log.status_code}`}
      </span>
    );
  };

  return (
    <div className="flex items-center justify-between py-2 px-3 rounded-lg bg-bg-tertiary/50 hover:bg-bg-tertiary transition-colors">
      <div className="flex items-center gap-2">
        <span
          className={`inline-flex items-center justify-center w-5 h-5 rounded-full text-xs ${getSuccessBadgeClass(log.success)}`}
        >
          {log.success ? "✓" : "✗"}
        </span>
        <span className="text-sm font-medium text-text-primary">
          {log.model}
        </span>
      </div>
      <div className="flex items-center gap-3 text-xs text-text-muted">
        {renderResult()}
        <span>{formattedTime}</span>
      </div>
    </div>
  );
}

// Custom hook for fetching recent logs
function useRecentLogs(providerId: string | undefined) {
  const api = useApi();
  const [state, setState] = useState<{
    logs: RequestLog[];
    fetchedFor: string | null;
  }>({
    logs: [],
    fetchedFor: null,
  });

  useEffect(() => {
    if (!providerId) return;

    let cancelled = false;

    api.logs
      .list({ provider_id: providerId, limit: RECENT_LOGS_LIMIT })
      .then((response) => {
        if (!cancelled) {
          setState({ logs: response.logs, fetchedFor: providerId });
        }
      })
      .catch(() => {
        if (!cancelled) {
          setState({ logs: [], fetchedFor: providerId });
        }
      });

    return () => {
      cancelled = true;
    };
  }, [api, providerId]);

  // Show logs only if fetched for current provider
  const currentLogs =
    providerId && state.fetchedFor === providerId ? state.logs : [];
  // Show loading only while fetching for this provider
  const loading = providerId ? state.fetchedFor !== providerId : false;

  return { logs: currentLogs, loading };
}

// Basic info section component
function BasicInfoSection({
  provider,
  getGroupName,
}: {
  provider: Provider;
  getGroupName: (groupId: string | null) => string;
}) {
  const groupName = getGroupName(provider.group_id);
  const groupColors = provider.group_id
    ? stringToColor(provider.group_id)
    : undefined;

  const renderGroupBadge = () => {
    if (!provider.group_id || !groupColors) {
      return <span className="text-text-muted">—</span>;
    }
    return (
      <span
        className="px-2 py-0.5 rounded-md text-xs font-medium border"
        style={{
          backgroundColor: groupColors.bg,
          color: groupColors.text,
          borderColor: groupColors.border,
        }}
      >
        {groupName}
      </span>
    );
  };

  return (
    <DetailSection title="Basic Information">
      <DetailRow label="Endpoint" value={provider.base_url} mono />
      <DetailRow label="Group" value={renderGroupBadge()} />
      <DetailRow
        label="Priority / Weight"
        value={`P${provider.priority} / W${provider.weight}`}
      />
      <DetailRow label="Concurrency" value={provider.concurrency} />
      <DetailRow
        label="Max Retries"
        value={
          <span className="flex items-center gap-2">
            <span>{provider.max_retries}</span>
            <span className="text-xs text-text-muted">
              {provider.max_retries === 0
                ? "(switch immediately on failure)"
                : `(retry ${provider.max_retries}x before switching)`}
            </span>
          </span>
        }
      />
      <DetailRow
        label="Auth Mode"
        value={
          <span className="px-1.5 py-0.5 text-xs rounded bg-primary-light text-primary-dark uppercase">
            {provider.auth_mode}
          </span>
        }
      />
      <DetailRow
        label="API Types"
        value={
          <div className="flex flex-wrap gap-1 justify-end">
            {provider.api_types?.map((apiType) => (
              <span
                key={apiType.api_type}
                className="px-1.5 py-0.5 text-xs rounded bg-info-light text-blue-700"
              >
                {apiType.api_type}
              </span>
            )) ?? <span className="text-text-muted">—</span>}
          </div>
        }
      />
    </DetailSection>
  );
}

// Health status section component
function HealthStatusSection({
  provider,
  status,
  onReset,
}: {
  provider: Provider;
  status: ProviderStatusType;
  onReset: () => void;
}) {
  const isUnhealthy = status === "unhealthy" || status === "pending-recovery";

  const renderRecoveryStatus = () => {
    if (status === "unhealthy" && provider.health?.disabled_until) {
      return (
        <DetailRow
          label="Recovery Time"
          value={
            <RecoveryTimer
              disabledUntil={provider.health.disabled_until}
              showExpired
              className="text-sm"
            />
          }
        />
      );
    }
    if (status === "pending-recovery") {
      return (
        <DetailRow
          label="Recovery Status"
          value={
            <span className="text-xs font-medium bg-warning-light text-warning-dark px-2 py-0.5 rounded inline-flex items-center gap-1">
              <span className="inline-block w-1.5 h-1.5 bg-warning rounded-full animate-pulse" />
              Probing
            </span>
          }
        />
      );
    }
    return null;
  };

  return (
    <DetailSection
      title="Health Status"
      action={
        isUnhealthy && (
          <button
            onClick={onReset}
            className="btn btn-sm text-warning hover:bg-warning-light text-xs"
          >
            🔄 Reset
          </button>
        )
      }
    >
      <DetailRow
        label="Current Status"
        value={
          <span
            className={`px-2 py-0.5 rounded text-xs font-medium ${statusBadgeClass[status]}`}
          >
            {statusLabel[status]}
          </span>
        }
      />
      <DetailRow
        label="Success Count"
        value={
          <span className="text-success font-medium">
            {provider.health?.success_count ?? 0}
          </span>
        }
      />
      <DetailRow
        label="Fail Count"
        value={
          provider.health?.fail_count ? (
            <span className="text-danger font-medium">
              {provider.health.fail_count}
            </span>
          ) : (
            <span className="text-text-muted">0</span>
          )
        }
      />
      {provider.health?.disabled_reason && (
        <DetailRow
          label="Disabled Reason"
          value={
            <span className="text-warning-dark text-xs">
              {provider.health.disabled_reason}
            </span>
          }
        />
      )}
      {renderRecoveryStatus()}
      {provider.health?.last_error && (
        <div className="mt-2 p-3 rounded-lg bg-danger-light/50 border border-danger-light">
          <p className="text-xs text-text-muted mb-1">Last Error</p>
          <p className="text-sm text-danger-dark font-mono wrap-break-word">
            {provider.health.last_error}
          </p>
        </div>
      )}
    </DetailSection>
  );
}

// Recent requests section component
function RecentRequestsSection({
  providerId,
  logs,
  loading,
}: {
  providerId: string;
  logs: RequestLog[];
  loading: boolean;
}) {
  const renderContent = () => {
    if (loading) {
      return (
        <div className="flex items-center justify-center py-8">
          <div className="w-6 h-6 border-2 border-primary border-t-transparent rounded-full animate-spin" />
        </div>
      );
    }
    if (logs.length > 0) {
      return (
        <div className="space-y-1">
          {logs.map((log) => (
            <RecentLogItem key={log.id} log={log} />
          ))}
        </div>
      );
    }
    return (
      <div className="text-center py-6 text-text-muted text-sm">
        No recent requests
      </div>
    );
  };

  return (
    <DetailSection
      title="Recent Requests"
      action={
        <Link
          to={`/logs?provider_id=${providerId}`}
          className="text-xs text-primary hover:text-primary-hover font-medium"
        >
          View All →
        </Link>
      }
    >
      {renderContent()}
    </DetailSection>
  );
}

export function ProviderDetailDrawer({
  provider,
  onClose,
  onEdit,
  onDelete,
  onToggle,
  onReset,
  getGroupName,
}: ProviderDetailDrawerProps) {
  const { logs: recentLogs, loading: logsLoading } = useRecentLogs(
    provider?.id,
  );

  useEffect(() => {
    if (provider) {
      const handleEscape = (e: KeyboardEvent) => {
        if (e.key === "Escape") onClose();
      };
      document.addEventListener("keydown", handleEscape);
      document.body.style.overflow = "hidden";
      return () => {
        document.removeEventListener("keydown", handleEscape);
        document.body.style.overflow = "";
      };
    }
  }, [provider, onClose]);

  if (!provider) return null;

  const status = getProviderStatus(
    provider.enabled,
    provider.health?.available,
    provider.health?.disabled_until,
  );

  return (
    <>
      <div
        className="fixed inset-0 z-40 bg-black/30 backdrop-blur-sm transition-opacity"
        onClick={onClose}
      />
      <div className="fixed inset-y-0 right-0 z-50 w-full max-w-md bg-bg-primary shadow-2xl border-l border-border-light flex flex-col animate-slide-in-right">
        {/* Header */}
        <div className="flex items-start justify-between p-6 border-b border-border-light bg-bg-secondary">
          <div className="flex-1 min-w-0 pr-4">
            <div className="flex items-center gap-2 mb-1">
              <div
                className={`w-2.5 h-2.5 rounded-full ${statusDotClass[status]}`}
              />
              <h2 className="text-xl font-bold text-text-primary truncate">
                {provider.name}
              </h2>
            </div>
            <p className="text-sm text-text-muted font-mono truncate">
              {provider.id}
            </p>
          </div>
          <button
            onClick={onClose}
            className="p-2 rounded-lg text-text-secondary hover:text-text-primary hover:bg-bg-tertiary transition-colors cursor-pointer"
            aria-label="Close"
          >
            <CloseIcon />
          </button>
        </div>

        {/* Content */}
        <div className="flex-1 overflow-y-auto p-6 space-y-6">
          <BasicInfoSection provider={provider} getGroupName={getGroupName} />
          <HealthStatusSection
            provider={provider}
            status={status}
            onReset={() => onReset(provider)}
          />
          <RecentRequestsSection
            providerId={provider.id}
            logs={recentLogs}
            loading={logsLoading}
          />
          <div className="text-xs text-text-muted space-y-1 pt-4 border-t border-border-light">
            <p>Created: {new Date(provider.created_at).toLocaleString()}</p>
            <p>Updated: {new Date(provider.updated_at).toLocaleString()}</p>
          </div>
        </div>

        {/* Footer Actions */}
        <div className="p-4 border-t border-border-light bg-bg-secondary">
          <div className="flex items-center justify-between gap-3">
            <button
              onClick={() => onDelete(provider)}
              className="btn btn-ghost text-danger hover:bg-danger-light"
              title="Delete Provider"
            >
              🗑️ Delete
            </button>
            <div className="flex items-center gap-2">
              <button
                onClick={() => onToggle(provider)}
                className="btn btn-secondary btn-sm"
                title={
                  provider.enabled ? "Disable Provider" : "Enable Provider"
                }
              >
                {provider.enabled ? "⏸️ Disable" : "▶️ Enable"}
              </button>
              <button
                onClick={() => onEdit(provider)}
                className="btn btn-primary btn-sm"
              >
                ✏️ Edit
              </button>
            </div>
          </div>
        </div>
      </div>
    </>
  );
}
