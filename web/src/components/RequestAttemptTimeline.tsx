import type { RequestAttempt } from "../api/types";
import { getStatusCodeBadgeClass } from "../lib/utils";

interface RequestAttemptTimelineProps {
  attempts: RequestAttempt[];
  /** Provider name map for display */
  providerNames?: Map<string, string>;
}

/**
 * Vertical timeline component showing request retry attempts.
 *
 * Features:
 * - Visual timeline with colored status indicators
 * - Status code badges with color coding
 * - Error messages for failed attempts
 * - Latency display for each attempt
 */
export function RequestAttemptTimeline({
  attempts,
  providerNames,
}: RequestAttemptTimelineProps) {
  if (!attempts || attempts.length === 0) {
    return null;
  }

  // Sort by attempt number
  const sortedAttempts = [...attempts].sort((a, b) => a.attempt - b.attempt);

  return (
    <div className="relative">
      {/* Vertical line */}
      <div className="absolute left-3 top-0 bottom-0 w-0.5 bg-border-light" />

      <div className="space-y-4">
        {sortedAttempts.map((attempt, index) => (
          <AttemptNode
            key={attempt.id}
            attempt={attempt}
            isLast={index === sortedAttempts.length - 1}
            providerName={providerNames?.get(attempt.provider_id)}
          />
        ))}
      </div>
    </div>
  );
}

interface AttemptNodeProps {
  attempt: RequestAttempt;
  isLast: boolean;
  providerName?: string;
}

function AttemptNode({ attempt, isLast, providerName }: AttemptNodeProps) {
  const isSuccess = attempt.status_code >= 200 && attempt.status_code < 400;
  const hasError = attempt.error && attempt.error.length > 0;

  // Determine the card border/background based on status
  const getCardClasses = () => {
    if (isLast && isSuccess) {
      return "border-green-200 bg-green-50/50 dark:border-green-800 dark:bg-green-900/10";
    }
    if (hasError) {
      return "border-red-200 bg-red-50/50 dark:border-red-800 dark:bg-red-900/10";
    }
    return "border-border-light bg-bg-tertiary";
  };

  // Determine the dot color based on status
  const getDotColor = () => {
    if (attempt.status_code === 0) {
      // No response (connection error)
      return "bg-gray-400";
    }
    if (isSuccess) {
      return "bg-green-500";
    }
    if (attempt.status_code >= 500) {
      return "bg-red-500";
    }
    if (attempt.status_code >= 400) {
      return "bg-amber-500";
    }
    return "bg-gray-400";
  };

  return (
    <div className="relative pl-8">
      {/* Timeline dot */}
      <div
        className={`absolute left-1.5 top-1.5 w-3 h-3 rounded-full ring-2 ring-bg-secondary ${getDotColor()}`}
      />

      <div className={`p-3 rounded-lg border ${getCardClasses()}`}>
        {/* Header row */}
        <div className="flex items-center justify-between gap-4">
          <div className="flex items-center gap-2">
            <span className="text-sm font-medium text-text-primary">
              Attempt {attempt.attempt + 1}
            </span>
            {attempt.status_code > 0 && (
              <span
                className={`inline-flex items-center px-2 py-0.5 rounded text-xs font-medium ${getStatusCodeBadgeClass(attempt.status_code)}`}
              >
                {attempt.status_code}
              </span>
            )}
            {attempt.status_code === 0 && (
              <span className="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-gray-100 text-gray-800 dark:bg-gray-900/30 dark:text-gray-300">
                No Response
              </span>
            )}
          </div>
          <span className="text-sm text-text-secondary font-mono">
            {attempt.latency_ms}ms
          </span>
        </div>

        {/* Provider */}
        <p className="text-sm text-text-secondary mt-1">
          Provider: {providerName || attempt.provider_id}
        </p>

        {/* Error message */}
        {hasError && (
          <div className="mt-2 p-2 rounded bg-red-100/50 dark:bg-red-900/20 border border-red-200 dark:border-red-800">
            <p className="text-xs text-red-700 dark:text-red-300 font-mono break-words">
              {attempt.error}
            </p>
          </div>
        )}
      </div>
    </div>
  );
}
