import { useEffect, useEffectEvent } from "react";
import { X } from "lucide-react";
import type {
  DebugCaptureBlobPreview,
  DebugCaptureHeaders,
  DebugCaptureRecordDetail,
} from "@/api";
import { CaptureFailureContext } from "./CaptureFailureContext";
import {
  formatBytes,
  formatCaptureValue,
  getContentType,
  isTextualContentType,
  presentBlobPreview,
} from "./presentation";

interface CaptureRecordDialogProps {
  recordId: string | null;
  detail: DebugCaptureRecordDetail | null;
  loading: boolean;
  error: Error | null;
  onClose: () => void;
  onRefresh: () => void;
}

function DetailRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="grid gap-1 py-1 sm:grid-cols-[12rem_1fr]">
      <dt className="text-sm text-text-secondary">{label}</dt>
      <dd className="wrap-break-word font-mono text-sm text-text-primary">
        {value}
      </dd>
    </div>
  );
}

function HeadersView({
  title,
  headers,
}: {
  title: string;
  headers?: DebugCaptureHeaders;
}) {
  if (!headers || Object.keys(headers).length === 0) return null;
  return (
    <section aria-label={title}>
      <h4 className="mb-2 text-sm font-semibold text-text-primary">{title}</h4>
      <dl className="rounded-lg bg-bg-secondary p-3">
        {Object.entries(headers).map(([name, values]) => (
          <DetailRow key={name} label={name} value={values.join(", ")} />
        ))}
      </dl>
    </section>
  );
}

function BlobPreviewView({
  title,
  preview,
  preferText,
}: {
  title: string;
  preview: DebugCaptureBlobPreview;
  preferText: boolean;
}) {
  return (
    <section aria-label={title}>
      <div className="mb-2 flex flex-wrap items-center justify-between gap-2">
        <h4 className="text-sm font-semibold text-text-primary">{title}</h4>
        <span className="text-xs text-text-muted">
          {formatBytes(preview.preview_bytes)} preview ·{" "}
          {formatBytes(preview.captured_bytes)} captured
          {preview.truncated ? " · truncated" : ""}
        </span>
      </div>
      <pre className="max-h-64 overflow-auto whitespace-pre-wrap break-words rounded-lg border border-border bg-slate-950 p-3 text-xs text-slate-100">
        {presentBlobPreview(preview, preferText)}
      </pre>
      <p className="mt-1 truncate font-mono text-[11px] text-text-muted">
        SHA-256 {preview.checksum_sha256 || "unavailable"}
      </p>
    </section>
  );
}

function HTTPDetail({ detail }: { detail: DebugCaptureRecordDetail }) {
  const exchange = detail.http;
  if (!exchange) return null;
  const requestContentType = getContentType(exchange.request.headers);
  const responseContentType = exchange.response
    ? getContentType(exchange.response.headers)
    : "";

  return (
    <div className="space-y-5">
      <section>
        <h3 className="mb-2 font-semibold text-text-primary">Request</h3>
        <dl>
          <DetailRow label="Method" value={exchange.request.method} />
          <DetailRow label="URL" value={exchange.request.url} />
          <DetailRow label="Host" value={exchange.request.host} />
          <DetailRow
            label="Content length"
            value={formatBytes(exchange.request.content_length)}
          />
        </dl>
      </section>
      <HeadersView title="Request headers" headers={exchange.request.headers} />
      <HeadersView
        title="Request trailers"
        headers={exchange.request.trailers}
      />
      <BlobPreviewView
        title="Request payload"
        preview={exchange.request_body}
        preferText={isTextualContentType(requestContentType)}
      />
      {exchange.response && (
        <>
          <section>
            <h3 className="mb-2 font-semibold text-text-primary">Response</h3>
            <dl>
              <DetailRow
                label="Status"
                value={String(exchange.response.status_code)}
              />
              <DetailRow label="Protocol" value={exchange.response.protocol} />
              <DetailRow
                label="Content length"
                value={formatBytes(exchange.response.content_length)}
              />
            </dl>
          </section>
          <HeadersView
            title="Response headers"
            headers={exchange.response.headers}
          />
          <HeadersView
            title="Response trailers"
            headers={exchange.response.trailers}
          />
        </>
      )}
      <BlobPreviewView
        title="Response payload"
        preview={exchange.response_body}
        preferText={isTextualContentType(responseContentType)}
      />
    </div>
  );
}

