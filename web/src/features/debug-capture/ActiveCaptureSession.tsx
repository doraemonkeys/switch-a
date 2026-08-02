import { useState } from "react";
import { RefreshCw, Square } from "lucide-react";
import type { DebugCaptureSessionStatus, DebugCaptureStatus } from "@/api";
import { useDebugCapture } from "./useDebugCapture";
import { useCaptureRecords } from "./useCaptureRecords";
import { useCaptureRecord } from "./useCaptureRecord";
import { CaptureTraceList } from "./CaptureTraceList";
import { CaptureRecordDialog } from "./CaptureRecordDialog";
import { CaptureExportPanel } from "./CaptureExportPanel";
import { StopCaptureDialog } from "./StopCaptureDialog";
import { formatBytes } from "./presentation";

interface ActiveCaptureSessionProps {
  status: DebugCaptureStatus;
  session: DebugCaptureSessionStatus;
}

function MetricCard({
  label,
  value,
  detail,
}: {
  label: string;
  value: string;
  detail?: string;
}) {
  return (
    <div className="card-flat p-4">
      <dt className="text-xs font-semibold uppercase tracking-wide text-text-muted">
        {label}
      </dt>
      <dd className="mt-1 text-xl font-bold text-text-primary">{value}</dd>
      {detail && <dd className="mt-1 text-xs text-text-secondary">{detail}</dd>}
    </div>
  );
}

function CaptureMetrics({ status, session }: ActiveCaptureSessionProps) {
  return (
    <dl className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
      <MetricCard
        label="Session retained"
        value={formatBytes(session.retained_bytes)}
        detail={"Limit " + formatBytes(session.retained_bytes_limit)}
      />
      <MetricCard
        label="Records"
        value={String(session.completed_record_count)}
        detail={session.active_record_count + " active"}
      />
      <MetricCard
        label="Gateway traces"
        value={String(session.gateway_trace_count)}
        detail={session.history_truncated_trace_count + " history-truncated"}
      />
      <MetricCard
        label="Process charged"
        value={formatBytes(status.process_memory.charged_bytes)}
        detail={"Ceiling " + formatBytes(status.process_memory.ceiling_bytes)}
      />
      <MetricCard
        label="Pinned"
        value={formatBytes(status.process_memory.pinned_bytes)}
        detail={status.pending_export_count + " pending exports"}
      />
      <MetricCard
        label="Releasing"
        value={formatBytes(status.process_memory.releasing_bytes)}
        detail={status.active_download_count + " active downloads"}
      />
      <MetricCard
        label="Overflowed"
        value={String(session.overflowed_record_count)}
        detail={session.evicted_record_count + " evicted"}
      />
      <MetricCard
        label="Dropped"
        value={String(
          session.dropped_trace_count + session.dropped_exchange_count,
        )}
        detail={session.dropped_transition_count + " transitions"}
      />
    </dl>
  );
}

