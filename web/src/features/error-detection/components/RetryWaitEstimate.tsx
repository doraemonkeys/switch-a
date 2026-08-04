import { formatDuration } from "@/lib/utils";
import type { InternalErrorRuleAction } from "../contracts";
import { estimateRetryWaitUpperBound } from "../domain";

function formatWait(milliseconds: number): string {
  return formatDuration(milliseconds, { smallestUnit: "ms" });
}

export function RetryWaitEstimate({
  action,
  globalMaxAttempts,
  configUnavailable = false,
}: {
  readonly action: InternalErrorRuleAction | null;
  readonly globalMaxAttempts: number | null;
  readonly configUnavailable?: boolean;
}) {
  if (!action) {
    return (
      <p role="status" className="text-xs text-text-muted">
        Enter valid retry settings to calculate the wait upper bound.
      </p>
    );
  }
  if (globalMaxAttempts === null) {
    return (
      <p role="status" className="text-xs text-text-muted">
        {configUnavailable
          ? "Wait estimate unavailable because global request limits could not be loaded."
          : "Loading the global attempt budget…"}
      </p>
    );
  }

  const estimate = estimateRetryWaitUpperBound(action, globalMaxAttempts);
  if (!estimate.valid) {
    return (
      <p role="status" className="text-xs text-danger">
        {estimate.error}
      </p>
    );
  }

  return (
    <section
      aria-label="Current provider wait estimate"
      className="rounded-xl border border-info-light bg-info-light/20 p-4"
    >
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <h4 className="text-sm font-semibold text-text-primary">
          Current-provider wait upper bound
        </h4>
        <strong className="font-mono text-sm text-primary">
          {formatWait(estimate.wait_upper_bound_ms)}
        </strong>
      </div>
      <dl className="mt-3 grid gap-2 text-xs text-text-secondary sm:grid-cols-3">
        <div>
          <dt>Effective same-provider retries</dt>
          <dd className="font-semibold text-text-primary">
            {estimate.effective_same_provider_retries}
          </dd>
        </div>
        <div>
          <dt>Probe windows</dt>
          <dd className="font-semibold text-text-primary">
            {estimate.probe_window_count} ·{" "}
            {formatWait(estimate.probe_upper_bound_ms)}
          </dd>
        </div>
        <div>
          <dt>Backoff bases</dt>
          <dd className="font-semibold text-text-primary">
            {formatWait(estimate.backoff_upper_bound_ms)}
          </dd>
        </div>
      </dl>
      {estimate.switch_attempt_reserved && (
        <p className="mt-3 rounded-lg bg-white/70 px-3 py-2 text-xs text-text-secondary">
          One finite global attempt is reserved for a provider switch. If no
          alternate can be reserved, the current response is committed.
        </p>
      )}
      <p className="mt-3 text-xs text-text-muted">
        Includes probe windows and unjittered backoff bases only. It excludes
        connection time, upstream time to first byte, streaming time, and time
        spent on a switched provider.
      </p>
    </section>
  );
}
