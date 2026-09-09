import type {
  RequestEvidenceCompletion,
  RequestEvidenceResponse,
  RequestEvidenceResponseProgress,
  RequestEvidenceWrite,
} from "@/api/evidence-types";
import {
  EvidenceCode,
  EvidenceField,
  EvidenceGrid,
  EvidenceSection,
  EvidenceSnippet,
} from "./EvidencePrimitives";

function ResponseFields({ response }: { response: RequestEvidenceResponse }) {
  return (
    <EvidenceGrid>
      <EvidenceField label="Round" value={response.round} />
      {response.response_id && (
        <EvidenceField
          label="Response ID"
          value={<EvidenceCode>{response.response_id}</EvidenceCode>}
        />
      )}
      <EvidenceField
        label="Observed Event"
        value={response.event_type ?? "No upstream event observed"}
      />
      {response.status && (
        <EvidenceField label="Upstream Status" value={response.status} />
      )}
      {response.observed_at && (
        <EvidenceField label="Observed At" value={response.observed_at} />
      )}
    </EvidenceGrid>
  );
}

export function UpstreamCompletionSection({
  completion,
}: {
  completion: RequestEvidenceCompletion;
}) {
  return (
    <EvidenceSection title="Upstream Completion">
      <EvidenceGrid>
        <EvidenceField
          label="Observed Event"
          value={<EvidenceCode>{completion.event_type}</EvidenceCode>}
        />
        <EvidenceField label="Observed At" value={completion.observed_at} />
      </EvidenceGrid>
    </EvidenceSection>
  );
}

export function UpstreamResponsesSection({
  progress,
}: {
  progress: RequestEvidenceResponseProgress;
}) {
  return (
    <EvidenceSection title="Upstream Responses">
      <p className="mb-2 text-xs text-text-muted">Current round</p>
      <ResponseFields response={progress.current} />
      <EvidenceField
        label="Completed Responses"
        value={progress.completed_responses}
      />
      {progress.last_completed && (
        <details className="mt-2 text-xs">
          <summary className="cursor-pointer text-text-secondary">
            Last completed response
          </summary>
          <ResponseFields response={progress.last_completed} />
        </details>
      )}
    </EvidenceSection>
  );
}

export function DownstreamWriteSection({
  writes,
}: {
  writes: RequestEvidenceWrite;
}) {
  return (
    <EvidenceSection title="Downstream Writes">
      <EvidenceGrid>
        <EvidenceField label="Write Calls" value={writes.calls} />
        <EvidenceField
          label="Successful Writes"
          value={writes.successful_calls}
        />
        <EvidenceField label="Failed Writes" value={writes.failed_calls} />
        <EvidenceField
          label="Bytes Reported Written"
          value={writes.confirmed_bytes}
        />
      </EvidenceGrid>
      <EvidenceSnippet label="Last Write Error" text={writes.last_error} />
    </EvidenceSection>
  );
}
