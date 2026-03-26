import { useState, type ReactNode } from "react";
import type { RequestAttempt, RequestAttemptPhase } from "../api/types";
import { BADGE_STYLES, getStatusCodeBadgeClass } from "../lib/utils";
import { ErrorBodyParser } from "./ErrorBodyParser";

interface RequestAttemptTimelineProps {
  attempts: RequestAttempt[];
  /** Provider name map for display */
  providerNames?: Map<string, string>;
  /** User-Agent from parent request log (for diagnostic tips) */
  userAgent?: string;
  /** WebSocket attempts are provider detail only; session lifecycle stays on RequestLog. */
  isWebSocket?: boolean;
  /** RequestLog.provider_id for highlighting final lifecycle attribution in the attempt list. */
  attributedProviderId?: string;
}

const NO_RESPONSE_STATUS_CODE = 0;
const WEBSOCKET_UPGRADE_STATUS_CODE = 101;
const ATTEMPT_DISPLAY_NUMBER_START = 1;
const MILLISECONDS_PER_SECOND = 1000;
const SECONDS_PER_MINUTE = 60;
const OUTCOME_OWNER_LABEL = "Outcome owner";
const ATTRIBUTION_NOTE =
  "RequestLog.provider_id attributes the final client-visible outcome to this provider.";
const WEBSOCKET_UPGRADE_LABEL = "101 Upgrade";
const PROVIDER_SCOPED_SEMANTIC_SWITCH_REASON = "provider_scoped_semantic_error";
const INFO_BADGE_CLASS = BADGE_STYLES.INFO;
const WEBSOCKET_PHASE_BADGE_CLASS =
  "inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-slate-100 text-slate-700 dark:bg-slate-900/30 dark:text-slate-300";
const WEBSOCKET_SUCCESS_BADGE_CLASS =
  "inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-green-100 text-green-800 dark:bg-green-900/30 dark:text-green-300";
const WEBSOCKET_WARNING_BADGE_CLASS =
  "inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300";
const WEBSOCKET_ERROR_BADGE_CLASS =
  "inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-300";

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
  if (offsetMs < MILLISECONDS_PER_SECOND) {
    return `+${offsetMs}ms`;
  }
  const seconds = offsetMs / MILLISECONDS_PER_SECOND;
  if (seconds < SECONDS_PER_MINUTE) {
    return `+${seconds.toFixed(1)}s`;
  }
  const minutes = Math.floor(seconds / SECONDS_PER_MINUTE);
  const remainingSeconds = seconds % SECONDS_PER_MINUTE;
  return `+${minutes}m ${remainingSeconds.toFixed(0)}s`;
}

function getAttemptPhaseLabel(
  phase?: RequestAttemptPhase | null,
): string | null {
  switch (phase) {
    case "pre_accept":
      return "Pre-accept";
    case "post_upgrade_pre_visible":
      return "Post-upgrade, pre-visible";
    case "visible":
      return "Visible";
    default:
      return null;
  }
}

function getAttemptOutcomePresentation(
  attempt: RequestAttempt,
): { text: string; className: string } | null {
  switch (attempt.outcome) {
    case "upstream_handshake_rejected":
      return {
        text:
          attempt.phase === "pre_accept"
            ? "Handshake rejected before client upgrade"
            : "Handshake rejected",
        className: WEBSOCKET_ERROR_BADGE_CLASS,
      };
    case "upstream_transport_error":
      if (attempt.phase === "pre_accept") {
        return {
          text: "Transport error before client upgrade",
          className: WEBSOCKET_ERROR_BADGE_CLASS,
        };
      }
      if (attempt.result_visible_to_client === false) {
        return {
          text: "Transport error before client-visible data",
          className: WEBSOCKET_ERROR_BADGE_CLASS,
        };
      }
      return {
        text: "Transport error after client-visible data",
        className: WEBSOCKET_ERROR_BADGE_CLASS,
      };
    case "upstream_semantic_error":
      if (attempt.result_visible_to_client === false) {
        return {
          text: "Semantic error suppressed before client-visible data",
          className: WEBSOCKET_WARNING_BADGE_CLASS,
        };
      }
      if (attempt.result_visible_to_client === true) {
        return {
          text: "Semantic error reached the client",
          className: WEBSOCKET_WARNING_BADGE_CLASS,
        };
      }
      return {
        text: "Semantic error ended this provider attempt",
        className: WEBSOCKET_WARNING_BADGE_CLASS,
      };
    case "visible_session":
      return {
        text: "This provider owned the client-visible session",
        className: INFO_BADGE_CLASS,
      };
    default:
      return null;
  }
}

function ownsVisibleWebSocketSession(attempt: RequestAttempt): boolean {
  return attempt.outcome === "visible_session";
}

function hasWebSocketAttemptFailure(
  attempt: RequestAttempt,
  hasError: boolean,
  isNoResponse: boolean,
): boolean {
  return (
    (attempt.outcome !== undefined &&
      attempt.outcome !== null &&
      !ownsVisibleWebSocketSession(attempt)) ||
    hasError ||
    isNoResponse ||
    attempt.status_code >= 400 ||
    Boolean(attempt.switch_reason)
  );
}

