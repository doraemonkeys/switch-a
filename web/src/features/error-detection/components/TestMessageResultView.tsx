import type {
  InternalErrorRuleListResponse,
  TestMessageMatch,
  TestMessageExtractedError,
  TestMessageResponse,
} from "../contracts";

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
  ruleSet,
}: {
  readonly result: TestMessageResponse;
  readonly ruleSet: InternalErrorRuleListResponse;
}) {
  // A newer rule name must not be presented as part of an older analysis.
  const winningRule =
    result.rule_set_revision === ruleSet.rule_set_revision
      ? ruleSet.rules.find((rule) => rule.id === result.winner?.rule_id)
      : undefined;
  return (
    <section aria-label="Test Message result" className="p-6">
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
          <strong className="block text-text-primary">
            Winning rule{winningRule ? ` · ${winningRule.name}` : ""}
          </strong>
          <span className="mt-1 block break-all font-mono text-xs text-text-secondary">
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
