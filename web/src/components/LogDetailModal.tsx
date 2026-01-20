import { useEffect } from "react";
import type { RequestLog } from "../api/types";
import { getSuccessBadgeClass, getStatusCodeBadgeClass } from "../lib/utils";
import { CopyButton } from "./CopyButton";
import { ErrorBodyParser } from "./ErrorBodyParser";
import { RequestAttemptTimeline } from "./RequestAttemptTimeline";

interface LogDetailModalProps {
  log: RequestLog | null;
  providerName: string;
  /** Provider name map for displaying names in attempt timeline */
  providerNames?: Map<string, string>;
  onClose: () => void;
}

/**
 * Format the modal title based on request method and path
 * Falls back to "Request Details" for old data without these fields
 */
function getModalTitle(log: RequestLog): string {
  if (log.request_method && log.request_path) {
    return `${log.request_method} ${log.request_path}`;
  }
  return "Request Details";
}

/**
 * Get method badge color based on HTTP method
 */
function getMethodBadgeClass(method: string): string {
  switch (method.toUpperCase()) {
    case "GET":
      return "bg-emerald-100 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300";
    case "POST":
      return "bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-300";
    case "PUT":
    case "PATCH":
      return "bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300";
    case "DELETE":
      return "bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-300";
    default:
      return "bg-gray-100 text-gray-700 dark:bg-gray-900/30 dark:text-gray-300";
  }
}

export function LogDetailModal({
  log,
  providerName,
  providerNames,
  onClose,
}: LogDetailModalProps) {
  // Handle Escape key to close modal
  useEffect(() => {
    if (!log) return;
    const handleEscape = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    document.addEventListener("keydown", handleEscape);
    return () => document.removeEventListener("keydown", handleEscape);
  }, [log, onClose]);

  if (!log) return null;

  const formattedTime = new Date(log.created_at).toLocaleString("en-US", {
    year: "numeric",
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });

  const modalTitle = getModalTitle(log);
  const hasEndpointInfo = log.request_method && log.request_path;

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-labelledby="log-detail-modal-title"
      className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/50 backdrop-blur-sm"
      onClick={onClose}
    >
      <div
        className="bg-bg-secondary w-full max-w-lg max-h-[90vh] rounded-xl shadow-2xl border border-border-light flex flex-col"
        onClick={(e) => e.stopPropagation()}
      >
        {/* Header - Shows METHOD /path as primary title */}
        <div className="p-6 border-b border-border-light flex justify-between items-start flex-shrink-0">
          <div className="min-w-0 flex-1 mr-4">
            <div className="flex items-center gap-2 flex-wrap">
              {hasEndpointInfo && (
                <span
                  className={`inline-flex items-center px-2 py-0.5 rounded text-xs font-bold uppercase ${getMethodBadgeClass(log.request_method!)}`}
                >
                  {log.request_method}
                </span>
              )}
              <h2
                id="log-detail-modal-title"
                className="text-lg font-bold text-text-primary font-mono truncate"
                title={modalTitle}
              >
                {hasEndpointInfo ? log.request_path : modalTitle}
              </h2>
            </div>
            <div className="flex items-center gap-3 mt-1.5 text-sm text-text-muted">
              <span>#{log.id}</span>
              <span>•</span>
              <span>{formattedTime}</span>
            </div>
          </div>
          <CloseButton onClick={onClose} />
        </div>

        {/* Content */}
        <div className="p-6 space-y-6 overflow-y-auto flex-1 min-h-0">
          <StatusBadges log={log} formattedTime={formattedTime} />
          <RequestInfo log={log} providerName={providerName} />
          <ResponseInfo log={log} />
          <ClientInfo log={log} />

          {/* Error Details - Smart parsing with diagnostic tips */}
          {!log.success && log.error_msg && (
            <DetailSection title="Error Details">
              <ErrorBodyParser
                body={log.error_msg}
                statusCode={log.status_code}
                userAgent={log.user_agent}
              />
            </DetailSection>
          )}

          {/* Attempt Timeline */}
          {log.attempts && log.attempts.length > 0 && (
            <DetailSection title="Request Attempts">
              <RequestAttemptTimeline
                attempts={log.attempts}
                providerNames={providerNames}
                userAgent={log.user_agent}
              />
            </DetailSection>
          )}
        </div>

        {/* Footer */}
        <div className="flex justify-end gap-3 p-6 border-t border-border-light flex-shrink-0">
          <button type="button" onClick={onClose} className="btn btn-secondary">
            Close
          </button>
        </div>
      </div>
    </div>
  );
}

// =============================================================================
// Sub-components (extracted for better organization and line count)
// =============================================================================

