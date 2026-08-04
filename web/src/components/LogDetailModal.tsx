import { useEffect, type ReactNode } from "react";
import type { RequestLog } from "../api/types";
import { BADGE_STYLES } from "../lib/utils";
import { CopyButton } from "./CopyButton";
import { ProviderChain } from "./ProviderChain";
import { TransferStats } from "./TransferStats";
import { TokenUsageStats } from "./TokenUsageStats";
import {
  getDiagnosticToneClass,
  getLogEvidenceSummary,
  getLogLifecyclePresentation,
  getPrimaryProviderLabel,
  getTransportBadgeClass,
} from "./logs/diagnostics";
import {
  RequestAttemptTimeline,
  RequestEvidenceViewer,
} from "@/features/request-attempt";

interface LogDetailModalProps {
  log: RequestLog | null;
  providerName: string;
  providerNames?: Map<string, string>;
  onClose: () => void;
}

const WEBSOCKET_ATTEMPTS_NOTE =
  "RequestLog keeps the final request/session assessment. Timeline rows stay provider-attempt evidence only.";
const BADGE_CLASS =
  "inline-flex items-center px-2 py-0.5 rounded text-xs font-medium";

function getModalTitle(log: RequestLog): string {
  if (log.request_method && log.request_path) {
    return `${log.request_method} ${log.request_path}`;
  }
  return "Request Details";
}

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
  useEffect(() => {
    if (!log) return;
    const handleEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        onClose();
      }
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
        className="bg-bg-secondary w-full max-w-4xl max-h-[90vh] rounded-xl shadow-2xl border border-border-light flex flex-col"
        onClick={(event) => event.stopPropagation()}
      >
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

        <div className="p-6 space-y-6 overflow-y-auto flex-1 min-h-0">
          <StatusBadges log={log} />
          <EvidenceSummaryLine log={log} />
          <RequestInfo
            log={log}
            providerName={resolvedProviderName}
            providerNames={providerNames}
          />
          <ReasoningInfo log={log} />
          <ResponseInfo log={log} />
          <SemanticsInfo log={log} />
          {log.is_websocket && <WebSocketLifecycleInfo log={log} />}
          <ClientInfo log={log} />
          <TransferStats
            requestBytes={log.request_bytes}
            responseBytes={log.response_bytes}
            contentType={log.content_type}
            requestLabel={log.is_websocket ? "Sent" : undefined}
            responseLabel={log.is_websocket ? "Received" : undefined}
            flowLabel={log.is_websocket ? "Upstream" : undefined}
          />
          <TokenUsageStats log={log} />
          <DetailSection title="Session Evidence">
            <RequestEvidenceViewer evidenceJson={log.session_evidence_json} />
          </DetailSection>
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

        <div className="flex justify-end gap-3 p-6 border-t border-border-light flex-shrink-0">
          <button type="button" onClick={onClose} className="btn btn-secondary">
            Close
          </button>
        </div>
      </div>
    </div>
  );
}

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

/**
 * Shared transport / evidence summary line shown beneath the status badges.
 *
 * Uses `getLogEvidenceSummary`, which prefers gateway terminal text when
 * present and otherwise formats v2 transport evidence as
 * `{source} {kind} ({signal}) {stage-phrase}` — keeping detail and list views
 * in lock-step with no duplicated heuristics.
 */
function EvidenceSummaryLine({ log }: { log: RequestLog }) {
  const summary = getLogEvidenceSummary(log);
  if (!summary) {
    return null;
  }
  return (
    <p className="text-sm text-text-secondary" role="note">
      {summary}
    </p>
  );
}

