import { useEffect } from "react";
import type { RequestLog } from "../api/types";
import {
  BADGE_STYLES,
  getSuccessBadgeClass,
  getStatusCodeBadgeClass,
} from "../lib/utils";
import { CopyButton } from "./CopyButton";
import { ErrorBodyParser } from "./ErrorBodyParser";
import { ProviderChain } from "./ProviderChain";
import { RequestAttemptTimeline } from "./RequestAttemptTimeline";
import { TransferStats } from "./TransferStats";
import { TokenUsageStats } from "./TokenUsageStats";
import {
  getDiagnosticToneClass,
  getLogLifecyclePresentation,
  getPrimaryProviderLabel,
} from "./logs/diagnostics";

interface LogDetailModalProps {
  log: RequestLog | null;
  providerName: string;
  /** Provider name map for displaying names in attempt timeline */
  providerNames?: Map<string, string>;
  onClose: () => void;
}

const WEBSOCKET_HANDSHAKE_STATUS_CODE = 101;
const WEBSOCKET_ATTEMPTS_NOTE =
  "RequestLog defines the final WebSocket lifecycle attribution. These rows show provider-attempt detail only.";

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
  const lifecycle = getLogLifecyclePresentation(log);
  const resolvedProviderName =
    providerNames?.get(log.provider_id) || providerName || log.provider_id;

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
          <StatusBadges log={log} lifecycle={lifecycle} />
          <RequestInfo
            log={log}
            providerName={resolvedProviderName}
            providerNames={providerNames}
          />
          <ResponseInfo log={log} />
          {lifecycle.showLifecycle && (
            <WebSocketLifecycleInfo
              log={log}
              lifecycle={lifecycle}
              providerName={resolvedProviderName}
            />
          )}
          <ClientInfo log={log} />

          {/* Transfer Statistics - Collapsible section for request/response sizes */}
          <TransferStats
            requestBytes={log.request_bytes}
            responseBytes={log.response_bytes}
            contentType={log.content_type}
            requestLabel={log.is_websocket ? "Sent" : undefined}
            responseLabel={log.is_websocket ? "Received" : undefined}
            flowLabel={log.is_websocket ? "Upstream" : undefined}
          />

          {/* Token Usage Statistics - Collapsible section for token counts and cache info */}
          <TokenUsageStats log={log} />

          {/* Error Details - Smart parsing with diagnostic tips */}
          {lifecycle.shouldShowErrorDetails && log.error_msg && (
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
            <DetailSection
              title={
                log.is_websocket ? "Provider Attempts" : "Request Attempts"
              }
            >
              {log.is_websocket && (
                <p className="text-xs text-text-muted">
                  {WEBSOCKET_ATTEMPTS_NOTE}
                </p>
              )}
              <RequestAttemptTimeline
                attempts={log.attempts}
                providerNames={providerNames}
                userAgent={log.user_agent}
                isWebSocket={log.is_websocket}
                attributedProviderId={
                  log.is_websocket ? log.provider_id : undefined
                }
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
  lifecycle,
}: {
  log: RequestLog;
  lifecycle: ReturnType<typeof getLogLifecyclePresentation>;
}) {
  const statusBadgeClass = log.is_websocket
    ? getDiagnosticToneClass(lifecycle.outcomeTone)
    : getSuccessBadgeClass(log.success);
  let statusLabel = lifecycle.shortOutcomeLabel;
  if (!log.is_websocket) {
    statusLabel = log.success ? "✅ Success" : "❌ Failed";
  }

  return (
    <div className="flex items-center gap-3 flex-wrap">
      <span
        className={`inline-flex items-center gap-2 px-3 py-1.5 rounded-full text-sm font-medium ${statusBadgeClass}`}
        title={log.is_websocket ? lifecycle.outcomeLabel : undefined}
      >
        {statusLabel}
      </span>
      {lifecycle.showLifecycle && (
        <>
          <span
            className={`inline-flex items-center gap-1.5 px-3 py-1.5 rounded-full text-sm font-medium ${getDiagnosticToneClass(lifecycle.commitmentTone)}`}
          >
            {lifecycle.commitmentLabel}
          </span>
          <span
            className={`inline-flex items-center gap-1.5 px-3 py-1.5 rounded-full text-sm font-medium ${getDiagnosticToneClass(lifecycle.terminalCauseTone)}`}
          >
            {lifecycle.terminalCauseLabel}
          </span>
        </>
      )}
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
    </div>
  );
}

const BADGE_CLASS =
  "inline-flex items-center px-2 py-0.5 rounded text-xs font-medium";

