import type { TokenClientAPIKeyDTO } from "../../api/token-usage-types";
import { tokenAPIKeyLabel } from "./token-format";

const ALL_API_KEYS = "all";

interface TokenAPIKeyFilterProps {
  keys: TokenClientAPIKeyDTO[];
  selected: string | undefined;
  onSelect: (fingerprint: string | undefined) => void;
  loading: boolean;
  error: Error | null;
  onRetry: () => void;
}

export function TokenAPIKeyFilter({
  keys,
  selected,
  onSelect,
  loading,
  error,
  onRetry,
}: TokenAPIKeyFilterProps) {
  return (
    <div className="card rounded-2xl p-4 space-y-2">
      <div className="flex flex-wrap items-center gap-3">
        <label htmlFor="token-api-key" className="text-sm font-medium">
          Client API Key
        </label>
        <select
          id="token-api-key"
          className="input input-sm min-w-0 max-w-full flex-1 sm:max-w-xl"
          value={selected ?? ALL_API_KEYS}
          onChange={(event) =>
            onSelect(
              event.target.value === ALL_API_KEYS
                ? undefined
                : event.target.value,
            )
          }
          aria-busy={loading}
        >
          <option value={ALL_API_KEYS}>All API keys</option>
          <option value="">Unattributed requests</option>
          {selected && !keys.some((key) => key.fingerprint === selected) && (
            <option value={selected}>{selected}</option>
          )}
          {keys.map((key) => (
            <option key={key.fingerprint} value={key.fingerprint}>
              {tokenAPIKeyLabel(key)}
            </option>
          ))}
        </select>
        {loading && (
          <span className="text-xs text-text-muted">Loading keys…</span>
        )}
      </div>
      <p className="text-xs text-text-muted">
        Usage is grouped by original key, including unregistered keys.
        Unattributed requests include older logs, missing keys and ambiguous
        credentials.
      </p>
      {error && (
        <div
          role="alert"
          className="flex items-center gap-2 text-xs text-danger"
        >
          <span>Failed to load API keys: {error.message}</span>
          <button
            type="button"
            className="btn btn-secondary btn-sm"
            onClick={onRetry}
          >
            Retry API keys
          </button>
        </div>
      )}
    </div>
  );
}
