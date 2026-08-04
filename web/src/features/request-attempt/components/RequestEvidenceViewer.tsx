import type { RequestEvidence } from "@/api/evidence-types";
import { parseRequestEvidenceJson } from "@/api/evidence/decoder";
import { SECTION_CARD_CLASS, SNIPPET_CLASS } from "../evidence/view-model";
import {
  GatewaySection,
  HandshakeSection,
  TransportV1Section,
  TransportV2Section,
  UpstreamEventSection,
} from "./RequestEvidenceSections";
import { SemanticEvidencePanel } from "./SemanticEvidencePanel";

interface RequestEvidenceViewerProps {
  evidenceJson?: string | null;
}

function hasRenderableSection(
  section: object | null | undefined,
): section is object {
  if (!section) {
    return false;
  }
  return Object.values(section).some(
    (value) =>
      value !== null &&
      value !== undefined &&
      (typeof value !== "string" || value.trim() !== ""),
  );
}

function UnavailableEvidence({
  evidenceJson,
  reason,
  detail,
}: {
  evidenceJson: string;
  reason: string;
  detail: string;
}) {
  return (
    <section
      className={SECTION_CARD_CLASS}
      aria-label="Structured evidence unavailable"
    >
      <h4 className="text-sm font-medium text-text-primary">
        Structured evidence unavailable
      </h4>
      <p className="mt-1 text-xs text-text-muted">
        The evidence was preserved but could not be decoded safely ({reason}).
      </p>
      <p className="mt-1 text-xs text-text-muted">{detail}</p>
      <details className="mt-2" aria-label="View raw evidence">
        <summary className="cursor-pointer text-xs text-text-secondary">
          View raw evidence
        </summary>
        <pre className={`${SNIPPET_CLASS} mt-2 max-h-64 overflow-auto`}>
          {evidenceJson}
        </pre>
      </details>
    </section>
  );
}

function EvidenceSections({ evidence }: { evidence: RequestEvidence }) {
  const showGateway = hasRenderableSection(evidence.gateway);
  const showTransport = hasRenderableSection(evidence.transport);
  const showHandshake = hasRenderableSection(evidence.upstream_handshake);
  const showUpstreamEvent = hasRenderableSection(evidence.upstream_event);
  const semantic = evidence.v === 2 ? evidence.semantic_error : null;
  if (
    !semantic &&
    !showGateway &&
    !showTransport &&
    !showHandshake &&
    !showUpstreamEvent
  ) {
    return (
      <p className="text-sm text-text-muted">
        No structured evidence captured.
      </p>
    );
  }
  return (
    <div className="space-y-3">
      {semantic && <SemanticEvidencePanel evidence={semantic} />}
      {showGateway && <GatewaySection gateway={evidence.gateway!} />}
      {showTransport && evidence.v === 2 && (
        <TransportV2Section transport={evidence.transport!} />
      )}
      {showTransport && evidence.v !== 2 && (
        <TransportV1Section transport={evidence.transport!} />
      )}
      {showHandshake && (
        <HandshakeSection handshake={evidence.upstream_handshake!} />
      )}
      {showUpstreamEvent && (
        <UpstreamEventSection event={evidence.upstream_event!} />
      )}
    </div>
  );
}

export function RequestEvidenceViewer({
  evidenceJson,
}: RequestEvidenceViewerProps) {
  const result = parseRequestEvidenceJson(evidenceJson);
  if (result.state === "absent") {
    return (
      <p className="text-sm text-text-muted">
        No structured evidence captured.
      </p>
    );
  }
  if (result.state === "unavailable") {
    return (
      <UnavailableEvidence
        evidenceJson={evidenceJson ?? ""}
        reason={result.reason.replaceAll("_", " ")}
        detail={result.detail}
      />
    );
  }
  return <EvidenceSections evidence={result.evidence} />;
}

export { TransportV2Section as RequestEvidenceTransportV2Section };
