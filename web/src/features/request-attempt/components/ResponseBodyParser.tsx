/**
 * Renders an attempt's response body snippet with a tone derived from the
 * attempt's health assessment. Body snippets are captured for both error and
 * non-error attempts (e.g. a client-canceled stream's last payload), so the
 * red error treatment is opt-in via `tone` rather than unconditional.
 */
export type ResponseBodyTone = "error" | "warning" | "neutral";

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

interface ResponseBodyParserProps {
  body: string;
  statusCode: number;
  userAgent?: string;
  tone?: ResponseBodyTone;
  className?: string;
}

const NEUTRAL_BODY_CLASS =
  "p-3 rounded-lg bg-bg-tertiary border border-border-light";
const NEUTRAL_TEXT_CLASS =
  "text-sm text-text-secondary font-mono whitespace-pre-wrap break-words";

interface ToneStyles {
  box: string;
  header: string;
  icon: string;
  accent: string;
  divider: string;
  detail: string;
  raw: string;
  rawText: string;
}

const ERROR_STYLES: ToneStyles = {
  box: "rounded-lg border border-red-200 dark:border-red-800 bg-red-50/50 dark:bg-red-900/10 overflow-hidden",
  header:
    "px-3 py-2 bg-red-100/50 dark:bg-red-900/20 border-b border-red-200 dark:border-red-800 flex items-center gap-2",
  icon: "text-red-600 dark:text-red-400",
  accent: "text-red-700 dark:text-red-300",
  divider: "border-red-200 dark:border-red-800",
  detail: "text-red-600 dark:text-red-400",
  raw: "p-3 rounded-lg bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-red-800",
  rawText:
    "text-sm text-red-700 dark:text-red-300 font-mono whitespace-pre-wrap break-words",
};

const WARNING_STYLES: ToneStyles = {
  box: "rounded-lg border border-amber-200 dark:border-amber-800 bg-amber-50/50 dark:bg-amber-900/10 overflow-hidden",
  header:
    "px-3 py-2 bg-amber-100/50 dark:bg-amber-900/20 border-b border-amber-200 dark:border-amber-800 flex items-center gap-2",
  icon: "text-amber-600 dark:text-amber-400",
  accent: "text-amber-700 dark:text-amber-300",
  divider: "border-amber-200 dark:border-amber-800",
  detail: "text-amber-600 dark:text-amber-400",
  raw: "p-3 rounded-lg bg-amber-50 dark:bg-amber-900/20 border border-amber-200 dark:border-amber-800",
  rawText:
    "text-sm text-amber-700 dark:text-amber-300 font-mono whitespace-pre-wrap break-words",
};

export function ResponseBodyParser({
  body,
  statusCode,
  userAgent,
  tone = "neutral",
  className = "",
}: ResponseBodyParserProps) {
  if (!body || body.trim() === "") {
    return null;
  }

  // Non-error bodies are evidence, not diagnostics: render them plain instead
  // of forcing them through the error frame.
  if (tone === "neutral") {
    return (
      <div className={`${NEUTRAL_BODY_CLASS} ${className}`}>
        <p className={NEUTRAL_TEXT_CLASS}>{body}</p>
      </div>
    );
  }

  const parsed = parseErrorBody(body);
  const styles = tone === "warning" ? WARNING_STYLES : ERROR_STYLES;
  const tips = generateDiagnosticTips(parsed, statusCode, userAgent);

  return (
    <div className={`space-y-3 ${className}`}>
      {parsed.isJson ? (
        <StructuredError parsed={parsed} styles={styles} />
      ) : (
        <RawErrorDisplay body={body} styles={styles} />
      )}

      {tips.length > 0 && <DiagnosticTips tips={tips} />}

      {parsed.isJson && (
        <details className="text-xs text-text-muted" aria-label="Raw response">
          <summary className="cursor-pointer hover:text-text-secondary transition-colors">
            Raw response
          </summary>
          <pre className="mt-2 p-3 rounded-lg bg-bg-tertiary text-xs font-mono text-text-secondary overflow-x-auto whitespace-pre-wrap break-words max-h-48 overflow-y-auto">
            {JSON.stringify(JSON.parse(body) as unknown, null, 2)}
          </pre>
        </details>
      )}
    </div>
  );
}

