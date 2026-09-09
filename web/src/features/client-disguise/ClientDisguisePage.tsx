import { useState } from "react";
import { Link, useSearchParams } from "react-router";
import {
  ArrowRight,
  CheckCircle2,
  Fingerprint,
  KeyRound,
  Library,
  SlidersHorizontal,
} from "lucide-react";
import { LoginSettings } from "./LoginSettings";
import { LoginList } from "./LoginList";
import { ReferenceSettings } from "./ReferenceSettings";
import { ClientIdentitySettings } from "./references/ClientIdentitySettings";
import {
  createLoginDraft,
  hasLoginChanges,
  type LoginDraft,
} from "./loginDraft";
import { useClientDisguise } from "./useClientDisguise";
import "./client-disguise.css";

const SECTIONS = [
  { id: "profiles", label: "Login profiles", icon: SlidersHorizontal },
  { id: "references", label: "Reference library", icon: Library },
  { id: "clients", label: "Client identities", icon: KeyRound },
] as const;

export function ClientDisguisePage() {
  const { api, state, error, notice, busy, mutate, retry } =
    useClientDisguise();
  const [params, setParams] = useSearchParams();
  // Keep drafts by login so navigation never silently discards an unfinished edit.
  const [drafts, setDrafts] = useState<Record<string, LoginDraft>>({});
  const section =
    SECTIONS.find((item) => item.id === params.get("view"))?.id ?? "profiles";
  const login =
    state?.logins.find(
      (item) => item.credential_session_id === params.get("login"),
    ) ?? state?.logins[0];
  function navigate(key: string, value: string) {
    setParams((previous) => {
      const next = new URLSearchParams(previous);
      next.set(key, value);
      return next;
    });
  }
  const edited = new Set(
    state?.logins
      .filter((item) => {
        const draft = drafts[item.credential_session_id];
        return draft && hasLoginChanges(draft, item, state);
      })
      .map((item) => item.credential_session_id),
  );

  return (
    <div className="client-disguise">
      <header className="cd-page-header">
        <div>
          <p className="cd-eyebrow">
            <Fingerprint size={15} aria-hidden="true" /> CLIENT IDENTITY
          </p>
          <h1>
            Client disguise<span>.</span>
          </h1>
          <p>
            A stable device and client profile for every credential login.
            Conversation IDs and cache keys stay unchanged.
          </p>
        </div>
        <Link className="cd-button cd-button-secondary" to="/providers">
          Manage providers <ArrowRight size={15} aria-hidden="true" />
        </Link>
      </header>
      <nav className="cd-navigation" aria-label="Client disguise sections">
        {SECTIONS.map(({ id, label, icon: Icon }) => (
          <button
            type="button"
            key={id}
            aria-current={section === id ? "page" : undefined}
            onClick={() => navigate("view", id)}
          >
            <Icon size={16} aria-hidden="true" />
            {label}
            {id === "references" && state && (
              <span className="cd-count">{state.references.length}</span>
            )}
          </button>
        ))}
      </nav>
      {error && (
        <div role="alert" className="cd-feedback cd-feedback-error">
          <span>{error}</span>
          {!state && (
            <button className="cd-button cd-button-secondary" onClick={retry}>
              Try again
            </button>
          )}
        </div>
      )}
      {notice && (
        <div role="status" className="cd-feedback cd-feedback-success">
          <CheckCircle2 size={17} aria-hidden="true" />
          {notice}
        </div>
      )}
      {!state ? (
        !error && (
          <div className="cd-loading" role="status">
            <span className="cd-loading-line" />
            Loading client disguise settings…
          </div>
        )
      ) : (
        <>
          <div hidden={section !== "profiles"}>
            <div className="cd-workspace">
              <LoginList
                logins={state.logins}
                selected={login?.credential_session_id}
                drafts={edited}
                select={(id) => navigate("login", id)}
              />
              {login ? (
                <LoginSettings
                  key={login.credential_session_id}
                  login={login}
                  state={state}
                  busy={busy}
                  draft={
                    drafts[login.credential_session_id] ??
                    createLoginDraft(login, state)
                  }
                  change={(draft) =>
                    setDrafts((previous) => ({
                      ...previous,
                      [login.credential_session_id]: draft,
                    }))
                  }
                  save={async (binding) => {
                    const id = login.credential_session_id;
                    const saved = await mutate(
                      () => api.clientDisguise.saveBinding(id, binding),
                      "Login profile saved. New requests and connections will use these settings.",
                    );
                    if (saved)
                      setDrafts((previous) => {
                        const next = { ...previous };
                        delete next[id];
                        return next;
                      });
                  }}
                />
              ) : (
                <div className="cd-empty">
                  <span className="cd-empty-icon">
                    <Fingerprint size={30} aria-hidden="true" />
                  </span>
                  <h2>Give each login its own client profile</h2>
                  <p>
                    Create a credential login before configuring its profile.
                  </p>
                  <Link
                    className="cd-button cd-button-primary"
                    to="/credentials"
                  >
                    Manage credentials{" "}
                    <ArrowRight size={16} aria-hidden="true" />
                  </Link>
                </div>
              )}
            </div>
            <p className="cd-page-note">
              Changes apply to new HTTP requests and WebSocket connections.
              In-flight requests keep their original profile.
            </p>
          </div>
          <div hidden={section !== "references"}>
            <ReferenceSettings state={state} busy={busy} mutate={mutate} />
          </div>
          <div hidden={section !== "clients"}>
            <ClientIdentitySettings state={state} busy={busy} mutate={mutate} />
          </div>
        </>
      )}
    </div>
  );
}
