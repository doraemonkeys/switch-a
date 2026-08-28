import { useEffect, useState } from "react";
import { Link } from "react-router";
import type { Provider, RequestLog } from "../../api/types";
import { useApi } from "../../api";
import {
  getProviderStatus,
  statusDotClass,
  statusBadgeClass,
  statusLabel,
  type ProviderStatusType,
} from "../../pages/providers/types";
import {
  formatProviderCredentialType,
  resolveProviderCredentialSession,
} from "../../lib/providerAuth";
import { stringToColor } from "../../lib/utils";
import { DetailSection, DetailRow } from "./DrawerSection";
import { AuthSection } from "./ProviderDetailDrawerAuthSection";
import { ProviderErrorDetectionSummary } from "./ProviderErrorDetectionSummary";
import { RecoveryTimer } from "./RecoveryTimer";
import { CloseIcon } from "../icons/CloseIcon";
import {
  RECENT_LOGS_LIMIT,
  FAILOVER_SCOPES,
  VENDOR_WILDCARD,
  PROVIDER_DEFAULTS,
  PROVIDER_USAGE_LIMIT_POLICY_OPTIONS,
  defaultProviderUsageLimitPolicy,
} from "../../config/constants";
import {
  getDiagnosticToneClass,
  getLogEvidenceSummary,
  getLogLifecyclePresentation,
} from "../logs/diagnostics";

interface ProviderDetailDrawerProps {
  provider: Provider | null;
  onClose: () => void;
  onEdit: (provider: Provider) => void;
  onDelete: (provider: Provider) => void;
  onToggle: (provider: Provider) => void;
  onReset: (provider: Provider) => void;
  onRefreshCredential?: (provider: Provider) => Promise<void>;
  onRefreshUsage?: (provider: Provider) => Promise<void>;
  getGroupName: (groupId: string | null) => string;
}

