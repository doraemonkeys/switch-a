import type { Provider, ProviderAuthView } from "../../api/client";
import { hasProviderApiKey } from "../../lib/providerApiKey";
import {
  formatProviderPlanType,
  formatProviderUsageWindowSummary,
  resolveProviderPlanType,
  resolveProviderUsage,
} from "../../lib/providerUsage";
import {
  AUTH_STATUS_BADGE_CLASS,
  resolveProviderAuthView,
} from "../../lib/providerAuth";
import { stringToColor } from "../../lib/utils";
import { RecoveryTimer } from "../../components/RecoveryTimer";
import {
  Info,
  Play,
  Pause,
  Edit2,
  Trash2,
  Eye,
  RotateCw,
  Activity,
  AlertCircle,
  PlusCircle,
  FolderOpen,
  RefreshCw,
  ServerCrash,
} from "lucide-react";
import {
  getProviderStatus,
  statusDotClass,
  statusBadgeClass,
  statusLabel,
  type ProviderStatusType,
} from "./types";

export interface ProvidersTableBodyProps {
  loading: boolean;
  providers: Provider[];
  filteredProviders: Provider[];
  onToggle: (provider: Provider) => void;
  onEdit: (provider: Provider) => void;
  onDelete: (provider: Provider) => void;
  onReset: (provider: Provider) => void;
  onAddClick: () => void;
  onGroupClick?: (groupId: string) => void;
  onViewDetail?: (provider: Provider) => void;
  getGroupName: (groupId: string | null) => string;
  getGroupEnabled: (groupId: string | null) => boolean | undefined;
}

// Status badge with optional tooltip
function StatusCell({
  status,
  authView,
  disabledReason,
  lastError,
}: {
  status: ProviderStatusType;
  authView: ProviderAuthView | null;
  disabledReason?: string;
  lastError?: string;
}) {
  const showTooltip =
    (status === "unhealthy" || status === "pending-recovery") && disabledReason;

  const getBorderColor = (s: ProviderStatusType) => {
    if (s === "healthy") return "border-success/20";
    if (s === "unhealthy") return "border-danger/20";
    if (s === "pending-recovery") return "border-warning/20";
    return "border-gray-200";
  };

  return (
    <div className="space-y-2">
      <div className="group relative flex items-center gap-1.5">
        <div
          className={`px-2.5 py-1 rounded-full text-xs font-semibold tracking-wide border inline-flex items-center gap-1.5 ${statusBadgeClass[status]} ${getBorderColor(status)}`}
        >
          <div
            className={`w-1.5 h-1.5 rounded-full ${statusDotClass[status]} ${status === "pending-recovery" ? "animate-pulse" : ""}`}
          />
          {statusLabel[status]}
        </div>
        {showTooltip && (
          <div className="relative group/tooltip">
            <Info className="w-4 h-4 text-text-muted hover:text-text-primary cursor-help transition-colors" />
            <div className="absolute left-1/2 -translate-x-1/2 bottom-full mb-2 w-56 p-2.5 bg-gray-900 text-white text-xs rounded-lg shadow-xl opacity-0 invisible group-hover/tooltip:opacity-100 group-hover/tooltip:visible transition-all z-10 pointer-events-none">
              <div className="font-medium">{disabledReason}</div>
              {lastError && (
                <>
                  <div className="h-px bg-gray-700 my-2" />
                  <span className="opacity-80 break-words line-clamp-3 leading-relaxed">
                    Error: {lastError}
                  </span>
                </>
              )}
              <div className="absolute -bottom-1 left-1/2 -translate-x-1/2 w-2 h-2 bg-gray-900 rotate-45" />
            </div>
          </div>
        )}
      </div>
      {authView && (
        <div className="space-y-1">
          <span className="text-[10px] font-semibold uppercase tracking-wide text-text-muted">
            Auth
          </span>
          <div
            className={`inline-flex rounded-full px-2 py-0.5 text-[11px] font-semibold tracking-wide ${AUTH_STATUS_BADGE_CLASS[authView.status]}`}
          >
            {authView.status}
          </div>
          {authView.reason && (
            <p className="text-[11px] text-text-secondary">{authView.reason}</p>
          )}
        </div>
      )}
      {authView?.last_error && (
        <div className="text-[11px] text-danger" title={authView.last_error}>
          {authView.last_error}
        </div>
      )}
    </div>
  );
}

