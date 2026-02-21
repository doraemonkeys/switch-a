import { useMemo, useEffect, useRef } from "react";
import { LiveRequestsPanel } from "../components/LiveRequestsPanel";
import { useLiveRequests, useStatus, useProviders } from "../hooks";
import { useLocalStorage } from "../hooks/useLocalStorage";

/** Threshold in ms to highlight long-running requests */
const LONG_RUNNING_REQUEST_THRESHOLD_MS = 300000;

/** Poll interval options in milliseconds */
const POLL_INTERVALS = [
  { value: 0, label: "Paused" },
  { value: 2000, label: "2s" },
  { value: 5000, label: "5s" },
  { value: 10000, label: "10s" },
  { value: 30000, label: "30s" },
] as const;

/** Calculate human-readable time until a future ISO date */
function formatTimeUntil(isoDate: string): string {
  const target = new Date(isoDate).getTime();
  const now = Date.now();
  const diffMs = target - now;

  if (diffMs <= 0) return "recovering...";

  const seconds = Math.floor(diffMs / 1000);
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.floor(minutes / 60);
  return `${hours}h ${minutes % 60}m`;
}

/** Format disabled reason for display (removes "auto: " prefix for readability) */
function formatDisabledReason(reason: string): string {
  if (reason.startsWith("auto: ")) {
    return reason.slice(6);
  }
  if (reason.startsWith("manual: ")) {
    return `Manual: ${reason.slice(8)}`;
  }
  return reason;
}

/** Build a map of provider ID to name for display */
function buildProviderNameMap(
  providers: Array<{ id: string; name: string }> | undefined,
): Map<string, string> {
  const map = new Map<string, string>();
  if (providers) {
    for (const p of providers) {
      map.set(p.id, p.name);
    }
  }
  return map;
}

interface StatusDotProps {
  enabled: boolean;
  available: boolean;
  disabledReason?: string | null;
}

function StatusDot({ enabled, available, disabledReason }: StatusDotProps) {
  if (!enabled) {
    return (
      <span
        className="w-2.5 h-2.5 rounded-full bg-gray-400 flex-shrink-0"
        title="Disabled by configuration"
        role="img"
        aria-label="Disabled"
      />
    );
  }
  if (!available) {
    const title = disabledReason
      ? `Unhealthy: ${disabledReason.replace(/^(auto|manual): /, "")}`
      : "Unhealthy";
    return (
      <span
        className="w-2.5 h-2.5 rounded-full bg-red-500 flex-shrink-0"
        title={title}
        role="img"
        aria-label={title}
      />
    );
  }
  return (
    <span
      className="w-2.5 h-2.5 rounded-full bg-green-500 flex-shrink-0"
      title="Healthy"
      role="img"
      aria-label="Healthy"
    />
  );
}

