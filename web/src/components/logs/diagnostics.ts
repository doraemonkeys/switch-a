import type {
  ClientAction,
  CommitSource,
  CompletionState,
  LegacyRequestLog,
  NormalizedRequestLog,
  RequestLog,
  SemanticsVersion,
  ServiceOutcome,
  TerminationActor,
  TerminationReason,
} from "../../api/types";
import { BADGE_STYLES, getStatusCodeBadgeClass } from "../../lib/utils";
import {
  formatTransportSummary,
  getRequestEvidenceSummary,
  getV2Transport,
  isV2Evidence,
  parseRequestEvidence,
} from "@/features/request-attempt/evidence/presentation";

export type DiagnosticTone = "success" | "danger" | "warning" | "info";

export interface LogLifecyclePresentation {
  isLegacy: boolean;
  semanticsVersion: SemanticsVersion;
  semanticsVersionLabel: string;
  showLifecycle: boolean;
  outcomeLabel: string;
  shortOutcomeLabel: string;
  outcomeTone: DiagnosticTone;
  serviceOutcome: ServiceOutcome | null;
  completionStateLabel: string | null;
  completionStateTone: DiagnosticTone | null;
  clientAction: ClientAction | null;
  clientActionLabel: string | null;
  clientActionTone: DiagnosticTone | null;
  terminationActorLabel: string | null;
  terminationReason: TerminationReason | null;
  terminationReasonLabel: string | null;
  terminationReasonTone: DiagnosticTone | null;
  commitmentLabel: string | null;
  commitmentTone: DiagnosticTone | null;
  clientVisibilityLabel: string | null;
  clientVisibilityTone: DiagnosticTone | null;
  commitSourceLabel: string | null;
  transportStatusLabel: string | null;
  transportTone: DiagnosticTone | null;
  legacyNote: string | null;
  tableDetailLabel: string | null;
}

const LEGACY_NOTE =
  "This row predates normalized request-log assessment and is rendered as legacy without remapping.";
const NONE_CLIENT_ACTION = "none";

const SEMANTICS_VERSION_LABELS: Record<SemanticsVersion, string> = {
  legacy_pre_assessment: "Legacy",
  normalized_v1: "Normalized",
};

const SERVICE_OUTCOME_PRESENTATION: Record<
  ServiceOutcome,
  { label: string; shortLabel: string; tone: DiagnosticTone }
> = {
  completed: {
    label: "Service completed",
    shortLabel: "Completed",
    tone: "success",
  },
  interrupted: {
    label: "Service was interrupted",
    shortLabel: "Interrupted",
    tone: "danger",
  },
  never_started: {
    label: "Service never started",
    shortLabel: "Never started",
    tone: "warning",
  },
  abandoned_by_client: {
    label: "Service was abandoned by the client",
    shortLabel: "Abandoned",
    tone: "info",
  },
  unknown: {
    label: "Service outcome is unknown",
    shortLabel: "Unknown",
    tone: "warning",
  },
};

const COMPLETION_STATE_PRESENTATION: Record<
  CompletionState,
  { label: string; tone: DiagnosticTone }
> = {
  completed: { label: "Completed", tone: "success" },
  incomplete: { label: "Incomplete", tone: "warning" },
  unknown: { label: "Unknown", tone: "info" },
};

const CLIENT_ACTION_PRESENTATION: Record<
  ClientAction,
  { label: string; tone: DiagnosticTone }
> = {
  none: { label: "None", tone: "info" },
  transparent_retry: { label: "Transparent Retry", tone: "info" },
  reconnect_required: { label: "Reconnect Required", tone: "danger" },
};

const TERMINATION_REASON_PRESENTATION: Record<
  TerminationReason,
  { label: string; tone: DiagnosticTone }
> = {
  provider_unavailable: { label: "Provider Unavailable", tone: "danger" },
  provider_configuration_error: {
    label: "Provider Configuration Error",
    tone: "danger",
  },
  usage_limit_reached: { label: "Usage Limit Reached", tone: "danger" },
  websocket_connection_limit_reached: {
    label: "WebSocket Connection Limit Reached",
    tone: "info",
  },
  client_request_error: { label: "Client Request Error", tone: "warning" },
  client_disconnect: { label: "Client Disconnect", tone: "info" },
  transport_error: { label: "Transport Error", tone: "warning" },
  upstream_semantic_error: {
    label: "Upstream Semantic Error",
    tone: "danger",
  },
  upstream_handshake_rejected: {
    label: "Upstream Handshake Rejected",
    tone: "danger",
  },
  client_upgrade_rejected: {
    label: "Client Upgrade Rejected",
    tone: "danger",
  },
  internal_error: { label: "Internal Error", tone: "danger" },
  clean_close: { label: "Clean Close", tone: "success" },
  unknown: { label: "Unknown", tone: "info" },
};

