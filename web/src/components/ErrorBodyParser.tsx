import { useState, useMemo } from "react";

interface ParsedError {
  type?: string;
  code?: string;
  message?: string;
  details?: Record<string, unknown>;
  allowedClients?: string[];
  raw: string;
  isJson: boolean;
}

interface DiagnosticTip {
  icon: string;
  text: string;
  severity: "info" | "warning" | "error";
}

interface ErrorBodyParserProps {
  body: string;
  statusCode: number;
  userAgent?: string;
  className?: string;
}

/**
 * Smart error body parser that:
 * 1. Parses JSON error responses into structured display
 * 2. Generates actionable diagnostic tips based on error patterns
 * 3. Falls back to raw text display for non-JSON responses
 */
export function ErrorBodyParser({
  body,
  statusCode,
  userAgent,
  className = "",
}: ErrorBodyParserProps) {
  const [showRaw, setShowRaw] = useState(false);

  const parsed = useMemo(() => parseErrorBody(body), [body]);
  const tips = useMemo(
    () => generateDiagnosticTips(parsed, statusCode, userAgent),
    [parsed, statusCode, userAgent],
  );

  if (!body || body.trim() === "") {
    return null;
  }

  return (
    <div className={`space-y-3 ${className}`}>
      {/* Structured Error Display */}
      {parsed.isJson ? (
        <StructuredError parsed={parsed} />
      ) : (
        <RawErrorDisplay body={body} />
      )}

      {/* Diagnostic Tips */}
      {tips.length > 0 && <DiagnosticTips tips={tips} />}

      {/* Toggle Raw View for JSON */}
      {parsed.isJson && (
        <button
          type="button"
          onClick={() => setShowRaw(!showRaw)}
          className="text-xs text-text-muted hover:text-text-secondary transition-colors flex items-center gap-1"
        >
          <svg
            className={`w-3 h-3 transition-transform ${showRaw ? "rotate-90" : ""}`}
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
          {showRaw ? "Hide" : "Show"} raw response
        </button>
      )}

      {/* Raw JSON Collapsible */}
      {showRaw && parsed.isJson && (
        <pre className="p-3 rounded-lg bg-bg-tertiary text-xs font-mono text-text-secondary overflow-x-auto whitespace-pre-wrap break-words max-h-48 overflow-y-auto">
          {JSON.stringify(JSON.parse(body), null, 2)}
        </pre>
      )}
    </div>
  );
}

