import { useState } from "react";
import type { Provider } from "../api/types";
import {
  formatProviderPlanType,
  formatProviderResetAt,
  formatProviderUsagePercent,
  resolveProviderPlanType,
  resolveProviderUsage,
} from "../lib/providerUsage";
import {
  AUTH_STATUS_BADGE_CLASS,
  formatProviderCredentialType,
  resolveProviderAuthView,
} from "../lib/providerAuth";
import { DetailRow, DetailSection } from "./DrawerSection";

function UsageWindowCard({
  label,
  percentLabel,
  resetAt,
  progressWidth,
}: {
  label: string;
  percentLabel: string;
  resetAt?: string;
  progressWidth: number;
}) {
  return (
    <div className="rounded-lg border border-border/60 bg-bg-secondary/40 p-3 space-y-2">
      <div className="flex items-center justify-between gap-3 text-sm">
        <span className="font-medium text-text-secondary">{label}</span>
        <span className="font-semibold text-text-primary">
          {percentLabel} used
        </span>
      </div>
      <div className="h-2 rounded-full bg-bg-tertiary overflow-hidden">
        <div
          className="h-full rounded-full bg-primary transition-all duration-300"
          style={{ width: `${progressWidth}%` }}
        />
      </div>
      <div className="text-[11px] text-text-muted">
        Reset: {formatProviderResetAt(resetAt)}
      </div>
    </div>
  );
}

export function AuthSection({
  provider,
  onRefreshCredential,
  onRefreshUsage,
}: {
  provider: Provider;
  onRefreshCredential?: (provider: Provider) => Promise<void>;
  onRefreshUsage?: (provider: Provider) => Promise<void>;
}) {
  const authView = resolveProviderAuthView(provider);
  const usage = resolveProviderUsage(authView);
  const planType = resolveProviderPlanType(authView);
  const hasUsage = Boolean(usage?.five_hour || usage?.one_week);
  const [refreshingCredential, setRefreshingCredential] = useState(false);
  const [refreshingUsage, setRefreshingUsage] = useState(false);
  const canSyncChatGPT =
    provider.credential_type === "chatgpt" && authView?.status === "active";

  const handleRefreshCredential = async () => {
    if (!onRefreshCredential) {
      return;
    }
    setRefreshingCredential(true);
    try {
      await onRefreshCredential(provider);
    } finally {
      setRefreshingCredential(false);
    }
  };

  const handleRefreshUsage = async () => {
    if (!onRefreshUsage) {
      return;
    }
    setRefreshingUsage(true);
    try {
      await onRefreshUsage(provider);
    } finally {
      setRefreshingUsage(false);
    }
  };

  const renderUsageHint = () => {
    if (provider.credential_type !== "chatgpt") {
      return null;
    }
    if (authView?.status === "not_connected") {
      return (
        <div className="rounded-lg border border-border/60 bg-bg-secondary/40 p-3 text-xs text-text-muted">
          Connect a GPT account from Edit before this provider can participate
          in Codex routing.
        </div>
      );
    }
    if (authView?.status === "reauth_required") {
      return (
        <div className="rounded-lg border border-danger/20 bg-danger-light/30 p-3 text-xs text-danger">
          Reconnect this provider from Edit before manual sync can resume.
        </div>
      );
    }
    return (
      <div className="rounded-lg border border-border/60 bg-bg-secondary/40 p-3 text-xs text-text-muted">
        Usage data is not available yet. Trigger Refresh Usage or wait for the
        next background sync snapshot.
      </div>
    );
  };

  return (
    <DetailSection
      title="Authentication"
      action={
        provider.credential_type === "chatgpt" ? (
          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={() => void handleRefreshCredential()}
              disabled={
                !canSyncChatGPT || !onRefreshCredential || refreshingCredential
              }
              className="btn btn-sm text-xs"
            >
              {refreshingCredential ? "Refreshing..." : "Refresh Credential"}
            </button>
            <button
              type="button"
              onClick={() => void handleRefreshUsage()}
              disabled={!canSyncChatGPT || !onRefreshUsage || refreshingUsage}
              className="btn btn-sm text-xs"
            >
              {refreshingUsage ? "Refreshing..." : "Refresh Usage"}
            </button>
          </div>
        ) : undefined
      }
    >
      <DetailRow
        label="Type"
        value={formatProviderCredentialType(provider.credential_type)}
      />
      <DetailRow
        label="Status"
        value={
          authView ? (
            <span
              className={`inline-flex rounded-full px-2 py-0.5 text-xs font-semibold tracking-wide ${AUTH_STATUS_BADGE_CLASS[authView.status]}`}
            >
              {authView.status}
            </span>
          ) : (
            "—"
          )
        }
      />
      {authView?.reason && <DetailRow label="Reason" value={authView.reason} />}
      {authView?.email && <DetailRow label="Account" value={authView.email} />}
      {authView?.account_id && (
        <DetailRow label="Workspace" value={authView.account_id} mono />
      )}
      {planType && (
        <DetailRow label="Plan" value={formatProviderPlanType(planType)} />
      )}
      {authView?.expires_at && (
        <DetailRow
          label="Expires"
          value={new Date(authView.expires_at).toLocaleString()}
        />
      )}
      {authView?.last_refresh_at && (
        <DetailRow
          label="Last Refresh"
          value={new Date(authView.last_refresh_at).toLocaleString()}
        />
      )}
      {usage?.fetched_at && (
        <DetailRow
          label="Usage Updated"
          value={new Date(usage.fetched_at).toLocaleString()}
        />
      )}
      {authView?.last_error && (
        <div className="rounded-lg border border-danger/20 bg-danger-light/30 p-3">
          <p className="text-xs text-text-muted mb-1">Last Auth Error</p>
          <p className="text-sm text-danger-dark font-mono wrap-break-word">
            {authView.last_error}
          </p>
        </div>
      )}
      {provider.credential_type === "chatgpt" &&
        (hasUsage ? (
          <div className="space-y-3 pt-1">
            {usage?.five_hour && (
              <UsageWindowCard
                label="5 Hours"
                percentLabel={formatProviderUsagePercent(usage.five_hour)}
                resetAt={usage.five_hour.reset_at}
                progressWidth={Math.max(
                  0,
                  Math.min(100, usage.five_hour.used_percent),
                )}
              />
            )}
            {usage?.one_week && (
              <UsageWindowCard
                label="1 Week"
                percentLabel={formatProviderUsagePercent(usage.one_week)}
                resetAt={usage.one_week.reset_at}
                progressWidth={Math.max(
                  0,
                  Math.min(100, usage.one_week.used_percent),
                )}
              />
            )}
          </div>
        ) : (
          renderUsageHint()
        ))}
    </DetailSection>
  );
}
