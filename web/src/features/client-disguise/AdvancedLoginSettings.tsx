import { ChevronDown, Settings2 } from "lucide-react";
import type { DisguiseState } from "@/api/client-disguise/types";
import type { LoginDraft } from "./loginDraft";

export function AdvancedLoginSettings({
  state,
  draft,
  change,
}: {
  state: DisguiseState;
  draft: LoginDraft;
  change: (draft: LoginDraft) => void;
}) {
  return (
    <details className="cd-disclosure cd-advanced">
      <summary>
        <span>
          <Settings2 size={17} aria-hidden="true" />
          <strong>Advanced login settings</strong>
          <small>Cache keys, telemetry & transport</small>
        </span>
        <ChevronDown size={16} aria-hidden="true" />
      </summary>
      <div className="cd-advanced-body">
        <label className="cd-checkbox">
          <input
            type="checkbox"
            checked={draft.cacheKeys}
            onChange={(event) =>
              change({ ...draft, cacheKeys: event.target.checked })
            }
          />
          <span>
            <strong>Stably remap prompt cache keys</strong>
            <small>
              Preserves original cache grouping. Disabled keeps original cache
              keys.
            </small>
          </span>
        </label>
        <label className="cd-field">
          Telemetry path mappings (JSON)
          <textarea
            aria-label="Telemetry path mappings"
            rows={4}
            spellCheck={false}
            value={draft.paths}
            onChange={(event) =>
              change({ ...draft, paths: event.target.value })
            }
          />
          <span className="cd-field-help">
            Applies to recognized telemetry metadata. Workspace paths and
            request content stay intact.
          </span>
        </label>
        <label className="cd-field">
          Transport sample
          <select
            value={draft.transport}
            onChange={(event) =>
              change({ ...draft, transport: event.target.value })
            }
          >
            <option value="">Default transport</option>
            {state.transport_samples.map((item) => (
              <option key={item.id} value={item.id}>
                {item.name} · {item.captured_at}
              </option>
            ))}
          </select>
          <span className="cd-field-help">
            Uses an independent TLS / HTTP observation. Enabling disguise does
            not change Cookie or Accept-Encoding.
          </span>
        </label>
      </div>
    </details>
  );
}
