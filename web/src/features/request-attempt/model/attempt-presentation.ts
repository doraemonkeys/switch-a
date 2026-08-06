import type {
  RequestAttempt,
  RequestAttemptOutcome,
  RequestAttemptPhase,
  RequestAttemptSwitchMode,
} from "@/api/types";

const NO_RESPONSE_STATUS_CODE = 0;
const WEBSOCKET_UPGRADE_STATUS_CODE = 101;
const MILLISECONDS_PER_SECOND = 1_000;
const SECONDS_PER_MINUTE = 60;

export type AttemptVisualState =
  | "success"
  | "semantic"
  | "failure"
  | "upgrade"
  | "warning"
  | "no_response"
  | "neutral";

export interface AttemptPresentation {
  readonly hasError: boolean;
  readonly hasBodySnippet: boolean;
  readonly hasRequestBodySnippet: boolean;
  readonly isNoResponse: boolean;
  readonly isWebSocketUpgrade: boolean;
  readonly isSuccess: boolean;
  readonly hasFailure: boolean;
  readonly visualState: AttemptVisualState;
  readonly phaseLabel: string | null;
  readonly outcome: {
    readonly text: string;
    readonly tone: "info" | "warning" | "error";
  } | null;
}

export function sortRequestAttempts(
  attempts: readonly RequestAttempt[],
): RequestAttempt[] {
  return [...attempts].sort(
    (left, right) => left.attempt - right.attempt || left.id - right.id,
  );
}

