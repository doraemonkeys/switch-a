import { useState } from "react";
import type { ActiveRequest } from "../../api/types";
import type { GroupViewMode } from "./types";
import {
  formatDuration,
  getRequestDuration,
  formatBytes,
  formatIdleDuration,
} from "./utils";
import {
  COMPACT_MODEL_MAX_WIDTH,
  COMPACT_PROVIDER_MAX_WIDTH,
  COMPACT_IP_MAX_WIDTH,
  SSE_BADGE_FONT_SIZE,
  SSE_BADGE_COLORS,
  WS_BADGE_COLORS,
  WS_IDLE_WARNING_THRESHOLD_MS,
} from "./constants";

// =============================================================================
// Truncated Text with Tooltip
// =============================================================================

interface TruncatedTextProps {
  text: string;
  maxWidth?: string;
  className?: string;
}

function TruncatedText({ text, maxWidth, className = "" }: TruncatedTextProps) {
  return (
    <span
      className={`truncate min-w-0 ${className}`}
      style={maxWidth ? { maxWidth } : undefined}
      title={text}
    >
      {text}
    </span>
  );
}

// =============================================================================
// Request Detail Panel (Expanded View)
// =============================================================================

interface RequestDetailPanelProps {
  request: ActiveRequest;
  providerName?: string;
  durationStr: string;
  isLongRunning: boolean;
  currentTime: number;
}

function RequestDetailPanel({
  request,
  providerName,
  durationStr,
  isLongRunning,
  currentTime,
}: RequestDetailPanelProps) {
  return (
    <div className="px-4 py-3 bg-bg-secondary border-t border-border-light">
      <div className="grid grid-cols-2 gap-x-6 gap-y-2 text-sm">
        {/* Model */}
        <div>
          <span className="text-text-muted text-xs uppercase tracking-wide">
            Model
          </span>
          <p className="font-medium text-text-primary break-all">
            {request.model || "Unknown"}
          </p>
        </div>

        {/* Provider */}
        <div>
          <span className="text-text-muted text-xs uppercase tracking-wide">
            Provider
          </span>
          <p className="text-text-primary break-all">
            {providerName || request.provider_id}
          </p>
        </div>

        {/* API Type */}
        <div>
          <span className="text-text-muted text-xs uppercase tracking-wide">
            API Type
          </span>
          <p className="text-text-primary">
            <span className="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-blue-100 text-blue-800 dark:bg-blue-900/30 dark:text-blue-300 uppercase">
              {request.api_type}
            </span>
            {request.is_sse && (
              <span
                className={`ml-2 inline-flex items-center px-2 py-0.5 rounded text-xs font-medium ${SSE_BADGE_COLORS}`}
              >
                SSE
              </span>
            )}
            {request.is_websocket && (
              <span
                className={`ml-2 inline-flex items-center px-2 py-0.5 rounded text-xs font-medium ${WS_BADGE_COLORS}`}
              >
                WS
              </span>
            )}
          </p>
        </div>

        {/* Duration */}
        <div>
          <span className="text-text-muted text-xs uppercase tracking-wide">
            Duration
          </span>
          <p
            className={`font-mono font-semibold ${
              isLongRunning
                ? "text-amber-600 dark:text-amber-400"
                : "text-text-primary"
            }`}
          >
            {durationStr}
            {isLongRunning && (
              <span className="ml-1 text-amber-500">⚠ Long running</span>
            )}
          </p>
        </div>

        {/* Client IP */}
        <div>
          <span className="text-text-muted text-xs uppercase tracking-wide">
            Client IP
          </span>
          <p className="text-text-primary font-mono text-xs">
            {request.client_ip}
          </p>
        </div>

        {/* User ID */}
        {request.user_id && (
          <div>
            <span className="text-text-muted text-xs uppercase tracking-wide">
              User ID
            </span>
            <p className="text-text-primary font-mono text-xs break-all">
              {request.user_id}
            </p>
          </div>
        )}

        {/* Request ID */}
        <div className="col-span-2">
          <span className="text-text-muted text-xs uppercase tracking-wide">
            Request ID
          </span>
          <p className="text-text-muted font-mono text-xs break-all">
            {request.request_id}
          </p>
        </div>

        {/* Started At */}
        <div className="col-span-2">
          <span className="text-text-muted text-xs uppercase tracking-wide">
            Started At
          </span>
          <p className="text-text-muted text-xs">
            {new Date(request.started_at).toLocaleString()}
          </p>
        </div>

        {/* WebSocket Data Transfer */}
        {request.is_websocket &&
          ((request.bytes_sent ?? 0) > 0 ||
            (request.bytes_received ?? 0) > 0) && (
            <div className="col-span-2">
              <span className="text-text-muted text-xs uppercase tracking-wide">
                Data Transfer
              </span>
              <p className="text-text-primary font-mono text-xs">
                ↑ {formatBytes(request.bytes_sent ?? 0)} (
                {request.msgs_sent ?? 0} msgs)
                {" / "}↓ {formatBytes(request.bytes_received ?? 0)} (
                {request.msgs_received ?? 0} msgs)
              </p>
            </div>
          )}

        {/* WebSocket Last Activity */}
        {request.is_websocket && !!request.last_activity_at && (
          <div className="col-span-2">
            <span className="text-text-muted text-xs uppercase tracking-wide">
              Last Activity
            </span>
            <p
              className={`text-xs font-mono ${
                currentTime - request.last_activity_at >
                WS_IDLE_WARNING_THRESHOLD_MS
                  ? "text-amber-600 dark:text-amber-400 font-semibold"
                  : "text-text-primary"
              }`}
            >
              {formatIdleDuration(request.last_activity_at, currentTime) ||
                "just now"}{" "}
              ({new Date(request.last_activity_at).toLocaleTimeString()})
            </p>
          </div>
        )}
      </div>
    </div>
  );
}

