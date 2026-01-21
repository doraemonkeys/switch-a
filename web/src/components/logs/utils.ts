import {
  SUCCESS_RATE_THRESHOLDS,
  ERROR_COUNT_THRESHOLDS,
} from "../../config/constants";
import { CLAUDE_CACHE_BILLING } from "./constants";

// Type for stat card variants
export type StatVariantValue = "success" | "warning" | "danger";
export type StatVariant = StatVariantValue | undefined;

// =============================================================================
// Token Formatting Utilities
// =============================================================================

/**
 * Format token count to human-readable string (compact notation)
 * - >999 displays as 1.2k
 * - >99999 displays as 100k
 */
export function formatTokenCount(count: number): string {
  if (count >= 100000) {
    return `${Math.round(count / 1000)}k`;
  }
  if (count >= 1000) {
    return `${(count / 1000).toFixed(1)}k`;
  }
  return count.toString();
}

/**
 * Format token count with locale formatting (for detail views)
 */
export function formatTokenLocale(count: number): string {
  return count.toLocaleString();
}

/**
 * Calculate cache hit rate percentage
 * @returns Cache hit rate as percentage (0-100), or null if not calculable
 */
export function calculateCacheHitRate(
  cacheReadTokens: number | null | undefined,
  promptTokens: number | null | undefined,
): number | null {
  if (
    promptTokens == null ||
    promptTokens === 0 ||
    cacheReadTokens == null ||
    cacheReadTokens === 0
  ) {
    return null;
  }
  return Math.round((cacheReadTokens / promptTokens) * 100);
}

/**
 * Calculate effective billable input tokens for Claude cache
 * Formula: uncached + cache_read×READ_RATE + cache_creation×WRITE_RATE
 * Where: uncached = prompt_tokens - cache_read
 *
 * @see CLAUDE_CACHE_BILLING for billing rates
 */
export function calculateEffectiveCost(
  promptTokens: number,
  cacheReadTokens: number,
  cacheCreationTokens: number,
): { billable: number; uncached: number } {
  const uncached = promptTokens - cacheReadTokens;
  const billable = Math.round(
    uncached +
      cacheReadTokens * CLAUDE_CACHE_BILLING.READ_RATE +
      cacheCreationTokens * CLAUDE_CACHE_BILLING.WRITE_RATE,
  );
  return { billable, uncached };
}

// Helper to determine aria-sort value for sortable table headers
export type AriaSortValue = "ascending" | "descending" | "none";

// Helper functions for determining stat variants
export function getSuccessRateVariant(rate: number | undefined): StatVariant {
  if (rate === undefined) return undefined;
  if (rate >= SUCCESS_RATE_THRESHOLDS.SUCCESS) return "success";
  if (rate >= SUCCESS_RATE_THRESHOLDS.WARNING) return "warning";
  return "danger";
}

export function getErrorCountVariant(count: number | undefined): StatVariant {
  if (count === undefined) return undefined;
  if (count === 0) return "success";
  if (count < ERROR_COUNT_THRESHOLDS.WARNING_MAX) return "warning";
  return "danger";
}

export function getStatVariantClass(variant: StatVariant): string {
  if (variant === "success") return "text-success";
  if (variant === "warning") return "text-warning";
  if (variant === "danger") return "text-danger";
  return "text-text-primary";
}

export function getAriaSortValue(
  field: string,
  currentSortBy: string,
  currentSortOrder: string,
): AriaSortValue {
  if (currentSortBy !== field) return "none";
  return currentSortOrder === "asc" ? "ascending" : "descending";
}
