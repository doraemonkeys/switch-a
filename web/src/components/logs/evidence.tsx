import type { ReactNode } from "react";
import type {
  RequestEvidence,
  RequestEvidenceTransport,
  RequestEvidenceTransportV2,
} from "../../api/types";
import { getStatusCodeBadgeClass } from "../../lib/utils";
import {
  getTransportKindLabel,
  getTransportSourceLabel,
  getTransportStagePhrase,
  getV1Transport,
  getV2Transport,
  isV2Evidence,
  parseRequestEvidence,
} from "./evidence-utils";

const SECTION_CARD_CLASS =
  "rounded-lg border border-border-light bg-bg-tertiary/60 p-3";
const FIELD_LABEL_CLASS = "text-[11px] uppercase tracking-wide text-text-muted";
const SNIPPET_CLASS =
  "mt-1 rounded border border-border-light bg-bg-secondary p-2 text-xs font-mono text-text-secondary whitespace-pre-wrap break-words";

interface EvidenceFieldProps {
  label: string;
  value: ReactNode;
}

function EvidenceField({ label, value }: EvidenceFieldProps) {
  return (
    <div>
      <p className={FIELD_LABEL_CLASS}>{label}</p>
      <div className="mt-1 text-sm text-text-primary">{value}</div>
    </div>
  );
}

function EvidenceSnippet({
  label,
  text,
}: {
  label: string;
  text: string | undefined;
}) {
  if (!text) {
    return null;
  }

  return (
    <div>
      <p className={FIELD_LABEL_CLASS}>{label}</p>
      <pre className={SNIPPET_CLASS}>{text}</pre>
    </div>
  );
}

function hasRenderableValue(value: unknown): boolean {
  if (value === null || value === undefined) {
    return false;
  }
  if (typeof value === "string") {
    return value.trim() !== "";
  }
  return true;
}

function hasRenderableSection(
  section: object | null | undefined,
): section is object {
  if (!section) {
    return false;
  }

  return Object.values(section).some(hasRenderableValue);
}

interface RequestEvidenceViewerProps {
  evidenceJson?: string | null;
}

export function RequestEvidenceViewer({
  evidenceJson,
}: RequestEvidenceViewerProps) {
  if (!evidenceJson || evidenceJson.trim() === "") {
    return (
      <p className="text-sm text-text-muted">
        No structured evidence captured.
      </p>
    );
  }

  const evidence = parseRequestEvidence(evidenceJson);
  if (!evidence) {
    return (
      <div className={SECTION_CARD_CLASS}>
        <p className="text-sm text-text-primary font-medium">Raw evidence</p>
        <pre className={`${SNIPPET_CLASS} mt-2`}>{evidenceJson}</pre>
      </div>
    );
  }

  const showGateway = hasRenderableSection(evidence.gateway);
  const showTransport = hasRenderableSection(evidence.transport);
  const showHandshake = hasRenderableSection(evidence.upstream_handshake);
  const showUpstreamEvent = hasRenderableSection(evidence.upstream_event);

  if (!showGateway && !showTransport && !showHandshake && !showUpstreamEvent) {
    return (
      <p className="text-sm text-text-muted">
        No structured evidence captured.
      </p>
    );
  }

  // Route to the v2 transport renderer when the envelope carries "v": 2.
  // Historical rows (v missing or 1) keep flowing through the v1 renderer —
  // the two renderers never share branches, ensuring v2 changes cannot regress
  // legacy rendering and vice versa.
  const renderTransport = () => {
    if (!showTransport) {
      return null;
    }
    if (isV2Evidence(evidence)) {
      const v2Transport = getV2Transport(evidence);
      return v2Transport ? (
        <RequestEvidenceTransportV2Section transport={v2Transport} />
      ) : null;
    }
    const v1Transport = getV1Transport(evidence);
    return v1Transport ? (
      <RequestEvidenceTransportV1Section transport={v1Transport} />
    ) : null;
  };

  return (
    <div className="space-y-3">
      {showGateway && <GatewaySection evidence={evidence} />}
      {renderTransport()}
      {showHandshake && <HandshakeSection evidence={evidence} />}
      {showUpstreamEvent && <UpstreamEventSection evidence={evidence} />}
    </div>
  );
}

function GatewaySection({ evidence }: { evidence: RequestEvidence }) {
  return (
    <div className={SECTION_CARD_CLASS}>
      <p className="text-sm font-medium text-text-primary">Gateway</p>
      <div className="mt-3 grid gap-3 sm:grid-cols-2">
        {evidence.gateway?.terminal_status_code !== undefined && (
          <EvidenceField
            label="Terminal Status"
            value={
              <span
                className={`inline-flex items-center rounded px-2 py-0.5 text-xs font-medium ${getStatusCodeBadgeClass(evidence.gateway.terminal_status_code)}`}
              >
                {evidence.gateway.terminal_status_code}
              </span>
            }
          />
        )}
        {evidence.gateway?.terminal_error_code && (
          <EvidenceField
            label="Terminal Error Code"
            value={
              <span className="font-mono">
                {evidence.gateway.terminal_error_code}
              </span>
            }
          />
        )}
      </div>
      <EvidenceSnippet
        label="Terminal Message"
        text={evidence.gateway?.terminal_message_snippet}
      />
    </div>
  );
}

