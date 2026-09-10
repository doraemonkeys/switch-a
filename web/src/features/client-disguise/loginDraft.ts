import type {
  DisguiseState,
  LoginView,
  ProfileBinding,
} from "@/api/client-disguise/types";

export interface LoginDraft {
  versionSource: "" | "official_stable";
  revisionID: string;
  version: string;
  mode: ProfileBinding["mode"];
  reference: string;
  transport: string;
  paths: string;
}

export function createLoginDraft(
  login: LoginView,
  state: DisguiseState,
): LoginDraft {
  const binding = login.binding;
  return {
    versionSource: binding?.version_source ?? "",
    revisionID: binding?.revision_id ?? "",
    version:
      state.profiles.find((profile) => profile.id === binding?.revision_id)
        ?.client_version ?? "",
    mode: binding?.mode ?? "auto",
    reference: binding?.reference_source_id ?? "",
    transport: binding?.transport_sample_id ?? "",
    paths: JSON.stringify(binding?.telemetry_path_mappings ?? {}, null, 2),
  };
}

export function hasLoginChanges(
  draft: LoginDraft,
  login: LoginView,
  state: DisguiseState,
) {
  const saved = createLoginDraft(login, state);
  return Object.keys(saved).some(
    (key) =>
      key !== "version" &&
      draft[key as keyof LoginDraft] !== saved[key as keyof LoginDraft],
  );
}

export function buildProfileBinding(
  draft: LoginDraft,
  login: LoginView,
  state: DisguiseState,
): ProfileBinding {
  const profile = state.profiles.find((item) => item.id === draft.revisionID);
  if (!profile) throw new Error("Select a profile revision.");
  const mappings: unknown = JSON.parse(draft.paths);
  if (
    !mappings ||
    Array.isArray(mappings) ||
    typeof mappings !== "object" ||
    Object.values(mappings).some((value) => typeof value !== "string")
  ) {
    throw new Error("Telemetry mappings must be an object of path strings.");
  }
  return {
    credential_session_id: login.credential_session_id,
    ...(draft.versionSource ? { version_source: draft.versionSource } : {}),
    tuple: profile.tuple,
    revision_id: draft.revisionID,
    mode: draft.mode,
    reference_source_id: draft.reference,
    transport_sample_id: draft.transport,
    telemetry_path_mappings: mappings as Record<string, string>,
  };
}
