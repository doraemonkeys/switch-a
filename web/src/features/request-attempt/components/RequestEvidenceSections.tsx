import type {
  RequestEvidenceGateway,
  RequestEvidenceTransport,
  RequestEvidenceTransportV2,
  RequestEvidenceUpstreamEvent,
  RequestEvidenceUpstreamHandshake,
} from "@/api/evidence-types";
import { getStatusCodeBadgeClass } from "@/lib/utils";
import {
  getTransportKindLabel,
  getTransportSourceLabel,
  getTransportStagePhrase,
} from "../evidence/presentation";
import {
  EvidenceCode,
  EvidenceField,
  EvidenceGrid,
  EvidenceSection,
  EvidenceSnippet,
} from "./EvidencePrimitives";

function StatusValue({ value }: { value: number }) {
  return (
    <span
      className={`inline-flex items-center rounded px-2 py-0.5 text-xs font-medium ${getStatusCodeBadgeClass(value)}`}
    >
      {value}
    </span>
  );
}

export function GatewaySection({
  gateway,
}: {
  gateway: RequestEvidenceGateway;
}) {
  return (
    <EvidenceSection title="Gateway">
      <EvidenceGrid>
        {gateway.terminal_status_code !== undefined && (
          <EvidenceField
            label="Terminal Status"
            value={<StatusValue value={gateway.terminal_status_code} />}
          />
        )}
        {gateway.terminal_error_code && (
          <EvidenceField
            label="Terminal Error Code"
            value={<EvidenceCode>{gateway.terminal_error_code}</EvidenceCode>}
          />
        )}
      </EvidenceGrid>
      <EvidenceSnippet
        label="Terminal Message"
        text={gateway.terminal_message_snippet}
      />
    </EvidenceSection>
  );
}

export function TransportV1Section({
  transport,
}: {
  transport: RequestEvidenceTransport;
}) {
  return (
    <EvidenceSection title="Transport">
      <EvidenceGrid>
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
      </EvidenceGrid>
      <EvidenceSnippet label="Message" text={transport.message_snippet} />
      <EvidenceSnippet label="Raw Error" text={transport.raw_error_snippet} />
    </EvidenceSection>
  );
}

export function TransportV2Section({
  transport,
}: {
  transport: RequestEvidenceTransportV2;
}) {
  const stagePhrase = getTransportStagePhrase(transport.stage);
  const kindLabel = getTransportKindLabel(transport.kind);
  const sourceLabel = getTransportSourceLabel(transport.source);
  return (
    <EvidenceSection title="Transport">
      <EvidenceGrid>
        {sourceLabel && <EvidenceField label="Source" value={sourceLabel} />}
        {kindLabel && <EvidenceField label="Kind" value={kindLabel} />}
        {transport.signal && (
          <EvidenceField
            label="Signal"
            value={<EvidenceCode>{transport.signal}</EvidenceCode>}
          />
        )}
        {stagePhrase && <EvidenceField label="Stage" value={stagePhrase} />}
        {transport.close_code !== undefined && (
          <EvidenceField
            label="Close Code"
            value={<EvidenceCode>{transport.close_code}</EvidenceCode>}
          />
        )}
      </EvidenceGrid>
      <EvidenceSnippet
        label="Close Reason"
        text={transport.close_reason_snippet}
      />
      <EvidenceSnippet label="Raw Error" text={transport.raw_error_snippet} />
    </EvidenceSection>
  );
}

export function HandshakeSection({
  handshake,
}: {
  handshake: RequestEvidenceUpstreamHandshake;
}) {
  return (
    <EvidenceSection title="Upstream Handshake">
      {handshake.status_code !== undefined && (
        <EvidenceGrid>
          <EvidenceField
            label="Status"
            value={<StatusValue value={handshake.status_code} />}
          />
        </EvidenceGrid>
      )}
      <EvidenceSnippet label="Body Snippet" text={handshake.body_snippet} />
    </EvidenceSection>
  );
}

export function UpstreamEventSection({
  event,
}: {
  event: RequestEvidenceUpstreamEvent;
}) {
  return (
    <EvidenceSection title="Upstream Event">
      <EvidenceGrid>
        {event.envelope_type && (
          <EvidenceField label="Envelope Type" value={event.envelope_type} />
        )}
        {event.provider_error_type && (
          <EvidenceField
            label="Provider Error Type"
            value={event.provider_error_type}
          />
        )}
        {event.provider_error_code && (
          <EvidenceField
            label="Provider Error Code"
            value={<EvidenceCode>{event.provider_error_code}</EvidenceCode>}
          />
        )}
        {event.status_code !== undefined && (
          <EvidenceField
            label="Status"
            value={<StatusValue value={event.status_code} />}
          />
        )}
      </EvidenceGrid>
      <EvidenceSnippet label="Message" text={event.message_snippet} />
      <EvidenceSnippet label="Raw Payload" text={event.raw_payload_snippet} />
    </EvidenceSection>
  );
}