function StructuredError({
  parsed,
  styles,
}: {
  parsed: ParsedError;
  styles: ToneStyles;
}) {
  return (
    <div className={styles.box}>
      <div className={`${styles.header} flex items-center gap-2`}>
        <span className={styles.icon}>
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
        <span className={`text-sm font-medium ${styles.accent}`}>
          {parsed.type || parsed.code || "Error"}
        </span>
      </div>

      <div className="p-3 space-y-2">
        {parsed.message && (
          <p className={`text-sm ${styles.accent}`}>{parsed.message}</p>
        )}

        {parsed.details && Object.keys(parsed.details).length > 0 && (
          <div className={`pt-2 mt-2 border-t ${styles.divider}`}>
            <p className="text-xs text-text-muted mb-1.5">Details:</p>
            <div className="space-y-1">
              {Object.entries(parsed.details).map(([key, value]) => (
                <div key={key} className="flex items-start gap-2 text-xs">
                  <span className="text-text-muted min-w-[80px]">{key}:</span>
                  <span className={`${styles.detail} font-mono break-all`}>
                    {typeof value === "object"
                      ? JSON.stringify(value)
                      : String(value)}
                  </span>
                </div>
              ))}
            </div>
          </div>
        )}

        {parsed.allowedClients && parsed.allowedClients.length > 0 && (
          <div className={`pt-2 mt-2 border-t ${styles.divider}`}>
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

function RawErrorDisplay({
  body,
  styles,
}: {
  body: string;
  styles: ToneStyles;
}) {
  return (
    <div className={styles.raw}>
      <p className={styles.rawText}>{body}</p>
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

function parseErrorBody(body: string): ParsedError {
  const trimmed = body.trim();

  try {
    const json = JSON.parse(trimmed) as unknown;
    if (!isRecord(json)) {
      return {
        raw: trimmed,
        isJson: true,
        details: { value: json },
      };
    }
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

  const errorObj = isRecord(json.error) ? json.error : json;

  result.type =
    getString(errorObj, "type") ||
    getString(json, "type") ||
    getString(errorObj, "error_type");
  result.code =
    getString(errorObj, "code") ||
    getString(json, "code") ||
    getString(errorObj, "error_code");

  result.message =
    getString(errorObj, "message") ||
    getString(json, "message") ||
    getString(errorObj, "error") ||
    getString(json, "error");

  const allowedClients =
    getArray(errorObj, "allowedClients") || getArray(json, "allowedClients");
  if (allowedClients) {
    result.allowedClients = allowedClients.filter(
      (item): item is string => typeof item === "string",
    );
  }

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

  const sourceObj = isRecord(json.error) ? errorObj : json;
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

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
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

function generateDiagnosticTips(
  parsed: ParsedError,
  statusCode: number,
  userAgent?: string,
): DiagnosticTip[] {
  const tips: DiagnosticTip[] = [];

  handle403Tips(tips, parsed, userAgent);
  handleAuthTips(tips, statusCode);
  handleServerErrorTips(tips, statusCode);
  handleBadRequestTips(tips, statusCode, parsed);

  return tips;
}

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

  // Allow-list guidance identifies the concrete mismatch, so adding the generic
  // forbidden hint would dilute the operator's most actionable diagnosis.
  if (statusCode === 403 && tips.length > 0) return;

  const tip = authTips[statusCode];
  if (tip) tips.push(tip);
}

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
  const match = userAgent.match(/^([a-zA-Z0-9_-]+)/);
  return match ? match[1] : userAgent.split("/")[0] || userAgent;
}
