import { CONFIG_KEYS, DEFAULTS, FORM_CONSTRAINTS } from "../../config";
import { ConfigSection } from "../ConfigSection";
import { ModifiedBadge } from "./ModifiedBadge";
import type { SectionProps } from "./types";

export function StickySessionSection({
  getValue,
  handleChange,
  getDefault,
}: SectionProps) {
  return (
    <ConfigSection
      title="Sticky Session"
      description="Keep users connected to the same provider for conversation continuity."
      icon="📌"
    >
      <div className="space-y-4">
        <label className="flex items-center gap-3 p-4 rounded-xl bg-bg-secondary border border-border-light cursor-pointer hover:border-primary/30 transition-colors">
          <input
            type="checkbox"
            id="sticky_enabled"
            checked={
              getValue(CONFIG_KEYS.STICKY_ENABLED, DEFAULTS.STICKY_ENABLED) ===
              "true"
            }
            onChange={(e) =>
              handleChange(CONFIG_KEYS.STICKY_ENABLED, String(e.target.checked))
            }
          />
          <div className="flex items-center gap-2">
            <span className="font-medium text-text-primary">
              Enable sticky session
            </span>
            <ModifiedBadge
              configKey={CONFIG_KEYS.STICKY_ENABLED}
              currentValue={getValue(
                CONFIG_KEYS.STICKY_ENABLED,
                DEFAULTS.STICKY_ENABLED,
              )}
              getDefault={getDefault}
            />
          </div>
        </label>

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
            disabled={
              getValue(CONFIG_KEYS.STICKY_ENABLED, DEFAULTS.STICKY_ENABLED) !==
              "true"
            }
          />
        </div>
      </div>
    </ConfigSection>
  );
}