function CloseButton({ onClick }: { onClick: () => void }) {
  return (
    <button
      onClick={onClick}
      className="text-text-secondary hover:text-text-primary transition-colors cursor-pointer flex-shrink-0"
      aria-label="Close"
    >
      <svg
        className="w-5 h-5"
        fill="none"
        stroke="currentColor"
        viewBox="0 0 24 24"
      >
        <path
          strokeLinecap="round"
          strokeLinejoin="round"
          strokeWidth={2}
          d="M6 18L18 6M6 6l12 12"
        />
      </svg>
    </button>
  );
}

function StatusBadges({
  log,
  formattedTime,
}: {
  log: RequestLog;
  formattedTime: string;
}) {
  return (
    <div className="flex items-center gap-3 flex-wrap">
      <span
        className={`inline-flex items-center gap-2 px-3 py-1.5 rounded-full text-sm font-medium ${getSuccessBadgeClass(log.success)}`}
      >
        {log.success ? "✅ Success" : "❌ Failed"}
      </span>
      {log.is_sticky && (
        <span className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-full text-sm font-medium bg-indigo-100 text-indigo-800 dark:bg-indigo-900/30 dark:text-indigo-300">
          🔗 Sticky Session
        </span>
      )}
      {log.retry_count > 0 && (
        <span className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-full text-sm font-medium bg-amber-100 text-amber-800 dark:bg-amber-900/30 dark:text-amber-300">
          🔄 {log.retry_count} {log.retry_count === 1 ? "retry" : "retries"}
        </span>
      )}
      <span className="text-sm text-text-muted">{formattedTime}</span>
    </div>
  );
}

function RequestInfo({
  log,
  providerName,
}: {
  log: RequestLog;
  providerName: string;
}) {
  return (
    <DetailSection title="Request Information">
      <DetailRow label="Provider" value={providerName || log.provider_id} />
      <DetailRow label="Model" value={log.model} mono />
      <DetailRow
        label="API Type"
        value={
          <span className="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-blue-100 text-blue-800 dark:bg-blue-900/30 dark:text-blue-300 uppercase">
            {log.api_type}
          </span>
        }
      />
      <DetailRow
        label="Request Type"
        value={
          log.is_sse ? (
            <span className="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-purple-100 text-purple-800 dark:bg-purple-900/30 dark:text-purple-300">
              SSE Stream
            </span>
          ) : (
            <span className="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-gray-100 text-gray-800 dark:bg-gray-900/30 dark:text-gray-300">
              Regular
            </span>
          )
        }
      />
    </DetailSection>
  );
}

function ResponseInfo({ log }: { log: RequestLog }) {
  return (
    <DetailSection title="Response">
      <DetailRow
        label="Status Code"
        value={
          <span
            className={`inline-flex items-center px-2 py-0.5 rounded text-xs font-medium ${getStatusCodeBadgeClass(log.status_code)}`}
          >
            {log.status_code}
          </span>
        }
      />
      <DetailRow label="Latency" value={`${log.latency_ms}ms`} />
    </DetailSection>
  );
}

function ClientInfo({ log }: { log: RequestLog }) {
  return (
    <DetailSection title="Client Information">
      <DetailRow label="Client IP" value={log.client_ip} mono />
      {log.user_id && <DetailRow label="User ID" value={log.user_id} mono />}
      {log.user_agent && (
        <CopyableField label="User-Agent" value={log.user_agent} />
      )}
      {log.request_id_header && (
        <CopyableField
          label="Request ID (X-Request-ID)"
          value={log.request_id_header}
        />
      )}
    </DetailSection>
  );
}

// =============================================================================
// Shared UI Components
// =============================================================================

interface DetailSectionProps {
  title: string;
  children: React.ReactNode;
}

function DetailSection({ title, children }: DetailSectionProps) {
  return (
    <div>
      <h3 className="text-sm font-medium text-text-muted mb-2">{title}</h3>
      <div className="space-y-2">{children}</div>
    </div>
  );
}

interface DetailRowProps {
  label: string;
  value: React.ReactNode;
  mono?: boolean;
}

function DetailRow({ label, value, mono }: DetailRowProps) {
  return (
    <div className="flex items-center justify-between py-1">
      <span className="text-sm text-text-secondary">{label}</span>
      <span
        className={`text-sm text-text-primary ${mono ? "font-mono" : "font-medium"}`}
      >
        {value}
      </span>
    </div>
  );
}

interface CopyableFieldProps {
  label: string;
  value: string;
}

function CopyableField({ label, value }: CopyableFieldProps) {
  return (
    <div className="py-1">
      <div className="flex items-center justify-between mb-1">
        <span className="text-sm text-text-secondary">{label}</span>
        <CopyButton text={value} />
      </div>
      <p className="text-sm text-text-primary font-mono bg-bg-tertiary px-2 py-1.5 rounded break-all">
        {value}
      </p>
    </div>
  );
}