/**
 * WebSocket attempt rows record provider ownership of the visible session, not the
 * request-level verdict. A provider can cross the visible boundary and still end
 * the session on a later semantic or transport failure.
 */
function isSuccessfulAttempt(
  attempt: RequestAttempt,
  isWebSocket: boolean,
  hasError: boolean,
  isNoResponse: boolean,
): boolean {
  if (isWebSocket) {
    return (
      ownsVisibleWebSocketSession(attempt) &&
      !hasWebSocketAttemptFailure(attempt, hasError, isNoResponse)
    );
  }
  if (attempt.outcome) {
    return false;
  }
  return attempt.status_code >= 200 && attempt.status_code < 400;
}

function getWebSocketUpgradeBadgeClass(
  attempt: RequestAttempt,
  hasAttemptFailure: boolean,
): string {
  if (ownsVisibleWebSocketSession(attempt) && !hasAttemptFailure) {
    return WEBSOCKET_SUCCESS_BADGE_CLASS;
  }
  return INFO_BADGE_CLASS;
}

function formatReqBody(body: string): string {
  try {
    const parsed = JSON.parse(body);
    return JSON.stringify(parsed, null, 2);
  } catch {
    return body;
  }
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
    case PROVIDER_SCOPED_SEMANTIC_SWITCH_REASON:
      return {
        text: "Provider-scoped semantic error — switched provider",
        icon: "🛡️",
      };
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
  isWebSocket = false,
  attributedProviderId,
}: RequestAttemptTimelineProps) {
  if (!attempts || attempts.length === 0) {
    return null;
  }

  // Sort by attempt number
  const sortedAttempts = [...attempts].sort((a, b) => a.attempt - b.attempt);
  const firstAttemptTime = sortedAttempts[0]?.created_at;
  const attributedAttemptId =
    isWebSocket && attributedProviderId
      ? [...sortedAttempts]
          .reverse()
          .find((attempt) => attempt.provider_id === attributedProviderId)?.id
      : undefined;

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
            displayAttemptNumber={index + ATTEMPT_DISPLAY_NUMBER_START}
            isWebSocket={isWebSocket}
            isAttributedAttempt={attempt.id === attributedAttemptId}
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
  displayAttemptNumber: number;
  isWebSocket: boolean;
  isAttributedAttempt: boolean;
}

interface AttemptHeaderProps {
  isWebSocket: boolean;
  displayAttemptNumber: number;
  latencyMs: number;
  statusBadge: ReactNode;
}

function AttemptHeader({
  isWebSocket,
  displayAttemptNumber,
  latencyMs,
  statusBadge,
}: AttemptHeaderProps) {
  return (
    <div className="flex items-center justify-between gap-4">
      <div className="flex items-center gap-2">
        <span className="text-sm font-medium text-text-primary">
          {`${isWebSocket ? "Provider Attempt" : "Attempt"} ${displayAttemptNumber}`}
        </span>
        {statusBadge}
      </div>
      <span className="text-sm text-text-secondary font-mono">
        {latencyMs}ms
      </span>
    </div>
  );
}

interface AttemptProviderMetaProps {
  providerName?: string;
  providerId: string;
  isAttributedAttempt: boolean;
  createdAt?: string;
  isFirst: boolean;
  firstAttemptTime?: string;
}

function AttemptProviderMeta({
  providerName,
  providerId,
  isAttributedAttempt,
  createdAt,
  isFirst,
  firstAttemptTime,
}: AttemptProviderMetaProps) {
  return (
    <>
      <div className="flex items-center justify-between mt-1 gap-3">
        <div className="flex items-center gap-2 flex-wrap">
          <p className="text-sm text-text-secondary">
            Provider: {providerName || providerId}
          </p>
          {isAttributedAttempt && (
            <span
              className={`inline-flex items-center px-2 py-0.5 rounded text-xs font-medium ${INFO_BADGE_CLASS}`}
              title={ATTRIBUTION_NOTE}
            >
              {OUTCOME_OWNER_LABEL}
            </span>
          )}
        </div>
        {createdAt && (
          <span
            className="text-xs text-text-muted font-mono flex items-center gap-1.5"
            title={formatFullTimestamp(createdAt)}
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
              formatTime(createdAt)
            ) : (
              <span className="text-amber-600 dark:text-amber-400">
                {formatOffset(getTimeOffset(createdAt, firstAttemptTime!))}
              </span>
            )}
          </span>
        )}
      </div>

      {isAttributedAttempt && (
        <p className="mt-2 text-xs text-blue-700 dark:text-blue-300">
          {ATTRIBUTION_NOTE}
        </p>
      )}
    </>
  );
}

