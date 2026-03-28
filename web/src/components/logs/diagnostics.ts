import type {
  CommitSource,
  RecoveryAction,
  RequestLog,
  TerminalCause,
  WebSocketProbeOutcome,
} from "../../api/types";
import { BADGE_STYLES } from "../../lib/utils";

export type DiagnosticTone = "success" | "danger" | "warning" | "info";
export type CommitmentState = "committed" | "uncommitted" | "unknown";
export type ClientVisibilityState = "visible" | "not_visible" | "unknown";

export interface LogLifecyclePresentation {
  showLifecycle: boolean;
  outcomeLabel: string;
  shortOutcomeLabel: string;
  outcomeTone: DiagnosticTone;
  commitmentState: CommitmentState;
  commitmentLabel: string;
  commitmentTone: DiagnosticTone;
  clientVisibilityState: ClientVisibilityState;
  clientVisibilityLabel: string;
  clientVisibilityTone: DiagnosticTone;
  terminalCause: TerminalCause;
  terminalCauseLabel: string;
  terminalCauseTone: DiagnosticTone;
  probeOutcome: WebSocketProbeOutcome;
  probeOutcomeLabel: string | null;
  commitSourceLabel: string | null;
  recoveryAction: RecoveryAction | null;
  recoveryActionLabel: string | null;
  recoveryActionTone: DiagnosticTone | null;
  stickyWrittenLabel: string | null;
  tableDetailLabel: string | null;
  shouldShowErrorDetails: boolean;
}

export function getPrimaryProviderLabel(log: RequestLog): string {
  return log.is_websocket ? "Outcome Provider" : "Provider";
}

const UNKNOWN_STATUS_CODE = 0;

const COMMITMENT_LABELS: Record<CommitmentState, string> = {
  committed: "Committed",
  uncommitted: "Uncommitted",
  unknown: "Commit unknown",
};

const COMMITMENT_TONES: Record<CommitmentState, DiagnosticTone> = {
  committed: "success",
  uncommitted: "warning",
  unknown: "info",
};

const CLIENT_VISIBILITY_LABELS: Record<ClientVisibilityState, string> = {
  visible: "Visible",
  not_visible: "Not Visible",
  unknown: "Visibility Unknown",
};

const CLIENT_VISIBILITY_TONES: Record<ClientVisibilityState, DiagnosticTone> = {
  visible: "info",
  not_visible: "warning",
  unknown: "info",
};

const TERMINAL_CAUSE_LABELS: Record<TerminalCause, string> = {
  unknown: "Unknown",
  provider_unavailable: "Provider unavailable",
  provider_configuration_error: "Provider configuration error",
  clean_close: "Clean close",
  client_disconnect: "Client disconnect",
  upstream_transport_error: "Upstream transport error",
  upstream_semantic_error: "Upstream semantic error",
  upstream_handshake_rejected: "Upstream handshake rejected",
  client_upgrade_rejected: "Client upgrade rejected",
  internal_error: "Internal error",
};

const TERMINAL_CAUSE_TONES: Record<TerminalCause, DiagnosticTone> = {
  unknown: "info",
  provider_unavailable: "danger",
  provider_configuration_error: "danger",
  clean_close: "success",
  client_disconnect: "info",
  upstream_transport_error: "warning",
  upstream_semantic_error: "danger",
  upstream_handshake_rejected: "danger",
  client_upgrade_rejected: "danger",
  internal_error: "danger",
};

const COMMIT_SOURCE_LABELS: Record<CommitSource, string> = {
  semantic_event: "Semantic event",
  upstream_message: "First upstream message",
  unknown: "Unknown",
};

const RECOVERY_ACTION_LABELS: Record<RecoveryAction, string> = {
  none: "None",
  transparent_retry: "Transparent Retry",
  reconnect_required: "Reconnect Required",
};

const RECOVERY_ACTION_TONES: Record<RecoveryAction, DiagnosticTone> = {
  none: "info",
  transparent_retry: "info",
  reconnect_required: "danger",
};

const PROBE_OUTCOME_LABELS: Record<WebSocketProbeOutcome, string> = {
  unknown: "Unknown",
  bypassed: "Bypassed",
  demand_resolution_failed: "Demand resolution failed",
  unsupported: "Unsupported",
  observed_usable_model: "Observed usable model",
  completed_without_usable_model: "Completed without usable model",
  transport_failed: "Transport failed",
};

