import {
  bool,
  list,
  parseBinding,
  parseDisguiseState,
  parseProfile,
  parseReference,
  parseTransport,
  record,
  str,
} from "./decoder";
import type {
  ClientSample,
  LearnResult,
  ProfileBinding,
  ReferenceSource,
  TransportSample,
} from "./types";

type Request = <T>(endpoint: string, options?: RequestInit) => Promise<T>;
const BASE = "/client-disguise";
export function createClientDisguiseApi(request: Request) {
  const write = (path: string, method: string, body: unknown) =>
    request<unknown>(`${BASE}${path}`, { method, body: JSON.stringify(body) });
  return {
    get: async () => parseDisguiseState(await request<unknown>(BASE)),
    saveBinding: async (id: string, binding: ProfileBinding) =>
      parseBinding(
        await write(`/logins/${encodeURIComponent(id)}`, "PUT", binding),
      ),
    importSample: async (sample: ClientSample): Promise<LearnResult> => {
      const result = record(await write("/samples", "POST", sample));
      return {
        revision: parseProfile(result.revision),
        created: bool(result.created),
        advanced_sessions: list(result.advanced_sessions, str),
      };
    },
    saveReference: async (reference: ReferenceSource) =>
      parseReference(
        await write(
          `/references/${encodeURIComponent(reference.id)}`,
          "PUT",
          reference,
        ),
      ),
    importTransport: async (sample: TransportSample) =>
      parseTransport(await write("/transport-samples", "POST", sample)),
    bindKey: async (api_key: string, client_id: string) => {
      const result = record(
        await write("/key-bindings", "POST", { api_key, client_id }),
      );
      return { client_id: str(result.client_id) };
    },
  };
}
