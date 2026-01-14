import { useState, useEffect } from "react";
import type { ActiveRequest } from "../api/types";

interface LiveRequestsPanelProps {
  requests: ActiveRequest[];
  loading: boolean;
  error: Error | null;
  /** Threshold in ms to highlight long-running requests (default: 30000 = 30s) */
  longRunningThreshold?: number;
  /** Provider name map for display */
  providerNames?: Map<string, string>;
}

/**
 * Panel component for displaying live/active requests.
 *
 * Features:
 * - Real-time duration updates
 * - Highlights long-running requests
 * - Pulse animation for active requests
 * - SSE badge indicator
 */
export function LiveRequestsPanel({
  requests,
  loading,
  error,
  longRunningThreshold = 30000,
  providerNames,
}: LiveRequestsPanelProps) {
  // Current timestamp for duration calculation, updated every second
  const [currentTime, setCurrentTime] = useState(() => Date.now());
  useEffect(() => {
    const interval = setInterval(() => setCurrentTime(Date.now()), 1000);
    return () => clearInterval(interval);
  }, []);

  if (error) {
    return (
      <div className="p-4 text-center text-red-500 dark:text-red-400">
        Failed to load active requests: {error.message}
      </div>
    );
  }

  if (loading && requests.length === 0) {
    return (
      <div className="p-8 text-center">
        <div className="inline-block animate-spin rounded-full h-8 w-8 border-b-2 border-accent-primary" />
        <p className="mt-2 text-text-secondary">Loading active requests...</p>
      </div>
    );
  }

  if (requests.length === 0) {
    return (
      <div className="p-8 text-center text-text-secondary">
        <svg
          className="mx-auto h-12 w-12 text-text-muted mb-4"
          fill="none"
          stroke="currentColor"
          viewBox="0 0 24 24"
          aria-hidden="true"
        >
          <path
            strokeLinecap="round"
            strokeLinejoin="round"
            strokeWidth={1.5}
            d="M20 7l-8-4-8 4m16 0l-8 4m8-4v10l-8 4m0-10L4 7m8 4v10M4 7v10l8 4"
          />
        </svg>
        <p className="text-lg font-medium text-text-primary">
          No active requests
        </p>
        <p className="mt-1 text-sm">
          Requests will appear here when they are in progress
        </p>
      </div>
    );
  }

  // Sort by started_at (oldest first to highlight long-running)
  const sortedRequests = [...requests].sort(
    (a, b) =>
      new Date(a.started_at).getTime() - new Date(b.started_at).getTime(),
  );

  return (
    <div className="divide-y divide-border-light">
      {sortedRequests.map((req) => (
        <RequestRow
          key={req.request_id}
          request={req}
          longRunningThreshold={longRunningThreshold}
          providerName={providerNames?.get(req.provider_id)}
          currentTime={currentTime}
        />
      ))}
    </div>
  );
}

interface RequestRowProps {
  request: ActiveRequest;
  longRunningThreshold: number;
  providerName?: string;
  currentTime: number;
}

function RequestRow({
  request,
  longRunningThreshold,
  providerName,
  currentTime,
}: RequestRowProps) {
  const startTime = new Date(request.started_at).getTime();
  const durationMs = currentTime - startTime;
  const isLongRunning = durationMs > longRunningThreshold;

  const durationStr = formatDuration(durationMs);

  return (
    <div
      role="article"
      aria-label={`Active request for model ${request.model || "unknown"}`}
      tabIndex={0}
      className={`p-4 hover:bg-bg-hover transition-colors ${
        isLongRunning ? "bg-amber-50/50 dark:bg-amber-900/10" : ""
      }`}
    >
      <div className="flex items-start justify-between gap-4">
        <div className="flex items-center gap-3 min-w-0 flex-1">
          {/* Pulse indicator */}
          <div className="flex-shrink-0">
            <span className="relative flex h-3 w-3">
              <span
                className={`animate-ping absolute inline-flex h-full w-full rounded-full opacity-75 ${
                  isLongRunning ? "bg-amber-400" : "bg-green-400"
                }`}
              />
              <span
                className={`relative inline-flex rounded-full h-3 w-3 ${
                  isLongRunning ? "bg-amber-500" : "bg-green-500"
                }`}
              />
            </span>
          </div>

          <div className="min-w-0 flex-1">
            {/* First row: Model and badges */}
            <div className="flex items-center gap-2 flex-wrap">
              <span className="font-medium text-text-primary truncate">
                {request.model || "Unknown model"}
              </span>
              <span className="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-blue-100 text-blue-800 dark:bg-blue-900/30 dark:text-blue-300 uppercase">
                {request.api_type}
              </span>
              {request.is_sse && (
                <span className="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-purple-100 text-purple-800 dark:bg-purple-900/30 dark:text-purple-300">
                  SSE
                </span>
              )}
            </div>

            {/* Second row: Provider and details */}
            <div className="flex items-center gap-3 mt-1 text-sm text-text-secondary">
              <span className="truncate">
                {providerName || request.provider_id}
              </span>
              {request.user_id && (
                <>
                  <span className="text-text-muted">•</span>
                  <span className="truncate font-mono text-xs">
                    {request.user_id}
                  </span>
                </>
              )}
              <span className="text-text-muted">•</span>
              <span className="font-mono text-xs">{request.client_ip}</span>
            </div>
          </div>
        </div>

        {/* Duration */}
        <div className="flex-shrink-0 text-right">
          <span
            className={`font-mono text-lg font-semibold ${
              isLongRunning
                ? "text-amber-600 dark:text-amber-400"
                : "text-text-primary"
            }`}
          >
            {durationStr}
          </span>
          {isLongRunning && (
            <p className="text-xs text-amber-600 dark:text-amber-400 mt-0.5">
              Long running
            </p>
          )}
        </div>
      </div>
    </div>
  );
}

/**
 * Format duration in milliseconds to human-readable string.
 * Shows seconds for < 60s, minutes:seconds for < 1h, hours:minutes for longer.
 */
function formatDuration(ms: number): string {
  const seconds = Math.floor(ms / 1000);
  const minutes = Math.floor(seconds / 60);
  const hours = Math.floor(minutes / 60);

  if (hours > 0) {
    return `${hours}h ${minutes % 60}m`;
  }
  if (minutes > 0) {
    return `${minutes}m ${seconds % 60}s`;
  }
  return `${seconds}s`;
}
