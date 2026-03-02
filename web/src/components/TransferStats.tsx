import { useState, type ReactNode } from "react";

interface TransferStatsProps {
  /** Request body size in bytes */
  requestBytes?: number;
  /** Response body size in bytes */
  responseBytes?: number;
  /** Request Content-Type header */
  contentType?: string;
  /** Default expanded state */
  defaultExpanded?: boolean;
  /** Label for the request stat card (defaults to "Request") */
  requestLabel?: string;
  /** Label for the response stat card (defaults to "Response") */
  responseLabel?: string;
  /** Label for the data flow indicator (defaults to "Server") */
  flowLabel?: string;
}

/**
 * Format bytes to human-readable string (B, KB, MB, GB)
 */
function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  if (bytes < 1024 * 1024 * 1024)
    return `${(bytes / (1024 * 1024)).toFixed(2)} MB`;
  return `${(bytes / (1024 * 1024 * 1024)).toFixed(2)} GB`;
}

/**
 * Get a shortened display name for content type
 */
function getContentTypeDisplay(contentType: string): {
  short: string;
  icon: string;
} {
  const ct = contentType.toLowerCase();
  if (ct.includes("application/json")) {
    return { short: "JSON", icon: "{ }" };
  }
  if (ct.includes("text/event-stream")) {
    return { short: "SSE Stream", icon: "⚡" };
  }
  if (ct.includes("text/plain")) {
    return { short: "Plain Text", icon: "📄" };
  }
  if (ct.includes("multipart/form-data")) {
    return { short: "Multipart", icon: "📎" };
  }
  if (ct.includes("application/x-www-form-urlencoded")) {
    return { short: "Form Data", icon: "📝" };
  }
  // Return first part before semicolon for other types
  const shortType = contentType.split(";")[0].trim();
  return { short: shortType, icon: "📦" };
}

/**
 * Collapsible transfer statistics section showing request/response sizes
 * and content type information.
 *
 * Features:
 * - Human-readable file sizes (B, KB, MB, GB)
 * - Visual data flow indicators
 * - Smart content-type display
 * - Collapsed by default to save space
 */
export function TransferStats({
  requestBytes,
  responseBytes,
  contentType,
  defaultExpanded = false,
  requestLabel = "Request",
  responseLabel = "Response",
  flowLabel = "Server",
}: TransferStatsProps) {
  const [isExpanded, setIsExpanded] = useState(defaultExpanded);

  // Check if we have any data to show
  const hasRequestBytes =
    requestBytes !== undefined && requestBytes !== null && requestBytes >= 0;
  const hasResponseBytes =
    responseBytes !== undefined && responseBytes !== null && responseBytes >= 0;
  const hasContentType = contentType && contentType.length > 0;

  // Don't render if no data
  if (!hasRequestBytes && !hasResponseBytes && !hasContentType) {
    return null;
  }

  const contentTypeInfo = hasContentType
    ? getContentTypeDisplay(contentType!)
    : null;

  // Calculate total transfer (for the collapsed summary)
  const totalBytes =
    (hasRequestBytes ? requestBytes! : 0) +
    (hasResponseBytes ? responseBytes! : 0);

  return (
    <div className="rounded-lg border border-border-light bg-bg-tertiary/50 overflow-hidden">
      {/* Collapsible Header */}
      <button
        type="button"
        onClick={() => setIsExpanded(!isExpanded)}
        className="w-full px-3 py-2.5 flex items-center justify-between hover:bg-bg-tertiary transition-colors"
      >
        <div className="flex items-center gap-2">
          {/* Expand/Collapse Icon */}
          <svg
            className={`w-4 h-4 text-text-muted transition-transform duration-200 ${
              isExpanded ? "rotate-90" : ""
            }`}
            fill="none"
            stroke="currentColor"
            viewBox="0 0 24 24"
          >
            <path
              strokeLinecap="round"
              strokeLinejoin="round"
              strokeWidth={2}
              d="M9 5l7 7-7 7"
            />
          </svg>

          {/* Section Icon */}
          <svg
            className="w-4 h-4 text-text-muted"
            fill="none"
            stroke="currentColor"
            viewBox="0 0 24 24"
          >
            <path
              strokeLinecap="round"
              strokeLinejoin="round"
              strokeWidth={2}
              d="M8 7h12m0 0l-4-4m4 4l-4 4m0 6H4m0 0l4 4m-4-4l4-4"
            />
          </svg>

          <span className="text-sm font-medium text-text-secondary">
            Transfer Details
          </span>
        </div>

        {/* Collapsed Summary */}
        {!isExpanded && totalBytes > 0 && (
          <span className="text-xs text-text-muted font-mono">
            {formatBytes(totalBytes)} total
          </span>
        )}
      </button>

      {/* Expanded Content */}
      {isExpanded && (
        <div className="px-3 pb-3 pt-1">
          <div className="grid grid-cols-3 gap-2">
            {/* Request Size */}
            <TransferStatCard
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
                    d="M7 11l5-5m0 0l5 5m-5-5v12"
                  />
                </svg>
              }
              label={requestLabel}
              value={hasRequestBytes ? formatBytes(requestBytes!) : "—"}
              iconColor="text-blue-500"
            />

            {/* Response Size */}
            <TransferStatCard
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
                    d="M17 13l-5 5m0 0l-5-5m5 5V6"
                  />
                </svg>
              }
              label={responseLabel}
              value={hasResponseBytes ? formatBytes(responseBytes!) : "—"}
              iconColor="text-green-500"
            />

            {/* Content Type */}
            <TransferStatCard
              icon={
                <span className="text-sm">{contentTypeInfo?.icon || "📦"}</span>
              }
              label="Content"
              value={contentTypeInfo?.short || "—"}
              title={hasContentType ? contentType : undefined}
              iconColor="text-purple-500"
            />
          </div>

          {/* Visual Data Flow Indicator */}
          {hasRequestBytes && hasResponseBytes && (
            <div className="mt-3 flex items-center justify-center gap-2 text-xs text-text-muted">
              <span className="font-mono">{formatBytes(requestBytes!)}</span>
              <div className="flex items-center gap-1">
                <span className="text-blue-400">→</span>
                <span className="px-1.5 py-0.5 rounded bg-bg-secondary text-[10px] uppercase tracking-wide">
                  {flowLabel}
                </span>
                <span className="text-green-400">→</span>
              </div>
              <span className="font-mono">{formatBytes(responseBytes!)}</span>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

interface TransferStatCardProps {
  icon: ReactNode;
  label: string;
  value: string;
  title?: string;
  iconColor?: string;
}

function TransferStatCard({
  icon,
  label,
  value,
  title,
  iconColor = "text-text-muted",
}: TransferStatCardProps) {
  return (
    <div
      className="px-2.5 py-2 rounded-md bg-bg-secondary border border-border-light"
      title={title}
    >
      <div className="flex items-center gap-1.5 mb-1">
        <span className={iconColor}>{icon}</span>
        <span className="text-[11px] text-text-muted uppercase tracking-wide">
          {label}
        </span>
      </div>
      <p
        className={`text-sm font-mono truncate ${
          value === "—" ? "text-text-muted" : "text-text-primary"
        }`}
      >
        {value}
      </p>
    </div>
  );
}
