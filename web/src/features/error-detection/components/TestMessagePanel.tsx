import { useId, useState, type FormEvent } from "react";
import { FlaskConical, RefreshCw } from "lucide-react";
import type { APICatalog } from "@/api/api-catalog";
import type { Provider } from "@/api/types";
import type {
  TestMessageExtractedError,
  TestMessageInput,
  TestMessageMatch,
  TestMessageResponse,
} from "../contracts";
import type { ErrorDetectionPrefill } from "../model";

function MatchDetails({ match }: { match: TestMessageMatch }) {
  return (
    <li className="rounded-lg border border-border bg-white p-3">
      <span className="block break-all font-mono text-xs text-text-primary">
        {match.rule_id}
      </span>
      <dl className="mt-2 grid gap-2 text-xs text-text-secondary sm:grid-cols-2">
        <div>
          <dt>Keywords</dt>
          <dd className="font-mono text-text-primary">
            {match.matched_keywords.join(", ") || "—"}
          </dd>
        </div>
        <div>
          <dt>Fields</dt>
          <dd className="font-mono text-text-primary">
            {match.matched_fields.join(", ") || "—"}
          </dd>
        </div>
      </dl>
    </li>
  );
}

function ExtractedErrorCard({
  error,
  index,
  decisive,
}: {
  error: TestMessageExtractedError;
  index: number;
  decisive: boolean;
}) {
  const fields = ["type", "code", "message", "reason"] as const;
  return (
    <li className="rounded-xl border border-border bg-bg-secondary p-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h4 className="text-sm font-semibold text-text-primary">
          Error {index + 1} · frame {error.frame_index}
        </h4>
        {decisive && (
          <span className="rounded-full bg-primary-light px-2 py-0.5 text-xs font-semibold text-primary">
            Decisive
          </span>
        )}
      </div>
      <dl className="mt-3 grid gap-2 text-xs sm:grid-cols-2">
        {fields.map((field) => (
          <div key={field}>
            <dt className="uppercase tracking-wide text-text-muted">{field}</dt>
            <dd className="mt-0.5 break-words font-mono text-text-primary">
              {error[field] ?? "—"}
            </dd>
          </div>
        ))}
      </dl>
      <div className="mt-3">
        <h5 className="text-xs font-semibold uppercase tracking-wide text-text-muted">
          Ordered matches
        </h5>
        {error.matches.length === 0 ? (
          <p className="mt-1 text-xs text-text-secondary">No rules matched.</p>
        ) : (
          <ol className="mt-2 space-y-2">
            {error.matches.map((match, matchIndex) => (
              <MatchDetails
                key={`${match.rule_id}-${matchIndex}`}
                match={match}
              />
            ))}
          </ol>
        )}
      </div>
    </li>
  );
}

export function TestMessageResultView({
  result,
}: {
  readonly result: TestMessageResponse;
}) {
  return (
    <section
      aria-label="Test Message result"
      className="mt-5 rounded-xl border border-border bg-white p-4"
    >
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h3 className="text-sm font-semibold text-text-primary">
            Analysis{" "}
            {result.analysis_status === "complete" ? "complete" : "failed open"}
          </h3>
          <p className="mt-1 text-xs text-text-secondary">
            Rule revision {result.rule_set_revision} · Protocol{" "}
            <span className="font-mono">
              {result.response_protocol_id ?? "unavailable"}
            </span>
          </p>
        </div>
        <span
          className={`rounded-full px-2.5 py-1 text-xs font-semibold ${
            result.analysis_status === "complete"
              ? "bg-success-light text-success-dark"
              : "bg-warning-light text-warning-dark"
          }`}
        >
          {result.analysis_status === "complete" ? "Complete" : "Fail open"}
        </span>
      </div>

      {result.analysis_reason && (
        <p className="mt-3 rounded-lg bg-warning-light/40 p-3 font-mono text-xs text-warning-dark">
          {result.analysis_reason}
        </p>
      )}
      {result.winner ? (
        <div className="mt-4 rounded-lg border border-primary/20 bg-primary-light/30 p-3 text-sm">
          <strong className="text-text-primary">Winning rule</strong>
          <span className="ml-2 break-all font-mono text-xs text-primary">
            {result.winner.rule_id}
          </span>
          <span className="mt-1 block text-xs text-text-secondary">
            Error {result.winner.error_index + 1}; matched{" "}
            {result.winner.matched_fields.join(", ")}.
          </span>
        </div>
      ) : (
        <p className="mt-4 text-sm text-text-secondary">No winning rule.</p>
      )}

      {result.errors.length === 0 ? (
        <p className="mt-4 text-sm text-text-secondary">
          No structured error objects were extracted.
        </p>
      ) : (
        <ol className="mt-4 space-y-3">
          {result.errors.map((error, index) => (
            <ExtractedErrorCard
              key={`${error.frame_index}-${index}`}
              error={error}
              index={index}
              decisive={result.decisive_error_index === index}
            />
          ))}
        </ol>
      )}
    </section>
  );
}

export interface TestMessagePanelProps {
  readonly catalog: APICatalog;
  readonly providers: readonly Provider[];
  readonly prefill?: ErrorDetectionPrefill;
  readonly disabled: boolean;
  readonly onTest: (input: TestMessageInput) => Promise<TestMessageResponse>;
}