export function getAttemptPhaseLabel(
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

export function getAttemptOutcomePresentation(
  attempt: RequestAttempt,
): AttemptPresentation["outcome"] {
  switch (attempt.outcome) {
    case "upstream_handshake_rejected":
      return {
        text:
          attempt.phase === "pre_accept"
            ? "Handshake rejected before client upgrade"
            : "Handshake rejected",
        tone: "error",
      };
    case "upstream_transport_error":
      if (attempt.phase === "pre_accept") {
        return { text: "Transport error before client upgrade", tone: "error" };
      }
      return {
        text:
          attempt.result_visible_to_client === false
            ? "Transport error before client-visible data"
            : "Transport error after client-visible data",
        tone: "error",
      };
    case "upstream_semantic_error":
      if (attempt.result_visible_to_client === false) {
        return {
          text: "Semantic error suppressed before client-visible data",
          tone: "warning",
        };
      }
      return {
        text:
          attempt.result_visible_to_client === true
            ? "Semantic error reached the client"
            : "Semantic error ended this provider attempt",
        tone: "warning",
      };
    case "upstream_completed":
      return { text: "Upstream completed", tone: "info" };
    case "upstream_http_status_error":
      return {
        text: "Upstream returned an error status",
        tone: "error",
      };
    case "upstream_incomplete":
      // A client-side cancel is not an upstream failure; the backend health
      // cause discriminates it from a genuinely truncated upstream stream.
      if (attempt.health_cause === "client_cancelled") {
        return { text: "Client canceled the response", tone: "info" };
      }
      return {
        text: "Upstream response incomplete",
        tone: "warning",
      };
    case "gateway_error":
      return { text: "Gateway error", tone: "error" };
    case "visible_session":
      return {
        text: "This provider owned the client-visible session",
        tone: "info",
      };
    default:
      return null;
  }
}

// Backend health verdicts are authoritative for normalized attempts:
// client_cancelled and incomplete are neutral there, so re-deriving failure
// from outcome + status would mislabel them. Semantic errors keep their
// amber state (rule-absorbed, not failure) unless the verdict says failure.
function getVerdictVisualState(
  attempt: RequestAttempt,
  isWebSocket: boolean,
): AttemptVisualState | null {
  const verdict = attempt.health_verdict;
  if (verdict == null) {
    return null;
  }
  if (verdict === "failure") {
    return "failure";
  }
  if (verdict === "success") {
    return "success";
  }
  if (attempt.outcome === "upstream_semantic_error") {
    return "semantic";
  }
  if (isWebSocket && attempt.status_code === WEBSOCKET_UPGRADE_STATUS_CODE) {
    return "upgrade";
  }
  return "neutral";
}

function getVisualState(
  attempt: RequestAttempt,
  isWebSocket: boolean,
  isSuccess: boolean,
  hasFailure: boolean,
  hasError: boolean,
  isNoResponse: boolean,
): AttemptVisualState {
  const verdictState = getVerdictVisualState(attempt, isWebSocket);
  if (verdictState != null) {
    return verdictState;
  }
  if (isSuccess) {
    return "success";
  }
  if (attempt.outcome === "upstream_semantic_error") {
    return "semantic";
  }
  if (isNoResponse) {
    return "no_response";
  }
  if (
    isWebSocket &&
    attempt.status_code === WEBSOCKET_UPGRADE_STATUS_CODE &&
    !hasFailure
  ) {
    return "upgrade";
  }
  if (attempt.status_code >= 500 || hasError) {
    return "failure";
  }
  if (attempt.status_code >= 400) {
    return "warning";
  }
  if (hasFailure) {
    return "failure";
  }
  return "neutral";
}

// Non-WebSocket attempts classify failure explicitly by outcome instead of
// "any outcome present": upstream_completed and upstream_semantic_error both
// legitimately carry HTTP 200 while meaning success vs. rule-absorbed error,
// so a blanket truthiness check would mislabel both.
function isFailureOutcome(
  outcome: RequestAttemptOutcome | null | undefined,
): boolean {
  switch (outcome) {
    case "upstream_handshake_rejected":
    case "upstream_transport_error":
    case "upstream_http_status_error":
    case "upstream_incomplete":
    case "gateway_error":
      return true;
    default:
      return false;
  }
}

function hasHeuristicFailure(
  attempt: RequestAttempt,
  isWebSocket: boolean,
  ownsVisibleSession: boolean,
  hasError: boolean,
  isNoResponse: boolean,
): boolean {
  if (isWebSocket) {
    return (
      (attempt.outcome !== undefined &&
        attempt.outcome !== null &&
        !ownsVisibleSession) ||
      hasError ||
      isNoResponse ||
      attempt.status_code >= 400
    );
  }
  return (
    isFailureOutcome(attempt.outcome) ||
    hasError ||
    isNoResponse ||
    attempt.status_code >= 400
  );
}

function isHeuristicSuccess(
  attempt: RequestAttempt,
  isWebSocket: boolean,
  ownsVisibleSession: boolean,
  hasFailure: boolean,
): boolean {
  if (isWebSocket) {
    return ownsVisibleSession && !hasFailure;
  }
  return (
    !hasFailure &&
    attempt.status_code >= 200 &&
    attempt.status_code < 400 &&
    // A rule-absorbed semantic error may carry 200; it is never success.
    attempt.outcome !== "upstream_semantic_error"
  );
}

export function buildAttemptPresentation(
  attempt: RequestAttempt,
  isWebSocket: boolean,
): AttemptPresentation {
  const hasError = (attempt.error?.length ?? 0) > 0;
  const isNoResponse = attempt.status_code === NO_RESPONSE_STATUS_CODE;
  const ownsVisibleSession = attempt.outcome === "visible_session";
  // The backend health verdict owns classification when present; the
  // outcome/status/error heuristics below only cover attempts recorded without
  // one (legacy rows). switch_reason is excluded because it records routing
  // history, not verdict.
  const hasHealthVerdict = attempt.health_verdict != null;
  const hasFailure = hasHealthVerdict
    ? attempt.health_verdict === "failure"
    : hasHeuristicFailure(
        attempt,
        isWebSocket,
        ownsVisibleSession,
        hasError,
        isNoResponse,
      );
  const isSuccess = hasHealthVerdict
    ? attempt.health_verdict === "success"
    : isHeuristicSuccess(attempt, isWebSocket, ownsVisibleSession, hasFailure);
  return Object.freeze({
    hasError,
    hasBodySnippet: (attempt.body_snippet?.length ?? 0) > 0,
    hasRequestBodySnippet: (attempt.req_body_snippet?.length ?? 0) > 0,
    isNoResponse,
    isWebSocketUpgrade:
      isWebSocket && attempt.status_code === WEBSOCKET_UPGRADE_STATUS_CODE,
    isSuccess,
    hasFailure,
    visualState: getVisualState(
      attempt,
      isWebSocket,
      isSuccess,
      hasFailure,
      hasError,
      isNoResponse,
    ),
    phaseLabel: getAttemptPhaseLabel(attempt.phase),
    outcome: getAttemptOutcomePresentation(attempt),
  });
}

export function getSwitchModeLabel(
  switchMode?: RequestAttemptSwitchMode | null,
): string | null {
  switch (switchMode) {
    case "initial":
      return "Initial selection";
    case "replacement":
      return "Pre-visible replacement";
    case "failover":
      return "Failover";
    default:
      return null;
  }
}

export function formatTime(dateString: string): string {
  return new Date(dateString).toLocaleTimeString("en-US", {
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hour12: false,
  });
}

export function formatFullTimestamp(dateString: string): string {
  return new Date(dateString).toLocaleString("en-US", {
    year: "numeric",
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hour12: false,
  });
}

export function formatTimeOffset(
  currentTime: string,
  firstTime: string,
): string {
  const offsetMs =
    new Date(currentTime).getTime() - new Date(firstTime).getTime();
  if (offsetMs < MILLISECONDS_PER_SECOND) {
    return `+${offsetMs}ms`;
  }
  const seconds = offsetMs / MILLISECONDS_PER_SECOND;
  if (seconds < SECONDS_PER_MINUTE) {
    return `+${seconds.toFixed(1)}s`;
  }
  const minutes = Math.floor(seconds / SECONDS_PER_MINUTE);
  return `+${minutes}m ${(seconds % SECONDS_PER_MINUTE).toFixed(0)}s`;
}

export function formatSeedAge(ageMs?: number | null): string | null {
  if (ageMs === undefined || ageMs === null) {
    return null;
  }
  if (ageMs < MILLISECONDS_PER_SECOND) {
    return `${ageMs}ms`;
  }
  const seconds = ageMs / MILLISECONDS_PER_SECOND;
  if (seconds < SECONDS_PER_MINUTE) {
    return `${seconds.toFixed(seconds % 1 === 0 ? 0 : 1)}s`;
  }
  const minutes = Math.floor(seconds / SECONDS_PER_MINUTE);
  const remaining = seconds % SECONDS_PER_MINUTE;
  return `${minutes}m ${remaining.toFixed(remaining % 1 === 0 ? 0 : 1)}s`;
}

export function formatProviderLabel(
  providerID?: string,
  providerName?: string,
): string | null {
  if (!providerID) {
    return providerName ?? null;
  }
  return providerName && providerName !== providerID
    ? `${providerName} (${providerID})`
    : providerID;
}

export function formatRequestBody(body: string): string {
  try {
    return JSON.stringify(JSON.parse(body) as unknown, null, 2);
  } catch {
    return body;
  }
}