const TERMINATION_ACTOR_LABELS: Record<TerminationActor, string> = {
  client: "Client",
  gateway: "Gateway",
  upstream: "Upstream",
  internal: "Internal",
  unknown: "Unknown",
};

const COMMIT_SOURCE_LABELS: Record<CommitSource, string> = {
  semantic_event: "Semantic Event",
  upstream_message: "First Upstream Message",
  unknown: "Unknown",
};

function getCommitmentPresentation(
  sessionCommitted: RequestLog["session_committed"],
): { label: string; tone: DiagnosticTone } | null {
  if (sessionCommitted === true) {
    return { label: "Committed", tone: "success" };
  }
  if (sessionCommitted === false) {
    return { label: "Not Committed", tone: "warning" };
  }
  return null;
}

function getClientVisibilityPresentation(
  clientVisible: RequestLog["client_visible"],
): { label: string; tone: DiagnosticTone } | null {
  if (clientVisible === true) {
    return { label: "Visible", tone: "info" };
  }
  if (clientVisible === false) {
    return { label: "Not Visible", tone: "warning" };
  }
  return null;
}

function getTransportTone(statusCode: number): DiagnosticTone {
  if (statusCode === 101) {
    return "info";
  }
  if (statusCode >= 200 && statusCode < 400) {
    return "success";
  }
  if (statusCode >= 400) {
    return "danger";
  }
  return "warning";
}

function getTransportStatusLabel(log: RequestLog): string | null {
  if (log.client_transport_status_code == null) {
    return null;
  }
  if (log.is_websocket && log.client_transport_status_code === 101) {
    return "101 Upgrade";
  }
  return String(log.client_transport_status_code);
}

function buildTableDetailLabel(log: RequestLog): string | null {
  if (log.semantics_version === "legacy_pre_assessment") {
    return "Legacy row";
  }

  const details: string[] = [];
  const transportStatusLabel = getTransportStatusLabel(log);

  if (transportStatusLabel) {
    details.push(`Code ${transportStatusLabel}`);
  }

  if (log.client_action && log.client_action !== NONE_CLIENT_ACTION) {
    details.push(CLIENT_ACTION_PRESENTATION[log.client_action].label);
  }

  if (log.termination_reason) {
    details.push(TERMINATION_REASON_PRESENTATION[log.termination_reason].label);
  }

  if (log.commit_source) {
    details.push(COMMIT_SOURCE_LABELS[log.commit_source]);
  }

  return details.length > 0 ? details.join(" • ") : null;
}

export function isLegacyRequestLog(log: RequestLog): log is LegacyRequestLog {
  return log.semantics_version === "legacy_pre_assessment";
}

export function isNormalizedRequestLog(
  log: RequestLog,
): log is NormalizedRequestLog {
  return log.semantics_version === "normalized_v1";
}

export function getPrimaryProviderLabel(log: RequestLog): string {
  return log.is_websocket && !isLegacyRequestLog(log)
    ? "Outcome Provider"
    : "Provider";
}

export function getLogEvidenceSummary(log: RequestLog): string | null {
  return getRequestEvidenceSummary(log.session_evidence_json);
}

/**
 * Resolve a list-ready transport summary for a log's session evidence.
 *
 * v1 payloads fall through this helper (returns null) because v1 `transport`
 * lacks the structured `kind`/`signal`/`stage` fields the summary is built
 * from. v1 callers should keep using `getLogEvidenceSummary`, which still
 * renders v1 `message_snippet`.
 */
export function getLogTransportSummary(log: RequestLog): string | null {
  const evidence = parseRequestEvidence(log.session_evidence_json);
  if (!isV2Evidence(evidence)) {
    return null;
  }
  return formatTransportSummary(getV2Transport(evidence));
}

export function getServiceOutcomeLabel(
  serviceOutcome?: ServiceOutcome | null,
): string | null {
  if (!serviceOutcome) {
    return null;
  }
  return SERVICE_OUTCOME_PRESENTATION[serviceOutcome].label;
}

export function getClientActionLabel(
  clientAction?: ClientAction | null,
): string | null {
  if (!clientAction) {
    return null;
  }
  return CLIENT_ACTION_PRESENTATION[clientAction].label;
}

