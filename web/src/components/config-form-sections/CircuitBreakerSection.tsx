import { CONFIG_KEYS, DEFAULTS, FORM_CONSTRAINTS } from "../../config";
import { ConfigSection } from "../ConfigSection";
import { ModifiedBadge } from "./ModifiedBadge";
import type { SectionProps } from "./types";

export function CircuitBreakerSection({
  getValue,
  handleChange,
  getDefault,
}: SectionProps) {
  return (
    <ConfigSection
      title="Circuit Breaker"
      description="Automatically disable failing providers to maintain service reliability."
      icon="⚡"
    >
      <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
        <div>
          <label className="block text-sm font-medium text-text-primary mb-1.5">
            Failure Threshold
            <ModifiedBadge
              configKey={CONFIG_KEYS.CIRCUIT_FAILURE}
              currentValue={getValue(
                CONFIG_KEYS.CIRCUIT_FAILURE,
                DEFAULTS.CIRCUIT_FAILURE,
              )}
              getDefault={getDefault}
            />
          </label>
          <input
            type="number"
            className="input"
            min={FORM_CONSTRAINTS.MIN_POSITIVE}
            value={getValue(
              CONFIG_KEYS.CIRCUIT_FAILURE,
              DEFAULTS.CIRCUIT_FAILURE,
            )}
            onChange={(e) =>
              handleChange(CONFIG_KEYS.CIRCUIT_FAILURE, e.target.value)
            }
          />
          <p className="text-xs text-text-muted mt-1.5">触发熔断的失败次数</p>
        </div>
        <div>
          <label className="block text-sm font-medium text-text-primary mb-1.5">
            Window (seconds)
            <ModifiedBadge
              configKey={CONFIG_KEYS.CIRCUIT_WINDOW}
              currentValue={getValue(
                CONFIG_KEYS.CIRCUIT_WINDOW,
                DEFAULTS.CIRCUIT_WINDOW,
              )}
              getDefault={getDefault}
            />
          </label>
          <input
            type="number"
            className="input"
            min={FORM_CONSTRAINTS.MIN_POSITIVE}
            value={getValue(
              CONFIG_KEYS.CIRCUIT_WINDOW,
              DEFAULTS.CIRCUIT_WINDOW,
            )}
            onChange={(e) =>
              handleChange(CONFIG_KEYS.CIRCUIT_WINDOW, e.target.value)
            }
          />
          <p className="text-xs text-text-muted mt-1.5">检测窗口时长</p>
        </div>
        <div>
          <label className="block text-sm font-medium text-text-primary mb-1.5">
            Disable Duration (seconds)
            <ModifiedBadge
              configKey={CONFIG_KEYS.CIRCUIT_DISABLE}
              currentValue={getValue(
                CONFIG_KEYS.CIRCUIT_DISABLE,
                DEFAULTS.CIRCUIT_DISABLE,
              )}
              getDefault={getDefault}
            />
          </label>
          <input
            type="number"
            className="input"
            min={FORM_CONSTRAINTS.MIN_POSITIVE}
            value={getValue(
              CONFIG_KEYS.CIRCUIT_DISABLE,
              DEFAULTS.CIRCUIT_DISABLE,
            )}
            onChange={(e) =>
              handleChange(CONFIG_KEYS.CIRCUIT_DISABLE, e.target.value)
            }
          />
          <p className="text-xs text-text-muted mt-1.5">熔断禁用时长</p>
        </div>
      </div>
    </ConfigSection>
  );
}
