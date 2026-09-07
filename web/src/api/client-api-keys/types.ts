export type ClientAccessMode = "permissive" | "restricted";

export interface ClientAPIKey {
  id: string;
  name: string;
  key: string;
  created_at: string;
  updated_at: string;
}

export interface ClientAPIKeysState {
  mode: ClientAccessMode;
  keys: ClientAPIKey[];
}

function record(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    throw new Error("Invalid client API key response");
  }
  return value as Record<string, unknown>;
}

function str(value: unknown): string {
  if (typeof value !== "string")
    throw new Error("Invalid client API key field");
  return value;
}

export function parseClientAccessMode(value: unknown): ClientAccessMode {
  if (value !== "permissive" && value !== "restricted") {
    throw new Error("Invalid client access mode");
  }
  return value;
}

export function parseClientAPIKey(value: unknown): ClientAPIKey {
  const item = record(value);
  return {
    id: str(item.id),
    name: str(item.name),
    key: str(item.key),
    created_at: str(item.created_at),
    updated_at: str(item.updated_at),
  };
}

export function parseClientAccessPolicy(value: unknown) {
  return { mode: parseClientAccessMode(record(value).mode) };
}

export function parseClientAPIKeysState(value: unknown): ClientAPIKeysState {
  const state = record(value);
  if (!Array.isArray(state.keys))
    throw new Error("Invalid client API keys list");
  return {
    mode: parseClientAccessMode(state.mode),
    keys: state.keys.map(parseClientAPIKey),
  };
}
