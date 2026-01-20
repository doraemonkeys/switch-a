import { CONFIG_KEYS, DEFAULTS, STRATEGY_OPTIONS } from "../../config";
import { ConfigSection } from "../ConfigSection";
import { ModifiedBadge } from "./ModifiedBadge";
import type { SectionProps } from "./types";

export function RoutingStrategySection({
  getValue,
  handleChange,
  getDefault,
}: SectionProps) {
  return (
    <ConfigSection
      title="Routing Strategy"
      description="Configure how requests are routed between groups."
      icon="🔀"
    >
      <div>
        <label className="block text-sm font-medium text-text-primary mb-1.5">
          Inter-Group Strategy
          <ModifiedBadge
            configKey={CONFIG_KEYS.INTER_GROUP_STRATEGY}
            currentValue={getValue(
              CONFIG_KEYS.INTER_GROUP_STRATEGY,
              DEFAULTS.INTER_GROUP_STRATEGY,
            )}
            getDefault={getDefault}
          />
        </label>
        <select
          className="input"
          value={getValue(
            CONFIG_KEYS.INTER_GROUP_STRATEGY,
            DEFAULTS.INTER_GROUP_STRATEGY,
          )}
          onChange={(e) =>
            handleChange(CONFIG_KEYS.INTER_GROUP_STRATEGY, e.target.value)
          }
        >
          {STRATEGY_OPTIONS.map((opt) => (
            <option key={opt.value} value={opt.value}>
              {opt.label}
            </option>
          ))}
        </select>
        <p className="text-xs text-text-muted mt-1.5">
          {
            STRATEGY_OPTIONS.find(
              (opt) =>
                opt.value ===
                getValue(
                  CONFIG_KEYS.INTER_GROUP_STRATEGY,
                  DEFAULTS.INTER_GROUP_STRATEGY,
                ),
            )?.description
          }
        </p>
      </div>
    </ConfigSection>
  );
}
