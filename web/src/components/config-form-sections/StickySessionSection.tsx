import {
  CONFIG_KEYS,
  DEFAULTS,
  FORM_CONSTRAINTS,
  STICKY_MODE_OPTIONS,
  STICKY_MODES,
} from "../../config";
import { ConfigSection } from "../ConfigSection";
import { ModifiedBadge } from "./ModifiedBadge";
import type { SectionProps } from "./types";

export function StickySessionSection({
  getValue,
  handleChange,
  getDefault,
}: SectionProps) {
  const currentMode = getValue(CONFIG_KEYS.STICKY_MODE, DEFAULTS.STICKY_MODE);

  return (
    <ConfigSection
      title="Sticky Session"
      description="Keep users connected to the same provider for conversation continuity."
      icon="📌"
    >
      <div className="space-y-4">
        <div>
          <label className="block text-sm font-medium text-text-primary mb-1.5">
            Sticky Mode
            <ModifiedBadge
              configKey={CONFIG_KEYS.STICKY_MODE}
              currentValue={currentMode}
              getDefault={getDefault}
            />
          </label>
          <select
            className="input"
            value={currentMode}
            onChange={(e) =>
              handleChange(CONFIG_KEYS.STICKY_MODE, e.target.value)
            }
          >
            {STICKY_MODE_OPTIONS.map((opt) => (
              <option key={opt.value} value={opt.value}>
                {opt.label}
              </option>
            ))}
          </select>
          <p className="text-xs text-text-muted mt-1.5">
            {
              STICKY_MODE_OPTIONS.find((opt) => opt.value === currentMode)
                ?.description
            }
          </p>
        </div>

        <div className="max-w-xs">
          <label className="block text-sm font-medium text-text-primary mb-1.5">
            Sticky TTL (seconds)
            <ModifiedBadge
              configKey={CONFIG_KEYS.STICKY_TTL}
              currentValue={getValue(
                CONFIG_KEYS.STICKY_TTL,
                DEFAULTS.STICKY_TTL,
              )}
              getDefault={getDefault}
            />
          </label>
          <input
            type="number"
            className="input"
            min={FORM_CONSTRAINTS.MIN_ZERO}
            value={getValue(CONFIG_KEYS.STICKY_TTL, DEFAULTS.STICKY_TTL)}
            onChange={(e) =>
              handleChange(CONFIG_KEYS.STICKY_TTL, e.target.value)
            }
            disabled={currentMode === STICKY_MODES.OFF}
          />
        </div>

        <label className="flex items-start gap-3 p-4 rounded-xl bg-bg-secondary border border-border-light cursor-pointer hover:border-primary/30 transition-colors">
          <input
            type="checkbox"
            id="websocket_probe_client_model"
            checked={
              getValue(
                CONFIG_KEYS.WEBSOCKET_PROBE_CLIENT_MODEL,
                DEFAULTS.WEBSOCKET_PROBE_CLIENT_MODEL,
              ) === "true"
            }
            onChange={(e) =>
              handleChange(
                CONFIG_KEYS.WEBSOCKET_PROBE_CLIENT_MODEL,
                String(e.target.checked),
              )
            }
          />
          <div className="space-y-1">
            <div className="flex items-center gap-2">
              <span className="font-medium text-text-primary">
                Probe WebSocket Client Model Before Selection
              </span>
              <ModifiedBadge
                configKey={CONFIG_KEYS.WEBSOCKET_PROBE_CLIENT_MODEL}
                currentValue={getValue(
                  CONFIG_KEYS.WEBSOCKET_PROBE_CLIENT_MODEL,
                  DEFAULTS.WEBSOCKET_PROBE_CLIENT_MODEL,
                )}
                getDefault={getDefault}
              />
            </div>
            <p className="text-xs text-text-muted">
              Allow replay-safe pre-selection probing when the WebSocket
              handshake does not expose a usable model. Disable this to force
              handshake-only selection semantics.
            </p>
          </div>
        </label>
      </div>
    </ConfigSection>
  );
}
