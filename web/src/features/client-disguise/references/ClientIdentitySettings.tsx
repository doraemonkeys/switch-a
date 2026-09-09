import { useState } from "react";
import { Fingerprint, KeyRound, Link2 } from "lucide-react";
import { useApi } from "@/api/useApi";
import type { DisguiseState } from "@/api/client-disguise/types";
import type { DisguiseMutation } from "../ReferenceSettings";

export function ClientIdentitySettings({
  state,
  busy,
  mutate,
}: {
  state: DisguiseState;
  busy: boolean;
  mutate: DisguiseMutation;
}) {
  const api = useApi();
  const [clientID, setClientID] = useState("");
  const [key, setKey] = useState("");
  async function submit() {
    if (
      await mutate(
        () => api.clientDisguise.bindKey(key, clientID),
        "Replacement key bound to the existing client identity.",
      )
    )
      setKey("");
  }
  return (
    <section className="cd-library">
      <header className="cd-section-intro">
        <div>
          <h2>Client identities</h2>
          <p>
            Keep the same downstream client identity when replacing its API key.
          </p>
        </div>
      </header>
      <div className="cd-library-grid">
        <div className="cd-panel">
          <div className="cd-panel-heading">
            <Fingerprint size={18} aria-hidden="true" />
            <h3>Known clients</h3>
            <span className="cd-count">{state.clients.length}</span>
          </div>
          {state.clients.length ? (
            <ul className="cd-client-list">
              {state.clients.map((client) => (
                <li key={client.client_id}>
                  <Fingerprint size={16} aria-hidden="true" />
                  <code>{client.client_id}</code>
                </li>
              ))}
            </ul>
          ) : (
            <div className="cd-panel-empty">
              <Fingerprint size={28} aria-hidden="true" />
              <h3>No client identities yet</h3>
              <p>
                Identities appear after a client sends a request through the
                gateway.
              </p>
            </div>
          )}
          <p className="cd-panel-note">
            These identities represent downstream clients. Credential logins
            represent the upstream accounts they use.
          </p>
        </div>
        <form
          className="cd-panel"
          onSubmit={(event) => {
            event.preventDefault();
            void submit();
          }}
        >
          <div className="cd-panel-heading">
            <KeyRound size={17} aria-hidden="true" />
            <h3>Bind a replacement API key</h3>
          </div>
          <p className="cd-description">
            Retain device mappings, conversation ownership, recovery and sticky
            routing.
          </p>
          <fieldset disabled={busy}>
            <label className="cd-field">
              Existing client identity
              <select
                required
                value={clientID}
                onChange={(event) => setClientID(event.target.value)}
              >
                <option value="">Select client identity</option>
                {state.clients.map((item) => (
                  <option key={item.client_id}>{item.client_id}</option>
                ))}
              </select>
            </label>
            <label className="cd-field">
              Replacement API key
              <input
                required
                type="password"
                autoComplete="off"
                placeholder="Enter the new client API key"
                value={key}
                onChange={(event) => setKey(event.target.value)}
              />
            </label>
            <p className="cd-field-help">
              A new key normally identifies a new client. Bind it here to
              continue using an existing identity.
            </p>
            <button
              className="cd-button cd-button-primary"
              disabled={!key.trim() || !clientID}
            >
              <Link2 size={16} aria-hidden="true" />
              Bind key to client
            </button>
          </fieldset>
        </form>
      </div>
    </section>
  );
}
