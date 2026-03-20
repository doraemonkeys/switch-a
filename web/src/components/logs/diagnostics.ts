import type { CommitSource, RequestLog, TerminalCause } from "../../api/types";
import { BADGE_STYLES } from "../../lib/utils";

export type DiagnosticTone = "success" | "danger" | "warning" | "info";
export type CommitmentState = "committed" | "uncommitted" | "unknown";

export interface LogLifecyclePresentation {
  showLifecycle: boolean;
  outcomeLabel: string;
  shortOutcomeLabel: string;
  outcomeTone: DiagnosticTone;
  commitmentState: CommitmentState;
  commitmentLabel: string;
  commitmentTone: DiagnosticTone;
  terminalCause: TerminalCause;
  terminalCauseLabel: string;
  terminalCauseTone: DiagnosticTone;
  commitSourceLabel: string | null;
  stickyWrittenLabel: string | null;
  tableDetailLabel: string | null;
  shouldShowErrorDetails: boolean;
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

function formatTableDetailLabel(
  statusCode: number,
  commitmentLabel: string,
): string | null {
  const details: string[] = [];

  if (statusCode > UNKNOWN_STATUS_CODE) {
    details.push(`Code ${statusCode}`);
  }

  details.push(commitmentLabel);

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

export function getLogLifecyclePresentation(
  log: RequestLog,
): LogLifecyclePresentation {
  if (!log.is_websocket) {
    return {
      showLifecycle: false,
      outcomeLabel: log.success ? "Success" : "Failed",
      shortOutcomeLabel: log.success ? "Success" : "Failed",
      outcomeTone: log.success ? "success" : "danger",
      commitmentState: "unknown",
      commitmentLabel: COMMITMENT_LABELS.unknown,
      commitmentTone: COMMITMENT_TONES.unknown,
      terminalCause: "unknown",
      terminalCauseLabel: TERMINAL_CAUSE_LABELS.unknown,
      terminalCauseTone: TERMINAL_CAUSE_TONES.unknown,
      commitSourceLabel: null,
      stickyWrittenLabel: getStickyWrittenLabel(log),
      tableDetailLabel:
        log.status_code > UNKNOWN_STATUS_CODE
          ? `Code ${log.status_code}`
          : null,
      shouldShowErrorDetails: !log.success && Boolean(log.error_msg),
    };
  }

  const commitmentState = getCommitmentState(log.session_committed);
  const terminalCause = getTerminalCause(log);

  let outcome: OutcomePresentation;
  if (commitmentState === "committed") {
    outcome = getCommittedOutcome(terminalCause, log.success);
  } else if (commitmentState === "uncommitted") {
    outcome = getUncommittedOutcome(terminalCause);
  } else {
    outcome = getUnknownCommitOutcome(terminalCause, log.success);
  }

  return {
    showLifecycle: true,
    outcomeLabel: outcome.label,
    shortOutcomeLabel: outcome.shortLabel,
    outcomeTone: outcome.tone,
    commitmentState,
    commitmentLabel: COMMITMENT_LABELS[commitmentState],
    commitmentTone: COMMITMENT_TONES[commitmentState],
    terminalCause,
    terminalCauseLabel: TERMINAL_CAUSE_LABELS[terminalCause],
    terminalCauseTone: TERMINAL_CAUSE_TONES[terminalCause],
    commitSourceLabel: getCommitSourceLabel(log),
    stickyWrittenLabel: getStickyWrittenLabel(log),
    tableDetailLabel: formatTableDetailLabel(
      log.status_code,
      COMMITMENT_LABELS[commitmentState],
    ),
    shouldShowErrorDetails: outcome.tone === "danger" && Boolean(log.error_msg),
  };
}