export function TestMessagePanel({
  catalog,
  providers,
  prefill,
  disabled,
  onTest,
}: TestMessagePanelProps) {
  const supportedAPIEntries = catalog.api_types.filter(
    (entry) => entry.semantic_error_supported,
  );
  const requestedAPIType = prefill?.api_type ?? null;
  const initialAPIType = supportedAPIEntries.some(
    (entry) => entry.api_type === requestedAPIType,
  )
    ? (requestedAPIType ?? "")
    : (supportedAPIEntries[0]?.api_type ?? "");
  const initialProviderID =
    prefill?.target?.kind === "provider" ? prefill.target.provider_id : "";
  const contentEncodingListID = useId();
  const [apiType, setAPIType] = useState(initialAPIType);
  const [providerID, setProviderID] = useState(initialProviderID);
  const [contentType, setContentType] = useState("application/json");
  const [contentEncoding, setContentEncoding] = useState("identity");
  const [bodyEncoding, setBodyEncoding] = useState<"utf8" | "base64">("utf8");
  const [body, setBody] = useState("");
  const [result, setResult] = useState<TestMessageResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const busy = disabled || submitting;
  const selectedProviderExists = providers.some(
    (provider) => provider.id === providerID,
  );

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setError(null);
    setSubmitting(true);
    try {
      const response = await onTest({
        api_type: apiType,
        provider_id: providerID || null,
        content_type: contentType,
        content_encoding: contentEncoding,
        body: { encoding: bodyEncoding, value: body },
      });
      setResult(response);
    } catch (reason) {
      setError(
        reason instanceof Error ? reason.message : "Test Message failed",
      );
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <section className="rounded-2xl border border-border bg-bg-secondary/50 p-5 shadow-sm">
      <div className="flex items-start gap-3">
        <div className="rounded-xl border border-primary/10 bg-white p-2.5">
          <FlaskConical className="h-5 w-5 text-primary" aria-hidden="true" />
        </div>
        <div>
          <h2 className="text-lg font-semibold text-text-primary">
            Test Message
          </h2>
          <p className="mt-1 text-sm text-text-secondary">
            Sends one side-effect-free backend analysis using the current rule
            revision. Matching is never reimplemented in the browser.
          </p>
        </div>
      </div>

      <form onSubmit={submit} aria-busy={busy} className="mt-5 space-y-4">
        <div className="grid gap-4 sm:grid-cols-2">
          <label className="space-y-1 text-sm text-text-secondary">
            <span>API type</span>
            <select
              className="input"
              required
              value={apiType}
              disabled={busy}
              onChange={(event) => setAPIType(event.target.value)}
            >
              {supportedAPIEntries.map((entry) => (
                <option key={entry.api_type} value={entry.api_type}>
                  {entry.label}
                </option>
              ))}
            </select>
          </label>
          <label className="space-y-1 text-sm text-text-secondary">
            <span>Rule scope</span>
            <select
              className="input"
              value={providerID}
              disabled={busy}
              onChange={(event) => setProviderID(event.target.value)}
            >
              <option value="">Global rules only</option>
              {!selectedProviderExists && providerID && (
                <option value={providerID}>
                  Deleted provider · {providerID}
                </option>
              )}
              {providers.map((provider) => (
                <option key={provider.id} value={provider.id}>
                  {provider.name} · provider and global rules
                </option>
              ))}
            </select>
          </label>
        </div>

        {!selectedProviderExists && providerID && (
          <p role="status" className="text-xs text-warning-dark">
            The prefilled provider no longer exists. Choose a current provider
            or evaluate global rules only.
          </p>
        )}

        <div className="grid gap-4 sm:grid-cols-2">
          <label className="space-y-1 text-sm text-text-secondary">
            <span>Content-Type</span>
            <input
              className="input font-mono"
              required
              value={contentType}
              disabled={busy}
              onChange={(event) => setContentType(event.target.value)}
            />
          </label>
          <label className="space-y-1 text-sm text-text-secondary">
            <span>Content-Encoding</span>
            <input
              className="input font-mono"
              list={contentEncodingListID}
              required
              value={contentEncoding}
              disabled={busy}
              onChange={(event) => setContentEncoding(event.target.value)}
            />
            <datalist id={contentEncodingListID}>
              <option value="identity" />
              <option value="gzip" />
              <option value="br" />
            </datalist>
          </label>
        </div>

        <div className="grid gap-4 sm:grid-cols-[180px_1fr]">
          <label className="space-y-1 text-sm text-text-secondary">
            <span>Body encoding</span>
            <select
              className="input"
              value={bodyEncoding}
              disabled={busy}
              onChange={(event) =>
                setBodyEncoding(event.target.value as "utf8" | "base64")
              }
            >
              <option value="utf8">UTF-8 text</option>
              <option value="base64">Base64 wire bytes</option>
            </select>
          </label>
          <label className="space-y-1 text-sm text-text-secondary">
            <span>Response body</span>
            <textarea
              className="input min-h-40 font-mono text-xs"
              value={body}
              disabled={busy}
              onChange={(event) => setBody(event.target.value)}
              placeholder={
                bodyEncoding === "utf8"
                  ? "Paste JSON or SSE frames"
                  : "Paste base64-encoded wire bytes"
              }
            />
          </label>
        </div>

        {contentEncoding !== "identity" && bodyEncoding !== "base64" && (
          <p role="status" className="text-xs text-warning-dark">
            Base64 body encoding is recommended for exact compressed wire bytes.
          </p>
        )}
        {error && (
          <p
            role="alert"
            className="rounded-lg bg-danger/5 p-3 text-sm text-danger"
          >
            {error}
          </p>
        )}

        <div className="flex justify-end">
          <button
            type="submit"
            disabled={busy || apiType.length === 0}
            className="btn btn-primary"
          >
            {submitting ? (
              <RefreshCw className="h-4 w-4 animate-spin" aria-hidden="true" />
            ) : (
              <FlaskConical className="h-4 w-4" aria-hidden="true" />
            )}
            {submitting ? "Analyzing…" : "Analyze message"}
          </button>
        </div>
      </form>

      {result && <TestMessageResultView result={result} />}
    </section>
  );
}
