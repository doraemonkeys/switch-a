import { useState, type FormEvent } from "react";
import { FlaskConical, RefreshCw } from "lucide-react";
import type { APICatalog } from "@/api/api-catalog";
import type { Provider } from "@/api/types";
import type { DebugCaptureMessageSourceState } from "@/features/debug-capture";
import type {
  InternalErrorRuleListResponse,
  TestMessageInput,
  TestMessageResponse,
} from "../contracts";
import type { ErrorDetectionPrefill } from "../model";
import { TestMessageCaptureSource } from "./TestMessageCaptureSource";
import { TestMessagePayloadFields } from "./TestMessagePayloadFields";
import {
  TestMessageAnalysis,
  type TestMessageAnalysisSnapshot,
} from "./TestMessageAnalysis";

const EMPTY_CAPTURE_SOURCE: DebugCaptureMessageSourceState = {
  session_id: null,
  records: [],
  loading: false,
  error: null,
  selected_record_id: null,
  selected_source: null,
  selected_loading: false,
  selected_error: null,
  select_record: () => undefined,
  refresh: async () => undefined,
};

export interface TestMessagePanelProps {
  readonly catalog: APICatalog;
  readonly ruleSet: InternalErrorRuleListResponse;
  readonly providers: readonly Provider[];
  readonly prefill?: ErrorDetectionPrefill;
  readonly captureSource?: DebugCaptureMessageSourceState;
  readonly disabled: boolean;
  readonly onTest: (input: TestMessageInput) => Promise<TestMessageResponse>;
}

export function TestMessagePanel({
  catalog,
  ruleSet,
  providers,
  prefill,
  captureSource = EMPTY_CAPTURE_SOURCE,
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
  const [apiType, setAPIType] = useState(initialAPIType);
  const [providerID, setProviderID] = useState(initialProviderID);
  const [contentType, setContentType] = useState("application/json");
  const [contentEncoding, setContentEncoding] = useState("identity");
  const [bodyEncoding, setBodyEncoding] = useState<"utf8" | "base64">("utf8");
  const [body, setBody] = useState("");
  const [appliedCaptureRecordId, setAppliedCaptureRecordId] = useState<
    string | null
  >(null);
  const [snapshot, setSnapshot] = useState<TestMessageAnalysisSnapshot | null>(
    null,
  );
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const busy = disabled || submitting;
  const selectedProviderExists = providers.some(
    (provider) => provider.id === providerID,
  );

  const input: TestMessageInput = {
    api_type: apiType,
    provider_id: providerID || null,
    content_type: contentType,
    content_encoding: contentEncoding,
    body: { encoding: bodyEncoding, value: body },
  };

  function applyCapturedResponse() {
    const source = captureSource.selected_source;
    if (!source) return;
    setAPIType(source.api_type);
    setProviderID(source.provider_id);
    setContentType(source.content_type);
    setContentEncoding(source.content_encoding);
    setBodyEncoding(source.body.encoding);
    setBody(source.body.value);
    setAppliedCaptureRecordId(source.record_id);
    setSnapshot(null);
    setError(null);
  }

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setError(null);
    setSnapshot(null);
    setSubmitting(true);
    try {
      const response = await onTest(input);
      setSnapshot({ input, response });
    } catch (reason) {
      setError(
        reason instanceof Error ? reason.message : "Test Message failed",
      );
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <section>
      <div className="detection-test-heading">
        <h2>Test Message</h2>
        <p>
          Check which rule wins using a real response. Tests do not affect live
          traffic or hit counts.
        </p>
      </div>
      <div className="detection-test-grid">
        <div className="detection-test-input">
          <h3 className="detection-section-title">
            <span className="detection-step">1</span>Upstream response
          </h3>
          <details className="detection-capture">
            <summary>Import from Debug Capture</summary>
            <TestMessageCaptureSource
              captureSource={captureSource}
              busy={busy}
              appliedRecordId={appliedCaptureRecordId}
              onApply={applyCapturedResponse}
            />
          </details>
          <form
            onSubmit={submit}
            aria-busy={busy}
            className="detection-test-form"
          >
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
                The prefilled provider no longer exists. Choose a current
                provider or evaluate global rules only.
              </p>
            )}

            <TestMessagePayloadFields
              contentType={contentType}
              contentEncoding={contentEncoding}
              bodyEncoding={bodyEncoding}
              body={body}
              busy={busy}
              onContentTypeChange={setContentType}
              onContentEncodingChange={setContentEncoding}
              onBodyEncodingChange={setBodyEncoding}
              onBodyChange={setBody}
            />
            {error && (
              <p
                role="alert"
                className="rounded-lg bg-danger/5 p-3 text-sm text-danger"
              >
                {error}
              </p>
            )}

            <div className="flex items-center justify-between gap-3">
              <span className="text-xs text-text-secondary">
                Uses saved rules
              </span>
              <button
                type="submit"
                disabled={busy || apiType.length === 0}
                className="btn btn-primary"
              >
                {submitting ? (
                  <RefreshCw
                    className="h-4 w-4 animate-spin"
                    aria-hidden="true"
                  />
                ) : (
                  <FlaskConical className="h-4 w-4" aria-hidden="true" />
                )}
                {submitting ? "Analyzing…" : "Analyze message"}
              </button>
            </div>
          </form>
        </div>
        <TestMessageAnalysis
          snapshot={snapshot}
          input={input}
          ruleSet={ruleSet}
          submitting={submitting}
          error={error}
        />
      </div>
    </section>
  );
}
