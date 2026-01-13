import { useEffect, useCallback } from "react";
import { Link } from "react-router-dom";
import type { Group, Provider } from "../api/types";
import {
  getProviderStatus,
  statusDotClass,
  statusBadgeClass,
  statusLabel,
} from "../pages/providers/types";
import { DetailSection, DetailRow } from "./DrawerSection";

interface GroupDetailDrawerProps {
  group: Group | null;
  onClose: () => void;
  onEdit: (group: Group) => void;
  onDelete: (group: Group) => void;
  onProviderClick?: (provider: Provider) => void;
}

// Close icon component
const CloseIcon = () => (
  <svg
    className="w-5 h-5"
    fill="none"
    stroke="currentColor"
    viewBox="0 0 24 24"
  >
    <path
      strokeLinecap="round"
      strokeLinejoin="round"
      strokeWidth={2}
      d="M6 18L18 6M6 6l12 12"
    />
  </svg>
);

// Strategy icons and descriptions
const STRATEGY_INFO: Record<
  string,
  { icon: string; label: string; description: string }
> = {
  priority: {
    icon: "🎯",
    label: "Priority",
    description:
      "Select providers in order of priority. Best for primary/backup setups.",
  },
  random: {
    icon: "🎲",
    label: "Random",
    description:
      "Randomly select from available providers. Good for basic load balancing.",
  },
  weight: {
    icon: "⚖️",
    label: "Weight",
    description:
      "Select based on configured weights. Fine-tune traffic distribution.",
  },
};

// Provider list item component
function ProviderListItem({
  provider,
  onClick,
}: {
  provider: Provider;
  onClick?: () => void;
}) {
  const status = getProviderStatus(
    provider.enabled,
    provider.health?.available,
    provider.health?.disabled_until,
  );

  return (
    <div
      className={`flex items-center justify-between py-2.5 px-3 rounded-lg bg-bg-tertiary/50 hover:bg-bg-tertiary transition-colors ${onClick ? "cursor-pointer" : ""}`}
      onClick={onClick}
    >
      <div className="flex items-center gap-3">
        <div className={`w-2 h-2 rounded-full ${statusDotClass[status]}`} />
        <div>
          <p className="text-sm font-medium text-text-primary">
            {provider.name}
          </p>
          <p className="text-xs text-text-muted truncate max-w-[180px]">
            {provider.base_url}
          </p>
        </div>
      </div>
      <div className="flex items-center gap-2">
        <span className="text-xs text-text-muted">
          P{provider.priority} / W{provider.weight}
        </span>
        <span
          className={`px-1.5 py-0.5 rounded text-xs font-medium ${statusBadgeClass[status]}`}
        >
          {statusLabel[status]}
        </span>
      </div>
    </div>
  );
}

// Health progress bar component
function HealthProgress({
  healthy,
  total,
}: {
  healthy: number;
  total: number;
}) {
  const percentage = total > 0 ? Math.round((healthy / total) * 100) : 0;

  const getBarColor = () => {
    if (percentage >= 80) return "bg-success";
    if (percentage >= 50) return "bg-warning";
    return "bg-danger";
  };

  return (
    <div className="space-y-1">
      <div className="flex items-center justify-between text-sm">
        <span className="text-text-secondary">Healthy Providers</span>
        <span className="font-medium text-text-primary">
          {healthy} / {total} ({percentage}%)
        </span>
      </div>
      <div className="h-2 bg-bg-tertiary rounded-full overflow-hidden">
        <div
          className={`h-full ${getBarColor()} transition-all duration-300`}
          style={{ width: `${percentage}%` }}
        />
      </div>
    </div>
  );
}

// Status badge component
function StatusBadge({ enabled }: { enabled: boolean }) {
  if (enabled) {
    return (
      <span className="px-2 py-0.5 rounded text-xs font-medium bg-success-light text-success-dark">
        Enabled
      </span>
    );
  }
  return (
    <span className="px-2 py-0.5 rounded text-xs font-medium bg-gray-100 text-gray-600">
      Disabled
    </span>
  );
}

// Empty providers state component
function EmptyProvidersState() {
  return (
    <div className="text-center py-8">
      <div className="w-12 h-12 mx-auto mb-3 bg-bg-tertiary rounded-full flex items-center justify-center">
        <span className="text-2xl">🔌</span>
      </div>
      <p className="text-sm text-text-muted mb-3">
        No providers in this group yet
      </p>
      <Link
        to="/providers"
        className="text-sm text-primary hover:text-primary-hover font-medium"
      >
        + Add Provider
      </Link>
    </div>
  );
}