function StatusBadges({ log }: { log: RequestLog }) {
  const lifecycle = getLogLifecyclePresentation(log);

  return (
    <div className="flex items-center gap-3 flex-wrap">
      <span
        className={`inline-flex items-center gap-2 px-3 py-1.5 rounded-full text-sm font-medium ${getDiagnosticToneClass(lifecycle.outcomeTone)}`}
        title={lifecycle.outcomeLabel}
      >
        {lifecycle.shortOutcomeLabel}
      </span>
      <span
        className={`inline-flex items-center gap-1.5 px-3 py-1.5 rounded-full text-sm font-medium ${lifecycle.isLegacy ? getDiagnosticToneClass("warning") : BADGE_STYLES.INFO}`}
      >
        {lifecycle.semanticsVersionLabel}
      </span>
      {!lifecycle.isLegacy && lifecycle.clientActionLabel && (
        <span
          className={`inline-flex items-center gap-1.5 px-3 py-1.5 rounded-full text-sm font-medium ${getDiagnosticToneClass(lifecycle.clientActionTone ?? "info")}`}
        >
          {lifecycle.clientActionLabel}
        </span>
      )}
      {!lifecycle.isLegacy && lifecycle.terminationReasonLabel && (
        <span
          className={`inline-flex items-center gap-1.5 px-3 py-1.5 rounded-full text-sm font-medium ${getDiagnosticToneClass(lifecycle.terminationReasonTone ?? "info")}`}
        >
          {lifecycle.terminationReasonLabel}
        </span>
      )}
      {log.client_transport_status_code != null &&
        lifecycle.transportStatusLabel && (
          <span
            className={`inline-flex items-center gap-1.5 px-3 py-1.5 rounded-full text-sm font-medium ${getTransportBadgeClass(log.client_transport_status_code)}`}
          >
            {lifecycle.transportStatusLabel}
          </span>
        )}
      {lifecycle.showLifecycle && lifecycle.clientVisibilityLabel && (
        <span
          className={`inline-flex items-center gap-1.5 px-3 py-1.5 rounded-full text-sm font-medium ${getDiagnosticToneClass(lifecycle.clientVisibilityTone ?? "info")}`}
        >
          {lifecycle.clientVisibilityLabel}
        </span>
      )}
      {lifecycle.showLifecycle && lifecycle.commitmentLabel && (
        <span
          className={`inline-flex items-center gap-1.5 px-3 py-1.5 rounded-full text-sm font-medium ${getDiagnosticToneClass(lifecycle.commitmentTone ?? "info")}`}
        >
          {lifecycle.commitmentLabel}
        </span>
      )}
      {log.is_sticky && (
        <span className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-full text-sm font-medium bg-indigo-100 text-indigo-800 dark:bg-indigo-900/30 dark:text-indigo-300">
          Sticky Session
        </span>
      )}
      {log.retry_count > 0 && (
        <span className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-full text-sm font-medium bg-amber-100 text-amber-800 dark:bg-amber-900/30 dark:text-amber-300">
          {log.retry_count} {log.retry_count === 1 ? "retry" : "retries"}
        </span>
      )}
    </div>
  );
}

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
  const lifecycle = getLogLifecyclePresentation(log);
  const requestAttempts = log.attempts ?? [];
  const hasMultipleAttempts = requestAttempts.length > 1;
  const showProviderChain =
    hasMultipleAttempts && !log.is_websocket && !lifecycle.isLegacy;
  const resolvedProviderName =
    providerNames?.get(log.provider_id) || providerName || log.provider_id;
  const providerLabel = getPrimaryProviderLabel(log);

  return (
    <DetailSection title="Request Information">
      {showProviderChain ? (
        <div className="py-2">
          <div className="flex items-center justify-between mb-2">
            <span className="text-sm text-text-secondary">Provider Chain</span>
          </div>
          <ProviderChain
            attempts={requestAttempts}
            providerNames={providerNames}
            serviceOutcome={log.service_outcome}
          />
        </div>
      ) : (
        <DetailRow label={providerLabel} value={resolvedProviderName} />
      )}
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
  const lifecycle = getLogLifecyclePresentation(log);
  const hasTTFT =
    log.is_sse &&
    log.first_token_ms !== null &&
    log.first_token_ms !== undefined;

  return (
    <DetailSection title="Response">
      <DetailRow
        label="Transport Status"
        value={
          lifecycle.transportStatusLabel ? (
            <span
              className={`inline-flex items-center px-2 py-0.5 rounded text-xs font-medium ${getTransportBadgeClass(log.client_transport_status_code)}`}
            >
              {lifecycle.transportStatusLabel}
            </span>
          ) : (
            "—"
          )
        }
      />
      <DetailRow
        label="Latency"
        value={
          <div className="flex items-center gap-2">
            <span className="font-mono">{log.latency_ms}ms</span>
            {hasTTFT && (
              <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded text-xs font-medium bg-purple-100 text-purple-700 dark:bg-purple-900/30 dark:text-purple-300">
                TTFT: {log.first_token_ms}ms
              </span>
            )}
          </div>
        }
      />
      <DetailRow
        label="Completion State"
        value={lifecycle.completionStateLabel || "—"}
      />
    </DetailSection>
  );
}

function ReasoningInfo({ log }: { log: RequestLog }) {
  return (
    <DetailSection title="Requested Reasoning">
      <DetailRow
        label="Observation"
        value={formatReasoningObservation(log.reasoning_observation_state)}
      />
      <DetailRow
        label="Effort"
        value={formatReasoningString(log.reasoning_effort)}
        mono
      />
      <DetailRow
        label="Thinking Mode"
        value={formatReasoningString(log.reasoning_mode)}
        mono
      />
      <DetailRow
        label="Thinking Budget"
        value={
          log.reasoning_budget_tokens == null
            ? "—"
            : `${log.reasoning_budget_tokens} tokens`
        }
        mono
      />
    </DetailSection>
  );
}

