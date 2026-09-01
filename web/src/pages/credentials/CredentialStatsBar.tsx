import {
  AlertTriangle,
  CheckCircle2,
  KeyRound,
  Layers,
  Sparkles,
} from "lucide-react";
import type { CredentialSession } from "../../api";

interface CredentialStatsBarProps {
  sessions: CredentialSession[];
}

export function CredentialStatsBar({ sessions }: CredentialStatsBarProps) {
  const totalCount = sessions.length;
  const apiKeyCount = sessions.filter((s) => s.kind === "api_key").length;
  const chatgptCount = sessions.filter((s) => s.kind === "chatgpt").length;

  const activeCount = sessions.filter(
    (s) => s.auth_state.status === "active",
  ).length;
  const activePercent =
    totalCount === 0 ? 100 : Math.round((activeCount / totalCount) * 100);

  const reauthCount = sessions.filter(
    (s) => s.auth_state.status === "reauth_required",
  ).length;

  const totalRouteRefs = sessions.reduce(
    (sum, s) => sum + s.route_references.length,
    0,
  );
  const unusedCount = sessions.filter(
    (s) => s.route_references.length === 0,
  ).length;

  return (
    <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
      {/* Card 1: Total Credentials */}
      <div className="relative overflow-hidden rounded-2xl border border-border bg-white p-5 shadow-xs transition-all duration-200 hover:shadow-sm">
        <div className="flex items-center justify-between">
          <span className="text-xs font-semibold uppercase tracking-wider text-text-secondary">
            Total Credentials
          </span>
          <div className="flex h-9 w-9 items-center justify-center rounded-xl bg-primary-light text-primary">
            <KeyRound className="h-4.5 w-4.5" />
          </div>
        </div>
        <div className="mt-3 flex items-baseline gap-2">
          <span className="text-2xl font-bold tracking-tight text-text-primary">
            {totalCount}
          </span>
          <span className="text-xs text-text-secondary">configured</span>
        </div>
        <div className="mt-2.5 flex items-center gap-2 text-xs text-text-secondary">
          <span className="inline-flex items-center gap-1 rounded-md bg-bg-secondary px-2 py-0.5 font-medium">
            {apiKeyCount} API {apiKeyCount === 1 ? "Key" : "Keys"}
          </span>
          <span className="inline-flex items-center gap-1 rounded-md bg-bg-secondary px-2 py-0.5 font-medium">
            {chatgptCount} GPT {chatgptCount === 1 ? "Session" : "Sessions"}
          </span>
        </div>
      </div>

      {/* Card 2: Operational Health */}
      <div className="relative overflow-hidden rounded-2xl border border-border bg-white p-5 shadow-xs transition-all duration-200 hover:shadow-sm">
        <div className="flex items-center justify-between">
          <span className="text-xs font-semibold uppercase tracking-wider text-text-secondary">
            Operational Health
          </span>
          <div className="flex h-9 w-9 items-center justify-center rounded-xl bg-emerald-50 text-emerald-600">
            <CheckCircle2 className="h-4.5 w-4.5" />
          </div>
        </div>
        <div className="mt-3 flex items-baseline gap-2">
          <span className="text-2xl font-bold tracking-tight text-emerald-600">
            {activePercent}%
          </span>
          <div className="flex items-center gap-1.5">
            <span className="relative flex h-2 w-2">
              {activeCount > 0 && (
                <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-emerald-400 opacity-75" />
              )}
              <span className="relative inline-flex h-2 w-2 rounded-full bg-emerald-500" />
            </span>
            <span className="text-xs font-medium text-emerald-700">
              {activeCount} Active
            </span>
          </div>
        </div>
        <div className="mt-2.5 flex items-center gap-1 text-xs text-text-muted">
          <Sparkles className="h-3.5 w-3.5 text-emerald-500" />
          <span>Ready for request routing</span>
        </div>
      </div>

      {/* Card 3: Re-auth Attention */}
      <div
        className={`relative overflow-hidden rounded-2xl border p-5 shadow-xs transition-all duration-200 hover:shadow-sm ${
          reauthCount > 0
            ? "border-amber-200 bg-amber-50/40"
            : "border-border bg-white"
        }`}
      >
        <div className="flex items-center justify-between">
          <span className="text-xs font-semibold uppercase tracking-wider text-text-secondary">
            Needs Attention
          </span>
          <div
            className={`flex h-9 w-9 items-center justify-center rounded-xl ${
              reauthCount > 0
                ? "bg-amber-100 text-amber-700 animate-pulse"
                : "bg-bg-secondary text-text-muted"
            }`}
          >
            <AlertTriangle className="h-4.5 w-4.5" />
          </div>
        </div>
        <div className="mt-3 flex items-baseline gap-2">
          <span
            className={`text-2xl font-bold tracking-tight ${
              reauthCount > 0 ? "text-amber-700" : "text-text-primary"
            }`}
          >
            {reauthCount}
          </span>
          <span className="text-xs text-text-secondary">
            {reauthCount === 1
              ? "session needs re-auth"
              : "sessions need re-auth"}
          </span>
        </div>
        <div className="mt-2.5 text-xs">
          {reauthCount > 0 ? (
            <span className="font-medium text-amber-700">
              Action required to maintain route continuity
            </span>
          ) : (
            <span className="text-text-muted">
              No expired or invalid credentials
            </span>
          )}
        </div>
      </div>

      {/* Card 4: Route References */}
      <div className="relative overflow-hidden rounded-2xl border border-border bg-white p-5 shadow-xs transition-all duration-200 hover:shadow-sm">
        <div className="flex items-center justify-between">
          <span className="text-xs font-semibold uppercase tracking-wider text-text-secondary">
            Route Bindings
          </span>
          <div className="flex h-9 w-9 items-center justify-center rounded-xl bg-indigo-50 text-indigo-600">
            <Layers className="h-4.5 w-4.5" />
          </div>
        </div>
        <div className="mt-3 flex items-baseline gap-2">
          <span className="text-2xl font-bold tracking-tight text-text-primary">
            {totalRouteRefs}
          </span>
          <span className="text-xs text-text-secondary">bound routes</span>
        </div>
        <div className="mt-2.5 flex items-center gap-2 text-xs text-text-secondary">
          {unusedCount > 0 ? (
            <span className="rounded-md bg-amber-50 px-2 py-0.5 font-medium text-amber-700">
              {unusedCount} unreferenced / unused
            </span>
          ) : (
            <span className="text-emerald-600">
              All credentials actively bound
            </span>
          )}
        </div>
      </div>
    </div>
  );
}
