import type { RequestLog } from "../../api/types";
import { formatTokenCount, calculateCacheHitRate } from "./utils";

interface TokenCellProps {
  log: RequestLog;
}

/** Shared token display component for input/output values */
interface TokenDisplayProps {
  inputDisplay: string;
  outputDisplay: string;
  separator: string;
  className?: string;
}

function TokenDisplay({
  inputDisplay,
  outputDisplay,
  separator,
  className = "",
}: TokenDisplayProps) {
  return (
    <span className={`items-center ${className}`}>
      <span className="text-blue-600 dark:text-blue-400">{inputDisplay}</span>
      <span className="text-text-muted">{separator}</span>
      <span className="text-green-600 dark:text-green-400">
        {outputDisplay}
      </span>
    </span>
  );
}

/**
 * Token cell component for the logs table.
 *
 * Display format:
 * - Desktop: `1.2k → 856 ⚡` (input → output with cache indicator)
 * - Tablet: `1.2k/856` (compact with slash)
 * - Mobile: Hidden (shown in detail modal)
 *
 * Features:
 * - Shows ⚡ icon when cache_read > 0 with hover tooltip
 * - Shows `—` for unavailable data (null)
 * - Shows `0` for explicit zero (distinguishes from null)
 * - Partial data shows available tokens with `—` for missing
 */
export function TokenCell({ log }: TokenCellProps) {
  const { prompt_tokens, completion_tokens, cache_read_input_tokens } = log;

  // Check if we have any token data
  const hasPromptTokens = prompt_tokens != null;
  const hasCompletionTokens = completion_tokens != null;
  const hasCacheRead =
    cache_read_input_tokens != null && cache_read_input_tokens > 0;

  // If no token data at all, show unavailable indicator
  if (!hasPromptTokens && !hasCompletionTokens) {
    return (
      <span className="text-text-muted" title="Token data unavailable">
        —
      </span>
    );
  }

  // Format individual values
  const inputDisplay = hasPromptTokens ? formatTokenCount(prompt_tokens) : "—";
  const outputDisplay = hasCompletionTokens
    ? formatTokenCount(completion_tokens)
    : "—";

  // Calculate cache hit rate for tooltip
  const cacheHitRate = calculateCacheHitRate(
    cache_read_input_tokens,
    prompt_tokens,
  );
  const cacheTooltip =
    cacheHitRate !== null ? `Cache Hit: ${cacheHitRate}%` : "";

  return (
    <span className="inline-flex items-center gap-1 font-mono text-sm">
      {/* Desktop: Full display with arrow */}
      <TokenDisplay
        inputDisplay={inputDisplay}
        outputDisplay={outputDisplay}
        separator="→"
        className="hidden lg:inline-flex gap-1"
      />

      {/* Tablet: Compact display with slash */}
      <TokenDisplay
        inputDisplay={inputDisplay}
        outputDisplay={outputDisplay}
        separator="/"
        className="hidden md:inline-flex lg:hidden"
      />

      {/* Cache indicator - shown when cache_read > 0 */}
      {hasCacheRead && (
        <span
          className="hidden md:inline-flex items-center ml-0.5"
          title={cacheTooltip}
        >
          {/* Zap/Lightning icon from Lucide */}
          <svg
            className="w-3.5 h-3.5 text-emerald-500"
            fill="none"
            stroke="currentColor"
            strokeWidth={2}
            viewBox="0 0 24 24"
          >
            <path
              strokeLinecap="round"
              strokeLinejoin="round"
              d="M13 10V3L4 14h7v7l9-11h-7z"
            />
          </svg>
        </span>
      )}
    </span>
  );
}
