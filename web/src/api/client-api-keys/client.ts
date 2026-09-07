import {
  parseClientAPIKey,
  parseClientAPIKeysState,
  parseClientAccessPolicy,
  type ClientAccessMode,
} from "./types";

type Request = <T>(endpoint: string, options?: RequestInit) => Promise<T>;
const BASE = "/client-api-keys";

export function createClientApiKeysApi(request: Request) {
  const write = (path: string, method: string, body: unknown) =>
    request<unknown>(BASE + path, { method, body: JSON.stringify(body) });
  return {
    get: async () => parseClientAPIKeysState(await request<unknown>(BASE)),
    setPolicy: async (mode: ClientAccessMode) =>
      parseClientAccessPolicy(await write("/policy", "PUT", { mode })),
    add: async (name: string, key: string) =>
      parseClientAPIKey(await write("", "POST", { name, key })),
    generate: async (name: string) =>
      parseClientAPIKey(await write("/generate", "POST", { name })),
    rename: async (id: string, name: string) =>
      parseClientAPIKey(
        await write("/keys/" + encodeURIComponent(id), "PUT", { name }),
      ),
    delete: (id: string) =>
      request<void>(BASE + "/keys/" + encodeURIComponent(id), {
        method: "DELETE",
      }),
  };
}
