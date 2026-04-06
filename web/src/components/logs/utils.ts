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
 *
 * In Claude API, prompt_tokens does NOT include cache tokens.
 * Total input = prompt_tokens + cache_read + cache_creation
 * Cache hit rate = cache_read / total_input * 100
 *
 * @returns Cache hit rate as percentage (0-100), or null if not calculable
 */
export function calculateCacheHitRate(
  cacheReadTokens: number | null | undefined,
  promptTokens: number | null | undefined,
  cacheCreationTokens?: number | null | undefined,
): number | null {
  if (cacheReadTokens == null || cacheReadTokens === 0) {
    return null;
  }

  // Total input = new tokens + cache read + cache creation
  const newTokens = promptTokens ?? 0;
  const cacheCreation = cacheCreationTokens ?? 0;
  const totalInput = newTokens + cacheReadTokens + cacheCreation;

  if (totalInput === 0) {
    return null;
  }

  return Math.round((cacheReadTokens / totalInput) * 100);
}

/**
 * Calculate effective billable input tokens for Claude cache
 * Formula: uncached + cache_read×READ_RATE + cache_creation×WRITE_RATE
 *
 * In Claude API, prompt_tokens does NOT include cache tokens:
 * - prompt_tokens = new (uncached) tokens only
 * - cache_read_input_tokens = tokens read from cache
 * - cache_creation_input_tokens = tokens written to cache
 *
 * @see CLAUDE_CACHE_BILLING for billing rates
 */
export function calculateEffectiveCost(
  promptTokens: number,
  cacheReadTokens: number,
  cacheCreationTokens: number,
): { billable: number; uncached: number } {
  // prompt_tokens IS the uncached tokens in Claude API
  const uncached = promptTokens;
  const billable = Math.round(
    uncached +
      cacheReadTokens * CLAUDE_CACHE_BILLING.READ_RATE +
      cacheCreationTokens * CLAUDE_CACHE_BILLING.WRITE_RATE,
  );
  return { billable, uncached };
}

// Helper to determine aria-sort value for sortable table headers
export type AriaSortValue = "ascending" | "descending" | "none";

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

// =============================================================================
// Model Name Formatting
// =============================================================================

/**
 * Shorten model name for compact display
 * Examples:
 *   - "claude-opus-4-5-20251101" -> "opus-4-5"
 *   - "claude-haiku-4-5-20251001" -> "haiku-4-5"
 *   - "gpt-4o-mini-2024-07-18" -> "gpt-4o-mini"
 *   - "gemini-1.5-pro" -> "gemini-1.5-pro"
 */
export function shortenModelName(model: string): string {
  // Remove date suffix (e.g., -20251101, -2024-07-18)
  const withoutDate = model
    .replace(/-\d{8}$/, "")
    .replace(/-\d{4}-\d{2}-\d{2}$/, "");

  // For Claude models, remove "claude-" prefix
  if (withoutDate.startsWith("claude-")) {
    return withoutDate.slice(7);
  }

  return withoutDate;
}
