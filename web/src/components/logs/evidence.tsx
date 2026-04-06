import type { ReactNode } from "react";
import { getStatusCodeBadgeClass } from "../../lib/utils";
import { parseRequestEvidence } from "./evidence-utils";

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

  return (
    <div className="space-y-3">
      {showGateway && (
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
      )}
      {showTransport && (
        <div className={SECTION_CARD_CLASS}>
          <p className="text-sm font-medium text-text-primary">Transport</p>
          <div className="mt-3 grid gap-3 sm:grid-cols-2">
            {evidence.transport?.source && (
              <EvidenceField label="Source" value={evidence.transport.source} />
            )}
            {evidence.transport?.is_timeout !== undefined && (
              <EvidenceField
                label="Timeout"
                value={evidence.transport.is_timeout ? "Yes" : "No"}
              />
            )}
            {evidence.transport?.is_client_cancel !== undefined && (
              <EvidenceField
                label="Client Cancel"
                value={evidence.transport.is_client_cancel ? "Yes" : "No"}
              />
            )}
          </div>
          <EvidenceSnippet
            label="Message"
            text={evidence.transport?.message_snippet}
          />
          <EvidenceSnippet
            label="Raw Error"
            text={evidence.transport?.raw_error_snippet}
          />
        </div>
      )}
      {showHandshake && (
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
      )}
      {showUpstreamEvent && (
        <div className={SECTION_CARD_CLASS}>
          <p className="text-sm font-medium text-text-primary">
            Upstream Event
          </p>
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
      )}
    </div>
  );
}
