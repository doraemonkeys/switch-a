import {
  CONFIG_KEYS,
  DEFAULTS,
  FORM_CONSTRAINTS,
  AUTH_MODE_OPTIONS,
  STRATEGY_OPTIONS,
} from "../config";
import { ConfigSection } from "./ConfigSection";

interface SectionProps {
  getValue: (key: string, defaultValue: string | number | boolean) => string;
  handleChange: (key: string, value: string) => void;
}

export function RoutingStrategySection({
  getValue,
  handleChange,
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

export function AuthSettingsSection({ getValue, handleChange }: SectionProps) {
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

export function StickySessionSection({ getValue, handleChange }: SectionProps) {
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
          <div>
            <span className="font-medium text-text-primary">
              Enable sticky session
            </span>
            <p className="text-xs text-text-muted mt-0.5">
              Route returning users to the same provider within the TTL window
            </p>
          </div>
        </label>

        <div className="max-w-xs">
          <label className="block text-sm font-medium text-text-primary mb-1.5">
            Sticky TTL (seconds)
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

export function CircuitBreakerSection({
  getValue,
  handleChange,
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

interface FormActionsProps {
  isDirty: boolean;
  saving: boolean;
  onReset: () => void;
}

export function ConfigFormActions({
  isDirty,
  saving,
  onReset,
}: FormActionsProps) {
  return (
    <div className="flex items-center justify-between pt-6 border-t border-border">
      <p className="text-sm text-text-muted">
        Changes take effect immediately after saving.
      </p>
      <div className="flex gap-3">
        <button
          type="button"
          className="btn btn-secondary"
          onClick={onReset}
          disabled={!isDirty || saving}
        >
          Reset
        </button>
        <button
          type="submit"
          className="btn btn-primary"
          disabled={!isDirty || saving}
        >
          {saving ? (
            <span className="w-4 h-4 border-2 border-white/30 border-t-white rounded-full animate-spin mr-2"></span>
          ) : (
            <span className="mr-2">💾</span>
          )}
          {saving ? "Saving..." : "Save Changes"}
        </button>
      </div>
    </div>
  );
}
