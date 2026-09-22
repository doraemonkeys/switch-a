import { useId, useState } from "react";
import { Eye, EyeOff } from "lucide-react";
import type { CredentialSession } from "../../api";
import { CopyButton } from "../../components";
import { normalizeProviderApiKey } from "../../lib/providerApiKey";
import type { RouteConfigurationDraft } from "./routeDrafts";

export function RouteConfigurationFields({
  configuration,
  entryLabel,
  credentialSessions,
  onChange,
}: {
  configuration: RouteConfigurationDraft;
  entryLabel: string;
  credentialSessions: CredentialSession[];
  onChange: (change: Partial<RouteConfigurationDraft>) => void;
}) {
  const keyFieldID = useId();
  const [visible, setVisible] = useState(false);
  const selectedSession = credentialSessions.find(
    (session) => session.id === configuration.credential_session_id,
  );
  const currentApiKey =
    selectedSession?.kind === "api_key"
      ? selectedSession.secret_data
      : undefined;
  const apiKey = configuration.api_key ?? currentApiKey ?? "";
  const replacingKey =
    Boolean(configuration.credential_session_id) &&
    configuration.api_key !== null;
  const credentialUnavailable =
    Boolean(configuration.credential_session_id) && !selectedSession;
  const usesChatGPT = selectedSession?.kind === "chatgpt";
  const visibilityAction = visible ? "Hide" : "Show";

  return (
    <div className="space-y-2.5">
      <label className="block space-y-1">
        <span className="text-[11px] font-medium uppercase tracking-wide text-text-muted">
          Base URL
        </span>
        <input
          type="url"
          className="input"
          value={configuration.base_url}
          onChange={(event) => onChange({ base_url: event.target.value })}
          placeholder="https://api.example.com"
          aria-label={`Base URL for ${entryLabel}`}
        />
      </label>
      <label className="block space-y-1">
        <span className="text-[11px] font-medium uppercase tracking-wide text-text-muted">
          Saved Credential
        </span>
        <select
          className="input"
          value={configuration.credential_session_id}
          onChange={(event) => {
            const sessionID = event.target.value;
            // A new selection supersedes the previous key draft.
            onChange({
              credential_session_id: sessionID,
              api_key: sessionID ? null : "",
            });
          }}
          aria-label={`Credential session for ${entryLabel}`}
        >
          <option value="">Enter an API key</option>
          {credentialUnavailable && (
            <option value={configuration.credential_session_id}>
              Current credential
            </option>
          )}
          {credentialSessions.map((session) => (
            <option key={session.id} value={session.id}>
              {session.name} ·{" "}
              {session.kind === "chatgpt" ? "GPT Login" : "API Key"} ·{" "}
              {session.route_references.length} route
              {session.route_references.length === 1 ? "" : "s"}
            </option>
          ))}
        </select>
      </label>
      {usesChatGPT ? (
        <p className="text-xs text-text-muted">
          This route uses a GPT account. To use an API key, choose “Enter an API
          key” above.
        </p>
      ) : (
        <>
          <div className="space-y-1">
            <label
              htmlFor={keyFieldID}
              className="text-[11px] font-medium uppercase tracking-wide text-text-muted"
            >
              API Key
            </label>
            <div className="flex items-center gap-2 mt-1">
              <div className="relative min-w-0 flex-1">
                <input
                  id={keyFieldID}
                  type={visible ? "text" : "password"}
                  className="input pr-10 font-mono"
                  value={apiKey}
                  disabled={credentialUnavailable}
                  onChange={(event) => {
                    const value = event.target.value;
                    onChange({
                      api_key:
                        currentApiKey &&
                        normalizeProviderApiKey(value) === currentApiKey
                          ? null
                          : value,
                    });
                  }}
                  autoComplete="new-password"
                  placeholder={
                    credentialUnavailable
                      ? "Loading current key..."
                      : "Enter an API key or use the shared key"
                  }
                  aria-label={`API key for ${entryLabel}`}
                />
                <button
                  type="button"
                  onClick={() => setVisible((current) => !current)}
                  className="absolute right-2 top-1/2 -translate-y-1/2 text-text-muted hover:text-text-primary transition-colors p-1"
                  aria-label={`${visibilityAction} API key for ${entryLabel}`}
                >
                  {visible ? (
                    <EyeOff className="w-5 h-5" />
                  ) : (
                    <Eye className="w-5 h-5" />
                  )}
                </button>
              </div>
              {apiKey && (
                <CopyButton
                  text={apiKey}
                  className="h-10 shrink-0 rounded-lg border border-border px-3 hover:border-primary"
                />
              )}
            </div>
          </div>
          <div className="flex items-start justify-between gap-3">
            <p className="text-xs text-text-muted">
              {configuration.credential_session_id
                ? `Key edits apply only to ${entryLabel} when you save. Other routes sharing this credential keep their key.`
                : "This key will be used when you save. Leave blank to use the shared key."}
            </p>
            {replacingKey && (
              <button
                type="button"
                className="shrink-0 text-xs font-medium text-primary hover:underline cursor-pointer"
                aria-label={`Undo API key change for ${entryLabel}`}
                onClick={() => onChange({ api_key: null })}
              >
                Undo key change
              </button>
            )}
          </div>
        </>
      )}
    </div>
  );
}
