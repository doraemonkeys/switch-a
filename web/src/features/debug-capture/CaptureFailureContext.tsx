import type {
  CaptureTerminationReason,
  DebugCaptureFailureFact,
  DebugCaptureFailureObservation,
} from "@/api";
import { formatCaptureValue } from "./presentation";

const MAX_DISPLAYED_FAILURE_MESSAGE_CHARACTERS = 240;

interface CaptureFailureContextProps {
  label: string;
  terminationReason?: CaptureTerminationReason;
  hasFailure: boolean;
  failure: DebugCaptureFailureObservation;
  metadataTruncated?: boolean;
}

interface PresentedFailureText {
  text: string;
  truncated: boolean;
}

function presentFailureText(
  value: string | undefined,
): PresentedFailureText | null {
  if (!value) return null;

  // Captured messages cross a trust boundary. Removing control and direction
  // characters keeps diagnostics readable without letting them reshape the UI.
  const sanitized = value
    .replace(/[\p{Cc}\p{Cf}]+/gu, " ")
    .replace(/\s+/gu, " ")
    .trim();
  if (!sanitized) return null;

  const characters = Array.from(sanitized);
  if (characters.length <= MAX_DISPLAYED_FAILURE_MESSAGE_CHARACTERS) {
    return { text: sanitized, truncated: false };
  }
  return {
    text:
      characters.slice(0, MAX_DISPLAYED_FAILURE_MESSAGE_CHARACTERS).join("") +
      "…",
    truncated: true,
  };
}

function FailureMetadata({ fact }: { fact: DebugCaptureFailureFact }) {
  const providerErrorType = presentFailureText(fact.provider_error_type);
  const providerErrorCode = presentFailureText(fact.provider_error_code);
  const message = presentFailureText(fact.message);
  return (
    <dl className="mt-1 space-y-1">
      <div className="flex flex-wrap gap-1">
        <dt>Site:</dt>
        <dd>{formatCaptureValue(fact.site)}</dd>
      </div>
      <div className="flex flex-wrap gap-1">
        <dt>Peer:</dt>
        <dd>{formatCaptureValue(fact.peer)}</dd>
      </div>
      <div className="flex flex-wrap gap-1">
        <dt>Class:</dt>
        <dd>{formatCaptureValue(fact.class)}</dd>
      </div>
      <div className="flex flex-wrap gap-1">
        <dt>Code:</dt>
        <dd>{formatCaptureValue(fact.code)}</dd>
      </div>
      {fact.http_status_code !== undefined && (
        <div className="flex flex-wrap gap-1">
          <dt>HTTP status:</dt>
          <dd>{fact.http_status_code}</dd>
        </div>
      )}
      {fact.websocket_close_code !== undefined && (
        <div className="flex flex-wrap gap-1">
          <dt>WebSocket close code:</dt>
          <dd>{fact.websocket_close_code}</dd>
        </div>
      )}
      {fact.system_error_code !== undefined && (
        <div className="flex flex-wrap gap-1">
          <dt>System error code:</dt>
          <dd>{fact.system_error_code}</dd>
        </div>
      )}
      {providerErrorType && (
        <div className="flex flex-wrap gap-1">
          <dt>Provider error type:</dt>
          <dd className="wrap-break-word font-mono">
            {providerErrorType.text}
          </dd>
        </div>
      )}
      {providerErrorCode && (
        <div className="flex flex-wrap gap-1">
          <dt>Provider error code:</dt>
          <dd className="wrap-break-word font-mono">
            {providerErrorCode.text}
          </dd>
        </div>
      )}
      {message && (
        <div className="flex flex-wrap gap-1">
          <dt>Message:</dt>
          <dd className="wrap-break-word font-mono">
            {message.text}
            {message.truncated && (
              <span className="sr-only"> Message display truncated</span>
            )}
          </dd>
        </div>
      )}
    </dl>
  );
}

function FailureFact({
  label,
  fact,
}: {
  label: string;
  fact: DebugCaptureFailureFact;
}) {
  return (
    <section aria-label={`${label} failure`}>
      <p className="font-semibold text-text-primary">{label}</p>
      <FailureMetadata fact={fact} />
    </section>
  );
}

export function CaptureFailureContext({
  label,
  terminationReason,
  hasFailure,
  failure,
  metadataTruncated = false,
}: CaptureFailureContextProps) {
  if (!terminationReason && !hasFailure && !metadataTruncated) return null;

  return (
    <aside
      role="note"
      aria-label={label}
      className="mt-3 rounded-lg border border-border bg-bg-secondary p-3 text-xs text-text-secondary"
    >
      <p className="font-semibold text-text-primary">{label}</p>
      {terminationReason && (
        <dl className="mt-1">
          <div className="flex flex-wrap gap-1">
            <dt>Termination:</dt>
            <dd>{formatCaptureValue(terminationReason)}</dd>
          </div>
        </dl>
      )}
      {hasFailure && (
        <div className="mt-2 space-y-2">
          <FailureFact label="Primary" fact={failure.primary} />
          {failure.has_secondary && (
            <FailureFact label="Secondary" fact={failure.secondary} />
          )}
        </div>
      )}
      {((hasFailure && failure.truncated) || metadataTruncated) && (
        <div className="mt-2 flex flex-wrap gap-1.5">
          {hasFailure && failure.truncated && (
            <span className="badge badge-warning">
              Failure details truncated
            </span>
          )}
          {metadataTruncated && (
            <span className="badge badge-warning">Metadata truncated</span>
          )}
        </div>
      )}
    </aside>
  );
}
