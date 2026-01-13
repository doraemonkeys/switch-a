import { useEffect, useCallback } from "react";
import type { RequestLog } from "../api/types";
import { getSuccessBadgeClass, getStatusCodeBadgeClass } from "../lib/utils";

interface LogDetailModalProps {
  log: RequestLog | null;
  providerName: string;
  onClose: () => void;
}

export function LogDetailModal({
  log,
  providerName,
  onClose,
}: LogDetailModalProps) {
  // Handle Escape key to close modal
  const handleEscape = useCallback(
    (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        onClose();
      }
    },
    [onClose],
  );

  useEffect(() => {
    if (log) {
      document.addEventListener("keydown", handleEscape);
      return () => document.removeEventListener("keydown", handleEscape);
    }
  }, [log, handleEscape]);

  if (!log) return null;

  const formattedTime = new Date(log.created_at).toLocaleString("en-US", {
    year: "numeric",
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/50 backdrop-blur-sm"
      onClick={onClose}
    >
      <div
        className="bg-bg-secondary w-full max-w-lg rounded-xl shadow-2xl border border-border-light"
        onClick={(e) => e.stopPropagation()}
      >
        {/* Header */}
        <div className="p-6 border-b border-border-light flex justify-between items-start">
          <div>
            <h2 className="text-xl font-bold text-text-primary">Log Details</h2>
            <p className="text-sm text-text-muted mt-1">#{log.id}</p>
          </div>
          <button
            onClick={onClose}
            className="text-text-secondary hover:text-text-primary transition-colors cursor-pointer"
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
        </div>

        {/* Content */}
        <div className="p-6 space-y-6">
          {/* Status Badge */}
          <div className="flex items-center gap-3">
            <span
              className={`inline-flex items-center gap-2 px-3 py-1.5 rounded-full text-sm font-medium ${getSuccessBadgeClass(log.success)}`}
            >
              {log.success ? "✅ Success" : "❌ Failed"}
            </span>
            <span className="text-sm text-text-muted">{formattedTime}</span>
          </div>

          {/* Basic Info */}
          <DetailSection title="Request Information">
            <DetailRow
              label="Provider"
              value={providerName || log.provider_id}
            />
            <DetailRow label="Model" value={log.model} mono />
            <DetailRow
              label="API Type"
              value={
                <span className="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-blue-100 text-blue-800 dark:bg-blue-900/30 dark:text-blue-300 uppercase">
                  {log.api_type}
                </span>
              }
            />
          </DetailSection>

          {/* Response Info */}
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

          {/* Client Info */}
          <DetailSection title="Client Information">
            <DetailRow label="Client IP" value={log.client_ip} mono />
            {log.user_id && (
              <DetailRow label="User ID" value={log.user_id} mono />
            )}
          </DetailSection>

          {/* Error Message (only show if failed and has error message) */}
          {!log.success && log.error_msg && (
            <DetailSection title="Error Details">
              <div className="p-3 rounded-lg bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-red-800">
                <p className="text-sm text-red-700 dark:text-red-300 font-mono whitespace-pre-wrap break-words">
                  {log.error_msg}
                </p>
              </div>
            </DetailSection>
          )}
        </div>

        {/* Footer */}
        <div className="flex justify-end gap-3 p-6 pt-0">
          <button type="button" onClick={onClose} className="btn btn-secondary">
            Close
          </button>
        </div>
      </div>
    </div>
  );
}

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
