import { useState } from "react";
import type { RequestAttempt } from "../api/types";
import { getStatusCodeBadgeClass } from "../lib/utils";
import { ErrorBodyParser } from "./ErrorBodyParser";

interface RequestAttemptTimelineProps {
  attempts: RequestAttempt[];
  /** Provider name map for display */
  providerNames?: Map<string, string>;
  /** User-Agent from parent request log (for diagnostic tips) */
  userAgent?: string;
}

/**
 * Format time as HH:MM:SS.mmm for display
 */
function formatTime(dateStr: string): string {
  const date = new Date(dateStr);
  return date.toLocaleTimeString("en-US", {
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hour12: false,
  });
}

/**
 * Format full timestamp for tooltip
 */
function formatFullTimestamp(dateStr: string): string {
  const date = new Date(dateStr);
  return date.toLocaleString("en-US", {
    year: "numeric",
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hour12: false,
  });
}

/**
 * Calculate time offset from first attempt in milliseconds
 */
function getTimeOffset(currentTime: string, firstTime: string): number {
  return new Date(currentTime).getTime() - new Date(firstTime).getTime();
}

/**
 * Format offset duration in human-readable form
 */
function formatOffset(offsetMs: number): string {
  if (offsetMs < 1000) {
    return `+${offsetMs}ms`;
  }
  const seconds = offsetMs / 1000;
  if (seconds < 60) {
    return `+${seconds.toFixed(1)}s`;
  }
  const minutes = Math.floor(seconds / 60);
  const remainingSeconds = seconds % 60;
  return `+${minutes}m ${remainingSeconds.toFixed(0)}s`;
}

/** Maps switch reason codes to human-readable labels with icons */
function getSwitchReasonLabel(
  reason: string,
): { text: string; icon: string } | null {
  switch (reason) {
    case "circuit_breaker_triggered":
      return {
        text: "Circuit breaker triggered — switched provider",
        icon: "⚡",
      };
    case "max_retries_exhausted":
      return { text: "Max retries reached — switched provider", icon: "🔄" };
    case "permanent_error_401":
      return { text: "Auth error (401) — switched provider", icon: "🔐" };
    case "permanent_error_402":
      return {
        text: "Payment required (402) — switched provider",
        icon: "💳",
      };
    case "permanent_error_403":
      return { text: "Forbidden (403) — switched provider", icon: "🚫" };
    default:
      return null;
  }
}

/**
 * Vertical timeline component showing request retry attempts.
 *
 * Features:
 * - Visual timeline with colored status indicators
 * - Status code badges with color coding
 * - Smart error parsing with diagnostic tips
 * - Request body snippet for failed attempts
 * - Latency display for each attempt
 */
export function RequestAttemptTimeline({
  attempts,
  providerNames,
  userAgent,
}: RequestAttemptTimelineProps) {
  if (!attempts || attempts.length === 0) {
    return null;
  }

  // Sort by attempt number
  const sortedAttempts = [...attempts].sort((a, b) => a.attempt - b.attempt);
  const firstAttemptTime = sortedAttempts[0]?.created_at;

  return (
    <div className="relative">
      {/* Vertical line */}
      <div className="absolute left-3 top-0 bottom-0 w-0.5 bg-border-light" />

      <div className="space-y-4">
        {sortedAttempts.map((attempt, index) => (
          <AttemptNode
            key={attempt.id}
            attempt={attempt}
            isFirst={index === 0}
            isLast={index === sortedAttempts.length - 1}
            providerName={providerNames?.get(attempt.provider_id)}
            userAgent={userAgent}
            firstAttemptTime={firstAttemptTime}
          />
        ))}
      </div>
    </div>
  );
}

interface AttemptNodeProps {
  attempt: RequestAttempt;
  isFirst: boolean;
  isLast: boolean;
  providerName?: string;
  userAgent?: string;
  firstAttemptTime?: string;
}