function RequestEvidenceTransportV1Section({
  transport,
}: {
  transport: RequestEvidenceTransport;
}) {
  return (
    <div className={SECTION_CARD_CLASS}>
      <p className="text-sm font-medium text-text-primary">Transport</p>
      <div className="mt-3 grid gap-3 sm:grid-cols-2">
        {transport.source && (
          <EvidenceField label="Source" value={transport.source} />
        )}
        {transport.is_timeout !== undefined && (
          <EvidenceField
            label="Timeout"
            value={transport.is_timeout ? "Yes" : "No"}
          />
        )}
        {transport.is_client_cancel !== undefined && (
          <EvidenceField
            label="Client Cancel"
            value={transport.is_client_cancel ? "Yes" : "No"}
          />
        )}
      </div>
      <EvidenceSnippet label="Message" text={transport.message_snippet} />
      <EvidenceSnippet label="Raw Error" text={transport.raw_error_snippet} />
    </div>
  );
}

/**
 * v2 transport renderer. Parses `source`, `stage` (three-state), `kind`,
 * `signal`, `close_code`, `close_reason_snippet`, and `raw_error_snippet`.
 *
 * Intentionally contains no v1 fallback branches — the routing in
 * `RequestEvidenceViewer` picks the renderer by `evidence.v`, so this
 * component only ever sees a v2 payload.
 */
export function RequestEvidenceTransportV2Section({
  transport,
}: {
  transport: RequestEvidenceTransportV2;
}) {
  const stagePhrase = getTransportStagePhrase(transport.stage);
  const kindLabel = getTransportKindLabel(transport.kind);
  const sourceLabel = getTransportSourceLabel(transport.source);

  return (
    <div className={SECTION_CARD_CLASS}>
      <p className="text-sm font-medium text-text-primary">Transport</p>
      <div className="mt-3 grid gap-3 sm:grid-cols-2">
        {sourceLabel && <EvidenceField label="Source" value={sourceLabel} />}
        {kindLabel && <EvidenceField label="Kind" value={kindLabel} />}
        {transport.signal && (
          <EvidenceField
            label="Signal"
            value={<span className="font-mono">{transport.signal}</span>}
          />
        )}
        {stagePhrase && <EvidenceField label="Stage" value={stagePhrase} />}
        {transport.close_code !== undefined && (
          <EvidenceField
            label="Close Code"
            value={<span className="font-mono">{transport.close_code}</span>}
          />
        )}
      </div>
      <EvidenceSnippet
        label="Close Reason"
        text={transport.close_reason_snippet}
      />
      <EvidenceSnippet label="Raw Error" text={transport.raw_error_snippet} />
    </div>
  );
}

function HandshakeSection({ evidence }: { evidence: RequestEvidence }) {
  return (
    <div className={SECTION_CARD_CLASS}>
      <p className="text-sm font-medium text-text-primary">
        Upstream Handshake
      </p>
      {evidence.upstream_handshake?.status_code !== undefined && (
        <div className="mt-3">
          <EvidenceField
            label="Status"
            value={
              <span
                className={`inline-flex items-center rounded px-2 py-0.5 text-xs font-medium ${getStatusCodeBadgeClass(evidence.upstream_handshake.status_code)}`}
              >
                {evidence.upstream_handshake.status_code}
              </span>
            }
          />
        </div>
      )}
      <EvidenceSnippet
        label="Body Snippet"
        text={evidence.upstream_handshake?.body_snippet}
      />
    </div>
  );
}

function UpstreamEventSection({ evidence }: { evidence: RequestEvidence }) {
  return (
    <div className={SECTION_CARD_CLASS}>
      <p className="text-sm font-medium text-text-primary">Upstream Event</p>
      <div className="mt-3 grid gap-3 sm:grid-cols-2">
        {evidence.upstream_event?.envelope_type && (
          <EvidenceField
            label="Envelope Type"
            value={evidence.upstream_event.envelope_type}
          />
        )}
        {evidence.upstream_event?.provider_error_type && (
          <EvidenceField
            label="Provider Error Type"
            value={evidence.upstream_event.provider_error_type}
          />
        )}
        {evidence.upstream_event?.provider_error_code && (
          <EvidenceField
            label="Provider Error Code"
            value={
              <span className="font-mono">
                {evidence.upstream_event.provider_error_code}
              </span>
            }
          />
        )}
        {evidence.upstream_event?.status_code !== undefined && (
          <EvidenceField
            label="Status"
            value={
              <span
                className={`inline-flex items-center rounded px-2 py-0.5 text-xs font-medium ${getStatusCodeBadgeClass(evidence.upstream_event.status_code)}`}
              >
                {evidence.upstream_event.status_code}
              </span>
            }
          />
        )}
      </div>
      <EvidenceSnippet
        label="Message"
        text={evidence.upstream_event?.message_snippet}
      />
      <EvidenceSnippet
        label="Raw Payload"
        text={evidence.upstream_event?.raw_payload_snippet}
      />
    </div>
  );
}