function AttemptLifecycleBadges({
  phaseLabel,
  outcomePresentation,
}: {
  phaseLabel: string | null;
  outcomePresentation: { text: string; className: string } | null;
}) {
  if (!phaseLabel && !outcomePresentation) {
    return null;
  }

  return (
    <div className="mt-2 flex flex-wrap items-center gap-2">
      {phaseLabel && (
        <span className={WEBSOCKET_PHASE_BADGE_CLASS}>Phase: {phaseLabel}</span>
      )}
      {outcomePresentation && (
        <span className={outcomePresentation.className}>
          {outcomePresentation.text}
        </span>
      )}
    </div>
  );
}

function AttemptNode({
  attempt,
  isFirst,
  isLast,
  providerName,
  userAgent,
  firstAttemptTime,
  displayAttemptNumber,
  isWebSocket,
  isAttributedAttempt,
}: AttemptNodeProps) {
  const [showReqBody, setShowReqBody] = useState(false);

  const hasError = (attempt.error?.length ?? 0) > 0;
  const hasBodySnippet = (attempt.body_snippet?.length ?? 0) > 0;
  const hasReqBodySnippet = (attempt.req_body_snippet?.length ?? 0) > 0;
  const isNoResponse = attempt.status_code === NO_RESPONSE_STATUS_CODE;
  const hasAttemptFailure = isWebSocket
    ? hasWebSocketAttemptFailure(attempt, hasError, isNoResponse)
    : false;
  const isSuccess = isSuccessfulAttempt(
    attempt,
    isWebSocket,
    hasError,
    isNoResponse,
  );
  const isWebSocketUpgrade =
    isWebSocket && attempt.status_code === WEBSOCKET_UPGRADE_STATUS_CODE;
  const phaseLabel = getAttemptPhaseLabel(attempt.phase);
  const outcomePresentation = getAttemptOutcomePresentation(attempt);
  const switchReasonInfo = attempt.switch_reason
    ? getSwitchReasonLabel(attempt.switch_reason)
    : null;

  // Determine the card border/background based on status
  const getCardClasses = () => {
    if (isWebSocket) {
      if (isLast && isSuccess) {
        return "border-green-200 bg-green-50/50 dark:border-green-800 dark:bg-green-900/10";
      }
      if (attempt.outcome === "upstream_semantic_error") {
        return "border-amber-200 bg-amber-50/50 dark:border-amber-800 dark:bg-amber-900/10";
      }
      if (hasAttemptFailure) {
        return "border-red-200 bg-red-50/50 dark:border-red-800 dark:bg-red-900/10";
      }
      if (isWebSocketUpgrade) {
        return "border-blue-100 bg-blue-50/30 dark:border-blue-900 dark:bg-blue-900/5";
      }
      return "border-border-light bg-bg-tertiary";
    }
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
    if (isWebSocket) {
      if (isSuccess) {
        return "bg-green-500";
      }
      if (attempt.outcome === "upstream_semantic_error") {
        return "bg-amber-500";
      }
      if (isNoResponse) {
        return "bg-gray-400";
      }
      if (isWebSocketUpgrade) {
        return "bg-sky-500";
      }
      if (attempt.status_code >= 500 || hasError || attempt.switch_reason) {
        return "bg-red-500";
      }
      if (attempt.status_code >= 400) {
        return "bg-amber-500";
      }
      return "bg-gray-400";
    }
    if (isNoResponse) {
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

  const renderStatusBadge = () => {
    if (isNoResponse) {
      return (
        <span className="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-gray-100 text-gray-800 dark:bg-gray-900/30 dark:text-gray-300">
          No Response
        </span>
      );
    }
    if (isWebSocketUpgrade) {
      return (
        <span
          className={getWebSocketUpgradeBadgeClass(attempt, hasAttemptFailure)}
        >
          {WEBSOCKET_UPGRADE_LABEL}
        </span>
      );
    }
    if (attempt.status_code > 0) {
      return (
        <span
          className={`inline-flex items-center px-2 py-0.5 rounded text-xs font-medium ${getStatusCodeBadgeClass(attempt.status_code)}`}
        >
          {attempt.status_code}
        </span>
      );
    }
    return null;
  };

  return (
    <div className="relative pl-8">
      {/* Timeline dot */}
      <div
        className={`absolute left-1.5 top-1.5 w-3 h-3 rounded-full ring-2 ring-bg-secondary ${getDotColor()}`}
      />

      <div className={`p-3 rounded-lg border ${getCardClasses()}`}>
        <AttemptHeader
          isWebSocket={isWebSocket}
          displayAttemptNumber={displayAttemptNumber}
          latencyMs={attempt.latency_ms}
          statusBadge={renderStatusBadge()}
        />
        <AttemptProviderMeta
          providerName={providerName}
          providerId={attempt.provider_id}
          isAttributedAttempt={isAttributedAttempt}
          createdAt={attempt.created_at}
          isFirst={isFirst}
          firstAttemptTime={firstAttemptTime}
        />
        <AttemptLifecycleBadges
          phaseLabel={phaseLabel}
          outcomePresentation={outcomePresentation}
        />

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
