import { Link } from "react-router";
import type { DisguiseEvidence } from "@/api/client-disguise/evidence";
export function DisguiseEvidencePanel({
  evidence,
}: {
  evidence: DisguiseEvidence;
}) {
  return (
    <section
      aria-label="Client disguise evidence"
      className="space-y-3 rounded-lg border border-border p-4"
    >
      <h4 className="font-semibold">Client disguise · {evidence.decision}</h4>
      {evidence.truncated && (
        <p className="text-sm text-text-muted">
          Evidence excerpts were shortened to retain the diagnostic and terminal
          failure.
        </p>
      )}
      {evidence.applied_scopes?.length ? (
        <p className="text-sm">
          Applied scope: {evidence.applied_scopes.join(", ")}
        </p>
      ) : null}
      <p className="text-sm">
        Diagnostic ID: <code>{evidence.diagnostic_id}</code>
      </p>
      <Link
        className="text-sm text-primary underline"
        to={
          "/client-disguise?login=" +
          encodeURIComponent(evidence.context.credential_session_id ?? "")
        }
      >
        Open client disguise settings
      </Link>
      <dl className="grid grid-cols-2 gap-2 text-sm">
        {Object.entries(evidence.context).map(([key, value]) => (
          <div key={key}>
            <dt className="text-text-muted">{key.replaceAll("_", " ")}</dt>
            <dd className="break-all font-mono">{value}</dd>
          </div>
        ))}
      </dl>
      {Object.keys(evidence.platform_facts).length > 0 && (
        <details>
          <summary>Original platform evidence</summary>
          <pre className="whitespace-pre-wrap break-all text-xs">
            {JSON.stringify(evidence.platform_facts, null, 2)}
          </pre>
        </details>
      )}
      {evidence.candidates.length > 0 && (
        <ul className="space-y-1 text-sm">
          {evidence.candidates.map((candidate, index) => (
            <li key={candidate.provider_id + index}>
              {candidate.provider_id}: {candidate.outcome} · {candidate.reason}{" "}
              {candidate.platform}
            </li>
          ))}
        </ul>
      )}
      {evidence.differences.length > 0 && (
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr>
                <th>Field</th>
                <th>Original</th>
                <th>Derived</th>
              </tr>
            </thead>
            <tbody>
              {evidence.differences.map((difference, index) => (
                <tr key={index}>
                  <td>
                    {difference.carrier} · {difference.location}
                  </td>
                  <td>
                    <pre className="whitespace-pre-wrap break-all">
                      {difference.original}
                    </pre>
                  </td>
                  <td>
                    <pre className="whitespace-pre-wrap break-all">
                      {difference.derived}
                    </pre>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {evidence.failure && (
        <div className="rounded bg-red-50 p-3 text-red-800">
          <p>
            Conversion terminated at {evidence.failure.phase}:{" "}
            {evidence.failure.location}
          </p>
          <p>{evidence.failure.error_chain.join(" → ")}</p>
          <details>
            <summary>Reproduction snippets</summary>
            <pre className="whitespace-pre-wrap break-all text-xs">
              Original: {evidence.failure.original_snippet}
              {"\n"}Derived: {evidence.failure.derived_snippet}
            </pre>
          </details>
        </div>
      )}
    </section>
  );
}