function WebSocketDetail({ detail }: { detail: DebugCaptureRecordDetail }) {
  const exchange = detail.websocket;
  if (!exchange) return null;

  return (
    <div className="space-y-5">
      <section>
        <h3 className="mb-2 font-semibold text-text-primary">
          WebSocket handshake
        </h3>
        <dl>
          <DetailRow label="Method" value={exchange.request.method} />
          <DetailRow label="URL" value={exchange.request.url} />
          <DetailRow
            label="Status"
            value={
              exchange.handshake
                ? String(exchange.handshake.status_code)
                : "Dial failed"
            }
          />
        </dl>
      </section>
      <HeadersView title="Request headers" headers={exchange.request.headers} />
      {exchange.handshake && (
        <HeadersView
          title="Handshake headers"
          headers={exchange.handshake.headers}
        />
      )}
      <BlobPreviewView
        title="Handshake body"
        preview={exchange.handshake_body}
        preferText
      />
      <section aria-labelledby="websocket-events-title">
        <div className="mb-2 flex items-center justify-between gap-2">
          <h3
            id="websocket-events-title"
            className="font-semibold text-text-primary"
          >
            Application messages
          </h3>
          {exchange.events_truncated && (
            <span className="badge badge-warning">Preview truncated</span>
          )}
        </div>
        {exchange.messages.length === 0 ? (
          <p className="text-sm text-text-secondary">No messages captured.</p>
        ) : (
          <ol className="space-y-3">
            {exchange.messages.map((message) => (
              <li
                key={message.message_id}
                className="rounded-lg border border-border p-3"
              >
                <div className="mb-2 flex flex-wrap gap-2">
                  <span className="badge badge-info">
                    {formatCaptureValue(message.direction)}
                  </span>
                  <span className="badge badge-neutral">
                    {formatCaptureValue(message.source)}
                  </span>
                  {message.disposition && (
                    <span className="badge badge-neutral">
                      {formatCaptureValue(message.disposition)}
                    </span>
                  )}
                  <span
                    className={
                      message.client_visible
                        ? "badge badge-success"
                        : "badge badge-warning"
                    }
                  >
                    {message.client_visible
                      ? "Client visible"
                      : "Not client visible"}
                  </span>
                </div>
                <dl className="mb-3 rounded-lg bg-bg-secondary px-3 py-1">
                  <DetailRow
                    label="Relative time"
                    value={`${message.relative_millis} ms`}
                  />
                  {message.source_message_id && (
                    <DetailRow
                      label="Source message"
                      value={message.source_message_id}
                    />
                  )}
                </dl>
                <CaptureFailureContext
                  label={`Message ${message.message_id} failure context`}
                  hasFailure={message.has_failure}
                  failure={message.failure}
                />
                <BlobPreviewView
                  title={
                    formatCaptureValue(message.message_type) +
                    " message " +
                    message.message_id
                  }
                  preview={message.payload}
                  preferText={message.message_type === "text"}
                />
              </li>
            ))}
          </ol>
        )}
      </section>
      {exchange.close && (
        <section aria-label="WebSocket close">
          <div className="mb-2 flex flex-wrap items-center gap-2">
            <h3 className="font-semibold text-text-primary">WebSocket close</h3>
            {exchange.close.reason_truncated && (
              <span className="badge badge-warning">Reason truncated</span>
            )}
          </div>
          <dl className="rounded-lg bg-bg-secondary p-3">
            <DetailRow
              label="Direction"
              value={formatCaptureValue(exchange.close.direction)}
            />
            <DetailRow label="Code" value={String(exchange.close.code)} />
            <DetailRow
              label="Clean"
              value={exchange.close.clean ? "Yes" : "No"}
            />
            {exchange.close.reason && (
              <DetailRow label="Reason" value={exchange.close.reason} />
            )}
          </dl>
        </section>
      )}
    </div>
  );
}