interface OutcomePresentation {
  label: string;
  shortLabel: string;
  tone: DiagnosticTone;
}

function getCommitmentState(
  sessionCommitted: RequestLog["session_committed"],
): CommitmentState {
  if (sessionCommitted === true) {
    return "committed";
  }
  if (sessionCommitted === false) {
    return "uncommitted";
  }
  return "unknown";
}

function getClientVisibilityState(
  clientVisible: RequestLog["client_visible"],
): ClientVisibilityState {
  if (clientVisible === true) {
    return "visible";
  }
  if (clientVisible === false) {
    return "not_visible";
  }
  return "unknown";
}

function getTerminalCause(log: RequestLog): TerminalCause {
  return log.terminal_cause ?? "unknown";
}

function getCommitSourceLabel(log: RequestLog): string | null {
  if (!log.commit_source) {
    return null;
  }
  return COMMIT_SOURCE_LABELS[log.commit_source];
}

function getStickyWrittenLabel(log: RequestLog): string | null {
  if (log.sticky_written == null) {
    return null;
  }
  return log.sticky_written ? "Written" : "Not written";
}

function getProbeOutcome(log: RequestLog): WebSocketProbeOutcome {
  return log.probe_outcome ?? "unknown";
}

function getProbeOutcomeLabel(log: RequestLog): string | null {
  if (log.probe_outcome == null) {
    return null;
  }
  return PROBE_OUTCOME_LABELS[getProbeOutcome(log)];
}

function getRecoveryAction(log: RequestLog): RecoveryAction | null {
  return log.recovery_action ?? null;
}

function getDisplayRecoveryAction(log: RequestLog): RecoveryAction | null {
  const recoveryAction = getRecoveryAction(log);
  if (recoveryAction == null || recoveryAction === "none") {
    return null;
  }
  return recoveryAction;
}

function getRecoveryActionLabel(log: RequestLog): string | null {
  const recoveryAction = getDisplayRecoveryAction(log);
  if (recoveryAction == null) {
    return null;
  }
  return RECOVERY_ACTION_LABELS[recoveryAction];
}

function getRecoveryActionTone(log: RequestLog): DiagnosticTone | null {
  const recoveryAction = getDisplayRecoveryAction(log);
  if (recoveryAction == null) {
    return null;
  }
  return RECOVERY_ACTION_TONES[recoveryAction];
}

function getCommittedOutcome(
  terminalCause: TerminalCause,
  success: boolean,
): OutcomePresentation {
  switch (terminalCause) {
    case "clean_close":
      return {
        label: "Committed session closed cleanly",
        shortLabel: "Clean close",
        tone: "success",
      };
    case "client_disconnect":
      return {
        label: "Committed session ended on client disconnect",
        shortLabel: "Client disconnect",
        tone: "info",
      };
    case "upstream_transport_error":
      return {
        label: "Committed session ended on upstream transport error",
        shortLabel: "Transport error",
        tone: "warning",
      };
    case "upstream_semantic_error":
      return {
        label: "Provider reported a semantic error after service started",
        shortLabel: "Provider error",
        tone: "danger",
      };
    case "internal_error":
      return {
        label: "Gateway failed after the session had already committed",
        shortLabel: "Internal error",
        tone: "danger",
      };
    case "unknown":
      return success
        ? {
            label: "Committed session completed",
            shortLabel: "Committed",
            tone: "success",
          }
        : {
            label: "Committed session ended with an unknown cause",
            shortLabel: "Committed",
            tone: "warning",
          };
    default:
      return {
        label: "Committed session ended with an unexpected lifecycle state",
        shortLabel: "Committed",
        tone: success ? "warning" : "danger",
      };
  }
}