// =============================================================================
// WebSocket Live Metrics
// =============================================================================

interface WsLiveIndicatorProps {
  request: ActiveRequest;
  currentTime: number;
}

/**
 * Compact inline indicator for WS data flow: "↑1.2MB ↓8.4MB idle 3s"
 */
function WsLiveIndicator({ request, currentTime }: WsLiveIndicatorProps) {
  if (!request.is_websocket) return null;

  const hasSent = (request.bytes_sent ?? 0) > 0;
  const hasReceived = (request.bytes_received ?? 0) > 0;
  if (!hasSent && !hasReceived) return null;

  const idleStr = formatIdleDuration(
    request.last_activity_at ?? 0,
    currentTime,
  );
  const isIdleWarning =
    !!request.last_activity_at &&
    currentTime - request.last_activity_at > WS_IDLE_WARNING_THRESHOLD_MS;

  return (
    <span className="inline-flex items-center gap-1.5 text-xs font-mono text-text-secondary flex-shrink-0">
      {hasSent && (
        <span title="Bytes sent (client → upstream)">
          ↑{formatBytes(request.bytes_sent!)}
        </span>
      )}
      {hasReceived && (
        <span title="Bytes received (upstream → client)">
          ↓{formatBytes(request.bytes_received!)}
        </span>
      )}
      {idleStr && (
        <span
          className={
            isIdleWarning
              ? "text-amber-600 dark:text-amber-400 font-semibold"
              : "text-text-muted"
          }
          title="Time since last WebSocket message"
        >
          {idleStr}
        </span>
      )}
    </span>
  );
}

// =============================================================================
// Shared Components
// =============================================================================

interface ExpandIconProps {
  isExpanded: boolean;
  size?: "sm" | "md";
}

function ExpandIcon({ isExpanded, size = "md" }: ExpandIconProps) {
  const sizeClass = size === "sm" ? "h-3 w-3" : "h-4 w-4";
  return (
    <svg
      className={`${sizeClass} text-text-muted transition-transform flex-shrink-0 ${
        isExpanded ? "rotate-90" : ""
      }`}
      fill="none"
      stroke="currentColor"
      viewBox="0 0 24 24"
      aria-hidden="true"
    >
      <path
        strokeLinecap="round"
        strokeLinejoin="round"
        strokeWidth={2}
        d="M9 5l7 7-7 7"
      />
    </svg>
  );
}

interface PulseIndicatorProps {
  isLongRunning: boolean;
  size?: "sm" | "md";
}

function PulseIndicator({ isLongRunning, size = "md" }: PulseIndicatorProps) {
  const sizeClass = size === "sm" ? "h-2 w-2" : "h-3 w-3";
  const colorClass = isLongRunning ? "bg-amber" : "bg-green";
  return (
    <span
      className={`relative flex ${sizeClass} flex-shrink-0`}
      aria-hidden="true"
    >
      <span
        className={`animate-ping absolute inline-flex h-full w-full rounded-full opacity-75 ${colorClass}-400`}
      />
      <span
        className={`relative inline-flex rounded-full ${sizeClass} ${colorClass}-500`}
      />
    </span>
  );
}

// =============================================================================
// Compact Row Component
// =============================================================================