export function getTerminationReasonLabel(
  terminationReason?: TerminationReason | null,
): string | null {
  if (!terminationReason) {
    return null;
  }
  return TERMINATION_REASON_PRESENTATION[terminationReason].label;
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

export function getTransportBadgeClass(
  clientTransportStatusCode?: number | null,
): string {
  if (clientTransportStatusCode == null) {
    return BADGE_STYLES.INFO;
  }
  if (clientTransportStatusCode === 101) {
    return BADGE_STYLES.INFO;
  }
  return getStatusCodeBadgeClass(clientTransportStatusCode);
}

export function getLogLifecyclePresentation(
  log: RequestLog,
): LogLifecyclePresentation {
  if (isLegacyRequestLog(log)) {
    return {
      isLegacy: true,
      semanticsVersion: log.semantics_version,
      semanticsVersionLabel: SEMANTICS_VERSION_LABELS[log.semantics_version],
      showLifecycle: false,
      outcomeLabel: "Legacy pre-assessment row",
      shortOutcomeLabel: "Legacy",
      outcomeTone: "warning",
      serviceOutcome: null,
      completionStateLabel: null,
      completionStateTone: null,
      clientAction: null,
      clientActionLabel: null,
      clientActionTone: null,
      terminationActorLabel: null,
      terminationReason: null,
      terminationReasonLabel: null,
      terminationReasonTone: null,
      commitmentLabel: null,
      commitmentTone: null,
      clientVisibilityLabel: null,
      clientVisibilityTone: null,
      commitSourceLabel: null,
      transportStatusLabel: getTransportStatusLabel(log),
      transportTone:
        log.client_transport_status_code != null
          ? getTransportTone(log.client_transport_status_code)
          : null,
      legacyNote: LEGACY_NOTE,
      tableDetailLabel: buildTableDetailLabel(log),
    };
  }

  const normalizedLog = log;
  const outcomePresentation =
    SERVICE_OUTCOME_PRESENTATION[normalizedLog.service_outcome];
  const completionPresentation =
    COMPLETION_STATE_PRESENTATION[normalizedLog.completion_state];
  const clientActionPresentation =
    CLIENT_ACTION_PRESENTATION[normalizedLog.client_action];
  const terminationReasonPresentation = normalizedLog.termination_reason
    ? TERMINATION_REASON_PRESENTATION[normalizedLog.termination_reason]
    : null;
  const commitmentPresentation = getCommitmentPresentation(
    normalizedLog.session_committed,
  );
  const visibilityPresentation = getClientVisibilityPresentation(
    normalizedLog.client_visible,
  );
  const transportStatusLabel = getTransportStatusLabel(normalizedLog);

  return {
    isLegacy: false,
    semanticsVersion: normalizedLog.semantics_version,
    semanticsVersionLabel:
      SEMANTICS_VERSION_LABELS[normalizedLog.semantics_version],
    showLifecycle: normalizedLog.is_websocket,
    outcomeLabel: outcomePresentation.label,
    shortOutcomeLabel: outcomePresentation.shortLabel,
    outcomeTone: outcomePresentation.tone,
    serviceOutcome: normalizedLog.service_outcome,
    completionStateLabel: completionPresentation?.label ?? null,
    completionStateTone: completionPresentation?.tone ?? null,
    clientAction: normalizedLog.client_action,
    clientActionLabel: clientActionPresentation?.label ?? null,
    clientActionTone: clientActionPresentation?.tone ?? null,
    terminationActorLabel: normalizedLog.termination_actor
      ? TERMINATION_ACTOR_LABELS[normalizedLog.termination_actor]
      : null,
    terminationReason: normalizedLog.termination_reason,
    terminationReasonLabel: terminationReasonPresentation?.label ?? null,
    terminationReasonTone: terminationReasonPresentation?.tone ?? null,
    commitmentLabel: commitmentPresentation?.label ?? null,
    commitmentTone: commitmentPresentation?.tone ?? null,
    clientVisibilityLabel: visibilityPresentation?.label ?? null,
    clientVisibilityTone: visibilityPresentation?.tone ?? null,
    commitSourceLabel: normalizedLog.commit_source
      ? COMMIT_SOURCE_LABELS[normalizedLog.commit_source]
      : null,
    transportStatusLabel,
    transportTone:
      normalizedLog.client_transport_status_code != null
        ? getTransportTone(normalizedLog.client_transport_status_code)
        : null,
    legacyNote: null,
    tableDetailLabel: buildTableDetailLabel(normalizedLog),
  };
}
