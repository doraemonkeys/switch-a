import { Eye, Route } from "lucide-react";
import type {
  DebugCaptureRecordSummary,
  DebugCaptureRecordsPage,
  DebugCaptureTraceEntry,
} from "@/api";
import { CaptureFailureContext } from "./CaptureFailureContext";
import { formatBytes, formatCaptureValue } from "./presentation";

interface CaptureTraceListProps {
  page: DebugCaptureRecordsPage;
  selectedRecordIds: ReadonlySet<string>;
  onRecordSelectionChange: (recordId: string, checked: boolean) => void;
  onOpenRecord: (recordId: string) => void;
}

function sourceBadgeClass(sourceCompletion: string): string {
  if (sourceCompletion === "complete") return "badge badge-success";
  if (sourceCompletion === "pending") return "badge badge-info";
  return "badge badge-warning";
}

function CompletenessBadges({ record }: { record: DebugCaptureRecordSummary }) {
  const sourceCompletion = record.source_completion ?? "pending";
  return (
    <div className="flex flex-wrap gap-1.5" aria-label="Capture completeness">
      <span className="badge badge-neutral">
        {formatCaptureValue(record.lifecycle_state)}
      </span>
      <span className={sourceBadgeClass(sourceCompletion)}>
        Source: {formatCaptureValue(sourceCompletion)}
      </span>
      <span
        className={
          record.capture_completion === "complete"
            ? "badge badge-success"
            : "badge badge-danger"
        }
      >
        Capture: {formatCaptureValue(record.capture_completion)}
      </span>
    </div>
  );
}

function RecordEntry({
  entry,
  record,
  selected,
  onSelectionChange,
  onOpen,
}: {
  entry: DebugCaptureTraceEntry & { kind: "record" };
  record?: DebugCaptureRecordSummary;
  selected: boolean;
  onSelectionChange: (checked: boolean) => void;
  onOpen: () => void;
}) {
  if (!record) {
    return (
      <li className="rounded-lg border border-dashed border-border bg-bg-secondary p-3 text-sm text-text-secondary">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <span className="font-medium text-text-primary">
            {entry.provider.name}
          </span>
          <span className="badge badge-neutral">Record outside this page</span>
        </div>
        <p className="mt-1 font-mono text-xs text-text-muted">
          {entry.record_id}
        </p>
        <p className="mt-1 text-xs text-text-muted">
          This gateway trace references the record, but its summary is not
          included in the current page.
        </p>
      </li>
    );
  }

  return (
    <li className="rounded-lg border border-border bg-white p-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <label className="flex min-w-0 cursor-pointer items-start gap-3">
          <input
            type="checkbox"
            checked={selected}
            onChange={(event) => onSelectionChange(event.target.checked)}
            aria-label={"Select record " + record.record_id}
          />
          <span className="min-w-0">
            <span className="block text-sm font-semibold text-text-primary">
              {record.provider.name} · {record.protocol.toUpperCase()}
            </span>
            <span className="block truncate font-mono text-xs text-text-muted">
              {record.record_id}
            </span>
          </span>
        </label>
        <button
          type="button"
          className="btn btn-secondary btn-sm"
          onClick={onOpen}
          aria-label={"View record " + record.record_id}
        >
          <Eye className="h-4 w-4" aria-hidden="true" />
          Preview
        </button>
      </div>
      <div className="mt-3 grid gap-2 text-xs text-text-secondary sm:grid-cols-3">
        <span>
          Attempt {record.provider_attempt_index + 1} ·{" "}
          {formatCaptureValue(record.selection_mode)} ·{" "}
          {formatCaptureValue(record.selection_source)}
        </span>
        <span>Observed {formatBytes(record.upstream_observed_bytes)}</span>
        <span>
          Write confirmed{" "}
          {formatBytes(record.application_write_confirmed_bytes)}
        </span>
      </div>
      <div className="mt-3">
        <CompletenessBadges record={record} />
      </div>
      <CaptureFailureContext
        label="Record failure context"
        terminationReason={record.termination_reason}
        hasFailure={record.has_failure}
        failure={record.failure}
      />
      <CaptureFailureContext
        label="Trace entry failure context"
        terminationReason={entry.termination_reason}
        hasFailure={entry.has_failure}
        failure={entry.failure}
        metadataTruncated={entry.metadata_truncated}
      />
    </li>
  );
}

function TransitionEntry({
  entry,
}: {
  entry: DebugCaptureTraceEntry & { kind: "transition" };
}) {
  return (
    <li className="rounded-lg border border-dashed border-border bg-bg-secondary p-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <span className="text-sm font-medium text-text-primary">
          {entry.provider.name}
        </span>
        <span className="badge badge-neutral">Transition only</span>
      </div>
      <p className="mt-1 text-xs text-text-secondary">
        Attempt {entry.provider_attempt_index + 1} ·{" "}
        {formatCaptureValue(entry.selection_mode)} ·{" "}
        {formatCaptureValue(entry.selection_source)} ·{" "}
        {formatCaptureValue(entry.credential_phase)}
      </p>
      <p className="mt-1 text-xs text-text-muted">
        Payload was not retained because this Provider was outside the selected
        capture scope.
      </p>
      <CaptureFailureContext
        label="Transition failure context"
        terminationReason={entry.termination_reason}
        hasFailure={entry.has_failure}
        failure={entry.failure}
        metadataTruncated={entry.metadata_truncated}
      />
    </li>
  );
}

export function CaptureTraceList({
  page,
  selectedRecordIds,
  onRecordSelectionChange,
  onOpenRecord,
}: CaptureTraceListProps) {
  const recordsById = new Map(
    page.records.map((record) => [record.record_id, record]),
  );

  if (page.gateway_traces.length === 0) {
    return (
      <div className="card empty-state">
        <Route className="mx-auto mb-3 h-8 w-8" aria-hidden="true" />
        <p>No captured exchanges yet.</p>
      </div>
    );
  }

  return (
    <div className="space-y-4">
      {page.gateway_traces.map((trace) => {
        const headingId = "trace-" + trace.gateway_trace_id;
        return (
          <section
            key={trace.gateway_trace_id}
            className="card-flat"
            aria-labelledby={headingId}
          >
            <div className="mb-4 flex flex-wrap items-start justify-between gap-3">
              <div className="min-w-0">
                <h3 id={headingId} className="font-semibold text-text-primary">
                  Gateway trace{" "}
                  <span className="font-mono text-xs text-text-muted">
                    {trace.gateway_trace_id}
                  </span>
                </h3>
                <p className="truncate text-xs text-text-muted">
                  Gateway request{" "}
                  <span className="font-mono">{trace.gateway_request_id}</span>
                </p>
              </div>
              {(trace.history_truncated_before ||
                trace.history_truncated_after) && (
                <span className="badge badge-warning">
                  Transition history truncated
                </span>
              )}
            </div>
            <ol className="space-y-2">
              {trace.entries.map((entry) =>
                entry.kind === "record" ? (
                  <RecordEntry
                    key={entry.entry_id}
                    entry={entry}
                    record={recordsById.get(entry.record_id)}
                    selected={selectedRecordIds.has(entry.record_id)}
                    onSelectionChange={(checked) =>
                      onRecordSelectionChange(entry.record_id, checked)
                    }
                    onOpen={() => onOpenRecord(entry.record_id)}
                  />
                ) : (
                  <TransitionEntry key={entry.entry_id} entry={entry} />
                ),
              )}
            </ol>
          </section>
        );
      })}
    </div>
  );
}
