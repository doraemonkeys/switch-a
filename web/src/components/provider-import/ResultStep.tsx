import { CheckCircle2 } from "lucide-react";
import type { ProviderImportCommitResult } from "../../api";

export function ResultStep({ result }: { result: ProviderImportCommitResult }) {
  return (
    <div className="space-y-6">
      <p
        className="sr-only"
        role="status"
        aria-live="polite"
        aria-atomic="true"
      >
        Import complete. {result.summary.created} created,{" "}
        {result.summary.updated} updated, {result.summary.skipped} skipped.
      </p>

      <div className="py-3 text-center">
        <div className="mx-auto flex h-14 w-14 items-center justify-center rounded-full bg-success-light text-success">
          <CheckCircle2 className="h-8 w-8" aria-hidden="true" />
        </div>
        <h3 className="mt-4 text-xl font-semibold text-text-primary">
          Import complete
        </h3>
        <p className="mt-1 text-sm text-text-secondary">
          The selected changes were committed atomically.
        </p>
      </div>

      <dl className="grid grid-cols-3 gap-3" aria-label="Import result summary">
        {[
          ["Created", result.summary.created],
          ["Updated", result.summary.updated],
          ["Skipped", result.summary.skipped],
        ].map(([label, count]) => (
          <div
            key={label}
            className="flex flex-col rounded-lg border border-border bg-bg-secondary p-4 text-center"
          >
            <dt className="order-2 mt-1 text-xs text-text-muted">{label}</dt>
            <dd className="order-1 text-2xl font-semibold text-text-primary">
              {count}
            </dd>
          </div>
        ))}
      </dl>

      {result.items.length > 0 && (
        <section>
          <h3 className="text-sm font-semibold text-text-primary">
            Provider results
          </h3>
          <ul className="mt-2 divide-y divide-border rounded-lg border border-border">
            {result.items.map((item) => (
              <li
                key={item.candidate_id}
                className="flex items-start justify-between gap-3 px-4 py-3 text-sm"
              >
                <div className="min-w-0">
                  <p className="truncate text-text-primary">
                    {item.name ?? item.provider_id}
                  </p>
                </div>
                <span className="shrink-0 text-xs capitalize text-text-muted">
                  {item.outcome.replace("_", " ")}
                </span>
              </li>
            ))}
          </ul>
        </section>
      )}
    </div>
  );
}
