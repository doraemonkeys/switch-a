import { useState } from "react";
import {
  Check,
  CheckCircle2,
  KeyRound,
  LoaderCircle,
  Plus,
  RefreshCw,
} from "lucide-react";
import type { ClientAPIKey } from "@/api/client-api-keys/types";
import { CopyButton } from "@/components/CopyButton";
import { ClientKeyDialog } from "./ClientKeyDialog";
import { ClientKeyForm } from "./ClientKeyForm";
import { ClientKeyList } from "./ClientKeyList";
import { GatewayAccess } from "./GatewayAccess";
import { useClientAPIKeys } from "./useClientAPIKeys";
import "./client-api-keys.css";

type KeyDialog =
  | { kind: "create" }
  | { kind: "created"; item: ClientAPIKey }
  | { kind: "delete"; item: ClientAPIKey };

export function ClientAPIKeysPage() {
  const { query, busy, error, clearError, setPolicy, create, rename, remove } =
    useClientAPIKeys();
  const [dialog, setDialog] = useState<KeyDialog | null>(null);
  const openDialog = (next: KeyDialog) => {
    clearError();
    setDialog(next);
  };
  const closeDialog = () => {
    clearError();
    setDialog(null);
  };

  return (
    <div className="client-keys-page mx-auto w-full max-w-7xl space-y-7 py-2 text-slate-900">
      <header className="flex flex-wrap items-center justify-between gap-5">
        <div>
          <p className="key-eyebrow flex items-center gap-2">
            <KeyRound size={14} aria-hidden="true" /> CLIENT ACCESS
          </p>
          <h1 className="mt-3 text-3xl font-semibold tracking-tight">
            API keys
          </h1>
          <p className="mt-2 text-sm leading-6 text-slate-500">
            Manage the keys your apps and devices use to connect to Switch-A.
          </p>
        </div>
        <button
          type="button"
          className="key-button-primary"
          disabled={busy || !query.data}
          onClick={() => openDialog({ kind: "create" })}
        >
          <Plus size={17} aria-hidden="true" /> Create key
        </button>
      </header>
      {!dialog && (error || query.error) && (
        <p role="alert" className="key-error">
          {error || query.error?.message}
        </p>
      )}
      {!query.data && (
        <div className="flex min-h-80 items-center justify-center gap-3 rounded-2xl border border-slate-200 bg-white text-sm text-slate-500">
          {query.loading ? (
            <>
              <LoaderCircle
                size={20}
                className="animate-spin"
                aria-hidden="true"
              />
              <p>Loading client API keys…</p>
            </>
          ) : (
            <button
              type="button"
              className="key-button-secondary"
              onClick={() => void query.refetch()}
            >
              <RefreshCw size={16} aria-hidden="true" /> Retry
            </button>
          )}
        </div>
      )}
      {query.data && (
        <div className="grid items-start gap-6 xl:grid-cols-[minmax(0,1fr)_320px]">
          <div className="min-w-0 space-y-5">
            <ClientKeyList
              keys={query.data.keys}
              busy={busy}
              onCreate={() => openDialog({ kind: "create" })}
              rename={rename}
              remove={(item) => openDialog({ kind: "delete", item })}
            />
            <p className="flex items-start gap-2 px-1 text-xs leading-5 text-slate-500">
              <KeyRound
                size={14}
                className="mt-0.5 shrink-0"
                aria-hidden="true"
              />
              Client keys are separate from upstream credentials and your admin
              sign-in token.
            </p>
          </div>
          <GatewayAccess
            mode={query.data.mode}
            keyCount={query.data.keys.length}
            busy={busy}
            setPolicy={(mode) => void setPolicy(mode)}
          />
        </div>
      )}
      {dialog?.kind === "create" && (
        <ClientKeyDialog
          title="Create a client key"
          description="Connect an app or device with a new or existing API key."
          busy={busy}
          onClose={closeDialog}
        >
          <ClientKeyForm
            busy={busy}
            error={error}
            create={create}
            onCreated={(item) => setDialog({ kind: "created", item })}
            onCancel={closeDialog}
          />
        </ClientKeyDialog>
      )}
      {dialog?.kind === "created" && (
        <ClientKeyDialog
          title="Your key is ready"
          description="Copy this key into your client. You can reveal and copy it again from the list."
          busy={false}
          onClose={closeDialog}
        >
          <section aria-label="Created key" className="space-y-5 p-6">
            <div className="flex items-center gap-3">
              <span className="flex size-10 shrink-0 items-center justify-center rounded-full bg-emerald-50 text-emerald-700">
                <CheckCircle2 size={20} aria-hidden="true" />
              </span>
              <h3 className="min-w-0 break-words font-medium">
                {dialog.item.name}
              </h3>
            </div>
            <div className="rounded-xl border border-slate-200 bg-slate-50 p-4">
              <code className="block break-all font-mono text-sm leading-6">
                {dialog.item.key}
              </code>
              <CopyButton
                text={dialog.item.key}
                className="key-copy-button mt-3"
              />
            </div>
            {query.data?.mode === "permissive" && (
              <p className="text-xs leading-5 text-slate-500">
                The gateway still allows any key. Select “Configured keys only”
                to limit access to your list.
              </p>
            )}
          </section>
          <footer className="flex justify-end border-t border-slate-100 px-6 py-4">
            <button
              type="button"
              className="key-button-primary"
              onClick={closeDialog}
            >
              <Check size={16} aria-hidden="true" /> Done
            </button>
          </footer>
        </ClientKeyDialog>
      )}
      {dialog?.kind === "delete" && (
        <ClientKeyDialog
          title="Delete client API key"
          description={
            'Delete "' +
            dialog.item.name +
            '"? When access is restricted, this key will no longer authorize new requests.'
          }
          busy={busy}
          onClose={closeDialog}
        >
          {error && (
            <p role="alert" className="key-error mx-6 mt-5">
              {error}
            </p>
          )}
          <footer className="flex justify-end gap-3 px-6 py-5">
            <button
              type="button"
              className="key-button-secondary"
              disabled={busy}
              onClick={closeDialog}
            >
              Cancel
            </button>
            <button
              type="button"
              className="key-button-danger"
              disabled={busy}
              onClick={() => {
                void remove(dialog.item.id).then((removed) => {
                  if (removed) closeDialog();
                });
              }}
            >
              {busy ? "Deleting…" : "Delete key"}
            </button>
          </footer>
        </ClientKeyDialog>
      )}
    </div>
  );
}