function getUncommittedOutcome(
  terminalCause: TerminalCause,
): OutcomePresentation {
  switch (terminalCause) {
    case "provider_unavailable":
      return {
        label: "No provider was available to start the session",
        shortLabel: "No provider",
        tone: "danger",
      };
    case "provider_configuration_error":
      return {
        label: "Selected provider was misconfigured before the session started",
        shortLabel: "Config error",
        tone: "danger",
      };
    case "upstream_semantic_error":
      return {
        label: "Provider failed before the session committed",
        shortLabel: "No service",
        tone: "danger",
      };
    case "upstream_handshake_rejected":
    case "client_upgrade_rejected":
      return {
        label: "Handshake was rejected before service started",
        shortLabel: "Handshake rejected",
        tone: "danger",
      };
    case "client_disconnect":
      return {
        label: "Client disconnected before the session committed",
        shortLabel: "Early disconnect",
        tone: "warning",
      };
    case "upstream_transport_error":
      return {
        label: "Transport ended before the session committed",
        shortLabel: "No service",
        tone: "warning",
      };
    case "internal_error":
      return {
        label: "Gateway failed before the session committed",
        shortLabel: "Internal error",
        tone: "danger",
      };
    case "clean_close":
      return {
        label: "Session closed before service committed",
        shortLabel: "Uncommitted",
        tone: "warning",
      };
    case "unknown":
    default:
      return {
        label: "Session never reached committed service",
        shortLabel: "Uncommitted",
        tone: "warning",
      };
  }
}

function getUnknownCommitOutcome(
  terminalCause: TerminalCause,
  success: boolean,
): OutcomePresentation {
  switch (terminalCause) {
    case "clean_close":
      return {
        label: "Session ended with a clean close",
        shortLabel: "Clean close",
        tone: "success",
      };
    case "client_disconnect":
      return {
        label: "Session ended on client disconnect",
        shortLabel: "Client disconnect",
        tone: "info",
      };
    case "upstream_transport_error":
      return {
        label: "Session ended on upstream transport error",
        shortLabel: "Transport error",
        tone: "warning",
      };
    case "upstream_semantic_error":
      return {
        label: "Provider reported a semantic error",
        shortLabel: "Provider error",
        tone: "danger",
      };
    case "upstream_handshake_rejected":
    case "client_upgrade_rejected":
      return {
        label: "Handshake was rejected",
        shortLabel: "Handshake rejected",
        tone: "danger",
      };
    case "internal_error":
      return {
        label: "Gateway hit an internal error",
        shortLabel: "Internal error",
        tone: "danger",
      };
    case "unknown":
    default:
      return success
        ? {
            label: "WebSocket session succeeded",
            shortLabel: "Success",
            tone: "success",
          }
        : {
            label: "WebSocket session failed",
            shortLabel: "Failed",
            tone: "danger",
          };
  }
}

function getReconnectRequiredOutcome(
  clientVisibilityState: ClientVisibilityState,
): OutcomePresentation {
  switch (clientVisibilityState) {
    case "visible":
      return {
        label: "Client-visible session ended with reconnect required",
        shortLabel: "Reconnect required",
        tone: "danger",
      };
    case "not_visible":
      return {
        label:
          "Session failed before output became visible and requires reconnect",
        shortLabel: "Reconnect required",
        tone: "danger",
      };
    case "unknown":
    default:
      return {
        label: "Session ended with reconnect required",
        shortLabel: "Reconnect required",
        tone: "danger",
      };
  }
}

function formatTableDetailLabel(
  statusCode: number,
  clientVisibilityLabel: string | null,
  commitmentLabel: string,
  recoveryActionLabel: string | null,
  probeOutcomeLabel: string | null,
): string | null {
  const details: string[] = [];

  if (statusCode > UNKNOWN_STATUS_CODE) {
    details.push(`Code ${statusCode}`);
  }

  if (clientVisibilityLabel) {
    details.push(clientVisibilityLabel);
  }

  details.push(commitmentLabel);

  if (
    recoveryActionLabel &&
    recoveryActionLabel !== RECOVERY_ACTION_LABELS.none
  ) {
    details.push(recoveryActionLabel);
  }

  if (probeOutcomeLabel) {
    details.push(probeOutcomeLabel);
  }

  return details.join(" • ");
}

export function getDiagnosticToneClass(tone: DiagnosticTone): string {
  switch (tone) {
    case "success":
      return BADGE_STYLES.SUCCESS;
    case "danger":
      return BADGE_STYLES.DANGER;
    case "warning":
      return BADGE_STYLES.WARNING;
    case "info":
      return BADGE_STYLES.INFO;
  }
}

