import { useState } from "react";
import { useApi } from "@/api/useApi";
import type {
  ClientAPIKey,
  ClientAccessMode,
  ClientAPIKeysState,
} from "@/api/client-api-keys/types";
import { useQuery } from "@/hooks/useQuery";
import { ConfirmModal } from "@/components/ConfirmModal";
import { CopyButton } from "@/components/CopyButton";
import { ClientKeyForm } from "./ClientKeyForm";
import { ClientKeyRow } from "./ClientKeyRow";

function withoutKey(state: ClientAPIKeysState | null, id: string) {
  return (
    state && { ...state, keys: state.keys.filter((item) => item.id !== id) }
  );
}

export function ClientAPIKeysPage() {
  const api = useApi();
  const query = useQuery(() => api.clientApiKeys.get(), { queryKey: api });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [generated, setGenerated] = useState<ClientAPIKey | null>(null);
  const [deleting, setDeleting] = useState<ClientAPIKey | null>(null);

  const mutate = async (action: () => Promise<void>): Promise<boolean> => {
    setBusy(true);
    setError(null);
    try {
      await action();
      return true;
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Operation failed");
      return false;
    } finally {
      setBusy(false);
    }
  };
  const upsert = (item: ClientAPIKey) => {
    query.updateData(
      (state) =>
        state && {
          ...state,
          keys: state.keys.some((current) => current.id === item.id)
            ? state.keys.map((current) =>
                current.id === item.id ? item : current,
              )
            : [...state.keys, item],
        },
    );
  };
  const setPolicy = (mode: ClientAccessMode) =>
    mutate(async () => {
      const policy = await api.clientApiKeys.setPolicy(mode);
      query.updateData((state) => state && { ...state, ...policy });
    });
  const create = (name: string, key?: string) =>
    mutate(async () => {
      const item =
        key === undefined
          ? await api.clientApiKeys.generate(name)
          : await api.clientApiKeys.add(name, key);
      upsert(item);
      if (key === undefined) setGenerated(item);
    });
  const rename = (id: string, name: string) =>
    mutate(async () => {
      const item = await api.clientApiKeys.rename(id, name);
      upsert(item);
      setGenerated((current) => (current?.id === id ? item : current));
    });

  const remove = (id: string) =>
    mutate(async () => {
      await api.clientApiKeys.delete(id);
      query.updateData((state) => withoutKey(state, id));
      setGenerated((current) => (current?.id === id ? null : current));
      setDeleting(null);
    });

  return (
    <main className="mx-auto max-w-5xl space-y-6 text-text-primary">
      <header>
        <h1 className="text-2xl font-bold">Client API Keys</h1>
        <p className="mt-2 text-text-secondary">
          Control which client keys can access the gateway. These keys are
          separate from upstream credentials and the admin sign-in token.
        </p>
      </header>
      {!deleting && (error || query.error) && (
        <p role="alert" className="rounded-lg bg-red-50 p-3 text-red-700">
          {error || query.error?.message}
        </p>
      )}
      {!query.data && query.loading && <p>Loading client API keys…</p>}
      {!query.data && !query.loading && (
        <button className="btn-secondary" onClick={() => void query.refetch()}>
          Retry
        </button>
      )}
      {query.data && (
        <>
          <section className="rounded-xl border border-border-light bg-bg-secondary p-5">
            <h2 className="text-lg font-semibold">Gateway access</h2>
            <fieldset className="mt-3 space-y-3" disabled={busy}>
              <legend className="sr-only">Access policy</legend>
              <label className="flex items-start gap-3">
                <input
                  className="mt-1"
                  type="radio"
                  name="access-policy"
                  checked={query.data.mode === "permissive"}
                  onChange={() => void setPolicy("permissive")}
                />
                <span>
                  <strong>Allow any key</strong>
                  <span className="block text-sm text-text-secondary">
                    Default. Accepts any API key, including requests without a
                    key.
                  </span>
                </span>
              </label>
              <label className="flex items-start gap-3">
                <input
                  className="mt-1"
                  type="radio"
                  name="access-policy"
                  checked={query.data.mode === "restricted"}
                  onChange={() => void setPolicy("restricted")}
                />
                <span>
                  <strong>Configured keys only</strong>
                  <span className="block text-sm text-text-secondary">
                    Rejects requests with a missing or unlisted key.
                  </span>
                </span>
              </label>
            </fieldset>
            <p className="mt-4 text-sm text-text-muted">
              Changes apply to new HTTP requests and WebSocket connections.
              Existing streams stay open.
            </p>
            {query.data.mode === "restricted" &&
              query.data.keys.length === 0 && (
                <p role="status" className="mt-3 text-amber-700">
                  No keys are configured. All new client requests are blocked
                  until you add a key or allow any key.
                </p>
              )}
          </section>
          <ClientKeyForm busy={busy} create={create} />
          {generated && (
            <section
              className="rounded-xl border border-primary p-5"
              aria-label="Generated key"
            >
              <h2 className="font-semibold">Generated key: {generated.name}</h2>
              <p className="mt-1 text-sm text-text-secondary">
                Copy this key into your client. You can reveal and copy it again
                from the list.
              </p>
              <code className="my-3 block break-all">{generated.key}</code>
              <div className="flex gap-4">
                <CopyButton text={generated.key} />
                <button
                  type="button"
                  className="text-sm text-primary"
                  onClick={() => setGenerated(null)}
                >
                  Dismiss
                </button>
              </div>
            </section>
          )}
          <section aria-label="Configured client keys">
            <h2 className="mb-3 text-lg font-semibold">
              Configured keys ({query.data.keys.length})
            </h2>
            {query.data.keys.length === 0 ? (
              <p className="text-text-secondary">
                No client keys yet. Add an existing key or generate one above.
              </p>
            ) : (
              <ul className="space-y-3">
                {query.data.keys.map((item) => (
                  <ClientKeyRow
                    key={item.id}
                    item={item}
                    busy={busy}
                    rename={rename}
                    remove={(item) => {
                      setError(null);
                      setDeleting(item);
                    }}
                  />
                ))}
              </ul>
            )}
          </section>
        </>
      )}
      <ConfirmModal
        isOpen={deleting !== null}
        onClose={() => setDeleting(null)}
        title="Delete client API key"
        message={
          'Delete "' +
          (deleting?.name ?? "") +
          '"? When access is restricted, this key will no longer authorize new requests.'
        }
        confirmText="Delete key"
        variant="danger"
        loading={busy}
        error={error}
        onConfirm={() => {
          if (deleting) void remove(deleting.id);
        }}
      />
    </main>
  );
}
