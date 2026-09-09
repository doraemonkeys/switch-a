import { useState } from "react";
import { Library, Plus, Radio, X } from "lucide-react";
import { useApi } from "@/api/useApi";
import type {
  DisguiseState,
  ReferenceSource,
} from "@/api/client-disguise/types";
import { SampleImport } from "./references/SampleImport";

export type DisguiseMutation = (
  action: () => Promise<unknown>,
  message?: string,
) => Promise<boolean>;
const EMPTY_SOURCE: ReferenceSource = {
  id: "",
  name: "",
  client_identity_id: "",
};

export function ReferenceSettings({
  state,
  busy,
  mutate,
}: {
  state: DisguiseState;
  busy: boolean;
  mutate: DisguiseMutation;
}) {
  const api = useApi();
  const [source, setSource] = useState<ReferenceSource | null>(null);
  const [editing, setEditing] = useState(false);
  async function save() {
    if (!source) return;
    if (
      await mutate(
        () => api.clientDisguise.saveReference(source),
        "Reference source saved.",
      )
    )
      setSource(null);
  }
  return (
    <section className="cd-library">
      <header className="cd-section-intro">
        <div>
          <h2>Reference library</h2>
          <p>Manage the clients and observations your login profiles follow.</p>
        </div>
        <button
          type="button"
          className="cd-button cd-button-primary"
          disabled={busy}
          onClick={() => {
            setSource({ ...EMPTY_SOURCE });
            setEditing(false);
          }}
        >
          <Plus size={16} aria-hidden="true" />
          Add reference
        </button>
      </header>
      <div className="cd-library-grid">
        <div className="cd-panel">
          <div className="cd-panel-heading">
            <Library size={17} aria-hidden="true" />
            <h3>Reference sources</h3>
            <span className="cd-count">{state.references.length}</span>
          </div>
          {state.references.length ? (
            <div className="cd-source-list">
              {state.references.map((item) => (
                <button
                  type="button"
                  key={item.id}
                  className="cd-source-item"
                  disabled={busy}
                  onClick={() => {
                    setSource({ ...item });
                    setEditing(true);
                  }}
                >
                  <span className="cd-source-icon">
                    <Radio size={19} aria-hidden="true" />
                  </span>
                  <span>
                    <strong>{item.name}</strong>
                    <small>{item.id}</small>
                  </span>
                  <span className="cd-source-edit">Edit</span>
                </button>
              ))}
            </div>
          ) : (
            <div className="cd-panel-empty">
              <Radio size={28} aria-hidden="true" />
              <h3>No reference sources yet</h3>
              <p>
                Add a reference client to learn from its observations, or use a
                built-in profile.
              </p>
            </div>
          )}
          {source && (
            <form
              className="cd-source-form"
              onSubmit={(event) => {
                event.preventDefault();
                void save();
              }}
            >
              <div className="cd-panel-heading">
                <h3>
                  {editing ? "Edit reference source" : "New reference source"}
                </h3>
                <button
                  type="button"
                  className="cd-icon-button"
                  aria-label="Close reference editor"
                  onClick={() => setSource(null)}
                >
                  <X size={16} />
                </button>
              </div>
              <fieldset disabled={busy}>
                <label className="cd-field">
                  Source name
                  <input
                    required
                    value={source.name}
                    placeholder="e.g. Office desktop"
                    onChange={(event) =>
                      setSource({ ...source, name: event.target.value })
                    }
                  />
                </label>
                <label className="cd-field">
                  Source ID
                  <input
                    required
                    value={source.id}
                    readOnly={editing}
                    placeholder="e.g. office-desktop"
                    onChange={(event) =>
                      setSource({ ...source, id: event.target.value })
                    }
                  />
                  <span className="cd-field-help">
                    The stable identifier used by imported samples.
                  </span>
                </label>
                <label className="cd-field">
                  Reference client
                  <select
                    required
                    value={source.client_identity_id}
                    onChange={(event) =>
                      setSource({
                        ...source,
                        client_identity_id: event.target.value,
                      })
                    }
                  >
                    <option value="">Select client identity</option>
                    {state.clients.map((item) => (
                      <option key={item.client_id}>{item.client_id}</option>
                    ))}
                  </select>
                </label>
                {state.clients.length === 0 && (
                  <p className="cd-field-help">
                    Client identities appear after a client sends a request
                    through the gateway.
                  </p>
                )}
                <button
                  className="cd-button cd-button-primary"
                  disabled={
                    !source.id.trim() ||
                    !source.name.trim() ||
                    !source.client_identity_id
                  }
                >
                  Save reference source
                </button>
              </fieldset>
            </form>
          )}
          <p className="cd-panel-note">
            Automatic follow accepts newer observations for the matching client
            type, platform and architecture. Older or duplicate samples never
            downgrade a profile.
          </p>
        </div>
        <SampleImport
          busy={busy}
          mutate={mutate}
          profileCount={state.profiles.length}
          transportCount={state.transport_samples.length}
        />
      </div>
    </section>
  );
}
