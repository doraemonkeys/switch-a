import type { ProviderAPITypeDraft } from "../../pages/providers/types";
import { generateClientKey } from "../../pages/providers/types";
import { CHATGPT_CODEX_BASE_URL } from "../../config/constants";

export function CodexTransportsField({
  entries,
  onChange,
}: {
  entries: ProviderAPITypeDraft[];
  onChange: (entries: ProviderAPITypeDraft[]) => void;
}) {
  const codexRoutes = entries.filter((entry) => entry.api_type === "codex");
  function toggle(transport: "http" | "websocket", enabled: boolean) {
    if (!enabled) {
      onChange(
        entries.filter(
          (entry) =>
            entry.api_type !== "codex" || entry.transport !== transport,
        ),
      );
      return;
    }
    const reference = codexRoutes[0];
    onChange([
      ...entries,
      {
        client_key: generateClientKey(),
        api_type: "codex",
        transport,
        base_url: reference?.base_url ?? CHATGPT_CODEX_BASE_URL,
        credential_session_id: reference?.credential_session_id ?? "",
        api_key: reference?.api_key ?? "",
      },
    ]);
  }
  return (
    <fieldset className="rounded-xl border border-border/70 p-3 space-y-2">
      <legend className="px-1 text-sm font-medium">Codex transports</legend>
      <div className="flex flex-wrap gap-5">
        {(["http", "websocket"] as const).map((transport) => (
          <label
            key={transport}
            className="flex items-center gap-2 text-sm cursor-pointer"
          >
            <input
              type="checkbox"
              checked={codexRoutes.some(
                (entry) => entry.transport === transport,
              )}
              onChange={(event) => toggle(transport, event.target.checked)}
            />
            {transport === "http" ? "HTTP / SSE" : "WebSocket"}
          </label>
        ))}
      </div>
      <p className="text-xs text-text-muted">
        Requests only use enabled transports. Each route can share a credential
        or use its own.
      </p>
    </fieldset>
  );
}