// Recent log item component
function RecentLogItem({ log }: { log: RequestLog }) {
  const formattedTime = new Date(log.created_at).toLocaleTimeString("en-US", {
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
  const lifecycle = getLogLifecyclePresentation(log);
  const evidenceSummary = getLogEvidenceSummary(log);

  return (
    <div className="flex items-center justify-between py-2 px-3 rounded-lg bg-bg-tertiary/50 hover:bg-bg-tertiary transition-colors">
      <div className="flex items-center gap-2">
        <span
          className={`inline-flex items-center justify-center min-w-5 h-5 rounded-full text-[10px] px-1 ${getDiagnosticToneClass(lifecycle.outcomeTone)}`}
        >
          {lifecycle.shortOutcomeLabel}
        </span>
        <div className="min-w-0">
          <span className="text-sm font-medium text-text-primary">
            {log.model}
          </span>
          <p className="text-xs text-text-muted truncate max-w-[180px]">
            {evidenceSummary ||
              lifecycle.terminationReasonLabel ||
              lifecycle.outcomeLabel}
          </p>
        </div>
      </div>
      <div className="flex items-center gap-3 text-xs text-text-muted">
        <span className="font-mono">
          {log.client_transport_status_code ?? "—"}
        </span>
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
      <DetailRow
        label="API Types"
        value={
          provider.api_types?.length ? (
            <div className="space-y-1.5">
              {provider.api_types.map((apiType) => {
                const session = resolveProviderCredentialSession(
                  provider,
                  apiType.api_type,
                );
                return (
                  <div
                    key={apiType.api_type}
                    className="flex items-center gap-2 justify-end"
                  >
                    <span className="px-1.5 py-0.5 text-xs rounded bg-info-light text-blue-700 shrink-0">
                      {apiType.api_type}
                    </span>
                    <span
                      className="px-1.5 py-0.5 text-[10px] font-medium rounded shrink-0 bg-bg-tertiary text-text-muted"
                      title={session?.id}
                    >
                      {formatProviderCredentialType(session?.kind)}
                    </span>
                    <span
                      className="text-xs font-mono text-text-secondary truncate"
                      title={apiType.base_url}
                    >
                      {apiType.base_url}
                    </span>
                  </div>
                );
              })}
            </div>
          ) : (
            <span className="text-text-muted">—</span>
          )
        }
      />
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
        label="Usage Limit Policy"
        value={
          PROVIDER_USAGE_LIMIT_POLICY_OPTIONS.find(
            (option) =>
              option.value ===
              (provider.usage_limit_policy ||
                defaultProviderUsageLimitPolicy()),
          )?.label || "Unknown"
        }
      />
    </DetailSection>
  );
}

// Failover scope badge component
function ScopeBadge({
  scope,
  direction,
}: {
  scope: string;
  direction: "out" | "in";
}) {
  const scopeConfig: Record<
    string,
    { label: string; icon: string; className: string }
  > = {
    [FAILOVER_SCOPES.NONE]: {
      label: "None",
      icon: "🚫",
      className: "bg-danger-light text-danger-dark",
    },
    [FAILOVER_SCOPES.VENDOR]: {
      label: "Vendor",
      icon: "🔗",
      className: "bg-warning-light text-warning-dark",
    },
    [FAILOVER_SCOPES.ANY]: {
      label: "Any",
      icon: "🌐",
      className: "bg-success-light text-success-dark",
    },
  };

  const config = scopeConfig[scope] ?? scopeConfig[FAILOVER_SCOPES.ANY];

  return (
    <span
      className={`inline-flex items-center gap-1 px-2 py-0.5 rounded text-xs font-medium ${config.className}`}
      title={
        direction === "out"
          ? "Outbound true-failover scope"
          : "Inbound true-failover acceptance"
      }
    >
      <span>{config.icon}</span>
      {config.label}
    </span>
  );
}

// Backoff info section component - only shown when max_retries > 0
function BackoffInfoSection({ provider }: { provider: Provider }) {
  // Only show if retries are enabled
  if (!provider.max_retries || provider.max_retries === 0) {
    return null;
  }

  const backoff = provider.backoff;
  const hasCustomBackoff =
    backoff &&
    (backoff.initial_delay !== PROVIDER_DEFAULTS.BACKOFF.INITIAL_DELAY ||
      backoff.max_delay !== PROVIDER_DEFAULTS.BACKOFF.MAX_DELAY ||
      (backoff.multiplier !== undefined &&
        backoff.multiplier !== PROVIDER_DEFAULTS.BACKOFF.MULTIPLIER) ||
      (backoff.jitter !== undefined &&
        backoff.jitter !== PROVIDER_DEFAULTS.BACKOFF.JITTER));

  // If no custom backoff, show a simple note about defaults
  if (!hasCustomBackoff) {
    return (
      <DetailSection title="Retry Backoff">
        <div className="p-2 rounded-lg bg-bg-tertiary/50 text-xs text-text-muted">
          Using default exponential backoff:{" "}
          <span className="font-mono">
            {PROVIDER_DEFAULTS.BACKOFF.INITIAL_DELAY}
          </span>{" "}
          →{" "}
          <span className="font-mono">
            {PROVIDER_DEFAULTS.BACKOFF.MAX_DELAY}
          </span>{" "}
          (×{PROVIDER_DEFAULTS.BACKOFF.MULTIPLIER})
        </div>
      </DetailSection>
    );
  }

  return (
    <DetailSection title="Retry Backoff">
      <DetailRow
        label="Initial Delay"
        value={
          <span className="font-mono text-xs">{backoff.initial_delay}</span>
        }
      />
      <DetailRow
        label="Max Delay"
        value={<span className="font-mono text-xs">{backoff.max_delay}</span>}
      />
      <DetailRow
        label="Multiplier"
        value={`×${backoff.multiplier ?? PROVIDER_DEFAULTS.BACKOFF.MULTIPLIER}`}
      />
      <DetailRow
        label="Jitter"
        value={
          backoff.jitter ? (
            <span className="px-1.5 py-0.5 text-xs rounded bg-info-light text-info-dark">
              Enabled
            </span>
          ) : (
            <span className="text-text-muted">Off</span>
          )
        }
      />
    </DetailSection>
  );
}

// Failover info section component
function FailoverInfoSection({ provider }: { provider: Provider }) {
  const hasVendorConfig =
    provider.vendor ||
    provider.failover_scope !== FAILOVER_SCOPES.ANY ||
    provider.accept_failover !== FAILOVER_SCOPES.ANY;

  // Show warning if vendor scope is set but vendor is empty
  const hasWarning =
    !provider.vendor &&
    (provider.failover_scope === FAILOVER_SCOPES.VENDOR ||
      provider.accept_failover === FAILOVER_SCOPES.VENDOR);

  if (!hasVendorConfig) {
    return null;
  }

  const renderVendorBadge = () => {
    if (!provider.vendor) {
      return <span className="text-text-muted italic">Not set</span>;
    }
    if (provider.vendor === VENDOR_WILDCARD) {
      return (
        <span className="px-2 py-0.5 bg-info-light text-info-dark rounded text-xs font-medium">
          * (Wildcard)
        </span>
      );
    }
    return (
      <span className="px-2 py-0.5 bg-primary-light text-primary-dark rounded text-xs font-medium font-mono">
        {provider.vendor}
      </span>
    );
  };

  return (
    <DetailSection title="Failover Isolation">
      <div className="mb-3 p-2 rounded-lg bg-info-light/30 border border-info-light/50 text-xs text-text-secondary">
        Accept Failover only governs true failover after client-visible
        continuity already exists. Pre-visible provider replacement is not
        blocked by these settings.
      </div>
      {hasWarning && (
        <div className="mb-3 p-2 rounded-lg bg-warning-light/50 border border-warning-light text-xs text-warning-dark flex items-start gap-2">
          <span>⚠️</span>
          <span>
            Vendor scope is configured but vendor is empty - failover may be
            blocked
          </span>
        </div>
      )}
      <DetailRow label="Vendor" value={renderVendorBadge()} />
      <DetailRow
        label="Failover To"
        value={
          <ScopeBadge
            scope={provider.failover_scope || FAILOVER_SCOPES.ANY}
            direction="out"
          />
        }
      />
      <DetailRow
        label="Accept Failover From"
        value={
          <ScopeBadge
            scope={provider.accept_failover || FAILOVER_SCOPES.ANY}
            direction="in"
          />
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
            aria-label="Reset health status"
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
        <div
          className="flex items-center justify-center py-8"
          role="status"
          aria-label="Loading recent requests"
        >
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
      <p className="text-center py-6 text-text-muted text-sm">
        No recent requests
      </p>
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
  onRefreshCredential,
  onRefreshUsage,
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
    <div role="dialog" aria-modal="true" aria-labelledby="drawer-title">
      <div
        className="fixed inset-0 z-40 bg-black/30 backdrop-blur-sm transition-opacity"
        onClick={onClose}
        aria-hidden="true"
      />
      <div className="fixed inset-y-0 right-0 z-50 w-full max-w-md bg-bg-primary shadow-2xl border-l border-border-light flex flex-col animate-slide-in-right">
        {/* Header */}
        <div className="flex items-start justify-between p-6 border-b border-border-light bg-bg-secondary">
          <div className="flex-1 min-w-0 pr-4">
            <div className="flex items-center gap-2 mb-1">
              <div
                className={`w-2.5 h-2.5 rounded-full ${statusDotClass[status]}`}
              />
              <h2
                id="drawer-title"
                className="text-xl font-bold text-text-primary truncate"
              >
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
          <ProviderErrorDetectionSummary provider={provider} />
          <AuthSection
            provider={provider}
            onRefreshCredential={onRefreshCredential}
            onRefreshUsage={onRefreshUsage}
          />
          <BackoffInfoSection provider={provider} />
          <FailoverInfoSection provider={provider} />
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
            <p>
              Created:{" "}
              <time dateTime={provider.created_at}>
                {new Date(provider.created_at).toLocaleString()}
              </time>
            </p>
            <p>
              Updated:{" "}
              <time dateTime={provider.updated_at}>
                {new Date(provider.updated_at).toLocaleString()}
              </time>
            </p>
          </div>
        </div>

        {/* Footer Actions */}
        <div className="p-4 border-t border-border-light bg-bg-secondary">
          <div className="flex items-center justify-between gap-3">
            <button
              onClick={() => onDelete(provider)}
              className="btn btn-ghost text-danger hover:bg-danger-light"
              aria-label="Delete provider"
            >
              🗑️ Delete
            </button>
            <div className="flex items-center gap-2">
              <button
                onClick={() => onToggle(provider)}
                className="btn btn-secondary btn-sm"
                aria-label={
                  provider.enabled ? "Disable provider" : "Enable provider"
                }
              >
                {provider.enabled ? "⏸️ Disable" : "▶️ Enable"}
              </button>
              <button
                onClick={() => onEdit(provider)}
                className="btn btn-primary btn-sm"
                aria-label="Edit provider"
              >
                ✏️ Edit
              </button>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
