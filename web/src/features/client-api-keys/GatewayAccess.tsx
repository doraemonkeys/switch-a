import { ArrowUpRight, CircleAlert, Globe2, LockKeyhole } from "lucide-react";
import type { ClientAccessMode } from "@/api/client-api-keys/types";

interface Props {
  mode: ClientAccessMode;
  keyCount: number;
  busy: boolean;
  setPolicy: (mode: ClientAccessMode) => void;
}

export function GatewayAccess({ mode, keyCount, busy, setPolicy }: Props) {
  return (
    <aside
      className="self-start rounded-2xl border border-slate-200 bg-white"
      aria-labelledby="gateway-access-title"
    >
      <div className="border-b border-slate-100 px-6 py-5">
        <p className="key-eyebrow">ACCESS POLICY</p>
        <h2
          id="gateway-access-title"
          className="mt-2 text-lg font-semibold tracking-tight"
        >
          Who can connect?
        </h2>
        <p className="mt-2 text-sm leading-6 text-slate-500">
          Choose how the gateway accepts client requests.
        </p>
      </div>
      <fieldset className="space-y-3 p-5" disabled={busy}>
        <legend className="sr-only">Gateway access</legend>
        <label className="key-policy-option">
          <input
            type="radio"
            name="access-policy"
            checked={mode === "permissive"}
            onChange={() => setPolicy("permissive")}
            className="sr-only"
          />
          <Globe2
            size={20}
            className="shrink-0 text-slate-500"
            aria-hidden="true"
          />
          <span className="min-w-0 flex-1">
            <span className="block text-sm font-semibold">Allow any key</span>
            <span className="mt-1 block text-xs leading-5 text-slate-500">
              Any key is accepted. Requests without a key are allowed too.
            </span>
          </span>
          <span className="key-radio-indicator" aria-hidden="true" />
        </label>
        <label className="key-policy-option">
          <input
            type="radio"
            name="access-policy"
            checked={mode === "restricted"}
            onChange={() => setPolicy("restricted")}
            className="sr-only"
          />
          <LockKeyhole
            size={20}
            className="shrink-0 text-slate-500"
            aria-hidden="true"
          />
          <span className="min-w-0 flex-1">
            <span className="block text-sm font-semibold">
              Configured keys only
            </span>
            <span className="mt-1 block text-xs leading-5 text-slate-500">
              Only keys in this list can connect. Missing or unlisted keys are
              rejected.
            </span>
          </span>
          <span className="key-radio-indicator" aria-hidden="true" />
        </label>
      </fieldset>
      {mode === "restricted" && keyCount === 0 && (
        <p
          role="status"
          className="mx-5 mb-5 flex items-start gap-2 rounded-lg bg-amber-50 p-3 text-xs leading-5 text-amber-800"
        >
          <CircleAlert
            size={16}
            className="mt-0.5 shrink-0"
            aria-hidden="true"
          />
          <span>
            No keys are configured. All new client requests are blocked until
            you add a key or allow any key.
          </span>
        </p>
      )}
      <div className="flex gap-2 border-t border-slate-100 px-6 py-4 text-xs leading-5 text-slate-500">
        <ArrowUpRight
          size={16}
          className="mt-0.5 shrink-0"
          aria-hidden="true"
        />
        <p>
          Changes apply to new HTTP requests and WebSocket connections. Existing
          streams stay open.
        </p>
      </div>
    </aside>
  );
}
