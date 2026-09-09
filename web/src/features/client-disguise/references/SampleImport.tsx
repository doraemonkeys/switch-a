import { useState } from "react";
import { FileJson, Upload } from "lucide-react";
import { useApi } from "@/api/useApi";
import type {
  ClientSample,
  TransportSample,
} from "@/api/client-disguise/types";
import type { DisguiseMutation } from "../ReferenceSettings";

export function SampleImport({
  busy,
  mutate,
  profileCount,
  transportCount,
}: {
  busy: boolean;
  mutate: DisguiseMutation;
  profileCount: number;
  transportCount: number;
}) {
  const api = useApi();
  const [kind, setKind] = useState<"application" | "transport">("application");
  const [sample, setSample] = useState("");
  const [transport, setTransport] = useState("");
  const application = kind === "application";
  async function submit() {
    const imported = await mutate(
      () =>
        application
          ? api.clientDisguise.importSample(JSON.parse(sample) as ClientSample)
          : api.clientDisguise.importTransport(
              JSON.parse(transport) as TransportSample,
            ),
      `${application ? "Application" : "Transport"} sample imported.`,
    );
    if (imported) {
      if (application) setSample("");
      else setTransport("");
    }
  }
  return (
    <form
      className="cd-panel cd-import"
      onSubmit={(event) => {
        event.preventDefault();
        void submit();
      }}
    >
      <div className="cd-panel-heading">
        <FileJson size={17} aria-hidden="true" />
        <h3>Import an observation</h3>
      </div>
      <p className="cd-description">
        Add a captured sample to your profile library.
      </p>
      <fieldset disabled={busy}>
        <div className="cd-segmented" aria-label="Observation type">
          <button
            type="button"
            aria-pressed={application}
            onClick={() => setKind("application")}
          >
            Application <span>{profileCount}</span>
          </button>
          <button
            type="button"
            aria-pressed={!application}
            onClick={() => setKind("transport")}
          >
            Transport <span>{transportCount}</span>
          </button>
        </div>
        <label className="cd-field">
          {application
            ? "Application sample JSON"
            : "Independent transport sample JSON"}
          <textarea
            aria-label={
              application
                ? "Application sample JSON"
                : "Independent transport sample JSON"
            }
            rows={10}
            spellCheck={false}
            placeholder={
              application
                ? "Paste your captured application sample…"
                : "Paste your captured transport sample…"
            }
            value={application ? sample : transport}
            onChange={(event) =>
              application
                ? setSample(event.target.value)
                : setTransport(event.target.value)
            }
          />
        </label>
        <p className="cd-field-help">
          {application
            ? "Use the original collection time. The server assigns an observation ID when omitted. Device, session, thread and request IDs belong to the login, not shared samples."
            : "Include id, name, source_id, captured_at, tls_profile, http_profile and supported adapter config from an actual transport observation."}
        </p>
        {application && (
          <details className="cd-profile-details">
            <summary>Application sample format</summary>
            <p className="cd-field-help">
              Include <code>source_id</code>, <code>captured_at</code>,{" "}
              <code>client_version</code> and a <code>tuple</code> with{" "}
              <code>client_type</code>, <code>platform</code> and{" "}
              <code>arch</code>. Place observed <code>user_agent</code>,{" "}
              <code>originator</code>, <code>client_version</code>,
              <code>desktop_build</code> and <code>os_version</code> values in{" "}
              <code>features</code>.
            </p>
          </details>
        )}
        <button
          className="cd-button cd-button-primary"
          disabled={!(application ? sample : transport).trim()}
        >
          <Upload size={15} aria-hidden="true" />
          {application
            ? "Import application sample"
            : "Import transport sample"}
        </button>
      </fieldset>
    </form>
  );
}