export function ActiveCaptureSession({
  status,
  session,
}: ActiveCaptureSessionProps) {
  const { stopCapture, operation } = useDebugCapture();
  const records = useCaptureRecords(session.session_id);
  const [selectedRecordIds, setSelectedRecordIds] = useState<Set<string>>(
    () => new Set(),
  );
  const [previewRecordId, setPreviewRecordId] = useState<string | null>(null);
  const record = useCaptureRecord(session.session_id, previewRecordId);
  const [stopDialogOpen, setStopDialogOpen] = useState(false);
  const [stopError, setStopError] = useState<string | null>(null);

  const handleRecordSelectionChange = (recordId: string, checked: boolean) => {
    setSelectedRecordIds((current) => {
      const next = new Set(current);
      if (checked) {
        next.add(recordId);
      } else {
        next.delete(recordId);
      }
      return next;
    });
  };

  const handleStop = async () => {
    setStopError(null);
    try {
      await stopCapture(session.session_id);
    } catch (reason) {
      setStopError(
        reason instanceof Error ? reason.message : "Unable to stop capture.",
      );
    }
  };

  const capturedRecordCount =
    session.active_record_count + session.completed_record_count;

  return (
    <div className="space-y-5">
      <section
        className="card space-y-4"
        aria-labelledby="active-session-title"
      >
        <div className="flex flex-wrap items-start justify-between gap-4">
          <div className="min-w-0">
            <div className="flex items-center gap-2">
              <span
                className="h-2.5 w-2.5 animate-pulse rounded-full bg-danger"
                aria-hidden="true"
              />
              <h2
                id="active-session-title"
                className="text-xl font-bold text-text-primary"
              >
                Capture active
              </h2>
            </div>
            <p className="mt-1 truncate font-mono text-xs text-text-muted">
              {session.session_id}
            </p>
            <p className="mt-2 text-sm text-text-secondary">
              Providers:{" "}
              {session.providers.map((provider) => provider.name).join(", ")}
            </p>
          </div>
          <button
            type="button"
            className="btn btn-danger"
            onClick={() => {
              setStopError(null);
              setStopDialogOpen(true);
            }}
          >
            <Square className="h-4 w-4" aria-hidden="true" />
            Stop
          </button>
        </div>
        <p
          role="note"
          className="rounded-lg border border-warning/30 bg-warning-light p-3 text-sm text-text-secondary"
        >
          Raw request and response payloads may contain sensitive data.
        </p>
      </section>

      <CaptureMetrics status={status} session={session} />

      <CaptureExportPanel
        sessionId={session.session_id}
        totalRecords={capturedRecordCount}
        selectedRecordIds={selectedRecordIds}
      />

      <section aria-labelledby="captured-records-title" className="space-y-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h2
              id="captured-records-title"
              className="text-lg font-semibold text-text-primary"
            >
              Captured gateway traces
            </h2>
            <p className="text-sm text-text-secondary">
              Record payloads and unselected-Provider transition stubs are shown
              in gateway order.
            </p>
          </div>
          <div className="flex flex-wrap gap-2">
            {!records.isLatestPage && (
              <button
                type="button"
                className="btn btn-secondary btn-sm"
                onClick={records.showLatestPage}
              >
                Back to latest
              </button>
            )}
            <button
              type="button"
              className="btn btn-secondary btn-sm"
              onClick={() => void records.refetch()}
              disabled={records.loading}
            >
              <RefreshCw
                className={"h-4 w-4" + (records.loading ? " animate-spin" : "")}
                aria-hidden="true"
              />
              Refresh
            </button>
          </div>
        </div>

        {records.error && (
          <p role="alert" className="text-sm font-medium text-danger">
            {records.error.message}
          </p>
        )}
        {records.page?.eviction_gap.detected && (
          <p
            role="alert"
            className="rounded-lg border border-warning/30 bg-warning-light p-3 text-sm text-text-secondary"
          >
            {records.page.eviction_gap.record_count} records were evicted while
            this snapshot was being viewed. The trace explicitly marks any
            resulting gaps.
          </p>
        )}
        {records.loading && !records.page && (
          <p role="status" className="card text-sm text-text-secondary">
            Loading captured records…
          </p>
        )}
        {records.page && (
          <CaptureTraceList
            page={records.page}
            selectedRecordIds={selectedRecordIds}
            onRecordSelectionChange={handleRecordSelectionChange}
            onOpenRecord={setPreviewRecordId}
          />
        )}

        {records.page && (
          <nav
            className="flex items-center justify-between"
            aria-label="Captured records pagination"
          >
            <button
              type="button"
              className="btn btn-secondary btn-sm"
              disabled={records.pageNumber === 1 || records.loading}
              onClick={records.showPreviousPage}
            >
              Previous
            </button>
            <span className="text-sm text-text-secondary">
              Page {records.pageNumber}
              {records.isLatestPage ? " · live" : " · frozen snapshot"}
            </span>
            <button
              type="button"
              className="btn btn-secondary btn-sm"
              disabled={!records.page.next_cursor || records.loading}
              onClick={records.showNextPage}
            >
              Next
            </button>
          </nav>
        )}
      </section>

      <CaptureRecordDialog
        recordId={previewRecordId}
        detail={record.detail}
        loading={record.loading}
        error={record.error}
        onClose={() => setPreviewRecordId(null)}
        onRefresh={() => void record.refresh()}
      />
      <StopCaptureDialog
        open={stopDialogOpen}
        sessionId={session.session_id}
        stopping={operation === "stop"}
        error={stopError}
        onClose={() => setStopDialogOpen(false)}
        onConfirm={() => void handleStop()}
      />
    </div>
  );
}
