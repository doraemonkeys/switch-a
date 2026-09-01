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

interface CredentialCardProps {
  session: CredentialSession;
  disabled: boolean;
  onReconnect: () => void;
  onRename: () => void;
  onRotate: () => void;
  onDelete: () => void;
}

function getQuotaColor(percent: number): string {
  if (percent > 90) return "bg-red-500";
  if (percent > 70) return "bg-amber-500";
  return "bg-emerald-500";
}

function QuotaBar({ label, percent }: { label: string; percent: number }) {
  const color = getQuotaColor(percent);
  const clampedWidth = Math.min(100, Math.max(0, percent));
  return (
    <div className="space-y-1 pt-1">
      <div className="flex justify-between text-[11px] text-text-secondary">
        <span>{label}</span>
        <span className="font-semibold text-text-primary">
          {Math.round(percent)}%
        </span>
      </div>
      <div className="h-1.5 w-full overflow-hidden rounded-full bg-slate-200">
        <div
          className={`h-full transition-all duration-300 ${color}`}
          style={{ width: `${clampedWidth}%` }}
        />
      </div>
    </div>
  );
}

export function CredentialCard({
  session,
  disabled,
  onReconnect,
  onRename,
  onRotate,
  onDelete,
}: CredentialCardProps) {
  const [showSecret, setShowSecret] = useState(false);
  const references = session.route_references;
  const isApiKey = session.kind === "api_key";
  const isChatGPT = session.kind === "chatgpt";
  const isActive = session.auth_state.status === "active";
  const isReauthRequired = session.auth_state.status === "reauth_required";

  const usage = session.auth_state.usage_snapshot;
  const fiveHourPercent = usage?.five_hour?.used_percent ?? null;
  const oneWeekPercent = usage?.one_week?.used_percent ?? null;

  return (
    <div className="group relative flex flex-col justify-between overflow-hidden rounded-2xl border border-border bg-white p-5 shadow-xs transition-all duration-200 hover:-translate-y-0.5 hover:shadow-md">
      {/* Top Section: Header & Status Badge */}
      <div>
        <div className="flex items-start justify-between gap-3">
          <div className="flex items-start gap-3 min-w-0">
            <div
              className={`flex h-11 w-11 shrink-0 items-center justify-center rounded-2xl ${
                isApiKey
                  ? "bg-indigo-50 text-indigo-600 ring-1 ring-indigo-500/10"
                  : "bg-emerald-50 text-emerald-600 ring-1 ring-emerald-500/10"
              }`}
            >
              {isApiKey ? (
                <KeyRound className="h-5 w-5" />
              ) : (
                <Sparkles className="h-5 w-5" />
              )}
            </div>

            <div className="min-w-0 flex-1">
              <div className="flex items-center gap-2">
                <h3
                  className="truncate text-base font-bold text-text-primary"
                  title={session.name}
                >
                  {session.name}
                </h3>
                <span className="rounded-md bg-bg-secondary px-1.5 py-0.5 font-mono text-[10px] font-semibold text-text-secondary">
                  v{session.version}
                </span>
              </div>
              <p
                className="mt-0.5 truncate font-mono text-xs text-text-muted"
                title={session.id}
              >
                {isApiKey ? "API Key" : "ChatGPT OAuth"} · {session.id}
              </p>
            </div>
          </div>

          <div className="shrink-0">
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
              <span className="inline-flex items-center gap-1.5 rounded-full bg-bg-secondary px-2.5 py-1 text-xs font-semibold text-text-secondary">
                {session.auth_state.status}
              </span>
            )}
          </div>
        </div>

        {/* Content Body */}
        <div className="mt-4 space-y-3 rounded-xl bg-bg-secondary/60 p-3.5 border border-border/40">
          {isApiKey && session.secret_data && (
            <div>
              <div className="flex items-center justify-between text-xs text-text-secondary">
                <span className="font-medium">Secret Key</span>
                <span className="text-[11px] text-text-muted">
                  {session.secret_data.length} characters
                </span>
              </div>
              <div className="mt-1.5 flex items-center gap-2">
                <div className="relative flex-1">
                  <input
                    type={showSecret ? "text" : "password"}
                    readOnly
                    value={session.secret_data}
                    aria-label={`API key for ${session.name}`}
                    className="input h-8 w-full bg-white font-mono text-xs pr-8"
                  />
                  <button
                    type="button"
                    onClick={() => setShowSecret(!showSecret)}
                    className="absolute right-2 top-1/2 -translate-y-1/2 text-text-muted hover:text-text-primary"
                    aria-label={showSecret ? "Hide secret" : "Show secret"}
                    title={showSecret ? "Hide secret" : "Show secret"}
                  >
                    {showSecret ? (
                      <EyeOff className="h-3.5 w-3.5" />
                    ) : (
                      <Eye className="h-3.5 w-3.5" />
                    )}
                  </button>
                </div>
                <CopyButton
                  text={session.secret_data}
                  className="h-8 shrink-0 rounded-lg border border-border bg-white px-2.5 hover:bg-bg-hover"
                />
              </div>
            </div>
          )}

          {isChatGPT && (
            <div className="space-y-2 text-xs">
              <div className="flex items-center justify-between">
                <div className="flex items-center gap-1.5 text-text-secondary">
                  <Mail className="h-3.5 w-3.5 text-text-muted" />
                  <span className="font-medium text-text-primary">
                    {session.auth_state.email || "GPT Account"}
                  </span>
                </div>
                {session.auth_state.plan_type && (
                  <span className="rounded-md bg-indigo-50 px-2 py-0.5 font-medium uppercase tracking-wider text-[10px] text-indigo-700">
                    {session.auth_state.plan_type}
                  </span>
                )}
              </div>

              {fiveHourPercent !== null && (
                <QuotaBar label="5-Hour Quota" percent={fiveHourPercent} />
              )}
              {oneWeekPercent !== null && (
                <QuotaBar label="1-Week Quota" percent={oneWeekPercent} />
              )}
            </div>
          )}

          {/* Route References */}
          <div>
            <div className="flex items-center justify-between text-[11px] font-medium text-text-secondary">
              <span>Bound Routes ({references.length})</span>
              <span className="text-text-muted">
                Updated {new Date(session.updated_at).toLocaleDateString()}
              </span>
            </div>
            <div className="mt-1.5 flex flex-wrap gap-1.5">
              {references.length === 0 ? (
                <span className="inline-flex items-center gap-1 rounded-lg bg-amber-100/70 px-2 py-0.5 text-xs font-semibold text-amber-800">
                  Unused · Safe to Delete
                </span>
              ) : (
                references.map((ref) => (
                  <span
                    key={`${ref.provider_id}/${ref.api_type}`}
                    className="inline-flex items-center gap-1 rounded-md border border-border/80 bg-white px-2 py-0.5 text-xs font-medium text-text-secondary shadow-2xs"
                    title={`Provider ID: ${ref.provider_id}`}
                  >
                    <span className="h-1.5 w-1.5 rounded-full bg-indigo-500" />
                    {ref.provider_name} · {ref.api_type}
                  </span>
                ))
              )}
            </div>
          </div>
        </div>
      </div>

      {/* Footer Actions */}
      <div className="mt-5 flex items-center justify-end gap-2 border-t border-border/60 pt-3">
        {isChatGPT && (
          <button
            type="button"
            className="btn btn-secondary h-8 px-3 text-xs"
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
          className="btn btn-secondary h-8 px-3 text-xs"
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
            className="btn btn-secondary h-8 px-3 text-xs"
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
          className={`btn h-8 px-3 text-xs transition-colors ${
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
    </div>
  );
}
