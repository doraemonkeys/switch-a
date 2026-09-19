import { useState } from "react";
import type { CredentialSession } from "../../api";
import { CopyButton } from "../../components";
import { hasProviderApiKey } from "../../lib/providerApiKey";
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
  const [overrideVisible, setOverrideVisible] = useState(false);
  const selectedSession = credentialSessions.find(
    (session) => session.id === configuration.credential_session_id,
  );
  const currentApiKey =
    selectedSession?.kind === "api_key"
      ? selectedSession.secret_data
      : undefined;
  const visibilityAction = overrideVisible ? "Hide" : "Show";

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
          Credential Session
        </span>
        <select
          className="input"
          value={configuration.credential_session_id}
          onChange={(event) =>
            onChange({ credential_session_id: event.target.value })
          }
          aria-label={`Credential session for ${entryLabel}`}
        >
          <option value="">Create or select a credential</option>
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
      {currentApiKey && (
        <CurrentApiKeyField apiKey={currentApiKey} entryLabel={entryLabel} />
      )}
      <div className="space-y-1">
        <p className="text-[11px] font-medium uppercase tracking-wide text-text-muted">
          New API Key
        </p>
        <div className="relative">
          <input
            type={overrideVisible ? "text" : "password"}
            className="input pr-10"
            value={configuration.api_key}
            onChange={(event) => onChange({ api_key: event.target.value })}
            autoComplete="new-password"
            placeholder="Keep selected session or use default key"
            aria-label={`API key override for ${entryLabel}`}
          />
          <button
            type="button"
            onClick={() => setOverrideVisible((visible) => !visible)}
            className="absolute right-2 top-1/2 -translate-y-1/2 text-text-muted hover:text-text-primary transition-colors p-1"
            aria-label={`${visibilityAction} API key override for ${entryLabel}`}
            title={`${visibilityAction} API key override for ${entryLabel}`}
          >
            <CredentialVisibilityIcon visible={overrideVisible} />
          </button>
        </div>
      </div>
      <p className="text-xs text-text-muted">
        {describeCredentialBinding(configuration)}
      </p>
    </div>
  );
}

function describeCredentialBinding(entry: RouteConfigurationDraft): string {
  if (hasProviderApiKey(entry.api_key)) {
    return "A new credential session will be created on save.";
  }
  if (entry.credential_session_id) {
    return `Bound to credential session ${entry.credential_session_id}.`;
  }
  return "Choose a session or provide a new API key before saving.";
}

function CredentialVisibilityIcon({ visible }: { visible: boolean }) {
  if (visible) {
    return (
      <svg
        className="w-5 h-5"
        fill="none"
        stroke="currentColor"
        viewBox="0 0 24 24"
      >
        <path
          strokeLinecap="round"
          strokeLinejoin="round"
          strokeWidth={2}
          d="M13.875 18.825A10.05 10.05 0 0112 19c-4.478 0-8.268-2.943-9.543-7a9.97 9.97 0 011.563-3.029m5.858.908a3 3 0 114.243 4.243M9.878 9.878l4.242 4.242M9.88 9.88l-3.29-3.29m7.532 7.532l3.29 3.29M3 3l3.59 3.59m0 0A9.953 9.953 0 0112 5c4.478 0 8.268 2.943 9.543 7a10.025 10.025 0 01-4.132 5.411m0 0L21 21"
        />
      </svg>
    );
  }
  return (
    <svg
      className="w-5 h-5"
      fill="none"
      stroke="currentColor"
      viewBox="0 0 24 24"
    >
      <path
        strokeLinecap="round"
        strokeLinejoin="round"
        strokeWidth={2}
        d="M15 12a3 3 0 11-6 0 3 3 0 016 0z"
      />
      <path
        strokeLinecap="round"
        strokeLinejoin="round"
        strokeWidth={2}
        d="M2.458 12C3.732 7.943 7.523 5 12 5c4.478 0 8.268 2.943 9.542 7-1.274 4.057-5.064 7-9.542 7-4.477 0-8.268-2.943-9.542-7z"
      />
    </svg>
  );
}

function CurrentApiKeyField({
  apiKey,
  entryLabel,
}: {
  apiKey: string;
  entryLabel: string;
}) {
  const [visible, setVisible] = useState(false);
  const visibilityAction = visible ? "Hide" : "Show";

  return (
    <div className="space-y-1">
      <p className="text-[11px] font-medium uppercase tracking-wide text-text-muted">
        Current API Key
      </p>
      <div className="flex items-center gap-2">
        <div className="relative min-w-0 flex-1">
          <input
            type={visible ? "text" : "password"}
            className="input pr-10 font-mono"
            value={apiKey}
            readOnly
            aria-label={`Current API key for ${entryLabel}`}
          />
          <button
            type="button"
            onClick={() => setVisible((current) => !current)}
            className="absolute right-2 top-1/2 -translate-y-1/2 text-text-muted hover:text-text-primary transition-colors p-1"
            aria-label={`${visibilityAction} current API key for ${entryLabel}`}
            title={`${visibilityAction} current API key`}
          >
            <CredentialVisibilityIcon visible={visible} />
          </button>
        </div>
        <CopyButton
          text={apiKey}
          className="h-10 shrink-0 rounded-lg border border-border px-3 hover:border-primary"
        />
      </div>
    </div>
  );
}
