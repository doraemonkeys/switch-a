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
      </div>
    </ConfigSection>
  );
}
