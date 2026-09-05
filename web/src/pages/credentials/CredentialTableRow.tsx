import { useState } from "react";
import {
  AlertTriangle,
  Eye,
  EyeOff,
  KeyRound,
  Mail,
  Pencil,
  RefreshCw,
  RotateCcw,
  Sparkles,
  Trash2,
} from "lucide-react";
import type { CredentialSession } from "../../api";
import { CopyButton } from "../../components";
import { CredentialDisguiseSummary } from "@/features/client-disguise/CredentialDisguiseSummary";

interface CredentialTableRowProps {
  session: CredentialSession;
  disabled: boolean;
  onReconnect: () => void;
  onRename: () => void;
  onRotate: () => void;
  onDelete: () => void;
}

export function CredentialTableRow({
  session,
  disabled,
  onReconnect,
  onRename,
  onRotate,
  onDelete,
}: CredentialTableRowProps) {
  const [showSecret, setShowSecret] = useState(false);
  const references = session.route_references;
  const isApiKey = session.kind === "api_key";
  const isChatGPT = session.kind === "chatgpt";
  const isActive = session.auth_state.status === "active";
  const isReauthRequired = session.auth_state.status === "reauth_required";

  return (
    <tr className="border-b border-border/60 transition-colors hover:bg-bg-secondary/40">
      {/* 1. Credential Name & Kind */}
      <td className="px-4 py-4 align-top">
        <div className="flex items-start gap-3">
          <div
            className={`flex h-9 w-9 shrink-0 items-center justify-center rounded-xl ${
              isApiKey
                ? "bg-indigo-50 text-indigo-600 ring-1 ring-indigo-500/10"
                : "bg-emerald-50 text-emerald-600 ring-1 ring-emerald-500/10"
            }`}
          >
            {isApiKey ? (
              <KeyRound className="h-4.5 w-4.5" />
            ) : (
              <Sparkles className="h-4.5 w-4.5" />
            )}
          </div>
          <div className="min-w-0">
            <CredentialDisguiseSummary session={session} />
            <div className="flex items-center gap-2">
              <span className="font-semibold text-text-primary">
                {session.name}
              </span>
              <span className="rounded bg-bg-secondary px-1.5 py-0.2 font-mono text-[10px] text-text-secondary">
                v{session.version}
              </span>
            </div>
            <p className="mt-0.5 font-mono text-xs text-text-muted">
              {isApiKey ? "API Key" : "GPT Login"} · {session.id}
            </p>
          </div>
        </div>
      </td>

      {/* 2. Secret / Account Info */}
      <td className="px-4 py-4 align-top">
        {isApiKey && session.secret_data && (
          <div className="flex max-w-xs items-center gap-2">
            <div className="relative flex-1">
              <input
                className="input h-8 w-full bg-white pr-7 font-mono text-xs"
                type={showSecret ? "text" : "password"}
                readOnly
                value={session.secret_data}
                aria-label={`API key for ${session.name}`}
              />
              <button
                type="button"
                onClick={() => setShowSecret(!showSecret)}
                className="absolute right-2 top-1/2 -translate-y-1/2 text-text-muted hover:text-text-primary"
                aria-label={showSecret ? "Hide secret" : "Show secret"}
              >
                {showSecret ? (
                  <EyeOff className="h-3 w-3" />
                ) : (
                  <Eye className="h-3 w-3" />
                )}
              </button>
            </div>
            <CopyButton
              text={session.secret_data}
              className="h-8 shrink-0 rounded-lg border border-border bg-white px-2 hover:bg-bg-hover"
            />
          </div>
        )}
        {isChatGPT && (
          <div className="flex flex-col gap-1 text-xs">
            <div className="flex items-center gap-1.5 text-text-secondary">
              <Mail className="h-3.5 w-3.5 text-text-muted" />
              <span className="font-medium text-text-primary">
                {session.auth_state.email || "GPT Account"}
              </span>
            </div>
            {session.auth_state.plan_type && (
              <span className="w-fit rounded-md bg-indigo-50 px-1.5 py-0.5 font-medium uppercase text-[10px] text-indigo-700">
                {session.auth_state.plan_type}
              </span>
            )}
          </div>
        )}
      </td>

      {/* 3. Status */}
      <td className="px-4 py-4 align-top">
        {isActive && (
          <span className="inline-flex items-center gap-1.5 rounded-full bg-emerald-50 px-2.5 py-1 text-xs font-semibold text-emerald-700 ring-1 ring-emerald-600/20">
            <span className="relative flex h-2 w-2">
              <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-emerald-400 opacity-75" />
              <span className="relative inline-flex h-2 w-2 rounded-full bg-emerald-500" />
            </span>
            Active
          </span>
        )}
        {isReauthRequired && (
          <span className="inline-flex items-center gap-1.5 rounded-full bg-amber-50 px-2.5 py-1 text-xs font-semibold text-amber-700 ring-1 ring-amber-600/20 animate-pulse">
            <AlertTriangle className="h-3 w-3" />
            Re-auth Required
          </span>
        )}
        {!isActive && !isReauthRequired && (
          <span className="inline-flex items-center gap-1.5 rounded-full bg-bg-secondary px-2.5 py-1 text-xs font-medium text-text-secondary">
            {session.auth_state.status}
          </span>
        )}
      </td>

      {/* 4. References */}
      <td className="px-4 py-4 align-top">
        {references.length === 0 ? (
          <span className="inline-flex items-center rounded-full bg-amber-100/80 px-2.5 py-0.5 text-xs font-semibold text-amber-800">
            Unused
          </span>
        ) : (
          <div className="flex max-w-md flex-wrap gap-1.5">
            {references.map((reference) => (
              <span
                key={`${reference.provider_id}/${reference.api_type}`}
                className="inline-flex items-center gap-1 rounded-md border border-border/70 bg-white px-2 py-0.5 text-xs text-text-secondary shadow-2xs"
                title={`Provider ID: ${reference.provider_id}`}
              >
                <span className="h-1.5 w-1.5 rounded-full bg-indigo-500" />
                {reference.provider_name} · {reference.api_type}
              </span>
            ))}
          </div>
        )}
      </td>

      {/* 5. Updated */}
      <td className="px-4 py-4 align-top text-xs text-text-secondary">
        {new Date(session.updated_at).toLocaleString()}
      </td>

      {/* 6. Actions */}
      <td className="px-4 py-4 align-top">
        <div className="flex justify-end gap-1.5">
          {isChatGPT && (
            <button
              type="button"
              className="btn btn-secondary h-8 px-2.5 text-xs"
              disabled={disabled}
              onClick={onReconnect}
              title="Reconnect this GPT credential for every referenced route"
            >
              <RefreshCw className="h-3.5 w-3.5" />
              Reconnect
            </button>
          )}

          <button
            type="button"
            className="btn btn-secondary h-8 px-2.5 text-xs"
            disabled={disabled}
            onClick={onRename}
            title="Rename credential"
          >
            <Pencil className="h-3.5 w-3.5" />
            Rename
          </button>

          {isApiKey && (
            <button
              type="button"
              className="btn btn-secondary h-8 px-2.5 text-xs"
              disabled={disabled}
              onClick={onRotate}
              title={`Rotate for ${references.length} routes`}
            >
              <RotateCcw className="h-3.5 w-3.5" />
              Rotate
            </button>
          )}

          <button
            type="button"
            className={`btn h-8 px-2.5 text-xs transition-colors ${
              references.length === 0
                ? "border border-danger/30 text-danger hover:bg-danger/5"
                : "border border-border text-text-muted opacity-40 cursor-not-allowed"
            }`}
            disabled={disabled || references.length !== 0}
            onClick={onDelete}
            title={
              references.length === 0
                ? "Delete unused credential"
                : "Remove all route references before deleting"
            }
          >
            <Trash2 className="h-3.5 w-3.5" />
            Delete
          </button>
        </div>
      </td>
    </tr>
  );
}
