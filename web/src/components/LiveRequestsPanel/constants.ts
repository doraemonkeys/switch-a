import type { GroupViewMode, SortField } from "./types";

// =============================================================================
// Constants
// =============================================================================

export const INITIAL_VISIBLE_COUNT = 10;
export const LOAD_MORE_INCREMENT = 20;

// Timing intervals (milliseconds)
export const CURRENT_TIME_REFRESH_INTERVAL_MS = 1000;

// Layout dimensions
export const GROUP_LABEL_MAX_WIDTH = "200px";
export const COMPACT_MODEL_MAX_WIDTH = "180px";
export const COMPACT_PROVIDER_MAX_WIDTH = "140px";
export const COMPACT_IP_MAX_WIDTH = "100px";
export const COMPACT_USER_MAX_WIDTH = "100px";
export const SSE_BADGE_FONT_SIZE = "10px";

// Shared badge color classes for protocol indicators (SSE, WS)
export const SSE_BADGE_COLORS =
  "bg-purple-100 text-purple-800 dark:bg-purple-900/30 dark:text-purple-300";
export const WS_BADGE_COLORS =
  "bg-emerald-100 text-emerald-800 dark:bg-emerald-900/30 dark:text-emerald-300";

export const VIEW_MODE_LABELS: Record<
  GroupViewMode,
  { icon: string; label: string }
> = {
  ip: { icon: "🌐", label: "By IP" },
  api: { icon: "📡", label: "By API" },
  model: { icon: "🤖", label: "By Model" },
  all: { icon: "📋", label: "All List" },
};

export const SORT_OPTIONS: { field: SortField; label: string }[] = [
  { field: "duration", label: "Duration" },
  { field: "started", label: "Started" },
  { field: "model", label: "Model" },
];
