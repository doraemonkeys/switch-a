import { useState } from "react";
import { ChevronRight, Search, UserRound } from "lucide-react";
import type { LoginView } from "@/api/client-disguise/types";

export function LoginList({
  logins,
  selected,
  select,
  drafts,
}: {
  logins: LoginView[];
  selected?: string;
  select: (id: string) => void;
  drafts: Set<string>;
}) {
  const [query, setQuery] = useState("");
  const search = query.trim().toLowerCase();
  const visible = logins.filter((login) =>
    [
      login.name,
      login.credential_session_id,
      ...login.providers.map((provider) => provider.provider_name),
    ].some((value) => value.toLowerCase().includes(search)),
  );
  return (
    <aside className="cd-login-list" aria-label="Credential logins">
      <div className="cd-list-heading">
        <h2>Credential logins</h2>
        <span className="cd-count">{logins.length}</span>
      </div>
      <label className="cd-search">
        <Search size={16} aria-hidden="true" />
        <input
          aria-label="Search credential logins"
          placeholder="Find a login or provider…"
          value={query}
          onChange={(event) => setQuery(event.target.value)}
        />
      </label>
      <div className="cd-login-items">
        {visible.map((login) => {
          const configured = Boolean(login.binding);
          const updateMode =
            login.binding?.mode === "auto" ? "Automatic" : "Pinned revision";
          return (
            <button
              type="button"
              key={login.credential_session_id}
              className="cd-login-item"
              aria-pressed={selected === login.credential_session_id}
              onClick={() => select(login.credential_session_id)}
            >
              <span className="cd-login-avatar">
                <UserRound size={17} aria-hidden="true" />
              </span>
              <span className="cd-login-copy">
                <strong>{login.name}</strong>
                <span>
                  {login.binding
                    ? [
                        login.binding.tuple.platform,
                        login.binding.tuple.arch,
                      ].join(" / ")
                    : "No profile selected"}
                </span>
                <span
                  className={
                    configured
                      ? "cd-inline-status"
                      : "cd-inline-status cd-muted-status"
                  }
                >
                  <i aria-hidden="true" />
                  {configured ? updateMode : "Not configured"}
                  {drafts.has(login.credential_session_id) && (
                    <em> · Edited</em>
                  )}
                </span>
              </span>
              <ChevronRight
                className="cd-list-chevron"
                size={15}
                aria-hidden="true"
              />
            </button>
          );
        })}
        {visible.length === 0 && (
          <p className="cd-list-empty">
            {logins.length
              ? "No matching logins. Try a name or provider."
              : "Your credential logins will appear here."}
          </p>
        )}
      </div>
      <p className="cd-list-footnote">
        One device identity per login.
        <br />
        Shared across its providers.
      </p>
    </aside>
  );
}
