import type {
  RequestEvidence,
  RequestEvidenceTransport,
  RequestEvidenceTransportV2,
  RequestEvidenceV2,
  TransportEvidenceKind,
  TransportEvidenceSignal,
  TransportEvidenceSource,
  TransportEvidenceStage,
} from "@/api/evidence-types";
import { parseRequestEvidenceJson } from "@/api/evidence/decoder";

const EVIDENCE_SCHEMA_V2 = 2 as const;

/** Stage-phrase mapping per the v2 evidence contract. */
const STAGE_PHRASES: Record<TransportEvidenceStage, string> = {
  pre_connection_visible: "before connection",
  pre_payload_visible: "before payload visible",
  post_payload_visible: "after payload visible",
};

/**
 * Kind tokens are surfaced as-is (no humanization) so the summary text stays
 * greppable in logs and matches the on-wire value — `protocol_error` should
 * read `protocol_error`, not `protocol error`, so operators searching for a
 * kind can correlate UI output with raw evidence.
 */
const KIND_PHRASES: Record<TransportEvidenceKind, string> = {
  timeout: "timeout",
  disconnect: "disconnect",
  protocol_error: "protocol_error",
  local_error: "local_error",
};

const SOURCE_PHRASES: Record<TransportEvidenceSource, string> = {
  upstream: "upstream",
  client: "client",
};

/**
 * Signals the plan explicitly excludes from first-choice list summaries.
 *
 * Pure client close-without-status carries no root-cause fault; surfacing it at
 * the top of a list row would mislead the reader. Detail views still render the
 * evidence in full via `RequestEvidenceTransportV2` so diagnostic information
 * is never lost.
 */
const SUPPRESSED_LIST_SIGNALS: ReadonlySet<TransportEvidenceSignal> = new Set([
  "close_without_status",
]);

export function parseRequestEvidence(
  evidenceJson?: string | null,
): RequestEvidence | null {
  const result = parseRequestEvidenceJson(evidenceJson);
  return result.state === "available" ? result.evidence : null;
}

export function isV2Evidence(
  evidence: RequestEvidence | null | undefined,
): evidence is RequestEvidenceV2 {
  return evidence?.v === EVIDENCE_SCHEMA_V2;
}

/**
 * Narrow an evidence transport payload to the v2 shape when the envelope is
 * v2. Returns null for v1 payloads so callers never accidentally read v2
 * fields from historical data.
 */
export function getV2Transport(
  evidence: RequestEvidence | null | undefined,
): RequestEvidenceTransportV2 | null {
  if (!isV2Evidence(evidence)) {
    return null;
  }
  return evidence.transport ?? null;
}

/**
 * Narrow an evidence transport payload to the v1 shape, mirroring
 * `getV2Transport`. Keeps historical rows isolated from v2 types so the v1
 * renderer stays uncoupled from the v2 schema.
 */
export function getV1Transport(
  evidence: RequestEvidence | null | undefined,
): RequestEvidenceTransport | null {
  if (!evidence || isV2Evidence(evidence)) {
    return null;
  }
  return evidence.transport ?? null;
}

/**
 * Format a v2 transport observation into a list-summary phrase.
 *
 * Shape: `{source} {kind} ({signal}) {stage-phrase}`, e.g.
 * `upstream timeout (sse_idle_timeout) before payload visible`.
 *
 * Returns null when the observation lacks enough structure to construct a
 * meaningful phrase, or when the signal is explicitly excluded from list
 * summaries (detail views still render it — see SUPPRESSED_LIST_SIGNALS).
 */
export function formatTransportSummary(
  transport: RequestEvidenceTransportV2 | null | undefined,
): string | null {
  if (!transport) {
    return null;
  }
  const { source, kind, signal, stage } = transport;
  if (!source || !kind || !signal) {
    return null;
  }
  if (SUPPRESSED_LIST_SIGNALS.has(signal)) {
    return null;
  }
  const sourcePhrase = SOURCE_PHRASES[source];
  const kindPhrase = KIND_PHRASES[kind];
  const stagePhrase = stage ? STAGE_PHRASES[stage] : null;

  const head = `${sourcePhrase} ${kindPhrase} (${signal})`;
  return stagePhrase ? `${head} ${stagePhrase}` : head;
}

/** Stage-phrase lookup used by detail renderers for consistent copy. */
export function getTransportStagePhrase(
  stage: TransportEvidenceStage | undefined,
): string | null {
  if (!stage) {
    return null;
  }
  return STAGE_PHRASES[stage] ?? null;
}

/** Kind label lookup used by detail renderers. */
export function getTransportKindLabel(
  kind: TransportEvidenceKind | undefined,
): string | null {
  if (!kind) {
    return null;
  }
  return KIND_PHRASES[kind] ?? null;
}

/** Source label lookup used by detail renderers. */
export function getTransportSourceLabel(
  source: TransportEvidenceSource | undefined,
): string | null {
  if (!source) {
    return null;
  }
  return SOURCE_PHRASES[source] ?? null;
}

/**
 * Summary text suitable for list rows and terse previews.
 *
 * Priority mirrors the gateway-first / transport-second / event-third
 * precedence used by historical rows, with one structural change: a v2
 * transport observation is formatted through `formatTransportSummary` rather
 * than pulling the freeform `message_snippet` (which no longer exists in v2).
 */
export function getRequestEvidenceSummary(
  evidenceJson?: string | null,
): string | null {
  const evidence = parseRequestEvidence(evidenceJson);
  if (!evidence) {
    return null;
  }

  const gatewaySnippet = evidence.gateway?.terminal_message_snippet;
  if (gatewaySnippet) {
    return gatewaySnippet;
  }

  if (isV2Evidence(evidence)) {
    const v2Transport = getV2Transport(evidence);
    const transportSummary = formatTransportSummary(v2Transport);
    if (transportSummary) {
      return transportSummary;
    }
    // Raw error snippet stays as a last-resort transport fallback — v2 keeps it
    // as the freeform escape hatch while the structured phrase remains primary.
    if (v2Transport?.raw_error_snippet) {
      return v2Transport.raw_error_snippet;
    }
  } else {
    const v1Transport = getV1Transport(evidence);
    if (v1Transport?.message_snippet) {
      return v1Transport.message_snippet;
    }
    if (v1Transport?.raw_error_snippet) {
      return v1Transport.raw_error_snippet;
    }
  }

  return (
    evidence.upstream_event?.message_snippet ||
    evidence.upstream_handshake?.body_snippet ||
    evidence.upstream_event?.raw_payload_snippet ||
    null
  );
}