function formatReasoningObservation(
  state: RequestLog["reasoning_observation_state"],
): string {
  switch (state) {
    case "captured":
      return "Captured";
    case "absent":
      return "Not requested";
    case "invalid":
      return "Invalid request";
    case "ambiguous":
      return "Ambiguous request";
    case "unsupported":
      return "Unsupported";
    default:
      return "Legacy unknown";
  }
}

function formatReasoningString(value?: string | null): string {
  if (value === "") return `""`;
  return value ?? "—";
}

function SemanticsInfo({ log }: { log: RequestLog }) {
  const lifecycle = getLogLifecyclePresentation(log);

  return (
    <DetailSection title="Assessment">
      <DetailRow
        label="Semantics Version"
        value={lifecycle.semanticsVersionLabel}
      />
      <DetailRow label="Service Outcome" value={lifecycle.outcomeLabel} />
      <DetailRow
        label="Client Action"
        value={lifecycle.clientActionLabel || "—"}
      />
      <DetailRow
        label="Termination Reason"
        value={lifecycle.terminationReasonLabel || "—"}
      />
      <DetailRow
        label="Termination Actor"
        value={lifecycle.terminationActorLabel || "—"}
      />
      {lifecycle.legacyNote && (
        <div className="rounded-lg border border-amber-200 bg-amber-50/70 px-3 py-2 text-sm text-amber-800 dark:border-amber-900 dark:bg-amber-950/30 dark:text-amber-200">
          {lifecycle.legacyNote}
        </div>
      )}
    </DetailSection>
  );
}

function WebSocketLifecycleInfo({ log }: { log: RequestLog }) {
  const lifecycle = getLogLifecyclePresentation(log);

  if (lifecycle.isLegacy) {
    return null;
  }

  return (
    <DetailSection title="WebSocket Lifecycle">
      <DetailRow label="Outcome" value={lifecycle.outcomeLabel} />
      <DetailRow
        label="Client Visibility"
        value={lifecycle.clientVisibilityLabel || "—"}
      />
      <DetailRow
        label="Session Commit"
        value={lifecycle.commitmentLabel || "—"}
      />
      <DetailRow
        label="Commit Source"
        value={lifecycle.commitSourceLabel || "—"}
      />
    </DetailSection>
  );
}

function ClientInfo({ log }: { log: RequestLog }) {
  const hasExtendedInfo =
    log.user_agent || log.request_id_header || log.user_id;

  return (
    <DetailSection title="Client Information">
      <div
        className={`grid gap-3 mb-3 ${log.user_id ? "grid-cols-2" : "grid-cols-1"}`}
      >
        <ClientInfoCard label="IP Address" value={log.client_ip} copyable />
        {log.user_id && (
          <ClientInfoCard label="User ID" value={log.user_id} copyable />
        )}
      </div>

      {hasExtendedInfo && (
        <div className="space-y-2">
          {log.user_agent && (
            <ClientInfoExpandedCard label="User-Agent" value={log.user_agent} />
          )}
          {log.request_id_header && (
            <ClientInfoExpandedCard
              label="Request ID (X-Request-ID)"
              value={log.request_id_header}
            />
          )}
        </div>
      )}
    </DetailSection>
  );
}

function ClientInfoCard({
  label,
  value,
  copyable = false,
}: {
  label: string;
  value: string;
  copyable?: boolean;
}) {
  return (
    <div className="px-3 py-2.5 rounded-lg bg-bg-tertiary border border-border-light">
      <div className="text-xs text-text-muted mb-1">{label}</div>
      <div className="flex items-center justify-between gap-2">
        <span
          className="text-sm font-mono truncate text-text-primary"
          title={value}
        >
          {value}
        </span>
        {copyable && value && (
          <CopyButton text={value} className="flex-shrink-0" />
        )}
      </div>
    </div>
  );
}

function ClientInfoExpandedCard({
  label,
  value,
}: {
  label: string;
  value: string;
}) {
  return (
    <div className="px-3 py-2.5 rounded-lg bg-bg-tertiary border border-border-light">
      <div className="flex items-center justify-between mb-1.5">
        <div className="text-xs text-text-muted">{label}</div>
        <CopyButton text={value} />
      </div>
      <p className="text-sm text-text-primary font-mono break-all leading-relaxed">
        {value}
      </p>
    </div>
  );
}

interface DetailSectionProps {
  title: string;
  children: ReactNode;
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
  value: ReactNode;
  mono?: boolean;
}

function DetailRow({ label, value, mono }: DetailRowProps) {
  const renderValue = () => {
    if (typeof value === "string" || typeof value === "number") {
      return (
        <span
          className={`text-sm text-text-primary ${mono ? "font-mono" : "font-medium"}`}
        >
          {value}
        </span>
      );
    }

    return value;
  };

  return (
    <div className="flex items-center justify-between gap-4 py-1">
      <span className="text-sm text-text-secondary">{label}</span>
      <div className="text-right">{renderValue()}</div>
    </div>
  );
}