function RequestTypeBadge({ log }: { log: RequestLog }) {
  if (log.is_websocket) {
    return (
      <span
        className={`${BADGE_CLASS} bg-emerald-100 text-emerald-800 dark:bg-emerald-900/30 dark:text-emerald-300`}
      >
        WebSocket
      </span>
    );
  }
  if (log.is_sse) {
    return (
      <span
        className={`${BADGE_CLASS} bg-purple-100 text-purple-800 dark:bg-purple-900/30 dark:text-purple-300`}
      >
        SSE Stream
      </span>
    );
  }
  return (
    <span
      className={`${BADGE_CLASS} bg-gray-100 text-gray-800 dark:bg-gray-900/30 dark:text-gray-300`}
    >
      Regular
    </span>
  );
}

function RequestInfo({
  log,
  providerName,
  providerNames,
}: {
  log: RequestLog;
  providerName: string;
  providerNames?: Map<string, string>;
}) {
  const requestAttempts = log.attempts ?? [];
  const hasMultipleAttempts = requestAttempts.length > 1;
  const showProviderChain = hasMultipleAttempts && !log.is_websocket;
  const resolvedProviderName =
    providerNames?.get(log.provider_id) || providerName || log.provider_id;
  const providerLabel = getPrimaryProviderLabel(log);
  let providerContent: React.ReactNode = null;

  if (showProviderChain) {
    providerContent = (
      <div className="py-2">
        <div className="flex items-center justify-between mb-2">
          <span className="text-sm text-text-secondary">Provider Chain</span>
        </div>
        <ProviderChain
          attempts={requestAttempts}
          providerNames={providerNames}
          success={log.success}
        />
      </div>
    );
  } else if (!log.is_websocket) {
    providerContent = (
      <DetailRow label={providerLabel} value={resolvedProviderName} />
    );
  }

  return (
    <DetailSection title="Request Information">
      {/* Provider Chain - show visual failover path when there are retries */}
      {providerContent}
      <DetailRow label="Model" value={log.model} mono />
      <DetailRow
        label="API Type"
        value={
          <span className="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-blue-100 text-blue-800 dark:bg-blue-900/30 dark:text-blue-300 uppercase">
            {log.api_type}
          </span>
        }
      />
      <DetailRow label="Request Type" value={<RequestTypeBadge log={log} />} />
    </DetailSection>
  );
}

function ResponseInfo({ log }: { log: RequestLog }) {
  const hasTTFT =
    log.is_sse &&
    log.first_token_ms !== null &&
    log.first_token_ms !== undefined;
  const isWebSocketUpgrade =
    log.is_websocket && log.status_code === WEBSOCKET_HANDSHAKE_STATUS_CODE;
  const statusLabel = isWebSocketUpgrade ? "101 Upgrade" : log.status_code;
  const statusBadgeClass = isWebSocketUpgrade
    ? BADGE_STYLES.INFO
    : getStatusCodeBadgeClass(log.status_code);
  const statusRowLabel = log.is_websocket ? "Upgrade Status" : "Status Code";

  return (
    <DetailSection title="Response">
      <DetailRow
        label={statusRowLabel}
        value={
          <span
            className={`inline-flex items-center px-2 py-0.5 rounded text-xs font-medium ${statusBadgeClass}`}
          >
            {statusLabel}
          </span>
        }
      />
      {/* Latency with TTFT for SSE requests */}
      <DetailRow
        label="Latency"
        value={
          <div className="flex items-center gap-2">
            <span className="font-mono">{log.latency_ms}ms</span>
            {hasTTFT && (
              <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded text-xs font-medium bg-purple-100 text-purple-700 dark:bg-purple-900/30 dark:text-purple-300">
                <svg
                  className="w-3 h-3"
                  fill="none"
                  stroke="currentColor"
                  viewBox="0 0 24 24"
                >
                  <path
                    strokeLinecap="round"
                    strokeLinejoin="round"
                    strokeWidth={2}
                    d="M13 10V3L4 14h7v7l9-11h-7z"
                  />
                </svg>
                TTFT: {log.first_token_ms}ms
              </span>
            )}
          </div>
        }
      />
    </DetailSection>
  );
}

function WebSocketLifecycleInfo({
  log,
  lifecycle,
  providerName,
}: {
  log: RequestLog;
  lifecycle: ReturnType<typeof getLogLifecyclePresentation>;
  providerName: string;
}) {
  return (
    <DetailSection title="WebSocket Lifecycle">
      <DetailRow label="Outcome" value={lifecycle.outcomeLabel} />
      <DetailRow
        label={getPrimaryProviderLabel(log)}
        value={providerName || log.provider_id}
      />
      <DetailRow label="Commit State" value={lifecycle.commitmentLabel} />
      {lifecycle.probeOutcomeLabel && (
        <DetailRow label="Probe Outcome" value={lifecycle.probeOutcomeLabel} />
      )}
      <DetailRow label="Terminal Cause" value={lifecycle.terminalCauseLabel} />
      {lifecycle.commitSourceLabel && (
        <DetailRow label="Commit Source" value={lifecycle.commitSourceLabel} />
      )}
      {lifecycle.stickyWrittenLabel && (
        <DetailRow label="Sticky Write" value={lifecycle.stickyWrittenLabel} />
      )}
      {log.error_msg && !lifecycle.shouldShowErrorDetails && (
        <DetailRow label="Connection Note" value={log.error_msg} />
      )}
    </DetailSection>
  );
}

