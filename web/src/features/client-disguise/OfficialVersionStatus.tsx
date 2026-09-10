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
    <section className="cd-feedback" aria-label="Codex CLI 官方稳定版">
      <div>
        <strong>Codex CLI 官方稳定版{version ? `: ${version}` : ""}</strong>
        <p className="cd-description">
          Checks Codex CLI releases every 6 hours while this version source is
          selected.
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
            View Codex CLI release
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
            "Codex CLI 官方稳定版已检查。新请求和连接将使用已保存的版本设置。",
          )
        }
      >
        <RefreshCw size={16} aria-hidden="true" /> Check now
      </button>
    </section>
  );
}
