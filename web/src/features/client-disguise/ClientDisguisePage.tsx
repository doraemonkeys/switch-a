import { useEffect, useState } from "react";
import { useSearchParams } from "react-router";
import { useApi } from "@/api/useApi";
import type { DisguiseState } from "@/api/client-disguise/types";
import { LoginSettings } from "./LoginSettings";
import { ReferenceSettings } from "./ReferenceSettings";

export function ClientDisguisePage() {
  const api = useApi();
  const [params] = useSearchParams();
  const [state, setState] = useState<DisguiseState | null>(null);
  const [selected, setSelected] = useState(params.get("login") ?? "");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    let active = true;
    api.clientDisguise
      .get()
      .then((value) => {
        if (active) setState(value);
      })
      .catch((reason) => {
        if (active) setError(String(reason));
      });
    return () => {
      active = false;
    };
  }, [api]);
  async function mutate(action: () => Promise<unknown>) {
    setBusy(true);
    setError("");
    try {
      await action();
      setState(await api.clientDisguise.get());
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason));
    } finally {
      setBusy(false);
    }
  }
  const login =
    state?.logins.find((item) => item.credential_session_id === selected) ??
    state?.logins[0];
  return (
    <main className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold text-text-primary">
          Client disguise
        </h1>
        <p className="mt-2 text-text-secondary">
          Each credential login owns a stable device identity and profile.
          Providers choose independently whether to apply them.
        </p>
        <p className="mt-1 text-sm text-text-muted">
          Changes apply to new HTTP requests and WebSocket connections. Internal
          retries retain their original profile. Platform exclusions skip a
          candidate; conversion failures terminate the request with a diagnostic
          ID.
        </p>
      </div>
      {error && (
        <p role="alert" className="rounded-lg bg-red-50 p-3 text-red-700">
          {error}
        </p>
      )}
      {!state ? (
        <p>Loading client disguise settings…</p>
      ) : (
        <>
          <label className="block">
            Credential login
            <select
              className="input mt-1 w-full"
              value={login?.credential_session_id ?? ""}
              onChange={(event) => setSelected(event.target.value)}
            >
              {state.logins.map((item) => (
                <option
                  key={item.credential_session_id}
                  value={item.credential_session_id}
                >
                  {item.name} — {item.credential_session_id}
                </option>
              ))}
            </select>
          </label>
          {login ? (
            <LoginSettings
              key={
                login.credential_session_id + (login.binding?.updated_at ?? "")
              }
              login={login}
              state={state}
              busy={busy}
              save={(binding) =>
                mutate(() =>
                  api.clientDisguise.saveBinding(
                    login.credential_session_id,
                    binding,
                  ),
                )
              }
            />
          ) : (
            <p>Create a credential login before configuring its profile.</p>
          )}
          <ReferenceSettings state={state} busy={busy} mutate={mutate} />
        </>
      )}
    </main>
  );
}
