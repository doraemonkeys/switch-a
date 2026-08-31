import { useState } from "react";
import { KeyRound, Pencil, RefreshCw, RotateCcw, Trash2 } from "lucide-react";
import type { CredentialSession } from "../../api";
import { ConfirmModal, CopyButton } from "../../components";
import { useCredentialSessions } from "../../hooks/useCredentialSessions";
import { useToast } from "../../hooks/useToast";
import { CredentialSessionReauthenticationModal } from "./CredentialSessionReauthenticationModal";

type EditorState =
  | { kind: "rename"; session: CredentialSession; value: string }
  | { kind: "rotate"; session: CredentialSession; value: string }
  | null;

export function CredentialSessions() {
  const {
    credentialSessions,
    loading,
    error,
    refetch,
    renameCredentialSession,
    updateCredentialSession,
    deleteCredentialSession,
  } = useCredentialSessions();
  const toast = useToast();
  const [editor, setEditor] = useState<EditorState>(null);
  const [deleteTarget, setDeleteTarget] = useState<CredentialSession | null>(
    null,
  );
  const [reauthenticationTarget, setReauthenticationTarget] =
    useState<CredentialSession | null>(null);
  const [busy, setBusy] = useState(false);

  const saveEditor = async () => {
    if (!editor || !editor.value.trim()) return;
    setBusy(true);
    try {
      if (editor.kind === "rename") {
        await renameCredentialSession(editor.session.id, {
          expected_version: editor.session.version,
          name: editor.value.trim(),
        });
        toast.success("Credential renamed");
      } else {
        await updateCredentialSession(editor.session.id, {
          expected_version: editor.session.version,
          secret_data: editor.value.trim(),
        });
        toast.success("API key rotated for every referenced route");
      }
      setEditor(null);
    } catch (cause) {
      toast.error(
        cause instanceof Error ? cause.message : "Credential update failed",
      );
    } finally {
      setBusy(false);
    }
  };

  const confirmDelete = async () => {
    if (!deleteTarget) return;
    setBusy(true);
    try {
      await deleteCredentialSession(deleteTarget.id);
      toast.success(`Credential "${deleteTarget.name}" deleted`);
      setDeleteTarget(null);
    } catch (cause) {
      toast.error(
        cause instanceof Error ? cause.message : "Credential deletion failed",
      );
    } finally {
      setBusy(false);
    }
  };

  const completeReauthentication = async (session: CredentialSession) => {
    await refetch();
    toast.success(`Credential "${session.name}" reconnected`);
    setReauthenticationTarget(null);
  };

  return (
    <div className="space-y-5">
      <header className="flex flex-col gap-4 rounded-2xl border border-border bg-white p-6 shadow-sm sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h2 className="text-2xl font-bold tracking-tight text-text-primary">
            Credentials
          </h2>
          <p className="mt-1.5 text-sm text-text-secondary">
            Name, rotate, and retire reusable provider credentials.
          </p>
        </div>
        <button
          type="button"
          className="btn btn-secondary h-10 px-4"
          disabled={loading}
          onClick={() => void refetch()}
        >
          <RefreshCw className={`h-4 w-4 ${loading ? "animate-spin" : ""}`} />
          Refresh
        </button>
      </header>

      {error && (
        <div
          role="alert"
          className="rounded-xl border border-danger/20 bg-danger/5 p-4 text-sm text-danger"
        >
          {error.message}
        </div>
      )}

      <div className="overflow-hidden rounded-2xl border border-border bg-white shadow-sm">
        <div className="overflow-x-auto">
          <table className="w-full min-w-[900px]">
            <thead className="border-b border-border bg-bg-secondary text-left text-[11px] font-semibold uppercase tracking-wider text-text-secondary">
              <tr>
                <th className="px-4 py-3.5">Credential</th>
                <th className="px-4 py-3.5">References</th>
                <th className="px-4 py-3.5">Status</th>
                <th className="px-4 py-3.5">Updated</th>
                <th className="px-4 py-3.5 text-right">Actions</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border/60">
              {credentialSessions.map((session) => (
                <CredentialSessionRow
                  key={session.id}
                  session={session}
                  disabled={busy}
                  onReconnect={() => setReauthenticationTarget(session)}
                  onRename={() =>
                    setEditor({ kind: "rename", session, value: session.name })
                  }
                  onRotate={() =>
                    setEditor({ kind: "rotate", session, value: "" })
                  }
                  onDelete={() => setDeleteTarget(session)}
                />
              ))}
              {!loading && credentialSessions.length === 0 && (
                <tr>
                  <td
                    colSpan={5}
                    className="px-6 py-16 text-center text-sm text-text-muted"
                  >
                    No credentials yet. Add a provider to create one.
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>

      {editor && (
        <CredentialEditor
          editor={editor}
          busy={busy}
          onChange={(value) => setEditor({ ...editor, value })}
          onCancel={() => setEditor(null)}
          onSave={() => void saveEditor()}
        />
      )}

      {reauthenticationTarget && (
        <CredentialSessionReauthenticationModal
          session={reauthenticationTarget}
          onClose={() => setReauthenticationTarget(null)}
          onReauthenticated={(session) =>
            void completeReauthentication(session)
          }
        />
      )}

      <ConfirmModal
        isOpen={deleteTarget !== null}
        onClose={() => setDeleteTarget(null)}
        onConfirm={() => void confirmDelete()}
        title="Delete Credential"
        message={`Delete "${deleteTarget?.name ?? ""}"? Only credentials with no route references can be deleted.`}
        confirmText="Delete"
        variant="danger"
        loading={busy}
      />
    </div>
  );
}

function CredentialSessionRow({
  session,
  disabled,
  onReconnect,
  onRename,
  onRotate,
  onDelete,
}: {
  session: CredentialSession;
  disabled: boolean;
  onReconnect: () => void;
  onRename: () => void;
  onRotate: () => void;
  onDelete: () => void;
}) {
  const references = session.route_references;
  return (
    <tr>
      <td className="px-4 py-4 align-top">
        <div className="flex items-start gap-3">
          <span className="rounded-lg bg-primary-light p-2 text-primary">
            <KeyRound className="h-4 w-4" />
          </span>
          <div className="min-w-0">
            <p className="font-medium text-text-primary">{session.name}</p>
            <p className="mt-0.5 font-mono text-xs text-text-muted">
              {session.kind === "api_key" ? "API Key" : "GPT Login"} ·{" "}
              {session.id}
            </p>
            {session.secret_data && (
              <div className="mt-2 flex max-w-sm items-center gap-2">
                <input
                  className="input h-8 font-mono text-xs"
                  type="password"
                  readOnly
                  value={session.secret_data}
                  aria-label={`API key for ${session.name}`}
                />
                <CopyButton
                  text={session.secret_data}
                  className="h-8 shrink-0 rounded-lg border border-border px-2"
                />
              </div>
            )}
          </div>
        </div>
      </td>
      <td className="px-4 py-4 align-top">
        {references.length === 0 ? (
          <span className="rounded-full bg-warning-light px-2.5 py-1 text-xs font-medium text-amber-700">
            Unused
          </span>
        ) : (
          <div className="flex max-w-md flex-wrap gap-1.5">
            {references.map((reference) => (
              <span
                key={`${reference.provider_id}/${reference.api_type}`}
                className="rounded-full bg-bg-secondary px-2.5 py-1 text-xs text-text-secondary"
                title={reference.provider_id}
              >
                {reference.provider_name} · {reference.api_type}
              </span>
            ))}
          </div>
        )}
      </td>
      <td className="px-4 py-4 align-top text-sm text-text-secondary">
        {session.auth_state.status}
      </td>
      <td className="px-4 py-4 align-top text-sm text-text-secondary">
        {new Date(session.updated_at).toLocaleString()}
      </td>
      <td className="px-4 py-4 align-top">
        <div className="flex justify-end gap-2">
          {session.kind === "chatgpt" && (
            <button
              type="button"
              className="btn btn-secondary h-9 px-3"
              disabled={disabled}
              onClick={onReconnect}
              title="Reconnect this GPT credential for every referenced route"
            >
              <RefreshCw className="h-4 w-4" />
              Reconnect
            </button>
          )}
          <button
            type="button"
            className="btn btn-secondary h-9 px-3"
            disabled={disabled}
            onClick={onRename}
            title="Rename credential"
          >
            <Pencil className="h-4 w-4" />
            Rename
          </button>
          {session.kind === "api_key" && (
            <button
              type="button"
              className="btn btn-secondary h-9 px-3"
              disabled={disabled}
              onClick={onRotate}
              title={`Rotate for ${references.length} routes`}
            >
              <RotateCcw className="h-4 w-4" />
              Rotate
            </button>
          )}
          <button
            type="button"
            className="btn h-9 border border-danger/30 px-3 text-danger hover:bg-danger/5"
            disabled={disabled || references.length !== 0}
            onClick={onDelete}
            title={
              references.length === 0
                ? "Delete unused credential"
                : "Remove all route references before deleting"
            }
          >
            <Trash2 className="h-4 w-4" />
            Delete
          </button>
        </div>
      </td>
    </tr>
  );
}

function CredentialEditor({
  editor,
  busy,
  onChange,
  onCancel,
  onSave,
}: {
  editor: Exclude<EditorState, null>;
  busy: boolean;
  onChange: (value: string) => void;
  onCancel: () => void;
  onSave: () => void;
}) {
  const rotating = editor.kind === "rotate";
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4 backdrop-blur-sm">
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby="credential-editor-title"
        className="w-full max-w-lg rounded-xl border border-border bg-white shadow-2xl"
      >
        <div className="border-b border-border p-6">
          <h3
            id="credential-editor-title"
            className="text-xl font-bold text-text-primary"
          >
            {rotating ? "Rotate API Key" : "Rename Credential"}
          </h3>
          <p className="mt-1 text-sm text-text-secondary">
            {rotating
              ? `This changes every one of the ${editor.session.route_references.length} referenced routes together.`
              : "The name is shown in provider credential selectors."}
          </p>
        </div>
        <div className="p-6">
          <label
            className="text-sm font-medium text-text-secondary"
            htmlFor="credential-editor-value"
          >
            {rotating ? "New API Key" : "Name"}
          </label>
          <input
            id="credential-editor-value"
            autoFocus
            className="input mt-2"
            type={rotating ? "password" : "text"}
            maxLength={rotating ? undefined : 120}
            value={editor.value}
            onChange={(event) => onChange(event.target.value)}
          />
        </div>
        <div className="flex justify-end gap-3 px-6 pb-6">
          <button
            type="button"
            className="btn btn-secondary"
            disabled={busy}
            onClick={onCancel}
          >
            Cancel
          </button>
          <button
            type="button"
            className="btn btn-primary"
            disabled={busy || !editor.value.trim()}
            onClick={onSave}
          >
            {busy ? "Saving..." : "Save"}
          </button>
        </div>
      </div>
    </div>
  );
}