function StructuredError({ parsed }: { parsed: ParsedError }) {
  return (
    <div className="rounded-lg border border-red-200 dark:border-red-800 bg-red-50/50 dark:bg-red-900/10 overflow-hidden">
      {/* Error Header */}
      <div className="px-3 py-2 bg-red-100/50 dark:bg-red-900/20 border-b border-red-200 dark:border-red-800 flex items-center gap-2">
        <span className="text-red-600 dark:text-red-400">
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
              d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z"
            />
          </svg>
        </span>
        <span className="text-sm font-medium text-red-700 dark:text-red-300">
          {parsed.type || parsed.code || "Error"}
        </span>
      </div>

      {/* Error Body */}
      <div className="p-3 space-y-2">
        {parsed.message && (
          <p className="text-sm text-red-700 dark:text-red-300">
            {parsed.message}
          </p>
        )}

        {/* Additional Details */}
        {parsed.details && Object.keys(parsed.details).length > 0 && (
          <div className="pt-2 mt-2 border-t border-red-200 dark:border-red-800">
            <p className="text-xs text-text-muted mb-1.5">Details:</p>
            <div className="space-y-1">
              {Object.entries(parsed.details).map(([key, value]) => (
                <div key={key} className="flex items-start gap-2 text-xs">
                  <span className="text-text-muted min-w-[80px]">{key}:</span>
                  <span className="text-red-600 dark:text-red-400 font-mono break-all">
                    {typeof value === "object"
                      ? JSON.stringify(value)
                      : String(value)}
                  </span>
                </div>
              ))}
            </div>
          </div>
        )}

        {/* Allowed Clients List (for 403 errors) */}
        {parsed.allowedClients && parsed.allowedClients.length > 0 && (
          <div className="pt-2 mt-2 border-t border-red-200 dark:border-red-800">
            <p className="text-xs text-text-muted mb-1.5">Allowed Clients:</p>
            <div className="flex flex-wrap gap-1.5">
              {parsed.allowedClients.map((client) => (
                <span
                  key={client}
                  className="px-2 py-0.5 rounded-full text-xs font-mono bg-amber-100 text-amber-800 dark:bg-amber-900/30 dark:text-amber-300"
                >
                  {client}
                </span>
              ))}
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

function RawErrorDisplay({ body }: { body: string }) {
  return (
    <div className="p-3 rounded-lg bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-red-800">
      <p className="text-sm text-red-700 dark:text-red-300 font-mono whitespace-pre-wrap break-words">
        {body}
      </p>
    </div>
  );
}

function DiagnosticTips({ tips }: { tips: DiagnosticTip[] }) {
  return (
    <div className="space-y-2">
      {tips.map((tip, index) => (
        <div
          key={index}
          className={`flex items-start gap-2 px-3 py-2 rounded-lg text-sm ${getTipStyles(tip.severity)}`}
        >
          <span className="flex-shrink-0 mt-0.5">{tip.icon}</span>
          <span>{tip.text}</span>
        </div>
      ))}
    </div>
  );
}

function getTipStyles(severity: DiagnosticTip["severity"]): string {
  switch (severity) {
    case "error":
      return "bg-red-50 dark:bg-red-900/20 text-red-700 dark:text-red-300 border border-red-200 dark:border-red-800";
    case "warning":
      return "bg-amber-50 dark:bg-amber-900/20 text-amber-700 dark:text-amber-300 border border-amber-200 dark:border-amber-800";
    case "info":
    default:
      return "bg-blue-50 dark:bg-blue-900/20 text-blue-700 dark:text-blue-300 border border-blue-200 dark:border-blue-800";
  }
}

// =============================================================================
// Parsing Logic
// =============================================================================

function parseErrorBody(body: string): ParsedError {
  const trimmed = body.trim();

  // Try parsing as JSON
  try {
    const json = JSON.parse(trimmed);
    return extractErrorInfo(json, trimmed);
  } catch {
    return {
      raw: trimmed,
      isJson: false,
    };
  }
}

function extractErrorInfo(
  json: Record<string, unknown>,
  raw: string,
): ParsedError {
  const result: ParsedError = {
    raw,
    isJson: true,
  };

  // Handle nested error object (common pattern)
  const errorObj =
    typeof json.error === "object" && json.error !== null
      ? (json.error as Record<string, unknown>)
      : json;

  // Extract type/code
  result.type =
    getString(errorObj, "type") ||
    getString(json, "type") ||
    getString(errorObj, "error_type");
  result.code =
    getString(errorObj, "code") ||
    getString(json, "code") ||
    getString(errorObj, "error_code");

  // Extract message
  result.message =
    getString(errorObj, "message") ||
    getString(json, "message") ||
    getString(errorObj, "error") ||
    getString(json, "error");

  // Extract allowed clients list (Claude API specific)
  const allowedClients =
    getArray(errorObj, "allowedClients") || getArray(json, "allowedClients");
  if (allowedClients) {
    result.allowedClients = allowedClients.filter(
      (item): item is string => typeof item === "string",
    );
  }

  // Collect additional details (exclude already extracted fields)
  const excludeKeys = new Set([
    "type",
    "code",
    "message",
    "error",
    "error_type",
    "error_code",
    "allowedClients",
  ]);
  const details: Record<string, unknown> = {};

  const sourceObj = typeof json.error === "object" ? errorObj : json;
  for (const [key, value] of Object.entries(sourceObj)) {
    if (!excludeKeys.has(key) && value !== undefined && value !== null) {
      details[key] = value;
    }
  }

  if (Object.keys(details).length > 0) {
    result.details = details;
  }

  return result;
}

function getString(
  obj: Record<string, unknown>,
  key: string,
): string | undefined {
  const value = obj[key];
  return typeof value === "string" ? value : undefined;
}

function getArray(
  obj: Record<string, unknown>,
  key: string,
): unknown[] | undefined {
  const value = obj[key];
  return Array.isArray(value) ? value : undefined;
}

// =============================================================================
// Diagnostic Tips Generation
// =============================================================================

function generateDiagnosticTips(
  parsed: ParsedError,
  statusCode: number,
  userAgent?: string,
): DiagnosticTip[] {
  const tips: DiagnosticTip[] = [];

  // Dispatch to specific handlers based on status code
  handle403Tips(tips, parsed, userAgent);
  handleAuthTips(tips, statusCode);
  handleServerErrorTips(tips, statusCode);
  handleBadRequestTips(tips, statusCode, parsed);

  return tips;
}

/** Handle 403 Forbidden tips - client not allowed scenarios */
function handle403Tips(
  tips: DiagnosticTip[],
  parsed: ParsedError,
  userAgent?: string,
): void {
  if (parsed.allowedClients && parsed.allowedClients.length > 0 && userAgent) {
    const clientName = extractClientName(userAgent);
    const isAllowed = parsed.allowedClients.some(
      (allowed) =>
        clientName.toLowerCase().includes(allowed.toLowerCase()) ||
        userAgent.toLowerCase().includes(allowed.toLowerCase()),
    );
    if (!isAllowed) {
      tips.push({
        icon: "💡",
        text: `Your client "${clientName}" is not in the allowed list. Allowed: ${parsed.allowedClients.join(", ")}`,
        severity: "warning",
      });
    }
  } else if (parsed.allowedClients && parsed.allowedClients.length > 0) {
    tips.push({
      icon: "💡",
      text: `This endpoint restricts client types. Allowed: ${parsed.allowedClients.join(", ")}`,
      severity: "info",
    });
  }
}

/** Handle authentication-related tips (401, 402, 403, 429) */
function handleAuthTips(tips: DiagnosticTip[], statusCode: number): void {
  const authTips: Record<number, DiagnosticTip> = {
    401: {
      icon: "🔐",
      text: "API key may be invalid, expired, or missing. Verify the key is correct and active.",
      severity: "warning",
    },
    402: {
      icon: "💳",
      text: "Account quota exhausted or billing issue. Check your provider's billing dashboard.",
      severity: "warning",
    },
    403: {
      icon: "🚫",
      text: "Access forbidden. Check if your API key has the required permissions.",
      severity: "warning",
    },
    429: {
      icon: "⏱️",
      text: "Rate limit exceeded. Consider reducing request frequency or increasing rate limit tier.",
      severity: "warning",
    },
  };

  // Only add 403 tip if no allowedClients tip was added (checked by existing tips)
  if (statusCode === 403 && tips.length > 0) return;

  const tip = authTips[statusCode];
  if (tip) tips.push(tip);
}

/** Handle 5xx server error tips */
function handleServerErrorTips(
  tips: DiagnosticTip[],
  statusCode: number,
): void {
  if (statusCode >= 500) {
    tips.push({
      icon: "🔧",
      text: "Provider server error. This is usually temporary — the request will be automatically retried.",
      severity: "info",
    });
  }
}

/** Handle 400 Bad Request tips based on error message patterns */
function handleBadRequestTips(
  tips: DiagnosticTip[],
  statusCode: number,
  parsed: ParsedError,
): void {
  if (statusCode !== 400) return;

  const message = (parsed.message || "").toLowerCase();

  if (message.includes("context") || message.includes("token")) {
    tips.push({
      icon: "📝",
      text: "Request may exceed context length limit. Try reducing message history or input size.",
      severity: "info",
    });
  }

  if (message.includes("model")) {
    tips.push({
      icon: "🤖",
      text: "Model may not be available or the model name is incorrect.",
      severity: "info",
    });
  }
}

function extractClientName(userAgent: string): string {
  // Extract the main client name from User-Agent
  // e.g., "cursor/0.50 (Windows)" -> "cursor"
  // e.g., "Claude-Code/1.0" -> "Claude-Code"
  const match = userAgent.match(/^([a-zA-Z0-9_-]+)/);
  return match ? match[1] : userAgent.split("/")[0] || userAgent;
}
