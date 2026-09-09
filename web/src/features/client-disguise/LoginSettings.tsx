import { useState } from "react";
import { Check, Fingerprint, Save } from "lucide-react";
import type {
  DisguiseState,
  LoginView,
  ProfileBinding,
} from "@/api/client-disguise/types";
import { ProfileFields } from "./ProfileFields";
import { AdvancedLoginSettings } from "./AdvancedLoginSettings";
import { LoginIdentity } from "./LoginIdentity";
import {
  buildProfileBinding,
  createLoginDraft,
  hasLoginChanges,
  type LoginDraft,
} from "./loginDraft";

export function LoginSettings({
  login,
  state,
  busy,
  save,
  draft,
  change,
}: {
  login: LoginView;
  state: DisguiseState;
  busy: boolean;
  save: (binding: ProfileBinding) => Promise<void>;
  draft: LoginDraft;
  change: (draft: LoginDraft) => void;
}) {
  const [error, setError] = useState("");
  const dirty = hasLoginChanges(draft, login, state);
  const profile = state.profiles.find((item) => item.id === draft.revisionID);
  const activeProviders = login.providers.filter(
    (provider) => provider.client_disguise.enabled,
  ).length;
  const providerLabel = activeProviders === 1 ? "provider" : "providers";
  async function submit() {
    setError("");
    try {
      await save(buildProfileBinding(draft, login, state));
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason));
    }
  }
  function reset() {
    change(createLoginDraft(login, state));
    setError("");
  }
  return (
    <form
      className="cd-editor"
      aria-label="Login identity and profile"
      onSubmit={(event) => {
        event.preventDefault();
        void submit();
      }}
    >
      <header className="cd-editor-header">
        <div className="cd-editor-title">
          <span className="cd-identity-icon">
            <Fingerprint size={24} aria-hidden="true" />
          </span>
          <div>
            <p className="cd-kicker">CREDENTIAL LOGIN</p>
            <h2>{login.name}</h2>
          </div>
        </div>
        <span
          className={
            activeProviders ? "cd-status cd-status-active" : "cd-status"
          }
        >
          <i aria-hidden="true" />
          {activeProviders
            ? `Enabled on ${activeProviders} ${providerLabel}`
            : "No providers enabled"}
        </span>
      </header>
      <fieldset disabled={busy} className="cd-editor-fields">
        <ProfileFields
          state={state}
          draft={draft}
          change={change}
          profile={profile}
        />
        <LoginIdentity login={login} />
        <AdvancedLoginSettings state={state} draft={draft} change={change} />
      </fieldset>
      <footer className="cd-save-bar">
        <div>
          {error ? (
            <p role="alert" className="cd-error-text">
              {error}
            </p>
          ) : (
            <p className={dirty ? "cd-dirty" : "cd-saved"}>
              {dirty ? (
                <>
                  <span aria-hidden="true">●</span> Unsaved changes
                </>
              ) : (
                <>
                  <Check size={15} aria-hidden="true" />
                  {login.binding
                    ? "All changes saved"
                    : "Select a profile to get started"}
                </>
              )}
            </p>
          )}
          <span>Provider settings control when disguise is applied.</span>
        </div>
        <div className="cd-actions">
          {dirty && (
            <button
              type="button"
              className="cd-button cd-button-secondary"
              disabled={busy}
              onClick={reset}
            >
              Reset
            </button>
          )}
          <button
            type="submit"
            className="cd-button cd-button-primary"
            disabled={busy || !profile || !dirty}
          >
            <Save size={15} aria-hidden="true" />
            {busy ? "Saving…" : "Save login settings"}
          </button>
        </div>
      </footer>
    </form>
  );
}
