import type { ProviderAPITypeDraft } from "../../pages/providers/types";
import { generateClientKey } from "../../pages/providers/types";
import { CHATGPT_CODEX_BASE_URL } from "../../config/constants";

export type RouteConfigurationDraft = Pick<
  ProviderAPITypeDraft,
  "base_url" | "credential_session_id" | "api_key"
>;

export function routeConfiguration(
  route: ProviderAPITypeDraft,
): RouteConfigurationDraft {
  return {
    base_url: route.base_url,
    credential_session_id: route.credential_session_id,
    api_key: route.api_key,
  };
}

export function haveSameRouteConfiguration(
  routes: ProviderAPITypeDraft[],
): boolean {
  const reference = routes[0];
  return routes.every(
    (route) =>
      route.base_url === reference.base_url &&
      route.credential_session_id === reference.credential_session_id &&
      route.api_key === reference.api_key,
  );
}

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
