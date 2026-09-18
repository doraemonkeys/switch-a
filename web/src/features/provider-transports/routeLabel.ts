import type { ProviderAPIType } from "../../api";

export function providerRouteLabel(
  route: Pick<ProviderAPIType, "api_type" | "transport">,
): string {
  if (route.api_type !== "codex") return route.api_type;
  const transport = route.transport === "websocket" ? "WS" : "HTTP";
  return `${route.api_type} / ${transport}`;
}