function ClientInfo({ log }: { log: RequestLog }) {
  const hasExtendedInfo =
    log.user_agent || log.request_id_header || log.user_id;

  return (
    <DetailSection title="Client Information">
      {/* Primary client info in a compact grid */}
      <div
        className={`grid gap-3 mb-3 ${log.user_id ? "grid-cols-2" : "grid-cols-1"}`}
      >
        {/* Client IP Card */}
        <ClientInfoCard
          icon={
            <svg
              className="w-4 h-4"
              fill="none"
              stroke="currentColor"
              viewBox="0 0 24 24"
            >
              <path
                strokeLinecap="round"
                strokeLinejoin="round"
                strokeWidth={2}
                d="M21 12a9 9 0 01-9 9m9-9a9 9 0 00-9-9m9 9H3m9 9a9 9 0 01-9-9m9 9c1.657 0 3-4.03 3-9s-1.343-9-3-9m0 18c-1.657 0-3-4.03-3-9s1.343-9 3-9m-9 9a9 9 0 019-9"
              />
            </svg>
          }
          label="IP Address"
          value={log.client_ip}
          copyable
        />
        {/* User ID Card - only shown when user_id exists */}
        {log.user_id && (
          <ClientInfoCard
            icon={
              <svg
                className="w-4 h-4"
                fill="none"
                stroke="currentColor"
                viewBox="0 0 24 24"
              >
                <path
                  strokeLinecap="round"
                  strokeLinejoin="round"
                  strokeWidth={2}
                  d="M16 7a4 4 0 11-8 0 4 4 0 018 0zM12 14a7 7 0 00-7 7h14a7 7 0 00-7-7z"
                />
              </svg>
            }
            label="User ID"
            value={log.user_id}
            copyable
          />
        )}
      </div>

      {/* Extended info in collapsible style cards */}
      {hasExtendedInfo && (
        <div className="space-y-2">
          {/* User-Agent */}
          {log.user_agent && (
            <ClientInfoExpandedCard
              icon={
                <svg
                  className="w-4 h-4"
                  fill="none"
                  stroke="currentColor"
                  viewBox="0 0 24 24"
                >
                  <path
                    strokeLinecap="round"
                    strokeLinejoin="round"
                    strokeWidth={2}
                    d="M9.75 17L9 20l-1 1h8l-1-1-.75-3M3 13h18M5 17h14a2 2 0 002-2V5a2 2 0 00-2-2H5a2 2 0 00-2 2v10a2 2 0 002 2z"
                  />
                </svg>
              }
              label="User-Agent"
              value={log.user_agent}
            />
          )}
          {/* Request ID */}
          {log.request_id_header && (
            <ClientInfoExpandedCard
              icon={
                <svg
                  className="w-4 h-4"
                  fill="none"
                  stroke="currentColor"
                  viewBox="0 0 24 24"
                >
                  <path
                    strokeLinecap="round"
                    strokeLinejoin="round"
                    strokeWidth={2}
                    d="M7 20l4-16m2 16l4-16M6 9h14M4 15h14"
                  />
                </svg>
              }
              label="Request ID (X-Request-ID)"
              value={log.request_id_header}
            />
          )}
        </div>
      )}
    </DetailSection>
  );
}

/** Compact card for primary client info (IP, User ID) */
function ClientInfoCard({
  icon,
  label,
  value,
  copyable = false,
  muted = false,
}: {
  icon: React.ReactNode;
  label: string;
  value: string;
  copyable?: boolean;
  muted?: boolean;
}) {
  return (
    <div className="px-3 py-2.5 rounded-lg bg-bg-tertiary border border-border-light">
      <div className="flex items-center gap-1.5 text-text-muted mb-1">
        {icon}
        <span className="text-xs">{label}</span>
      </div>
      <div className="flex items-center justify-between gap-2">
        <span
          className={`text-sm font-mono truncate ${muted ? "text-text-muted" : "text-text-primary"}`}
          title={value}
        >
          {value}
        </span>
        {copyable && value && value !== "—" && (
          <CopyButton text={value} className="flex-shrink-0" />
        )}
      </div>
    </div>
  );
}

/** Expanded card for longer values (User-Agent, Request ID) */
function ClientInfoExpandedCard({
  icon,
  label,
  value,
}: {
  icon: React.ReactNode;
  label: string;
  value: string;
}) {
  return (
    <div className="px-3 py-2.5 rounded-lg bg-bg-tertiary border border-border-light">
      <div className="flex items-center justify-between mb-1.5">
        <div className="flex items-center gap-1.5 text-text-muted">
          {icon}
          <span className="text-xs">{label}</span>
        </div>
        <CopyButton text={value} />
      </div>
      <p className="text-sm text-text-primary font-mono break-all leading-relaxed">
        {value}
      </p>
    </div>
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
