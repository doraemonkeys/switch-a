import { Pin, RefreshCw, SlidersHorizontal } from "lucide-react";
import type {
  DisguiseState,
  ProfileRevision,
} from "@/api/client-disguise/types";
import type { LoginDraft } from "./loginDraft";
import { ProfileSummary } from "./ProfileSummary";

export function ProfileFields({
  state,
  draft,
  change,
  profile,
}: {
  state: DisguiseState;
  draft: LoginDraft;
  change: (draft: LoginDraft) => void;
  profile?: ProfileRevision;
}) {
  const versions = [
    ...new Set(state.profiles.map((item) => item.client_version)),
  ];
  const revisions = state.profiles.filter(
    (item) => !draft.version || item.client_version === draft.version,
  );
  return (
    <section className="cd-editor-section" aria-labelledby="profile-heading">
      <div className="cd-section-heading">
        <SlidersHorizontal size={17} aria-hidden="true" />
        <h3 id="profile-heading">Client profile</h3>
      </div>
      <p className="cd-description">
        Choose the client version and environment this login presents upstream.
      </p>
      <div className="cd-field-grid">
        <label className="cd-field">
          Client version
          <select
            value={draft.version}
            onChange={(event) =>
              change({ ...draft, version: event.target.value, revisionID: "" })
            }
          >
            <option value="">All versions</option>
            {versions.map((version) => (
              <option key={version}>{version}</option>
            ))}
          </select>
        </label>
        <label className="cd-field">
          Profile revision
          <select
            value={draft.revisionID}
            onChange={(event) =>
              change({
                ...draft,
                revisionID: event.target.value,
                mode: "pinned",
              })
            }
          >
            <option value="">Select revision</option>
            {revisions.map((item) => (
              <option key={item.id} value={item.id}>
                {item.tuple.client_type} / {item.tuple.platform} /{" "}
                {item.tuple.arch} — {item.id}
              </option>
            ))}
          </select>
        </label>
      </div>
      {state.profiles.length === 0 && (
        <p className="cd-description">
          No profiles available. Import an application sample in the reference
          library to create one.
        </p>
      )}
      <fieldset className="cd-mode-fieldset">
        <legend>Update mode</legend>
        <div className="cd-mode-options">
          <label
            className="cd-mode-option"
            data-selected={draft.mode === "auto"}
          >
            <input
              type="radio"
              name="update-mode"
              value="auto"
              checked={draft.mode === "auto"}
              onChange={() => change({ ...draft, mode: "auto" })}
            />
            <RefreshCw size={18} aria-hidden="true" />
            <span>
              <strong>Automatic follow</strong>
              <small>Keep up with your reference source.</small>
            </span>
          </label>
          <label
            className="cd-mode-option"
            data-selected={draft.mode === "pinned"}
          >
            <input
              type="radio"
              name="update-mode"
              value="pinned"
              checked={draft.mode === "pinned"}
              onChange={() => change({ ...draft, mode: "pinned" })}
            />
            <Pin size={18} aria-hidden="true" />
            <span>
              <strong>Pin this revision</strong>
              <small>Keep this exact profile until you change it.</small>
            </span>
          </label>
        </div>
      </fieldset>
      {draft.mode === "auto" ? (
        <label className="cd-field cd-reference-field">
          Reference source
          <select
            aria-label="Reference source"
            value={draft.reference}
            onChange={(event) =>
              change({ ...draft, reference: event.target.value })
            }
          >
            <option value="">Built-in profile</option>
            {state.references.map((item) => (
              <option key={item.id} value={item.id}>
                {item.name}
              </option>
            ))}
          </select>
          <span className="cd-field-help">
            Follows newer samples matching this profile’s client type, platform
            and architecture.
          </span>
        </label>
      ) : (
        <p className="cd-field-help cd-mode-help">
          Manual revision selection pins the profile. Choose automatic follow to
          receive future updates.
        </p>
      )}
      {profile && <ProfileSummary profile={profile} />}
    </section>
  );
}
