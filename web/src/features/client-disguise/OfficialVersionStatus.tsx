import { RefreshCw } from "lucide-react";
import { useApi } from "@/api/useApi";
import type { OfficialVersionState } from "@/api/client-disguise/types";

export function OfficialVersionStatus({
  state,
  busy,
  mutate,
}: {
  state?: OfficialVersionState;
  busy: boolean;
  mutate: (
    action: () => Promise<unknown>,
    message?: string,
  ) => Promise<boolean>;
}) {
  const api = useApi();
  const version = state?.release.version;
  const checked = state?.checked_at && !state.checked_at.startsWith("0001-");
  return (
    <section className="cd-feedback" aria-label="Official stable version">
      <div>
        <strong>Official stable version{version ? `: ${version}` : ""}</strong>
        <p className="cd-description">
          Checks every 6 hours when a login follows official releases.
          {checked
            ? ` Last checked: ${new Date(state.checked_at).toLocaleString()}.`
            : " No release has been checked yet."}
        </p>
        {state?.last_error && (
          <p className="cd-error-text" role="alert">
            {state.last_error}.{" "}
            {version
              ? `Continuing with ${version}.`
              : "Using profile versions until synchronization succeeds."}
          </p>
        )}
        {version && (
          <a href={state.release.url} target="_blank" rel="noreferrer">
            View official release
          </a>
        )}
      </div>
      <button
        type="button"
        className="cd-button cd-button-secondary"
        disabled={busy}
        onClick={() =>
          void mutate(
            () => api.clientDisguise.syncOfficialVersion(),
            "Official stable version checked. New requests and connections use the saved version choice.",
          )
        }
      >
        <RefreshCw size={16} aria-hidden="true" /> Check now
      </button>
    </section>
  );
}
