import { useState } from "react";
import type { RequestLog } from "../api/types";
import {
  formatTokenCount,
  formatTokenLocale,
  calculateCacheHitRate,
} from "./logs/utils";
import { TokenStatCard, CacheSection } from "./logs/TokenStatCards";

interface TokenUsageStatsProps {
  log: RequestLog;
  /** Default expanded state */
  defaultExpanded?: boolean;
}

/**
 * Collapsible token usage statistics component for the log detail modal.
 *
 * Features:
 * - Shows input, output, and total token counts
 * - Claude cache section with hit rate progress bar
 * - Effective cost calculation for cache billing
 * - Graceful handling of null/missing data
 */
export function TokenUsageStats({
  log,
  defaultExpanded = false,
}: TokenUsageStatsProps) {
  const [isExpanded, setIsExpanded] = useState(defaultExpanded);

  const {
    prompt_tokens,
    completion_tokens,
    total_tokens,
    reasoning_tokens,
    cache_read_input_tokens,
    cache_creation_input_tokens,
  } = log;

  // Check what data we have
  const hasPromptTokens = prompt_tokens != null;
  const hasCompletionTokens = completion_tokens != null;
  const hasTotalTokens = total_tokens != null;
  const hasReasoningTokens = reasoning_tokens != null;
  const hasCacheRead =
    cache_read_input_tokens != null && cache_read_input_tokens > 0;
  const hasCacheCreation =
    cache_creation_input_tokens != null && cache_creation_input_tokens > 0;
  const hasCacheData = hasCacheRead || hasCacheCreation;

  // Don't render if no token data at all
  if (
    !hasPromptTokens &&
    !hasCompletionTokens &&
    !hasTotalTokens &&
    !hasReasoningTokens
  ) {
    return null;
  }

  // Calculate cache hit rate (considers total input = new + cache_read + cache_creation)
  const cacheHitRate = calculateCacheHitRate(
    cache_read_input_tokens,
    prompt_tokens,
    cache_creation_input_tokens,
  );

  // Calculate summary display
  // Note: Using `|| 0` to safely handle null values when calculating sum.
  // This is intentional - null tokens should be treated as 0 in the sum calculation,
  // but we still distinguish null from 0 in individual displays (showing "—" for null).
  const summaryTotal = hasTotalTokens
    ? total_tokens
    : (prompt_tokens || 0) + (completion_tokens || 0);
  const tokenGridClass = hasReasoningTokens
    ? "grid grid-cols-2 md:grid-cols-4 gap-2 mb-3"
    : "grid grid-cols-3 gap-2 mb-3";

  return (
    <div className="rounded-lg border border-border-light bg-bg-tertiary/50 overflow-hidden">
      {/* Collapsible Header */}
      <button
        type="button"
        onClick={() => setIsExpanded(!isExpanded)}
        className="w-full px-3 py-2.5 flex items-center justify-between hover:bg-bg-tertiary transition-colors"
      >
        <div className="flex items-center gap-2">
          {/* Expand/Collapse Icon */}
          <svg
            className={`w-4 h-4 text-text-muted transition-transform duration-200 ${
              isExpanded ? "rotate-90" : ""
            }`}
            fill="none"
            stroke="currentColor"
            viewBox="0 0 24 24"
          >
            <path
              strokeLinecap="round"
              strokeLinejoin="round"
              strokeWidth={2}
              d="M9 5l7 7-7 7"
            />
          </svg>

          {/* Token Usage Icon (BarChart3 from Lucide) */}
          <svg
            className="w-4 h-4 text-purple-500"
            fill="none"
            stroke="currentColor"
            strokeWidth={2}
            viewBox="0 0 24 24"
          >
            <path
              strokeLinecap="round"
              strokeLinejoin="round"
              d="M12 20V10m6 10V4M6 20v-4"
            />
          </svg>

          <span className="text-sm font-medium text-text-secondary">
            Token Usage
          </span>
        </div>

        {/* Collapsed Summary */}
        {!isExpanded && summaryTotal > 0 && (
          <span className="text-xs text-text-muted font-mono">
            {formatTokenCount(summaryTotal)} total
          </span>
        )}
      </button>

      {/* Expanded Content */}
      {isExpanded && (
        <div className="px-3 pb-3 pt-1">
          {/* Main Token Stats Grid */}
          <div className={tokenGridClass}>
            {/* Input Tokens */}
            <TokenStatCard
              icon={
                <svg
                  className="w-4 h-4"
                  fill="none"
                  stroke="currentColor"
                  strokeWidth={2}
                  viewBox="0 0 24 24"
                >
                  <path
                    strokeLinecap="round"
                    strokeLinejoin="round"
                    d="M19 14l-7 7m0 0l-7-7m7 7V3"
                  />
                </svg>
              }
              label="Input"
              value={hasPromptTokens ? formatTokenLocale(prompt_tokens) : "—"}
              iconColor="text-blue-500"
            />

            {/* Output Tokens */}
            <TokenStatCard
              icon={
                <svg
                  className="w-4 h-4"
                  fill="none"
                  stroke="currentColor"
                  strokeWidth={2}
                  viewBox="0 0 24 24"
                >
                  <path
                    strokeLinecap="round"
                    strokeLinejoin="round"
                    d="M5 10l7-7m0 0l7 7m-7-7v18"
                  />
                </svg>
              }
              label="Output"
              value={
                hasCompletionTokens ? formatTokenLocale(completion_tokens) : "—"
              }
              iconColor="text-green-500"
            />

            {hasReasoningTokens && (
              <TokenStatCard
                icon={
                  <svg
                    className="w-4 h-4"
                    fill="none"
                    stroke="currentColor"
                    strokeWidth={2}
                    viewBox="0 0 24 24"
                  >
                    <path
                      strokeLinecap="round"
                      strokeLinejoin="round"
                      d="M9 18h6m-5 3h4m2-8a6 6 0 10-8 0c.6.5 1 1.3 1 2.1V16h6v-.9c0-.8.4-1.6 1-2.1z"
                    />
                  </svg>
                }
                label="Reasoning"
                value={formatTokenLocale(reasoning_tokens)}
                iconColor="text-violet-500"
              />
            )}

            {/* Total Tokens */}
            <TokenStatCard
              icon={
                <svg
                  className="w-4 h-4"
                  fill="none"
                  stroke="currentColor"
                  strokeWidth={2}
                  viewBox="0 0 24 24"
                >
                  <path
                    strokeLinecap="round"
                    strokeLinejoin="round"
                    d="M12 20V10m6 10V4M6 20v-4"
                  />
                </svg>
              }
              label="Total"
              value={
                hasTotalTokens || hasPromptTokens || hasCompletionTokens
                  ? formatTokenLocale(summaryTotal)
                  : "—"
              }
              iconColor="text-purple-500"
            />
          </div>

          {/* Claude Cache Section */}
          {hasCacheData && hasPromptTokens && (
            <CacheSection
              cacheHitRate={cacheHitRate}
              hasCacheRead={hasCacheRead}
              hasCacheCreation={hasCacheCreation}
              cacheReadTokens={cache_read_input_tokens!}
              cacheCreationTokens={cache_creation_input_tokens || 0}
              promptTokens={prompt_tokens}
            />
          )}

          {/* Data Unavailable Message */}
          {!hasPromptTokens && !hasCompletionTokens && !hasReasoningTokens && (
            <div className="text-center py-4 text-sm text-text-muted">
              Token data unavailable
            </div>
          )}
        </div>
      )}
    </div>
  );
}