function getNonWebSocketLifecyclePresentation(
  log: RequestLog,
): LogLifecyclePresentation {
  return {
    showLifecycle: false,
    outcomeLabel: log.success ? "Success" : "Failed",
    shortOutcomeLabel: log.success ? "Success" : "Failed",
    outcomeTone: log.success ? "success" : "danger",
    commitmentState: "unknown",
    commitmentLabel: COMMITMENT_LABELS.unknown,
    commitmentTone: COMMITMENT_TONES.unknown,
    clientVisibilityState: "unknown",
    clientVisibilityLabel: CLIENT_VISIBILITY_LABELS.unknown,
    clientVisibilityTone: CLIENT_VISIBILITY_TONES.unknown,
    terminalCause: "unknown",
    terminalCauseLabel: TERMINAL_CAUSE_LABELS.unknown,
    terminalCauseTone: TERMINAL_CAUSE_TONES.unknown,
    probeOutcome: "unknown",
    probeOutcomeLabel: null,
    commitSourceLabel: null,
    recoveryAction: null,
    recoveryActionLabel: null,
    recoveryActionTone: null,
    stickyWrittenLabel: getStickyWrittenLabel(log),
    tableDetailLabel:
      log.status_code > UNKNOWN_STATUS_CODE ? `Code ${log.status_code}` : null,
    shouldShowErrorDetails: !log.success && Boolean(log.error_msg),
  };
}

function getLifecycleOutcomePresentation(
  recoveryAction: RecoveryAction | null,
  clientVisibilityState: ClientVisibilityState,
  commitmentState: CommitmentState,
  terminalCause: TerminalCause,
  success: boolean,
): OutcomePresentation {
  if (recoveryAction === "reconnect_required") {
    return getReconnectRequiredOutcome(clientVisibilityState);
  }

  if (commitmentState === "committed") {
    return getCommittedOutcome(terminalCause, success);
  }

  if (commitmentState === "uncommitted") {
    return getUncommittedOutcome(terminalCause);
  }

  return getUnknownCommitOutcome(terminalCause, success);
}

export function getLogLifecyclePresentation(
  log: RequestLog,
): LogLifecyclePresentation {
  if (!log.is_websocket) {
    return getNonWebSocketLifecyclePresentation(log);
  }

  const commitmentState = getCommitmentState(log.session_committed);
  const clientVisibilityState = getClientVisibilityState(log.client_visible);
  const terminalCause = getTerminalCause(log);
  const probeOutcome = getProbeOutcome(log);
  const probeOutcomeLabel = getProbeOutcomeLabel(log);
  const recoveryAction = getRecoveryAction(log);
  const recoveryActionLabel = getRecoveryActionLabel(log);
  const outcome = getLifecycleOutcomePresentation(
    recoveryAction,
    clientVisibilityState,
    commitmentState,
    terminalCause,
    log.success,
  );

  return {
    showLifecycle: true,
    outcomeLabel: outcome.label,
    shortOutcomeLabel: outcome.shortLabel,
    outcomeTone: outcome.tone,
    commitmentState,
    commitmentLabel: COMMITMENT_LABELS[commitmentState],
    commitmentTone: COMMITMENT_TONES[commitmentState],
    clientVisibilityState,
    clientVisibilityLabel: CLIENT_VISIBILITY_LABELS[clientVisibilityState],
    clientVisibilityTone: CLIENT_VISIBILITY_TONES[clientVisibilityState],
    terminalCause,
    terminalCauseLabel: TERMINAL_CAUSE_LABELS[terminalCause],
    terminalCauseTone: TERMINAL_CAUSE_TONES[terminalCause],
    probeOutcome,
    probeOutcomeLabel,
    commitSourceLabel: getCommitSourceLabel(log),
    recoveryAction,
    recoveryActionLabel,
    recoveryActionTone: getRecoveryActionTone(log),
    stickyWrittenLabel: getStickyWrittenLabel(log),
    tableDetailLabel: formatTableDetailLabel(
      log.status_code,
      log.client_visible == null
        ? null
        : CLIENT_VISIBILITY_LABELS[clientVisibilityState],
      COMMITMENT_LABELS[commitmentState],
      recoveryActionLabel,
      probeOutcomeLabel,
    ),
    shouldShowErrorDetails: outcome.tone === "danger" && Boolean(log.error_msg),
  };
}