// Recovery status cell
function RecoveryCell({
  status,
  disabledUntil,
}: {
  status: ProviderStatusType;
  disabledUntil?: string;
}) {
  if (status === "pending-recovery") {
    return (
      <span className="text-xs font-medium bg-warning-light/50 text-warning-dark px-2.5 py-1 rounded-md inline-flex items-center gap-1.5 border border-warning/10">
        <Activity className="w-3.5 h-3.5 animate-pulse text-warning" />
        Probing
      </span>
    );
  }
  if (disabledUntil) {
    return (
      <div className="flex items-center gap-1.5 text-xs text-text-secondary">
        <RecoveryTimer disabledUntil={disabledUntil} className="font-medium" />
      </div>
    );
  }
  return <span className="text-text-muted/50 text-sm">—</span>;
}

// Action buttons cell
function ActionsCell({
  provider,
  status,
  onViewDetail,
  onReset,
  onToggle,
  onEdit,
  onDelete,
}: {
  provider: Provider;
  status: string;
  onViewDetail?: (provider: Provider) => void;
  onReset: (provider: Provider) => void;
  onToggle: (provider: Provider) => void;
  onEdit: (provider: Provider) => void;
  onDelete: (provider: Provider) => void;
}) {
  const showReset = status === "unhealthy" || status === "pending-recovery";

  return (
    <div className="flex items-center justify-end gap-0.5 opacity-80 group-hover:opacity-100 transition-opacity">
      {onViewDetail && (
        <button
          onClick={() => onViewDetail(provider)}
          className="p-1.5 text-text-muted hover:text-primary hover:bg-primary-light rounded-md transition-colors"
          title="View Details"
        >
          <Eye className="w-4 h-4" />
        </button>
      )}
      <button
        onClick={() => onReset(provider)}
        className={`p-1.5 text-text-muted hover:text-warning hover:bg-warning-light rounded-md transition-colors ${showReset ? "" : "hidden"}`}
        title="Reset Circuit Breaker"
      >
        <RotateCw className="w-4 h-4" />
      </button>
      <button
        onClick={() => onToggle(provider)}
        className={`p-1.5 text-text-muted hover:bg-bg-hover rounded-md transition-colors ${provider.enabled ? "hover:text-warning" : "hover:text-success"}`}
        title={provider.enabled ? "Disable" : "Enable"}
      >
        {provider.enabled ? (
          <Pause className="w-4 h-4" />
        ) : (
          <Play className="w-4 h-4" />
        )}
      </button>
      <button
        onClick={() => onEdit(provider)}
        className="p-1.5 text-text-muted hover:text-primary hover:bg-bg-hover rounded-md transition-colors"
        title="Edit"
      >
        <Edit2 className="w-4 h-4" />
      </button>
      <button
        onClick={() => onDelete(provider)}
        className="p-1.5 text-text-muted hover:text-danger hover:bg-danger-light rounded-md transition-colors"
        title="Delete"
      >
        <Trash2 className="w-4 h-4" />
      </button>
    </div>
  );
}

function formatEndpointSummary(provider: Provider): string {
  const apiTypes = provider.api_types ?? [];
  const apiTypeCount = apiTypes.length;
  if (apiTypeCount === 0) {
    return "No endpoint configured";
  }
  if (apiTypeCount === 1) {
    return apiTypes[0]?.base_url || "No endpoint configured";
  }

  const apiKeyOverrideCount = apiTypes.filter((apiType) =>
    hasProviderApiKey(apiType.api_key),
  ).length;
  const endpointSummary = `${apiTypeCount} endpoints configured`;
  if (apiKeyOverrideCount === 0) {
    return endpointSummary;
  }

  const keySummary =
    apiKeyOverrideCount === 1
      ? "1 custom key"
      : `${apiKeyOverrideCount} custom keys`;
  return `${endpointSummary}, ${keySummary}`;
}

function formatChatGPTUsageSummary(provider: Provider): string | null {
  const authView = resolveProviderAuthView(provider);
  if (provider.credential_type !== "chatgpt" || authView?.status !== "active") {
    return null;
  }

  const planType = resolveProviderPlanType(authView);
  const usage = resolveProviderUsage(authView);
  const parts = [
    planType ? formatProviderPlanType(planType) : "GPT",
    formatProviderUsageWindowSummary("5h", usage?.five_hour),
    formatProviderUsageWindowSummary("1w", usage?.one_week),
  ];
  return parts.join(" • ");
}

