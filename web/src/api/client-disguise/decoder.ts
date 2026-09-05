import {
  parseDisguisePolicy,
  type ClientTuple,
  type DisguiseState,
  type LoginView,
  type ProfileBinding,
  type ProfileRevision,
  type ReferenceSource,
  type TransportSample,
} from "./types";

export function record(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value))
    throw new Error("Expected client disguise object");
  return value as Record<string, unknown>;
}
export function str(value: unknown): string {
  if (typeof value !== "string")
    throw new Error("Expected client disguise string");
  return value;
}
export function bool(value: unknown): boolean {
  if (typeof value !== "boolean")
    throw new Error("Expected client disguise boolean");
  return value;
}
export function list<T>(value: unknown, parse: (value: unknown) => T): T[] {
  if (value === null) return [];
  if (!Array.isArray(value)) throw new Error("Expected client disguise array");
  return value.map(parse);
}
export function stringMap(value: unknown): Record<string, string> {
  return Object.fromEntries(
    Object.entries(record(value)).map(([key, item]) => [key, str(item)]),
  );
}
function tuple(value: unknown): ClientTuple {
  const item = record(value);
  return {
    client_type: str(item.client_type),
    platform: str(item.platform),
    arch: str(item.arch),
  };
}
export function parseProfile(value: unknown): ProfileRevision {
  const item = record(value);
  const features = record(item.features);
  return {
    id: str(item.id),
    ...(item.evidence_kind == null
      ? {}
      : { evidence_kind: str(item.evidence_kind) }),
    ...(item.source_url == null ? {} : { source_url: str(item.source_url) }),
    tuple: tuple(item.tuple),
    client_version: str(item.client_version),
    source_id: str(item.source_id),
    captured_at: str(item.captured_at),
    created_at: str(item.created_at),
    features: {
      user_agent: str(features.user_agent),
      originator: str(features.originator),
      client_version: str(features.client_version),
      desktop_build: str(features.desktop_build),
      os_version: str(features.os_version),
      ...(features.headers == null
        ? {}
        : { headers: stringMap(features.headers) }),
    },
  };
}
export function parseBinding(value: unknown): ProfileBinding {
  const item = record(value);
  if (item.mode !== "auto" && item.mode !== "pinned")
    throw new Error("Invalid profile binding mode");
  return {
    credential_session_id: str(item.credential_session_id),
    tuple: tuple(item.tuple),
    mode: item.mode,
    revision_id: str(item.revision_id),
    reference_source_id: str(item.reference_source_id),
    transport_sample_id: str(item.transport_sample_id),
    remap_cache_keys: bool(item.remap_cache_keys),
    telemetry_path_mappings:
      item.telemetry_path_mappings == null
        ? null
        : stringMap(item.telemetry_path_mappings),
    ...(item.updated_at === undefined
      ? {}
      : { updated_at: str(item.updated_at) }),
  };
}
export function parseReference(value: unknown): ReferenceSource {
  const item = record(value);
  return {
    id: str(item.id),
    name: str(item.name),
    client_identity_id: str(item.client_identity_id),
  };
}
export function parseTransport(value: unknown): TransportSample {
  const item = record(value);
  return {
    id: str(item.id),
    name: str(item.name),
    source_id: str(item.source_id),
    captured_at: str(item.captured_at),
    tls_profile: str(item.tls_profile),
    http_profile: str(item.http_profile),
    config: item.config,
  };
}
function parseLogin(value: unknown): LoginView {
  const item = record(value);
  const identity = item.identity == null ? null : record(item.identity);
  return {
    credential_session_id: str(item.credential_session_id),
    name: str(item.name),
    ...(identity
      ? {
          identity: {
            credential_session_id: str(identity.credential_session_id),
            generation_id: str(identity.generation_id),
            device_id: str(identity.device_id),
            created_at: str(identity.created_at),
          },
        }
      : {}),
    ...(item.binding == null ? {} : { binding: parseBinding(item.binding) }),
    providers: list(item.providers, (value) => {
      const provider = record(value);
      return {
        provider_id: str(provider.provider_id),
        provider_name: str(provider.provider_name),
        client_disguise: parseDisguisePolicy(provider.client_disguise),
      };
    }),
  };
}
export function parseDisguiseState(value: unknown): DisguiseState {
  const item = record(value);
  return {
    logins: list(item.logins, parseLogin),
    profiles: list(item.profiles, parseProfile),
    references: list(item.references, parseReference),
    transport_samples: list(item.transport_samples, parseTransport),
    clients: list(item.clients, (value) => ({
      client_id: str(record(value).client_id),
    })),
  };
}
