import type { ActiveRequest } from "../../api/types";
import type {
  GroupViewMode,
  RequestGroup,
  SortField,
  SortOrder,
} from "./types";

// =============================================================================
// Utility Functions
// =============================================================================

/**
 * Format duration in milliseconds to human-readable string.
 */
export function formatDuration(ms: number): string {
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

/**
 * Get duration of a request from start time to now
 */
export function getRequestDuration(
  request: ActiveRequest,
  currentTime: number,
): number {
  return currentTime - new Date(request.started_at).getTime();
}

/**
 * Group requests by the specified mode
 */
export function groupRequests(
  requests: ActiveRequest[],
  mode: GroupViewMode,
  currentTime: number,
  longRunningThreshold: number,
): RequestGroup[] {
  if (mode === "all") {
    return [];
  }

  const groups = new Map<string, ActiveRequest[]>();

  for (const req of requests) {
    let key: string;
    switch (mode) {
      case "ip":
        key = req.client_ip || "Unknown IP";
        break;
      case "api":
        key = req.api_type || "Unknown API";
        break;
      case "model":
        key = req.model || "Unknown Model";
        break;
      default:
        key = "all";
    }

    const existing = groups.get(key) || [];
    existing.push(req);
    groups.set(key, existing);
  }

  const result: RequestGroup[] = [];
  for (const [key, reqs] of groups) {
    const durations = reqs.map((r) => getRequestDuration(r, currentTime));
    const longestDuration = Math.max(...durations);
    const hasLongRunning = durations.some(
      (d, i) => d > longRunningThreshold && !reqs[i].is_websocket,
    );

    result.push({
      key,
      label: key,
      requests: reqs,
      longestDuration,
      hasLongRunning,
    });
  }

  // Sort groups: has long-running first, then by request count, then by longest duration
  result.sort((a, b) => {
    if (a.hasLongRunning !== b.hasLongRunning) {
      return a.hasLongRunning ? -1 : 1;
    }
    if (a.requests.length !== b.requests.length) {
      return b.requests.length - a.requests.length;
    }
    return b.longestDuration - a.longestDuration;
  });

  return result;
}

/**
 * Sort requests within a group
 */
export function sortRequests(
  requests: ActiveRequest[],
  sortField: SortField,
  sortOrder: SortOrder,
  currentTime: number,
): ActiveRequest[] {
  const sorted = [...requests];

  sorted.sort((a, b) => {
    let comparison = 0;

    switch (sortField) {
      case "duration": {
        const durationA = getRequestDuration(a, currentTime);
        const durationB = getRequestDuration(b, currentTime);
        comparison = durationB - durationA; // Default: longest first
        break;
      }
      case "started": {
        const startA = new Date(a.started_at).getTime();
        const startB = new Date(b.started_at).getTime();
        comparison = startB - startA; // Default: newest first
        break;
      }
      case "model": {
        const modelA = a.model || "";
        const modelB = b.model || "";
        comparison = modelA.localeCompare(modelB);
        break;
      }
    }

    return sortOrder === "asc" ? -comparison : comparison;
  });

  return sorted;
}

/**
 * Filter requests by search query
 */
export function filterRequests(
  requests: ActiveRequest[],
  searchQuery: string,
): ActiveRequest[] {
  if (!searchQuery.trim()) {
    return requests;
  }

  const query = searchQuery.toLowerCase();
  return requests.filter(
    (req) =>
      req.model?.toLowerCase().includes(query) ||
      req.client_ip?.toLowerCase().includes(query) ||
      req.api_type?.toLowerCase().includes(query) ||
      req.user_id?.toLowerCase().includes(query) ||
      req.provider_id?.toLowerCase().includes(query),
  );
}

const BYTE_UNITS = ["B", "KB", "MB", "GB"] as const;

/**
 * Format byte count to human-readable string (e.g., "1.2 MB").
 */
export function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";
  let unitIndex = 0;
  let value = bytes;
  while (value >= 1024 && unitIndex < BYTE_UNITS.length - 1) {
    value /= 1024;
    unitIndex++;
  }
  const formatted = unitIndex === 0 ? value.toString() : value.toFixed(1);
  return `${formatted} ${BYTE_UNITS[unitIndex]}`;
}

/**
 * Format idle duration from last activity timestamp.
 * Returns "idle Xs" / "idle Xm" / "idle Xh", or empty string if no activity yet.
 */
export function formatIdleDuration(
  lastActivityMs: number,
  currentTime: number,
): string {
  if (!lastActivityMs) return "";
  const idleMs = currentTime - lastActivityMs;
  if (idleMs < 0) return "";
  const seconds = Math.floor(idleMs / 1000);
  if (seconds < 60) return `idle ${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `idle ${minutes}m`;
  const hours = Math.floor(minutes / 60);
  return `idle ${hours}h`;
}