function AttemptNode({
  attempt,
  isFirst,
  isLast,
  providerName,
  userAgent,
  firstAttemptTime,
}: AttemptNodeProps) {
  const [showReqBody, setShowReqBody] = useState(false);

  const isSuccess = attempt.status_code >= 200 && attempt.status_code < 400;
  const hasError = attempt.error && attempt.error.length > 0;
  const hasBodySnippet =
    attempt.body_snippet && attempt.body_snippet.length > 0;
  const hasReqBodySnippet =
    attempt.req_body_snippet && attempt.req_body_snippet.length > 0;
  const switchReasonInfo = attempt.switch_reason
    ? getSwitchReasonLabel(attempt.switch_reason)
    : null;

  // Determine the card border/background based on status
  const getCardClasses = () => {
    if (isLast && isSuccess) {
      return "border-green-200 bg-green-50/50 dark:border-green-800 dark:bg-green-900/10";
    }
    if (hasError || !isSuccess) {
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

  // Format request body for display
  const formatReqBody = (body: string): string => {
    try {
      const parsed = JSON.parse(body);
      return JSON.stringify(parsed, null, 2);
    } catch {
      return body;
    }
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

        {/* Provider and Timing info */}
        <div className="flex items-center justify-between mt-1">
          <p className="text-sm text-text-secondary">
            Provider: {providerName || attempt.provider_id}
          </p>
          {/* Time display: absolute time for first attempt, offset for subsequent */}
          {attempt.created_at && (
            <span
              className="text-xs text-text-muted font-mono flex items-center gap-1.5"
              title={formatFullTimestamp(attempt.created_at)}
            >
              <svg
                className="w-3 h-3"
                fill="none"
                stroke="currentColor"
                viewBox="0 0 24 24"
              >
                <path
                  strokeLinecap="round"
                  strokeLinejoin="round"
                  strokeWidth={2}
                  d="M12 8v4l3 3m6-3a9 9 0 11-18 0 9 9 0 0118 0z"
                />
              </svg>
              {isFirst ? (
                formatTime(attempt.created_at)
              ) : (
                <span className="text-amber-600 dark:text-amber-400">
                  {formatOffset(
                    getTimeOffset(attempt.created_at, firstAttemptTime!),
                  )}
                </span>
              )}
            </span>
          )}
        </div>

        {/* Error message */}
        {hasError && (
          <div className="mt-2 p-2 rounded bg-red-100/50 dark:bg-red-900/20 border border-red-200 dark:border-red-800">
            <p className="text-xs text-red-700 dark:text-red-300 font-mono break-words">
              {attempt.error}
            </p>
          </div>
        )}

        {/* Response body snippet - Now with smart parsing */}
        {hasBodySnippet && (
          <div className="mt-2">
            <p className="text-xs text-text-muted mb-1.5">Response Body:</p>
            <ErrorBodyParser
              body={attempt.body_snippet!}
              statusCode={attempt.status_code}
              userAgent={userAgent}
            />
          </div>
        )}

        {/* Request body snippet - Collapsible for error attempts */}
        {hasReqBodySnippet && (
          <div className="mt-2">
            <button
              type="button"
              onClick={() => setShowReqBody(!showReqBody)}
              className="text-xs text-text-muted hover:text-text-secondary transition-colors flex items-center gap-1"
            >
              <svg
                className={`w-3 h-3 transition-transform ${showReqBody ? "rotate-90" : ""}`}
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
              Request Body {showReqBody ? "(hide)" : "(show)"}
            </button>
            {showReqBody && (
              <pre className="mt-1.5 p-2 rounded bg-bg-tertiary text-xs font-mono text-text-secondary overflow-x-auto whitespace-pre-wrap break-words max-h-32 overflow-y-auto border border-border-light">
                {formatReqBody(attempt.req_body_snippet!)}
              </pre>
            )}
          </div>
        )}

        {/* Switch reason indicator */}
        {switchReasonInfo && (
          <div className="mt-2 flex items-center gap-1.5 text-xs text-amber-600 dark:text-amber-400 bg-amber-50 dark:bg-amber-900/20 px-2 py-1 rounded">
            <span>{switchReasonInfo.icon}</span>
            <span>{switchReasonInfo.text}</span>
          </div>
        )}
      </div>
    </div>
  );
}