export function GroupDetailDrawer({
  group,
  onClose,
  onEdit,
  onDelete,
  onProviderClick,
}: GroupDetailDrawerProps) {
  // Handle ESC key to close drawer
  const handleEscape = useCallback(
    (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    },
    [onClose],
  );

  useEffect(() => {
    if (group) {
      document.addEventListener("keydown", handleEscape);
      document.body.style.overflow = "hidden";
      return () => {
        document.removeEventListener("keydown", handleEscape);
        document.body.style.overflow = "";
      };
    }
  }, [group, handleEscape]);

  if (!group) return null;

  const providers = group.providers || [];
  const strategyInfo = STRATEGY_INFO[group.strategy] || STRATEGY_INFO.priority;

  // Calculate health stats
  const healthyCount = providers.filter((p) => {
    const status = getProviderStatus(
      p.enabled,
      p.health?.available,
      p.health?.disabled_until,
    );
    return status === "healthy";
  }).length;

  const renderProvidersList = () => {
    if (providers.length === 0) return <EmptyProvidersState />;
    return (
      <div className="space-y-1">
        {providers.map((provider) => (
          <ProviderListItem
            key={provider.id}
            provider={provider}
            onClick={
              onProviderClick ? () => onProviderClick(provider) : undefined
            }
          />
        ))}
      </div>
    );
  };

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
              <span className="text-xl">{strategyInfo.icon}</span>
              <h2 className="text-xl font-bold text-text-primary truncate">
                {group.name}
              </h2>
              <span
                className={`px-2 py-0.5 rounded text-xs font-medium ${group.enabled ? "bg-success-light text-success-dark" : "bg-gray-100 text-gray-600"}`}
              >
                {group.enabled ? "Active" : "Disabled"}
              </span>
            </div>
            <p className="text-sm text-text-muted font-mono">{group.id}</p>
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
          {/* Strategy Info Card */}
          <div className="p-4 rounded-xl bg-primary-light border border-primary/20">
            <div className="flex items-center gap-3 mb-2">
              <span className="text-2xl">{strategyInfo.icon}</span>
              <div>
                <h4 className="font-semibold text-text-primary">
                  {strategyInfo.label} Strategy
                </h4>
                <p className="text-sm text-text-secondary">
                  {strategyInfo.description}
                </p>
              </div>
            </div>
          </div>

          {/* Basic Info Section */}
          <DetailSection title="Configuration">
            <DetailRow label="Priority" value={group.priority} />
            <DetailRow label="Weight" value={group.weight} />
            <DetailRow
              label="Status"
              value={<StatusBadge enabled={group.enabled} />}
            />
          </DetailSection>

          {/* Health Overview */}
          {providers.length > 0 && (
            <DetailSection title="Health Overview">
              <HealthProgress healthy={healthyCount} total={providers.length} />
            </DetailSection>
          )}

          {/* Providers List */}
          <DetailSection
            title={`Providers (${providers.length})`}
            action={
              providers.length > 0 && (
                <Link
                  to={`/providers?group=${group.id}`}
                  className="text-xs text-primary hover:text-primary-hover font-medium"
                >
                  Manage →
                </Link>
              )
            }
          >
            {renderProvidersList()}
          </DetailSection>

          {/* Timestamps */}
          <div className="text-xs text-text-muted space-y-1 pt-4 border-t border-border-light">
            <p>Created: {new Date(group.created_at).toLocaleString()}</p>
            <p>Updated: {new Date(group.updated_at).toLocaleString()}</p>
          </div>
        </div>

        {/* Footer Actions */}
        <div className="p-4 border-t border-border-light bg-bg-secondary">
          <div className="flex items-center justify-between gap-3">
            <button
              onClick={() => onDelete(group)}
              className="btn btn-ghost text-danger hover:bg-danger-light"
              title="Delete Group"
            >
              🗑️ Delete
            </button>
            <button
              onClick={() => onEdit(group)}
              className="btn btn-primary btn-sm"
            >
              ✏️ Edit Group
            </button>
          </div>
        </div>
      </div>
    </>
  );
}
