import { CONFIG_KEYS, DEFAULTS, AUTH_MODE_OPTIONS } from "../../config";
import { ConfigSection } from "../ConfigSection";
import { ModifiedBadge } from "./ModifiedBadge";
import type { SectionProps } from "./types";

export function AuthSettingsSection({
  getValue,
  handleChange,
  getDefault,
}: SectionProps) {
  return (
    <ConfigSection
      title="Authentication"
      description="Configure how authentication is handled for proxied requests."
      icon="🔐"
    >
      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        <div>
          <label className="block text-sm font-medium text-text-primary mb-1.5">
            Auth Mode
            <ModifiedBadge
              configKey={CONFIG_KEYS.AUTH_MODE}
              currentValue={getValue(CONFIG_KEYS.AUTH_MODE, DEFAULTS.AUTH_MODE)}
              getDefault={getDefault}
            />
          </label>
          <select
            className="input"
            value={getValue(CONFIG_KEYS.AUTH_MODE, DEFAULTS.AUTH_MODE)}
            onChange={(e) =>
              handleChange(CONFIG_KEYS.AUTH_MODE, e.target.value)
            }
          >
            {AUTH_MODE_OPTIONS.map((opt) => (
              <option key={opt.value} value={opt.value}>
                {opt.label}
              </option>
            ))}
          </select>
          <p className="text-xs text-text-muted mt-1.5">
            全局认证模式，供应商配置可覆盖
          </p>
        </div>

        <div>
          <label className="block text-sm font-medium text-text-primary mb-1.5">
            User Header
            <ModifiedBadge
              configKey={CONFIG_KEYS.USER_HEADER}
              currentValue={getValue(
                CONFIG_KEYS.USER_HEADER,
                DEFAULTS.USER_HEADER,
              )}
              getDefault={getDefault}
            />
          </label>
          <input
            type="text"
            className="input"
            placeholder="X-User-ID"
            value={getValue(CONFIG_KEYS.USER_HEADER, DEFAULTS.USER_HEADER)}
            onChange={(e) =>
              handleChange(CONFIG_KEYS.USER_HEADER, e.target.value)
            }
          />
          <p className="text-xs text-text-muted mt-1.5">
            用于识别用户的请求头名称
          </p>
        </div>
      </div>
    </ConfigSection>
  );
}
