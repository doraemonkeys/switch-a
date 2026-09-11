import { ScanLine } from "lucide-react";
import type {
  InternalErrorRuleListResponse,
  TestMessageInput,
  TestMessageResponse,
} from "../contracts";
import { TestMessageResultView } from "./TestMessageResultView";

export interface TestMessageAnalysisSnapshot {
  readonly input: TestMessageInput;
  readonly response: TestMessageResponse;
}

interface TestMessageAnalysisProps {
  readonly snapshot: TestMessageAnalysisSnapshot | null;
  readonly input: TestMessageInput;
  readonly ruleSet: InternalErrorRuleListResponse;
  readonly submitting: boolean;
  readonly error: string | null;
}

export function TestMessageAnalysis({
  snapshot,
  input,
  ruleSet,
  submitting,
  error,
}: TestMessageAnalysisProps) {
  const previousInput = snapshot?.input;
  const inputChanged =
    previousInput &&
    (previousInput.api_type !== input.api_type ||
      previousInput.provider_id !== input.provider_id ||
      previousInput.content_type !== input.content_type ||
      previousInput.content_encoding !== input.content_encoding ||
      previousInput.body.encoding !== input.body.encoding ||
      previousInput.body.value !== input.body.value);
  const rulesChanged =
    snapshot &&
    snapshot.response.rule_set_revision !== ruleSet.rule_set_revision;
  return (
    <div
      className="detection-test-output"
      aria-live="polite"
      aria-busy={submitting}
    >
      <h3 className="detection-section-title">
        <span className="detection-step">2</span>Analysis result
      </h3>
      {snapshot ? (
        <>
          {(inputChanged || rulesChanged) && (
            <p
              role="status"
              className="border-b border-warning/20 bg-warning-light px-6 py-3 text-xs text-warning-dark"
            >
              {rulesChanged
                ? "Saved rules have changed."
                : "Input has changed."}{" "}
              Analyze again to update this result.
            </p>
          )}
          <TestMessageResultView result={snapshot.response} ruleSet={ruleSet} />
        </>
      ) : (
        <div className="detection-result-empty">
          <ScanLine size={32} strokeWidth={1.25} aria-hidden="true" />
          <h3>
            {submitting ? "Analyzing response…" : "See how your rules respond"}
          </h3>
          <p>
            {error
              ? "Analysis could not complete. Review the error and try again."
              : "Add a response and run the analysis to see extracted errors, matching keywords, and the winning rule."}
          </p>
        </div>
      )}
    </div>
  );
}