interface CompactRowProps {
  request: ActiveRequest;
  providerName?: string;
  durationStr: string;
  isLongRunning: boolean;
  isExpanded: boolean;
  onToggle: () => void;
  showModel: boolean;
  showIP: boolean;
  showAPIType: boolean;
  tooltipContent: string;
  currentTime: number;
}

function CompactRow({
  request,
  providerName,
  durationStr,
  isLongRunning,
  isExpanded,
  onToggle,
  showModel,
  showIP,
  showAPIType,
  tooltipContent,
  currentTime,
}: CompactRowProps) {
  return (
    <div
      role="button"
      tabIndex={0}
      aria-expanded={isExpanded}
      aria-label={`Active request for model ${request.model || "unknown"}. Click to ${isExpanded ? "collapse" : "expand"} details.`}
      title={!isExpanded ? tooltipContent : undefined}
      onClick={onToggle}
      onKeyDown={(e) => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          onToggle();
        }
      }}
      className={`px-4 py-2.5 hover:bg-bg-hover transition-colors flex items-center gap-3 cursor-pointer select-none ${
        isLongRunning ? "bg-amber-50/50 dark:bg-amber-900/10" : ""
      } ${isExpanded ? "bg-bg-hover" : ""}`}
    >
      <ExpandIcon isExpanded={isExpanded} size="sm" />
      <PulseIndicator isLongRunning={isLongRunning} size="sm" />

      {showModel && (
        <TruncatedText
          text={request.model || "Unknown"}
          maxWidth={COMPACT_MODEL_MAX_WIDTH}
          className="font-medium text-text-primary text-sm flex-shrink-0"
        />
      )}

      <TruncatedText
        text={providerName || request.provider_id}
        maxWidth={COMPACT_PROVIDER_MAX_WIDTH}
        className="text-text-secondary text-sm flex-shrink-0"
      />

      {showIP && (
        <TruncatedText
          text={request.client_ip}
          maxWidth={COMPACT_IP_MAX_WIDTH}
          className="text-text-muted text-xs font-mono flex-shrink-0"
        />
      )}

      {showAPIType && (
        <span
          className="inline-flex items-center px-1.5 py-0.5 rounded font-medium bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-300 uppercase flex-shrink-0"
          style={{ fontSize: SSE_BADGE_FONT_SIZE }}
        >
          {request.api_type}
        </span>
      )}

      {request.is_sse && (
        <span
          className={`inline-flex items-center px-1.5 py-0.5 rounded font-medium ${SSE_BADGE_COLORS} flex-shrink-0`}
          style={{ fontSize: SSE_BADGE_FONT_SIZE }}
        >
          SSE
        </span>
      )}

      {request.is_websocket && (
        <span
          className={`inline-flex items-center px-1.5 py-0.5 rounded font-medium ${WS_BADGE_COLORS} flex-shrink-0`}
          style={{ fontSize: SSE_BADGE_FONT_SIZE }}
        >
          WS
        </span>
      )}

      <WsLiveIndicator request={request} currentTime={currentTime} />

      <span
        className={`ml-auto font-mono text-sm font-semibold flex-shrink-0 ${
          isLongRunning
            ? "text-amber-600 dark:text-amber-400"
            : "text-text-primary"
        }`}
      >
        {durationStr}
        {isLongRunning && <span className="ml-1">⚠</span>}
      </span>
    </div>
  );
}

// =============================================================================
// Full Row Component
// =============================================================================

interface FullRowProps {
  request: ActiveRequest;
  providerName?: string;
  durationStr: string;
  isLongRunning: boolean;
  isExpanded: boolean;
  onToggle: () => void;
  currentTime: number;
}