export function CaptureRecordDialog({
  recordId,
  detail,
  loading,
  error,
  onClose,
  onRefresh,
}: CaptureRecordDialogProps) {
  const closeOnEscape = useEffectEvent((event: KeyboardEvent) => {
    if (event.key === "Escape") onClose();
  });

  useEffect(() => {
    if (!recordId) return;
    document.addEventListener("keydown", closeOnEscape);
    return () => document.removeEventListener("keydown", closeOnEscape);
  }, [recordId]);

  if (!recordId) return null;

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4 backdrop-blur-sm"
      role="dialog"
      aria-modal="true"
      aria-labelledby="capture-record-title"
      onClick={onClose}
    >
      <div
        className="flex max-h-[90vh] w-full max-w-5xl flex-col rounded-xl border border-border bg-white shadow-2xl"
        onClick={(event) => event.stopPropagation()}
      >
        <header className="flex items-start justify-between gap-4 border-b border-border p-5">
          <div className="min-w-0">
            <h2
              id="capture-record-title"
              className="text-lg font-bold text-text-primary"
            >
              Captured exchange preview
            </h2>
            <p className="truncate font-mono text-xs text-text-muted">
              {recordId}
            </p>
          </div>
          <button
            type="button"
            className="btn btn-ghost btn-sm"
            onClick={onClose}
            aria-label="Close record preview"
          >
            <X className="h-5 w-5" aria-hidden="true" />
          </button>
        </header>

        <div className="min-h-0 flex-1 overflow-y-auto p-5">
          {loading && <p role="status">Loading record preview…</p>}
          {error && (
            <div role="alert" className="space-y-3 text-sm text-danger">
              <p>{error.message}</p>
              <button
                type="button"
                className="btn btn-secondary btn-sm"
                onClick={onRefresh}
              >
                Retry
              </button>
            </div>
          )}
          {detail && (
            <div className="space-y-5">
              <div className="flex flex-wrap gap-2">
                <span className="badge badge-neutral">
                  {formatCaptureValue(detail.summary.lifecycle_state)}
                </span>
                <span className="badge badge-info">
                  Snapshot: {formatCaptureValue(detail.snapshot_state)}
                </span>
                <span
                  className={
                    detail.summary.capture_completion === "complete"
                      ? "badge badge-success"
                      : "badge badge-danger"
                  }
                >
                  Capture:{" "}
                  {formatCaptureValue(detail.summary.capture_completion)}
                </span>
              </div>
              <dl className="rounded-lg bg-bg-secondary p-3">
                <DetailRow
                  label="Provider"
                  value={detail.summary.provider.name}
                />
                <DetailRow
                  label="Target"
                  value={detail.summary.provider.target_url}
                />
                <DetailRow
                  label="Source completion"
                  value={formatCaptureValue(
                    detail.summary.source_completion ?? "pending",
                  )}
                />
                <DetailRow
                  label="Selection"
                  value={`${formatCaptureValue(detail.summary.selection_mode)} · ${formatCaptureValue(detail.summary.selection_source)}`}
                />
                <DetailRow
                  label="Credential phase"
                  value={formatCaptureValue(detail.summary.credential_phase)}
                />
                <DetailRow
                  label="Upstream observed"
                  value={formatBytes(detail.summary.upstream_observed_bytes)}
                />
                <DetailRow
                  label="Application write confirmed"
                  value={formatBytes(
                    detail.summary.application_write_confirmed_bytes,
                  )}
                />
              </dl>
              <CaptureFailureContext
                label="Record failure context"
                terminationReason={detail.summary.termination_reason}
                hasFailure={detail.summary.has_failure}
                failure={detail.summary.failure}
              />
              <HTTPDetail detail={detail} />
              <WebSocketDetail detail={detail} />
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