function GroupCell({
  provider,
  groupName,
  groupEnabled,
  onGroupClick,
}: {
  provider: Provider;
  groupName: string;
  groupEnabled: boolean | undefined;
  onGroupClick?: (groupId: string) => void;
}) {
  if (!provider.group_id) {
    return <span className="text-text-muted/50 text-sm">—</span>;
  }

  const isGroupDisabled = groupEnabled === false;
  const groupColors = stringToColor(provider.group_id);
  return (
    <span
      className={`px-2.5 py-1 rounded-md text-[11px] font-medium border whitespace-nowrap max-w-[120px] inline-flex items-center gap-1.5 cursor-pointer hover:opacity-80 transition-opacity shadow-sm ${
        isGroupDisabled
          ? "bg-warning-light/70 text-warning-dark border-warning/20"
          : ""
      }`}
      style={
        isGroupDisabled
          ? undefined
          : {
              backgroundColor: groupColors.bg,
              color: groupColors.text,
              borderColor: groupColors.border,
            }
      }
      title={
        isGroupDisabled
          ? `Filter by group: ${groupName} (group disabled)`
          : `Filter by group: ${groupName}`
      }
      onClick={() => onGroupClick?.(provider.group_id!)}
    >
      <FolderOpen
        className={`w-3 h-3 shrink-0 ${isGroupDisabled ? "opacity-90" : "opacity-70"}`}
      />
      <span className="truncate">{groupName}</span>
      {isGroupDisabled && (
        <AlertCircle
          className="w-3 h-3 shrink-0 text-warning-dark"
          aria-label="Group disabled"
        />
      )}
    </span>
  );
}

function APITypesCell({ provider }: { provider: Provider }) {
  const apiTypes = provider.api_types ?? [];
  if (apiTypes.length === 0) {
    return <span className="text-text-muted/50 text-sm">—</span>;
  }

  return (
    <div className="flex flex-wrap gap-1.5">
      {apiTypes.slice(0, 2).map((apiType) => (
        <span
          key={apiType.api_type}
          className="px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wider rounded-md bg-bg-tertiary text-text-secondary border border-border/50"
        >
          {apiType.api_type}
        </span>
      ))}
      {apiTypes.length > 2 && (
        <span className="px-1.5 py-0.5 text-[10px] font-semibold rounded-md bg-bg-tertiary text-text-muted border border-border/50">
          +{apiTypes.length - 2}
        </span>
      )}
    </div>
  );
}

function FailCountCell({ failCount }: { failCount: number }) {
  if (failCount === 0) {
    return <span className="text-text-muted/50 text-sm">—</span>;
  }

  return (
    <div className="inline-flex items-center gap-1 text-[11px] font-bold text-danger bg-danger-light/50 px-2 py-0.5 rounded-md border border-danger/10">
      <AlertCircle className="w-3 h-3" />
      {failCount}
    </div>
  );
}