function FullRow({
  request,
  providerName,
  durationStr,
  isLongRunning,
  isExpanded,
  onToggle,
  currentTime,
}: FullRowProps) {
  return (
    <div
      role="button"
      tabIndex={0}
      aria-expanded={isExpanded}
      aria-label={`Active request for model ${request.model || "unknown"}. Click to ${isExpanded ? "collapse" : "expand"} details.`}
      onClick={onToggle}
      onKeyDown={(e) => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          onToggle();
        }
      }}
      className={`p-4 hover:bg-bg-hover transition-colors cursor-pointer select-none ${
        isLongRunning ? "bg-amber-50/50 dark:bg-amber-900/10" : ""
      } ${isExpanded ? "bg-bg-hover" : ""}`}
    >
      <div className="flex items-start justify-between gap-4">
        <div className="flex items-center gap-3 min-w-0 flex-1">
          <ExpandIcon isExpanded={isExpanded} />
          <div className="flex-shrink-0" aria-hidden="true">
            <PulseIndicator isLongRunning={isLongRunning} />
          </div>

          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-2 flex-wrap">
              <span
                className="font-medium text-text-primary"
                title={request.model || "Unknown model"}
              >
                {request.model || "Unknown model"}
              </span>
              <span className="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-blue-100 text-blue-800 dark:bg-blue-900/30 dark:text-blue-300 uppercase">
                {request.api_type}
              </span>
              {request.is_sse && (
                <span
                  className={`inline-flex items-center px-2 py-0.5 rounded text-xs font-medium ${SSE_BADGE_COLORS}`}
                >
                  SSE
                </span>
              )}
              {request.is_websocket && (
                <span
                  className={`inline-flex items-center px-2 py-0.5 rounded text-xs font-medium ${WS_BADGE_COLORS}`}
                >
                  WS
                </span>
              )}
              <WsLiveIndicator request={request} currentTime={currentTime} />
            </div>

            <div className="flex items-center gap-3 mt-1 text-sm text-text-secondary">
              <span title={providerName || request.provider_id}>
                {providerName || request.provider_id}
              </span>
              {request.user_id && (
                <>
                  <span className="text-text-muted">•</span>
                  <span
                    className="truncate font-mono text-xs"
                    title={request.user_id}
                  >
                    {request.user_id}
                  </span>
                </>
              )}
              <span className="text-text-muted">•</span>
              <span className="font-mono text-xs">{request.client_ip}</span>
            </div>
          </div>
        </div>

        <div className="flex-shrink-0 text-right">
          <span
            className={`font-mono text-lg font-semibold ${
              isLongRunning
                ? "text-amber-600 dark:text-amber-400"
                : "text-text-primary"
            }`}
          >
            {durationStr}
          </span>
          {isLongRunning && (
            <p className="text-xs text-amber-600 dark:text-amber-400 mt-0.5">
              Long running
            </p>
          )}
        </div>
      </div>
    </div>
  );
}

// =============================================================================
// Request Row Component
// =============================================================================

export interface RequestRowProps {
  request: ActiveRequest;
  currentTime: number;
  longRunningThreshold: number;
  providerName?: string;
  compact?: boolean;
  /** Which field is used for grouping (to avoid showing redundant info) */
  groupBy?: GroupViewMode;
}

export function RequestRow({
  request,
  currentTime,
  longRunningThreshold,
  providerName,
  compact = false,
  groupBy,
}: RequestRowProps) {
  const [isExpanded, setIsExpanded] = useState(false);
  const durationMs = getRequestDuration(request, currentTime);
  // WebSocket connections are inherently long-lived; the threshold doesn't apply
  const isLongRunning =
    !request.is_websocket && durationMs > longRunningThreshold;
  const durationStr = formatDuration(durationMs);

  const showModel = groupBy !== "model";
  const showIP = groupBy !== "ip";
  const showAPIType = groupBy !== "api";

  let protocolSuffix = "";
  if (request.is_websocket) protocolSuffix = " (WS)";
  else if (request.is_sse) protocolSuffix = " (SSE)";

  const tooltipContent = [
    `Model: ${request.model || "Unknown"}`,
    `Provider: ${providerName || request.provider_id}`,
    `API: ${request.api_type}${protocolSuffix}`,
    `Duration: ${durationStr}`,
    `IP: ${request.client_ip}`,
    request.user_id ? `User: ${request.user_id}` : null,
  ]
    .filter(Boolean)
    .join("\n");

  const handleToggle = () => setIsExpanded(!isExpanded);

  const detailPanel = isExpanded && (
    <RequestDetailPanel
      request={request}
      providerName={providerName}
      durationStr={durationStr}
      isLongRunning={isLongRunning}
      currentTime={currentTime}
    />
  );

  if (compact) {
    return (
      <div className="flex flex-col">
        <CompactRow
          request={request}
          providerName={providerName}
          durationStr={durationStr}
          isLongRunning={isLongRunning}
          isExpanded={isExpanded}
          onToggle={handleToggle}
          showModel={showModel}
          showIP={showIP}
          showAPIType={showAPIType}
          tooltipContent={tooltipContent}
          currentTime={currentTime}
        />
        {detailPanel}
      </div>
    );
  }

  return (
    <div className="flex flex-col">
      <FullRow
        request={request}
        providerName={providerName}
        durationStr={durationStr}
        isLongRunning={isLongRunning}
        isExpanded={isExpanded}
        onToggle={handleToggle}
        currentTime={currentTime}
      />
      {detailPanel}
    </div>
  );
}
