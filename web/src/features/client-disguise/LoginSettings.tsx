import { useState } from "react";
import { Link } from "react-router";
import { ProfileSummary } from "./ProfileSummary";
import type {
  DisguiseState,
  LoginView,
  ProfileBinding,
} from "@/api/client-disguise/types";

export function LoginSettings({
  login,
  state,
  busy,
  save,
}: {
  login: LoginView;
  state: DisguiseState;
  busy: boolean;
  save: (binding: ProfileBinding) => Promise<void>;
}) {
  const initial = login.binding;
  const [revisionID, setRevisionID] = useState(initial?.revision_id ?? "");
  const [mode, setMode] = useState<ProfileBinding["mode"]>(
    initial?.mode ?? "auto",
  );
  const [reference, setReference] = useState(
    initial?.reference_source_id ?? "",
  );
  const [transport, setTransport] = useState(
    initial?.transport_sample_id ?? "",
  );
  const [cacheKeys, setCacheKeys] = useState(
    initial?.remap_cache_keys ?? false,
  );
  const [paths, setPaths] = useState(
    JSON.stringify(initial?.telemetry_path_mappings ?? {}, null, 2),
  );
  const [error, setError] = useState("");
  const profile = state.profiles.find((item) => item.id === revisionID);
  const [version, setVersion] = useState(profile?.client_version ?? "");
  const versions = [
    ...new Set(state.profiles.map((item) => item.client_version)),
  ];
  const revisions = state.profiles.filter(
    (item) => !version || item.client_version === version,
  );
  async function submit() {
    setError("");
    if (!profile) {
      setError("Select a profile revision.");
      return;
    }
    try {
      const mappings: unknown = JSON.parse(paths);
      if (
        !mappings ||
        Array.isArray(mappings) ||
        typeof mappings !== "object" ||
        Object.values(mappings).some((value) => typeof value !== "string")
      )
        throw new Error(
          "Telemetry mappings must be an object of path strings.",
        );
      await save({
        credential_session_id: login.credential_session_id,
        tuple: profile.tuple,
        revision_id: revisionID,
        mode,
        reference_source_id: reference,
        transport_sample_id: transport,
        remap_cache_keys: cacheKeys,
        telemetry_path_mappings: mappings as Record<string, string>,
      });
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason));
    }
  }
  return (
    <section className="space-y-4 rounded-xl border border-border bg-white p-5">
      <h2 className="text-lg font-semibold">Login identity and profile</h2>
      <dl className="grid gap-2 text-sm">
        <dt>Device identity</dt>
        <dd className="break-all font-mono">
          {login.identity?.device_id ??
            "Unbound — created atomically before the first eligible send"}
        </dd>
        <dt>Identity generation</dt>
        <dd className="break-all font-mono">
          {login.identity?.generation_id ?? "Not yet created"}
        </dd>
      </dl>
      <p className="text-sm">
        Providers sharing this login:{" "}
        {login.providers.length
          ? login.providers.map((provider, index) => (
              <span key={provider.provider_id}>
                {index > 0 && ", "}
                <Link className="text-primary underline" to="/providers">
                  {provider.provider_name}
                </Link>{" "}
                ({provider.client_disguise.enabled ? "enabled" : "disabled"})
              </span>
            ))
          : "None"}
      </p>
      <div className="grid gap-4 md:grid-cols-2">
        <label>
          Client version
          <select
            aria-label="Client version"
            className="input mt-1 w-full"
            value={version}
            onChange={(event) => {
              setVersion(event.target.value);
              setRevisionID("");
            }}
          >
            <option value="">All versions</option>
            {versions.map((item) => (
              <option key={item}>{item}</option>
            ))}
          </select>
        </label>
        <label>
          Profile revision
          <select
            aria-label="Profile revision"
            className="input mt-1 w-full"
            value={revisionID}
            onChange={(event) => {
              setRevisionID(event.target.value);
              setMode("pinned");
            }}
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
        <label>
          Update mode
          <select
            aria-label="Update mode"
            className="input mt-1 w-full"
            value={mode}
            onChange={(event) =>
              setMode(event.target.value as ProfileBinding["mode"])
            }
          >
            <option value="auto">Automatically follow reference</option>
            <option value="pinned">Pin selected revision</option>
          </select>
        </label>
        <label>
          Reference source
          <select
            aria-label="Reference source"
            className="input mt-1 w-full"
            value={reference}
            onChange={(event) => setReference(event.target.value)}
          >
            <option value="">Built-in profile</option>
            {state.references.map((item) => (
              <option key={item.id} value={item.id}>
                {item.name}
              </option>
            ))}
          </select>
        </label>
      </div>
      <p className="text-sm text-text-muted">
        Selecting an older revision pins it. Resume automatic follow explicitly
        to receive later samples. Client type, platform and architecture follow
        the selected revision.
      </p>
      {profile && <ProfileSummary profile={profile} />}
      <details className="space-y-3">
        <summary className="cursor-pointer font-medium">
          Advanced login settings
        </summary>
        <label className="flex gap-2">
          <input
            type="checkbox"
            checked={cacheKeys}
            onChange={(event) => setCacheKeys(event.target.checked)}
          />
          Stably remap prompt cache keys
        </label>
        <p className="text-sm text-text-muted">
          Preserves original cache grouping. Disabled keeps original cache keys.
        </p>
        <label className="block">
          Telemetry path mappings (JSON)
          <textarea
            aria-label="Telemetry path mappings"
            className="input mt-1 min-h-24 w-full font-mono"
            value={paths}
            onChange={(event) => setPaths(event.target.value)}
          />
        </label>
        <p className="text-sm text-text-muted">
          Applies only to recognized telemetry metadata. Workspace paths and
          request content stay intact.
        </p>
        <label className="block">
          Transport sample
          <select
            aria-label="Transport sample"
            className="input mt-1 w-full"
            value={transport}
            onChange={(event) => setTransport(event.target.value)}
          >
            <option value="">Default transport</option>
            {state.transport_samples.map((item) => (
              <option key={item.id} value={item.id}>
                {item.name} · {item.captured_at}
              </option>
            ))}
          </select>
        </label>
        <p className="text-sm text-text-muted">
          TLS and HTTP features use the selected independent transport
          observation. No Cookie or Accept-Encoding changes are implied by
          enabling disguise.
        </p>
      </details>
      {error && (
        <p role="alert" className="text-red-700">
          {error}
        </p>
      )}
      <button
        className="btn btn-primary"
        disabled={busy || !profile}
        onClick={() => void submit()}
      >
        Save login settings
      </button>
    </section>
  );
}
