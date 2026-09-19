import { useState } from "react";
import type { CredentialSession } from "../../api";
import type { ProviderAPITypeDraft } from "../../pages/providers/types";
import { CodexTransportsField } from "./CodexTransportsField";
import { RouteConfigurationFields } from "./RouteConfigurationFields";
import {
  haveSameRouteConfiguration,
  routeConfiguration,
  type RouteConfigurationDraft,
} from "./routeDrafts";

export function CodexRoutesField({
  entries,
  credentialSessions,
  onChange,
}: {
  entries: ProviderAPITypeDraft[];
  credentialSessions: CredentialSession[];
  onChange: (entries: ProviderAPITypeDraft[]) => void;
}) {
  const routes = entries.filter((entry) => entry.api_type === "codex");
  const httpRoute = routes.find((entry) => entry.transport === "http");
  const websocketRoute = routes.find(
    (entry) => entry.transport === "websocket",
  );
  const configurationsMatch = haveSameRouteConfiguration(routes);
  // Equal values must stay independently editable after the user opts to split.
  const [separateEditing, setSeparateEditing] = useState(!configurationsMatch);
  const useSeparateSettings = separateEditing || !configurationsMatch;
  const reference = httpRoute ?? websocketRoute;
  if (!reference) return null;

  const bothEnabled = Boolean(httpRoute && websocketRoute);
  const shared = bothEnabled && !useSeparateSettings;
  const visibleRoutes = shared ? [reference] : routes;

  function updateConfiguration(
    clientKey: string,
    change: Partial<RouteConfigurationDraft>,
  ) {
    if (useSeparateSettings) setSeparateEditing(true);
    onChange(
      entries.map((entry) =>
        entry.api_type === "codex" && (shared || entry.client_key === clientKey)
          ? { ...entry, ...change }
          : entry,
      ),
    );
  }

  function changeSeparateSettings(separate: boolean) {
    setSeparateEditing(separate);
    if (!separate && httpRoute) {
      const configuration = routeConfiguration(httpRoute);
      onChange(
        entries.map((entry) =>
          entry.api_type === "codex" ? { ...entry, ...configuration } : entry,
        ),
      );
    }
  }

  return (
    <CodexTransportsField entries={entries} onChange={onChange}>
      {bothEnabled && (
        <div className="space-y-1 border-t border-border/70 pt-3">
          <label className="flex items-center gap-2 text-sm cursor-pointer">
            <input
              type="checkbox"
              checked={useSeparateSettings}
              onChange={(event) => changeSeparateSettings(event.target.checked)}
            />
            Configure WebSocket separately
          </label>
          <p className="text-xs text-text-muted">
            {shared
              ? "HTTP / SSE and WebSocket share the base URL and credential below."
              : "Turn off to use the HTTP / SSE base URL and credential for both transports."}
          </p>
        </div>
      )}
      {visibleRoutes.map((entry) => (
        <div key={entry.client_key} className="space-y-2.5 pt-2">
          {bothEnabled && useSeparateSettings && (
            <p className="text-xs font-semibold">
              Codex ·{" "}
              {entry.transport === "websocket" ? "WebSocket" : "HTTP / SSE"}
            </p>
          )}
          <RouteConfigurationFields
            configuration={entry}
            entryLabel={
              entry.transport === "websocket" ? "codex WebSocket" : "codex"
            }
            credentialSessions={credentialSessions}
            onChange={(change) => updateConfiguration(entry.client_key, change)}
          />
        </div>
      ))}
      <button
        type="button"
        className="text-xs text-text-muted hover:text-danger cursor-pointer"
        aria-label="Remove codex"
        onClick={() =>
          onChange(entries.filter((entry) => entry.api_type !== "codex"))
        }
      >
        Remove Codex
      </button>
    </CodexTransportsField>
  );
}
