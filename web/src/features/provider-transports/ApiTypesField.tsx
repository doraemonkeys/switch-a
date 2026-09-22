import type { CredentialSession } from "../../api";
import { useAPICatalog } from "../../api/useApi";
import {
  generateClientKey,
  type ProviderAPITypeDraft,
  type ProviderCredentialMode,
} from "../../pages/providers/types";
import { CodexRoutesField } from "./CodexRoutesField";
import { RouteConfigurationFields } from "./RouteConfigurationFields";
import type { RouteConfigurationDraft } from "./routeDrafts";

type RouteChange = Partial<
  RouteConfigurationDraft & Pick<ProviderAPITypeDraft, "api_type">
>;

function ApiTypeRouteRow({
  entry,
  index,
  credentialSessions,
  onChange,
  onRemove,
}: {
  entry: ProviderAPITypeDraft;
  index: number;
  credentialSessions: CredentialSession[];
  onChange: (change: RouteChange) => void;
  onRemove: () => void;
}) {
  const entryLabel = entry.api_type || `entry ${index + 1}`;

  return (
    <div className="rounded-xl border border-border/70 bg-bg-secondary/30 p-3 space-y-2.5">
      <div className="flex items-center gap-2">
        <input
          type="text"
          className="input"
          value={entry.api_type}
          onChange={(event) => onChange({ api_type: event.target.value })}
          placeholder="API type"
          aria-label={`API type ${index + 1}`}
        />
        <button
          type="button"
          onClick={onRemove}
          className="h-10 px-3 rounded-lg border border-border text-text-muted hover:text-danger hover:border-danger/30 transition-colors shrink-0 cursor-pointer"
          aria-label={`Remove ${entryLabel}`}
        >
          Remove
        </button>
      </div>
      <RouteConfigurationFields
        configuration={entry}
        entryLabel={entryLabel}
        credentialSessions={credentialSessions}
        onChange={onChange}
      />
    </div>
  );
}

export function ApiTypesField({
  entries,
  onChange,
  credentialSessions = [],
  credentialMode = "api_key",
}: {
  entries: ProviderAPITypeDraft[];
  onChange: (entries: ProviderAPITypeDraft[]) => void;
  credentialSessions?: CredentialSession[];
  credentialMode?: ProviderCredentialMode;
}) {
  const { catalog, loading, error, refetch } = useAPICatalog();
  const apiKeySessions = credentialSessions.filter(
    (session) => session.kind === "api_key",
  );
  const firstCodexRoute = entries.find((entry) => entry.api_type === "codex");

  function updateEntry(clientKey: string, change: RouteChange) {
    onChange(
      entries.map((entry) =>
        entry.client_key === clientKey
          ? {
              ...entry,
              ...change,
              ...(change.api_type !== undefined && change.api_type !== "codex"
                ? { transport: "http" as const }
                : {}),
            }
          : entry,
      ),
    );
  }

  function addEntry(apiType = "") {
    const lastUrl = entries.at(-1)?.base_url ?? "";
    onChange([
      ...entries,
      {
        client_key: generateClientKey(),
        api_type: apiType,
        transport: "http",
        base_url: lastUrl,
        credential_session_id: "",
        api_key: "",
      },
    ]);
  }

  function toggleQuickType(type: string) {
    if (entries.some((entry) => entry.api_type === type)) {
      onChange(entries.filter((entry) => entry.api_type !== type));
    } else {
      addEntry(type);
    }
  }

  const selectedTypes = new Set(entries.map((entry) => entry.api_type));

  return (
    <fieldset>
      <legend className="block text-sm font-medium text-text-secondary mb-1">
        API Types
      </legend>
      <p className="text-xs text-text-muted mb-3">
        Edit the API key directly, or select a saved credential. Key changes
        take effect when you save this provider.
      </p>
      <div className="space-y-3">
        {entries.map((entry, index) =>
          entry.api_type === "codex" ? (
            entry === firstCodexRoute && (
              <CodexRoutesField
                key="codex"
                entries={entries}
                onChange={onChange}
                credentialSessions={
                  credentialMode === "mixed"
                    ? credentialSessions
                    : apiKeySessions
                }
              />
            )
          ) : (
            <ApiTypeRouteRow
              key={entry.client_key}
              entry={entry}
              index={index}
              credentialSessions={apiKeySessions}
              onChange={(change) => updateEntry(entry.client_key, change)}
              onRemove={() =>
                onChange(
                  entries.filter(
                    (route) => route.client_key !== entry.client_key,
                  ),
                )
              }
            />
          ),
        )}
      </div>
      <div className="flex flex-wrap items-center gap-2 mt-2">
        {catalog?.api_types.map((entry) => {
          const selected = selectedTypes.has(entry.api_type);
          return (
            <button
              key={entry.api_type}
              type="button"
              aria-pressed={selected}
              onClick={() => toggleQuickType(entry.api_type)}
              title={entry.description}
              className={`px-2 py-1 text-xs rounded-full border transition-colors cursor-pointer ${
                selected
                  ? "bg-primary text-white border-primary"
                  : "bg-bg-secondary text-text-secondary border-border hover:border-primary"
              }`}
            >
              {entry.api_type}
            </button>
          );
        })}
        <button
          type="button"
          onClick={() => addEntry()}
          className="px-2 py-1 text-xs rounded-full border border-dashed border-border text-text-secondary hover:border-primary hover:text-primary transition-colors cursor-pointer"
        >
          + Custom
        </button>
        {loading && (
          <span className="text-xs text-text-muted" role="status">
            Loading built-in API types...
          </span>
        )}
        {error && (
          <button
            type="button"
            onClick={() => void refetch()}
            className="text-xs text-danger hover:underline cursor-pointer"
            title={error.message}
          >
            Retry API type catalog
          </button>
        )}
      </div>
    </fieldset>
  );
}
