import type { ProviderAPITypeDraft } from "../../pages/providers/types";
import { generateClientKey } from "../../pages/providers/types";
import { CHATGPT_CODEX_BASE_URL } from "../../config/constants";

export function defaultCodexRoutes(): ProviderAPITypeDraft[] {
  return (["http", "websocket"] as const).map((transport) => ({
    client_key: generateClientKey(),
    api_type: "codex",
    transport,
    base_url: CHATGPT_CODEX_BASE_URL,
    credential_session_id: "",
    api_key: "",
  }));
}