export function Monitor() {
  const [pollInterval, setPollInterval] = useLocalStorage(
    "monitor:pollInterval",
    5000,
  );

  const {
    requests,
    count,
    loading: requestsLoading,
    error: requestsError,
    refetch: refetchRequests,
    isPolling,
  } = useLiveRequests({ pollInterval, enabled: pollInterval > 0 });

  const {
    status,
    loading: statusLoading,
    error: statusError,
    refetch: refetchStatus,
  } = useStatus();

  const { providers } = useProviders();

  // Track if tab is visible for polling optimization
  const isVisibleRef = useRef(true);

  // Auto-refresh status when polling is enabled
  useEffect(() => {
    if (pollInterval <= 0) return;

    const handleVisibilityChange = () => {
      isVisibleRef.current = document.visibilityState === "visible";
      // Immediately refetch when becoming visible
      if (isVisibleRef.current) {
        refetchStatus();
      }
    };

    document.addEventListener("visibilitychange", handleVisibilityChange);
    isVisibleRef.current = document.visibilityState === "visible";

    const intervalId = setInterval(() => {
      // Only poll if tab is visible
      if (isVisibleRef.current) {
        refetchStatus();
      }
    }, pollInterval);

    return () => {
      document.removeEventListener("visibilitychange", handleVisibilityChange);
      clearInterval(intervalId);
    };
  }, [pollInterval, refetchStatus]);

  const providerNames = buildProviderNameMap(providers);

  const handleRefresh = () => {
    refetchRequests();
    refetchStatus();
  };

  // Calculate provider stats
  const providerStats = (() => {
    if (!status) return { healthy: 0, unhealthy: 0, total: 0 };
    const total = status.providers.length;
    const healthy = status.providers.filter(
      (p) => p.enabled && p.health?.available !== false,
    ).length;
    return {
      total,
      healthy,
      unhealthy: total - healthy,
    };
  })();

  // Memoize long-running count to avoid calling Date.now() on every render
  const longRunningCount = useMemo(() => {
    // eslint-disable-next-line react-hooks/purity -- Date.now() is intentionally used once per requests update
    const now = Date.now();
    return requests.filter((r) => {
      const duration = now - new Date(r.started_at).getTime();
      return duration > LONG_RUNNING_REQUEST_THRESHOLD_MS;
    }).length;
  }, [requests]);

  return (
    <div className="space-y-6">
      {/* Page Header */}
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-2xl font-bold text-text-primary">Monitor</h2>
          <p className="text-text-secondary mt-1">
            Live request monitoring and system status
          </p>
        </div>
        <div className="flex items-center gap-3">
          {/* Poll Interval Selector */}
          <div className="flex items-center gap-2">
            <select
              value={pollInterval}
              onChange={(e) => setPollInterval(Number(e.target.value))}
              className="input py-1.5 text-sm w-24"
              aria-label="Refresh interval"
            >
              {POLL_INTERVALS.map((opt) => (
                <option key={opt.value} value={opt.value}>
                  {opt.label}
                </option>
              ))}
            </select>
          </div>

          {isPolling && pollInterval > 0 && (
            <span
              className="flex items-center gap-2 text-sm text-text-secondary"
              role="status"
              aria-live="polite"
            >
              <span className="relative flex h-2 w-2">
                <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-green-400 opacity-75" />
                <span className="relative inline-flex rounded-full h-2 w-2 bg-green-500" />
              </span>
              Auto-refreshing
            </span>
          )}
          <button
            onClick={handleRefresh}
            disabled={requestsLoading || statusLoading}
            className="btn btn-secondary btn-sm"
          >
            <span className={requestsLoading ? "animate-spin" : ""}>🔄</span>
            Refresh
          </button>
        </div>
      </div>

      {/* Stats Summary */}
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
        <div className="card bg-gradient-to-br from-blue-500/10 to-blue-600/5">
          <div className="flex items-center gap-4">
            <div className="text-4xl">🔄</div>
            <div>
              <p className="text-3xl font-bold text-text-primary">{count}</p>
              <p className="text-sm text-text-secondary">Active Requests</p>
            </div>
          </div>
        </div>
        <div className="card bg-gradient-to-br from-amber-500/10 to-amber-600/5">
          <div className="flex items-center gap-4">
            <div className="text-4xl">⚠️</div>
            <div>
              <p className="text-3xl font-bold text-text-primary">
                {longRunningCount}
              </p>
              <p className="text-sm text-text-secondary">
                Long Running (&gt;{LONG_RUNNING_REQUEST_THRESHOLD_MS / 1000}s)
              </p>
            </div>
          </div>
        </div>
        <div className="card bg-gradient-to-br from-green-500/10 to-green-600/5">
          <div className="flex items-center gap-4">
            <div className="text-4xl">✅</div>
            <div>
              <p className="text-3xl font-bold text-text-primary">
                {providerStats.healthy}
              </p>
              <p className="text-sm text-text-secondary">Healthy Providers</p>
            </div>
          </div>
        </div>
        <div className="card bg-gradient-to-br from-red-500/10 to-red-600/5">
          <div className="flex items-center gap-4">
            <div className="text-4xl">🔴</div>
            <div>
              <p className="text-3xl font-bold text-text-primary">
                {providerStats.unhealthy}
              </p>
              <p className="text-sm text-text-secondary">Unhealthy Providers</p>
            </div>
          </div>
        </div>
      </div>

      {/* Main Content */}
      <div className="grid grid-cols-1 lg:grid-cols-3 gap-6">
        {/* Live Requests Panel */}
        <div className="lg:col-span-2">
          <div className="card">
            <LiveRequestsPanel
              requests={requests}
              loading={requestsLoading}
              error={requestsError}
              providerNames={providerNames}
              longRunningThreshold={LONG_RUNNING_REQUEST_THRESHOLD_MS}
            />
          </div>
        </div>

        {/* Provider Status */}
        <div className="card">
          <h3 className="text-lg font-semibold text-text-primary mb-4 pb-4 border-b border-border-light">
            Provider Status
          </h3>

          {statusError && (
            <div className="p-4 text-center text-red-500 dark:text-red-400">
              Failed to load status: {statusError.message}
            </div>
          )}

          {statusLoading && !status && (
            <div className="p-4 text-center">
              <div
                className="inline-block animate-spin rounded-full h-6 w-6 border-b-2 border-accent-primary"
                role="status"
                aria-label="Loading provider status"
              />
            </div>
          )}

          {status && (
            <div className="space-y-2 max-h-96 overflow-y-auto">
              {status.providers.length === 0 ? (
                <p className="text-center text-text-secondary py-4">
                  No providers configured
                </p>
              ) : (
                status.providers.map((provider) => (
                  <div
                    key={provider.id}
                    className="flex items-center justify-between p-3 rounded-lg bg-bg-tertiary hover:bg-bg-hover transition-colors"
                  >
                    <div className="flex items-center gap-3 min-w-0 flex-1">
                      <StatusDot
                        enabled={provider.enabled}
                        available={provider.health?.available ?? true}
                        disabledReason={provider.health?.disabled_reason}
                      />
                      <div className="flex flex-col min-w-0">
                        <span className="truncate text-sm font-medium text-text-primary">
                          {provider.name}
                        </span>
                        {!provider.health?.available &&
                          provider.health?.disabled_reason && (
                            <span className="text-xs text-amber-600 dark:text-amber-400 truncate">
                              {formatDisabledReason(
                                provider.health.disabled_reason,
                              )}
                              {provider.health.disabled_until && (
                                <>
                                  {" "}
                                  · Recovers in{" "}
                                  {formatTimeUntil(
                                    provider.health.disabled_until,
                                  )}
                                </>
                              )}
                            </span>
                          )}
                      </div>
                    </div>
                    {provider.current_requests > 0 && (
                      <span className="badge badge-primary text-xs flex-shrink-0">
                        {provider.current_requests} req
                      </span>
                    )}
                  </div>
                ))
              )}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