// Provider row component
function ProviderRow({
  provider,
  onToggle,
  onEdit,
  onDelete,
  onReset,
  onGroupClick,
  onViewDetail,
  getGroupName,
  getGroupEnabled,
}: {
  provider: Provider;
  onToggle: (provider: Provider) => void;
  onEdit: (provider: Provider) => void;
  onDelete: (provider: Provider) => void;
  onReset: (provider: Provider) => void;
  onGroupClick?: (groupId: string) => void;
  onViewDetail?: (provider: Provider) => void;
  getGroupName: (groupId: string | null) => string;
  getGroupEnabled: (groupId: string | null) => boolean | undefined;
}) {
  const status = getProviderStatus(
    provider.enabled,
    provider.health?.available,
    provider.health?.disabled_until,
  );
  const failCount = provider.health?.fail_count || 0;
  const groupName = getGroupName(provider.group_id);
  const groupEnabled = getGroupEnabled(provider.group_id);
  const endpointSummary = formatEndpointSummary(provider);
  const chatGPTUsageSummary = formatChatGPTUsageSummary(provider);
  const authView = resolveProviderAuthView(provider);

  return (
    <tr className="group hover:bg-bg-secondary/60 transition-colors border-b border-border/40 last:border-b-0">
      <td className="px-4 py-3 align-middle">
        <div className="flex items-center gap-3">
          <div
            className={`w-1.5 h-6 rounded-full shrink-0 ${statusDotClass[status]} opacity-80`}
          />
          <div className="min-w-0">
            <p
              className={`font-semibold text-sm text-text-primary truncate ${onViewDetail ? "hover:text-primary cursor-pointer transition-colors" : ""}`}
              onClick={onViewDetail ? () => onViewDetail(provider) : undefined}
              title={onViewDetail ? "View details" : undefined}
            >
              {provider.name}
            </p>
            <p className="text-[11px] text-text-muted truncate mt-0.5">
              {endpointSummary}
            </p>
            {chatGPTUsageSummary && (
              <p
                className="text-[11px] text-text-secondary truncate mt-0.5"
                title={chatGPTUsageSummary}
              >
                {chatGPTUsageSummary}
              </p>
            )}
          </div>
        </div>
      </td>
      <td className="px-4 py-3 align-middle">
        <GroupCell
          provider={provider}
          groupName={groupName}
          groupEnabled={groupEnabled}
          onGroupClick={onGroupClick}
        />
      </td>
      <td className="px-4 py-3 align-middle">
        <APITypesCell provider={provider} />
      </td>
      <td className="px-4 py-3 align-middle">
        <StatusCell
          status={status}
          authView={authView}
          disabledReason={provider.health?.disabled_reason ?? undefined}
          lastError={provider.health?.last_error ?? undefined}
        />
      </td>
      <td className="px-4 py-3 align-middle text-center">
        <FailCountCell failCount={failCount} />
      </td>
      <td className="px-4 py-3 align-middle whitespace-nowrap">
        <RecoveryCell
          status={status}
          disabledUntil={provider.health?.disabled_until ?? undefined}
        />
      </td>
      <td className="px-4 py-3 align-middle whitespace-nowrap">
        <div className="flex items-center gap-2">
          <div
            className="flex items-center gap-1.5 px-2 py-1 rounded-md bg-bg-tertiary border border-border/50 text-[11px] font-medium text-text-secondary"
            title="Priority"
          >
            <span className="text-text-muted">Pri</span>
            <span className="text-text-primary">{provider.priority}</span>
          </div>
          <div
            className="flex items-center gap-1.5 px-2 py-1 rounded-md bg-bg-tertiary border border-border/50 text-[11px] font-medium text-text-secondary"
            title="Weight"
          >
            <span className="text-text-muted">Wt</span>
            <span className="text-text-primary">{provider.weight}</span>
          </div>
        </div>
      </td>
      <td className="px-4 py-3 align-middle text-right whitespace-nowrap">
        <ActionsCell
          provider={provider}
          status={status}
          onViewDetail={onViewDetail}
          onReset={onReset}
          onToggle={onToggle}
          onEdit={onEdit}
          onDelete={onDelete}
        />
      </td>
    </tr>
  );
}

export function ProvidersTableBody({
  loading,
  providers,
  filteredProviders,
  onToggle,
  onEdit,
  onDelete,
  onReset,
  onAddClick,
  onGroupClick,
  onViewDetail,
  getGroupName,
  getGroupEnabled,
}: ProvidersTableBodyProps) {
  if (loading && providers.length === 0) {
    return (
      <tr>
        <td colSpan={8} className="px-4 py-20">
          <div className="flex flex-col items-center justify-center text-text-muted">
            <RefreshCw className="w-8 h-8 animate-spin text-primary/60 mb-4" />
            <p className="text-sm font-medium">Loading providers...</p>
          </div>
        </td>
      </tr>
    );
  }

  if (filteredProviders.length === 0) {
    const noProvidersConfigured = providers.length === 0;
    return (
      <tr>
        <td colSpan={8} className="px-4 py-24">
          <div className="flex flex-col items-center text-center">
            <div className="w-20 h-20 bg-bg-secondary border border-border rounded-2xl flex items-center justify-center mb-5 shadow-sm">
              <ServerCrash className="w-10 h-10 text-text-muted" />
            </div>
            <h3 className="text-lg font-semibold text-text-primary mb-2">
              {noProvidersConfigured
                ? "No providers configured"
                : "No matching providers"}
            </h3>
            <p className="text-sm text-text-secondary mb-6 max-w-sm leading-relaxed">
              {noProvidersConfigured
                ? "Get started by adding your first AI provider to configure your proxy network."
                : "We couldn't find any providers matching your current filter criteria."}
            </p>
            {noProvidersConfigured && (
              <button
                onClick={onAddClick}
                className="btn btn-primary shadow-md"
              >
                <PlusCircle className="w-4 h-4" />
                <span>Add First Provider</span>
              </button>
            )}
          </div>
        </td>
      </tr>
    );
  }

  return (
    <>
      {filteredProviders.map((provider) => (
        <ProviderRow
          key={provider.id}
          provider={provider}
          onToggle={onToggle}
          onEdit={onEdit}
          onDelete={onDelete}
          onReset={onReset}
          onGroupClick={onGroupClick}
          onViewDetail={onViewDetail}
          getGroupName={getGroupName}
          getGroupEnabled={getGroupEnabled}
        />
      ))}
    </>
  );
}
