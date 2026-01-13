import { useEffect, useState, useRef } from "react";
import type { Provider } from "../../api/client";
import { stringToColor } from "../../lib/utils";
import {
  getProviderStatus,
  statusDotClass,
  statusBadgeClass,
  statusLabel,
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
  getGroupName: (groupId: string | null) => string;
}

function RecoveryTimer({ disabledUntil }: { disabledUntil: string }) {
  const [timeLeft, setTimeLeft] = useState<string>("");
  const timerRef = useRef<ReturnType<typeof setInterval> | null>(null);

  useEffect(() => {
    const calculateTimeLeft = () => {
      const now = new Date().getTime();
      const until = new Date(disabledUntil).getTime();
      const diff = until - now;

      if (diff <= 0) {
        setTimeLeft("");
        // Stop the interval once time has expired
        if (timerRef.current) {
          clearInterval(timerRef.current);
          timerRef.current = null;
        }
        return;
      }

      const minutes = Math.floor((diff % (1000 * 60 * 60)) / (1000 * 60));
      const seconds = Math.floor((diff % (1000 * 60)) / 1000);
      setTimeLeft(`${minutes}m ${seconds}s`);
    };

    calculateTimeLeft();
    timerRef.current = setInterval(calculateTimeLeft, 1000);

    return () => {
      if (timerRef.current) {
        clearInterval(timerRef.current);
        timerRef.current = null;
      }
    };
  }, [disabledUntil]);

  if (!timeLeft) return <span className="text-sm text-text-muted">—</span>;

  return (
    <span className="text-xs font-mono bg-warning-light text-warning-dark px-2 py-0.5 rounded">
      ⏱️ {timeLeft}
    </span>
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
  getGroupName,
}: ProvidersTableBodyProps) {
  if (loading && providers.length === 0) {
    return (
      <tr>
        <td colSpan={8} className="px-4 py-16">
          <div className="empty-state">
            <div className="w-8 h-8 mx-auto mb-4 border-2 border-primary border-t-transparent rounded-full animate-spin" />
            <p className="text-sm text-text-muted">Loading providers...</p>
          </div>
        </td>
      </tr>
    );
  }

  if (filteredProviders.length === 0) {
    const noProvidersConfigured = providers.length === 0;
    return (
      <tr>
        <td colSpan={8} className="px-4 py-16">
          <div className="empty-state">
            <div className="w-20 h-20 mx-auto mb-4 bg-bg-tertiary rounded-2xl flex items-center justify-center">
              <span className="text-4xl">🔌</span>
            </div>
            <p className="font-medium text-text-primary mb-1">
              {noProvidersConfigured
                ? "No providers configured yet"
                : "No providers match your filters"}
            </p>
            <p className="text-sm text-text-muted mb-4">
              {noProvidersConfigured
                ? "Add your first AI provider to start proxying requests."
                : "Try adjusting your search or filter criteria."}
            </p>
            {noProvidersConfigured && (
              <button onClick={onAddClick} className="btn btn-primary">
                <span>➕</span>
                Add Provider
              </button>
            )}
          </div>
        </td>
      </tr>
    );
  }

  return (
    <>
      {filteredProviders.map((provider) => {
        const status = getProviderStatus(
          provider.enabled,
          provider.health?.available,
          provider.health?.disabled_until,
        );

        const failCount = provider.health?.fail_count || 0;
        const disabledUntil = provider.health?.disabled_until;
        const disabledReason = provider.health?.disabled_reason;
        const lastError = provider.health?.last_error;
        const groupName = getGroupName(provider.group_id);
        const groupColors = provider.group_id
          ? stringToColor(provider.group_id)
          : undefined;

        return (
          <tr
            key={provider.id}
            className="hover:bg-bg-secondary/50 transition-colors"
          >
            <td className="table-cell">
              <div className="flex items-center gap-3">
                <div
                  className={`w-2 h-2 rounded-full ${statusDotClass[status]}`}
                />
                <div>
                  <p className="font-medium text-text-primary">
                    {provider.name}
                  </p>
                  <p className="text-xs text-text-muted truncate max-w-[200px]">
                    {provider.base_url}
                  </p>
                </div>
              </div>
            </td>
            <td className="table-cell">
              {provider.group_id && groupColors ? (
                <span
                  className="px-2 py-1 rounded-md text-xs font-medium border whitespace-nowrap truncate max-w-[120px] inline-block cursor-pointer hover:opacity-80 transition-opacity"
                  style={{
                    backgroundColor: groupColors.bg,
                    color: groupColors.text,
                    borderColor: groupColors.border,
                  }}
                  title={`Filter by group: ${groupName}`}
                  onClick={() => onGroupClick?.(provider.group_id!)}
                >
                  {groupName}
                </span>
              ) : (
                <span className="text-text-muted text-sm">—</span>
              )}
            </td>
            <td className="table-cell">
              <div className="flex flex-wrap gap-1">
                {provider.api_types?.map((apiType) => (
                  <span
                    key={apiType.api_type}
                    className="px-1.5 py-0.5 text-xs rounded bg-primary-light text-primary-dark"
                  >
                    {apiType.api_type}
                  </span>
                )) ?? <span className="text-text-muted text-sm">—</span>}
              </div>
            </td>
            <td className="table-cell">
              <div className="group relative flex items-center gap-1">
                <span
                  className={`px-2 py-1 rounded text-xs font-medium ${statusBadgeClass[status]}`}
                >
                  {statusLabel[status]}
                </span>
                {(status === "unhealthy" || status === "pending-recovery") &&
                  disabledReason && (
                    <div className="relative group/tooltip">
                      <span className="cursor-help text-text-muted">ℹ️</span>
                      <div className="absolute left-1/2 -translate-x-1/2 bottom-full mb-2 w-48 p-2 bg-gray-800 text-white text-xs rounded shadow-lg opacity-0 invisible group-hover/tooltip:opacity-100 group-hover/tooltip:visible transition-all z-10 pointer-events-none">
                        {disabledReason}
                        {lastError && (
                          <>
                            <hr className="my-1 border-gray-600" />
                            <span className="opacity-75">
                              Last error: {lastError}
                            </span>
                          </>
                        )}
                      </div>
                    </div>
                  )}
              </div>
            </td>
            <td className="table-cell text-center">
              {failCount > 0 ? (
                <span
                  className="inline-flex items-center justify-center min-w-[20px] h-5 px-1.5 text-xs font-medium text-white bg-danger rounded-full"
                  title="Consecutive failures"
                >
                  {failCount}
                </span>
              ) : (
                <span className="text-text-muted text-sm">—</span>
              )}
            </td>
            <td className="table-cell">
              {disabledUntil ? (
                <RecoveryTimer disabledUntil={disabledUntil} />
              ) : (
                <span className="text-text-muted text-sm">—</span>
              )}
            </td>
            <td className="table-cell">
              <span className="text-sm text-text-secondary">
                P{provider.priority} / W{provider.weight}
              </span>
            </td>
            <td className="table-cell text-right">
              <div className="flex items-center justify-end gap-1">
                {status === "unhealthy" && (
                  <button
                    onClick={() => onReset(provider)}
                    className="btn btn-ghost btn-sm text-warning hover:bg-warning-light"
                    title="Reset Circuit Breaker"
                  >
                    🔄
                  </button>
                )}
                <button
                  onClick={() => onToggle(provider)}
                  className="btn btn-ghost btn-sm"
                  title={provider.enabled ? "Disable" : "Enable"}
                >
                  {provider.enabled ? "⏸️" : "▶️"}
                </button>
                <button
                  onClick={() => onEdit(provider)}
                  className="btn btn-ghost btn-sm"
                  title="Edit"
                >
                  ✏️
                </button>
                <button
                  onClick={() => onDelete(provider)}
                  className="btn btn-ghost btn-sm text-danger hover:bg-danger-light"
                  title="Delete"
                >
                  🗑️
                </button>
              </div>
            </td>
          </tr>
        );
      })}
    </>
  );
}
