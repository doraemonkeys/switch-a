export interface ClientDisguisePolicy {
  enabled: boolean;
  match_platform?: boolean;
  unknown_platform: "exclude" | "allow_current";
}

export interface ClientTuple {
  client_type: string;
  platform: string;
  arch: string;
}
export interface ClientFeatures {
  user_agent: string;
  originator: string;
  client_version: string;
  desktop_build: string;
  os_version: string;
  headers?: Record<string, string>;
}
export interface ProfileRevision {
  evidence_kind?: string;
  source_url?: string;
  id: string;
  tuple: ClientTuple;
  client_version: string;
  features: ClientFeatures;
  source_id: string;
  captured_at: string;
  created_at: string;
}
export interface ProfileBinding {
  credential_session_id: string;
  tuple: ClientTuple;
  mode: "auto" | "pinned";
  revision_id: string;
  reference_source_id: string;
  transport_sample_id: string;
  remap_cache_keys: boolean;
  telemetry_path_mappings: Record<string, string> | null;
  updated_at?: string;
}
export interface LoginIdentity {
  credential_session_id: string;
  generation_id: string;
  device_id: string;
  created_at: string;
}
export interface LoginView {
  credential_session_id: string;
  name: string;
  identity?: LoginIdentity;
  binding?: ProfileBinding;
  providers: {
    provider_id: string;
    provider_name: string;
    client_disguise: ClientDisguisePolicy;
  }[];
}
export interface ReferenceSource {
  id: string;
  name: string;
  client_identity_id: string;
}
export interface TransportSample {
  id: string;
  source_id: string;
  captured_at: string;
  name: string;
  tls_profile: string;
  http_profile: string;
  config: unknown;
}
export interface ClientSample {
  /** Omit to let the server assign this observation an ID. */
  id?: string;
  source_id: string;
  captured_at: string;
  tuple: ClientTuple;
  client_version: string;
  features: ClientFeatures;
}
export interface DisguiseState {
  logins: LoginView[];
  profiles: ProfileRevision[];
  references: ReferenceSource[];
  transport_samples: TransportSample[];
  clients: { client_id: string }[];
}
export interface LearnResult {
  revision: ProfileRevision;
  created: boolean;
  advanced_sessions: string[];
}

export const DEFAULT_DISGUISE_POLICY: ClientDisguisePolicy = {
  enabled: false,
  match_platform: true,
  unknown_platform: "exclude",
};

export function parseDisguisePolicy(value: unknown): ClientDisguisePolicy {
  if (value === undefined || value === null)
    return { ...DEFAULT_DISGUISE_POLICY };
  if (typeof value !== "object" || Array.isArray(value)) {
    throw new Error("Invalid client_disguise policy");
  }
  const source = value as Record<string, unknown>;
  if (
    typeof source.enabled !== "boolean" ||
    (source.match_platform != null &&
      typeof source.match_platform !== "boolean") ||
    ![undefined, "", "exclude", "allow_current"].includes(
      source.unknown_platform as string,
    )
  ) {
    throw new Error("Invalid client_disguise policy");
  }
  return {
    enabled: source.enabled,
    match_platform:
      source.match_platform == null ? true : source.match_platform,
    unknown_platform:
      source.unknown_platform === "allow_current" ? "allow_current" : "exclude",
  };
}
