import { CONFIG_KEYS, DEFAULTS, FORM_CONSTRAINTS } from "../../config";
import { ConfigSection } from "../ConfigSection";
import { ModifiedBadge } from "./ModifiedBadge";
import type { SectionProps } from "./types";

export function OtherSettingsSection({
  getValue,
  handleChange,
  getDefault,
}: SectionProps) {
  return (
    <ConfigSection
      title="Other Settings"
      description="Additional system configuration."
      icon="⚙️"
    >
      <div className="space-y-4">
        <label className="flex items-center gap-3 p-4 rounded-xl bg-bg-secondary border border-border-light cursor-pointer hover:border-primary/30 transition-colors">
          <input
            type="checkbox"
            id="trust_proxy_headers"
            checked={
              getValue(
                CONFIG_KEYS.TRUST_PROXY_HEADERS,
                DEFAULTS.TRUST_PROXY_HEADERS,
              ) === "true"
            }
            onChange={(e) =>
              handleChange(
                CONFIG_KEYS.TRUST_PROXY_HEADERS,
                String(e.target.checked),
              )
            }
          />
          <div className="flex items-center gap-2">
            <span className="font-medium text-text-primary">
              Trust Proxy Headers
            </span>
            <ModifiedBadge
              configKey={CONFIG_KEYS.TRUST_PROXY_HEADERS}
              currentValue={getValue(
                CONFIG_KEYS.TRUST_PROXY_HEADERS,
                DEFAULTS.TRUST_PROXY_HEADERS,
              )}
              getDefault={getDefault}
            />
          </div>
        </label>
        <p className="text-xs text-text-muted -mt-2 ml-1">
          Whether to trust X-Forwarded-For headers (enable if behind a load
          balancer)
        </p>

        <div className="max-w-xs">
          <label className="block text-sm font-medium text-text-primary mb-1.5">
            Log Retention (days)
            <ModifiedBadge
              configKey={CONFIG_KEYS.LOG_RETENTION_DAYS}
              currentValue={getValue(
                CONFIG_KEYS.LOG_RETENTION_DAYS,
                DEFAULTS.LOG_RETENTION_DAYS,
              )}
              getDefault={getDefault}
            />
          </label>
          <input
            type="number"
            className="input"
            min={FORM_CONSTRAINTS.MIN_POSITIVE}
            value={getValue(
              CONFIG_KEYS.LOG_RETENTION_DAYS,
              DEFAULTS.LOG_RETENTION_DAYS,
            )}
            onChange={(e) =>
              handleChange(CONFIG_KEYS.LOG_RETENTION_DAYS, e.target.value)
            }
          />
          <p className="text-xs text-text-muted mt-1.5">日志保留天数</p>
        </div>
      </div>
    </ConfigSection>
  );
}
