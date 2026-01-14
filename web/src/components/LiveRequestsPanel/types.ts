import type { ActiveRequest } from "../../api/types";

// =============================================================================
// Types
// =============================================================================

export type GroupViewMode = "ip" | "api" | "model" | "all";
export type SortField = "duration" | "started" | "model";
export type SortOrder = "asc" | "desc";

export interface RequestGroup {
  key: string;
  label: string;
  requests: ActiveRequest[];
  longestDuration: number;
  hasLongRunning: boolean;
}

export interface LiveRequestsPanelProps {
  requests: ActiveRequest[];
  loading: boolean;
  error: Error | null;
  /** Threshold in ms to highlight long-running requests (default: 300000 = 5min) */
  longRunningThreshold?: number;
  /** Provider name map for display */
  providerNames?: Map<string, string>;
}
