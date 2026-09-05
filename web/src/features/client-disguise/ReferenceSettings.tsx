import { useState } from "react";
import { useApi } from "@/api/useApi";
import type {
  ClientSample,
  DisguiseState,
  TransportSample,
} from "@/api/client-disguise/types";

export function ReferenceSettings({
  state,
  busy,
  mutate,
}: {
  state: DisguiseState;
  busy: boolean;
  mutate: (action: () => Promise<unknown>) => Promise<void>;
}) {
  const api = useApi();
  const [sourceID, setSourceID] = useState("");
  const [sourceName, setSourceName] = useState("");
  const [clientID, setClientID] = useState("");
  const [keyClientID, setKeyClientID] = useState("");
  const [key, setKey] = useState("");
  const [sample, setSample] = useState("");
  const [transport, setTransport] = useState("");
  function importJSON(kind: "application" | "transport") {
    return mutate(async () => {
      if (kind === "application")
        return api.clientDisguise.importSample(
          JSON.parse(sample) as ClientSample,
        );
      return api.clientDisguise.importTransport(
        JSON.parse(transport) as TransportSample,
      );
    });
  }
  return (
    <section className="space-y-5 rounded-xl border border-border bg-white p-5">
      <h2 className="text-lg font-semibold">
        Reference clients and observations
      </h2>
      <p className="text-sm text-text-muted">
        Only the selected reference client's matching type, platform and
        architecture advances an automatic binding. Older or duplicate samples
        do not downgrade it.
      </p>
      <div className="grid gap-3 md:grid-cols-3">
        <label>
          Source ID
          <input
            aria-label="Source ID"
            className="input mt-1 w-full"
            value={sourceID}
            onChange={(event) => setSourceID(event.target.value)}
          />
        </label>
        <label>
          Source name
          <input
            aria-label="Source name"
            className="input mt-1 w-full"
            value={sourceName}
            onChange={(event) => setSourceName(event.target.value)}
          />
        </label>
        <label>
          Reference client
          <select
            aria-label="Reference client"
            className="input mt-1 w-full"
            value={clientID}
            onChange={(event) => setClientID(event.target.value)}
          >
            <option value="">Select client identity</option>
            {state.clients.map((item) => (
              <option key={item.client_id}>{item.client_id}</option>
            ))}
          </select>
        </label>
      </div>
      <button
        className="btn btn-primary"
        disabled={busy || !sourceID || !sourceName || !clientID}
        onClick={() =>
          void mutate(() =>
            api.clientDisguise.saveReference({
              id: sourceID,
              name: sourceName,
              client_identity_id: clientID,
            }),
          )
        }
      >
        Save reference source
      </button>
      <label className="block">
        Application sample JSON
        <textarea
          aria-label="Application sample JSON"
          className="input mt-1 min-h-32 w-full font-mono"
          placeholder='{"source_id":"reference","captured_at":"2026-09-05T00:00:00Z","tuple":{"client_type":"desktop","platform":"windows","arch":"amd64"},"client_version":"...","features":{"user_agent":"...","originator":"...","client_version":"...","desktop_build":"...","os_version":"..."}}'
          value={sample}
          onChange={(event) => setSample(event.target.value)}
        />
      </label>
      <p className="text-sm text-text-muted">
        The server assigns an observation ID when omitted. Use the original
        collection time. Device, session, thread and request identifiers do not
        belong in shared samples.
      </p>
      <button
        className="btn btn-secondary"
        disabled={busy || !sample}
        onClick={() => void importJSON("application")}
      >
        Import application sample
      </button>
      <details className="space-y-3">
        <summary className="cursor-pointer font-medium">
          Advanced client identity and transport
        </summary>
        <h3 className="font-medium">Bind a replacement API key</h3>
        <p className="text-sm text-text-muted">
          A new key normally identifies a new client. Bind it to an existing
          client to retain device mappings, prior conversation ownership,
          recovery and sticky routing.
        </p>
        <label className="block">
          Existing client identity
          <select
            aria-label="Existing client identity"
            className="input mt-1 w-full"
            value={keyClientID}
            onChange={(event) => setKeyClientID(event.target.value)}
          >
            <option value="">Select client identity</option>
            {state.clients.map((item) => (
              <option key={item.client_id}>{item.client_id}</option>
            ))}
          </select>
        </label>
        <label className="block">
          Replacement API key
          <input
            type="password"
            aria-label="Replacement API key"
            autoComplete="off"
            className="input mt-1 w-full"
            value={key}
            onChange={(event) => setKey(event.target.value)}
          />
        </label>
        <button
          className="btn btn-secondary"
          disabled={busy || !key || !keyClientID}
          onClick={() =>
            void mutate(async () => {
              await api.clientDisguise.bindKey(key, keyClientID);
              setKey("");
            })
          }
        >
          Bind key to client
        </button>
        <label className="block">
          Independent transport sample JSON
          <textarea
            aria-label="Independent transport sample JSON"
            className="input mt-1 min-h-32 w-full font-mono"
            value={transport}
            onChange={(event) => setTransport(event.target.value)}
          />
        </label>
        <p className="text-sm text-text-muted">
          Include id, name, source_id, captured_at, tls_profile, http_profile
          and supported adapter config from an actual transport observation.
        </p>
        <button
          className="btn btn-secondary"
          disabled={busy || !transport}
          onClick={() => void importJSON("transport")}
        >
          Import transport sample
        </button>
      </details>
    </section>
  );
}
