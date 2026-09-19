import type {
  DisguiseState,
  LoginView,
  ProfileBinding,
} from "@/api/client-disguise/types";
import {
  environmentKey,
  environmentProfiles,
  referenceHead,
  compareVersions,
} from "./profiles/profileCatalog";

export interface EnvironmentSelection {
  revisionID: string;
  reference: string;
}
export interface LoginDraft {
  versionSource: "" | "official_stable";
  environment: string;
  selections: Record<string, EnvironmentSelection>;
  mode: ProfileBinding["mode"];
  transport: string;
  paths: string;
}
const EMPTY_SELECTION: EnvironmentSelection = { revisionID: "", reference: "" };

export function profileSelection(draft: LoginDraft): EnvironmentSelection {
  return draft.selections[draft.environment] ?? EMPTY_SELECTION;
}
export function createLoginDraft(login: LoginView): LoginDraft {
  const binding = login.binding;
  const environment = binding ? environmentKey(binding.tuple) : "";
  return {
    versionSource: binding?.version_source ?? "",
    environment,
    selections: binding
      ? {
          [environment]: {
            revisionID: binding.revision_id,
            reference: binding.reference_source_id,
          },
        }
      : {},
    mode: binding?.mode ?? "auto",
    transport: binding?.transport_sample_id ?? "",
    paths: JSON.stringify(binding?.telemetry_path_mappings ?? {}, null, 2),
  };
}
export function changeProfileSelection(
  draft: LoginDraft,
  change: Partial<EnvironmentSelection>,
): LoginDraft {
  return {
    ...draft,
    selections: {
      ...draft.selections,
      [draft.environment]: { ...profileSelection(draft), ...change },
    },
  };
}
export function changeEnvironment(
  draft: LoginDraft,
  environment: string,
  state: DisguiseState,
): LoginDraft {
  const remembered = draft.selections[environment];
  const profiles = environmentProfiles(state, environment);
  if (
    remembered &&
    profiles.some((profile) => profile.id === remembered.revisionID)
  ) {
    return { ...draft, environment };
  }
  const previousReference = profileSelection(draft).reference;
  const head = referenceHead(state, environment, previousReference);
  const firstSourceHead =
    profiles[0] && referenceHead(state, environment, profiles[0].source_id);
  const profile = head ?? firstSourceHead ?? profiles[0];
  const reference =
    state.references.find((source) => source.id === profile?.source_id)?.id ??
    "";
  return {
    ...draft,
    environment,
    selections: {
      ...draft.selections,
      [environment]: {
        revisionID: profile?.id ?? "",
        reference,
      },
    },
  };
}
export function selectedProfile(draft: LoginDraft, state: DisguiseState) {
  return state.profiles.find(
    (profile) =>
      profile.id === profileSelection(draft).revisionID &&
      environmentKey(profile.tuple) === draft.environment,
  );
}
export function effectiveProfile(draft: LoginDraft, state: DisguiseState) {
  const selected = selectedProfile(draft, state);
  if (!selected || draft.mode !== "auto") return selected;
  const head = referenceHead(
    state,
    draft.environment,
    profileSelection(draft).reference,
  );
  return head &&
    compareVersions(head.client_version, selected.client_version) >= 0
    ? head
    : selected;
}
export function hasLoginChanges(draft: LoginDraft, login: LoginView) {
  const saved = createLoginDraft(login);
  const selection = profileSelection(draft),
    savedSelection = profileSelection(saved);
  // Choices remembered for other environments are navigation state, not pending writes.
  return (
    draft.environment !== saved.environment ||
    draft.versionSource !== saved.versionSource ||
    draft.mode !== saved.mode ||
    draft.transport !== saved.transport ||
    draft.paths !== saved.paths ||
    selection.revisionID !== savedSelection.revisionID ||
    selection.reference !== savedSelection.reference
  );
}
export function buildProfileBinding(
  draft: LoginDraft,
  login: LoginView,
  state: DisguiseState,
): ProfileBinding {
  const profile = selectedProfile(draft, state);
  if (!profile) throw new Error("Select an available environment snapshot.");
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
    revision_id: profile.id,
    mode: draft.mode,
    reference_source_id: profileSelection(draft).reference,
    transport_sample_id: draft.transport,
    telemetry_path_mappings: mappings as Record<string, string>,
  };
}
