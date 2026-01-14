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
  /** Get server default value for a key */
  getDefault?: (key: string) => string | undefined;
}

/** Modified indicator badge component - shows when current value differs from default */
function ModifiedBadge({
  currentValue,
  getDefault,
  configKey,
}: {
  currentValue: string;
  getDefault?: (key: string) => string | undefined;
  configKey: string;
}) {
  const defaultValue = getDefault?.(configKey);

  // Only show badge when value differs from default
  if (defaultValue === undefined || currentValue === defaultValue) {
    return null;
  }

  return (
    <span
      className="ml-2 inline-flex items-center px-1.5 py-0.5 rounded text-xs font-medium bg-primary/10 text-primary cursor-help"
      title={`Default: ${defaultValue}`}
    >
      Modified
    </span>
  );
}

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

export function TimeoutSettingsSection({
  getValue,
  handleChange,
  getDefault,
}: SectionProps) {
  return (
    <ConfigSection
      title="Timeout Settings"
      description="Configure connection and read timeouts for upstream requests."
      icon="⏱️"
    >
      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        <div>
          <label className="block text-sm font-medium text-text-primary mb-1.5">
            Connect Timeout (seconds)
            <ModifiedBadge
              configKey={CONFIG_KEYS.UPSTREAM_CONNECT_TIMEOUT}
              currentValue={getValue(
                CONFIG_KEYS.UPSTREAM_CONNECT_TIMEOUT,
                DEFAULTS.UPSTREAM_CONNECT_TIMEOUT,
              )}
              getDefault={getDefault}
            />
          </label>
          <input
            type="number"
            className="input"
            min={FORM_CONSTRAINTS.MIN_POSITIVE}
            value={getValue(
              CONFIG_KEYS.UPSTREAM_CONNECT_TIMEOUT,
              DEFAULTS.UPSTREAM_CONNECT_TIMEOUT,
            )}
            onChange={(e) =>
              handleChange(CONFIG_KEYS.UPSTREAM_CONNECT_TIMEOUT, e.target.value)
            }
          />
          <p className="text-xs text-text-muted mt-1.5">上游连接超时时间</p>
        </div>

        <div>
          <label className="block text-sm font-medium text-text-primary mb-1.5">
            First Byte Timeout (seconds)
            <ModifiedBadge
              configKey={CONFIG_KEYS.FIRST_BYTE_TIMEOUT}
              currentValue={getValue(
                CONFIG_KEYS.FIRST_BYTE_TIMEOUT,
                DEFAULTS.FIRST_BYTE_TIMEOUT,
              )}
              getDefault={getDefault}
            />
          </label>
          <input
            type="number"
            className="input"
            min={FORM_CONSTRAINTS.MIN_ZERO}
            value={getValue(
              CONFIG_KEYS.FIRST_BYTE_TIMEOUT,
              DEFAULTS.FIRST_BYTE_TIMEOUT,
            )}
            onChange={(e) =>
              handleChange(CONFIG_KEYS.FIRST_BYTE_TIMEOUT, e.target.value)
            }
          />
          <p className="text-xs text-text-muted mt-1.5">
            等待首字节的最大时长 (0 = 无限制)
          </p>
        </div>

        <div>
          <label className="block text-sm font-medium text-text-primary mb-1.5">
            Read Timeout (seconds)
            <ModifiedBadge
              configKey={CONFIG_KEYS.UPSTREAM_READ_TIMEOUT}
              currentValue={getValue(
                CONFIG_KEYS.UPSTREAM_READ_TIMEOUT,
                DEFAULTS.UPSTREAM_READ_TIMEOUT,
              )}
              getDefault={getDefault}
            />
          </label>
          <input
            type="number"
            className="input"
            min={FORM_CONSTRAINTS.MIN_ZERO}
            value={getValue(
              CONFIG_KEYS.UPSTREAM_READ_TIMEOUT,
              DEFAULTS.UPSTREAM_READ_TIMEOUT,
            )}
            onChange={(e) =>
              handleChange(CONFIG_KEYS.UPSTREAM_READ_TIMEOUT, e.target.value)
            }
          />
          <p className="text-xs text-text-muted mt-1.5">
            读取响应的超时时间 (0 = 无限制)
          </p>
        </div>

        <div>
          <label className="block text-sm font-medium text-text-primary mb-1.5">
            SSE Idle Timeout (seconds)
            <ModifiedBadge
              configKey={CONFIG_KEYS.SSE_IDLE_TIMEOUT}
              currentValue={getValue(
                CONFIG_KEYS.SSE_IDLE_TIMEOUT,
                DEFAULTS.SSE_IDLE_TIMEOUT,
              )}
              getDefault={getDefault}
            />
          </label>
          <input
            type="number"
            className="input"
            min={FORM_CONSTRAINTS.MIN_ZERO}
            value={getValue(
              CONFIG_KEYS.SSE_IDLE_TIMEOUT,
              DEFAULTS.SSE_IDLE_TIMEOUT,
            )}
            onChange={(e) =>
              handleChange(CONFIG_KEYS.SSE_IDLE_TIMEOUT, e.target.value)
            }
          />
          <p className="text-xs text-text-muted mt-1.5">
            SSE 流空闲超时 (0 = 无限制)
          </p>
        </div>
      </div>
    </ConfigSection>
  );
}

export function RequestLimitsSection({
  getValue,
  handleChange,
  getDefault,
}: SectionProps) {
  const globalMaxAttempts = getValue(
    CONFIG_KEYS.GLOBAL_MAX_ATTEMPTS,
    DEFAULTS.GLOBAL_MAX_ATTEMPTS,
  );

  return (
    <ConfigSection
      title="Request Limits & Retry"
      description="Configure request constraints and retry behavior across providers."
      icon="🔄"
    >
      <div className="space-y-6">
        {/* Global Max Attempts - Main setting */}
        <div className="p-4 rounded-xl bg-bg-secondary border border-border-light">
          <div className="flex items-start gap-3">
            <div className="flex-1">
              <label className="block text-sm font-medium text-text-primary mb-1.5">
                Global Max Attempts
                <ModifiedBadge
                  configKey={CONFIG_KEYS.GLOBAL_MAX_ATTEMPTS}
                  currentValue={globalMaxAttempts}
                  getDefault={getDefault}
                />
              </label>
              <div className="flex items-center gap-3">
                <input
                  type="number"
                  className="input w-24"
                  min={FORM_CONSTRAINTS.MIN_ZERO}
                  max={FORM_CONSTRAINTS.MAX_GLOBAL_ATTEMPTS}
                  value={globalMaxAttempts}
                  onChange={(e) =>
                    handleChange(
                      CONFIG_KEYS.GLOBAL_MAX_ATTEMPTS,
                      e.target.value,
                    )
                  }
                />
                <span className="text-sm text-text-secondary">
                  {globalMaxAttempts === "0" ? (
                    <span className="inline-flex items-center gap-1.5 px-2 py-0.5 rounded-full bg-success/10 text-success text-xs">
                      <span className="w-1.5 h-1.5 rounded-full bg-success"></span>
                      无限制 - 遍历所有可用 Provider
                    </span>
                  ) : (
                    <span className="inline-flex items-center gap-1.5 px-2 py-0.5 rounded-full bg-warning/10 text-warning text-xs">
                      <span className="w-1.5 h-1.5 rounded-full bg-warning"></span>
                      最多尝试 {globalMaxAttempts} 次
                    </span>
                  )}
                </span>
              </div>
              <p className="text-xs text-text-muted mt-2">
                整个请求最多尝试的 Provider 次数总和。设为 0
                表示无限制，会依次遍历所有可用的 Provider 直到成功。
              </p>
            </div>
          </div>
        </div>

        {/* Visual Flow Diagram */}
        <div className="px-4 py-3 rounded-lg bg-primary/5 border border-primary/10">
          <p className="text-xs font-medium text-primary mb-2">
            💡 重试流程说明
          </p>
          <div className="flex items-center gap-2 text-xs text-text-secondary overflow-x-auto pb-1">
            <span className="flex items-center gap-1 px-2 py-1 rounded bg-bg-primary border border-border whitespace-nowrap">
              <span className="text-primary">Provider A</span>
              <span className="text-text-muted">(重试 N 次)</span>
            </span>
            <span className="text-text-muted">→</span>
            <span className="flex items-center gap-1 px-2 py-1 rounded bg-bg-primary border border-border whitespace-nowrap">
              <span className="text-primary">Provider B</span>
              <span className="text-text-muted">(重试 M 次)</span>
            </span>
            <span className="text-text-muted">→</span>
            <span className="text-text-muted whitespace-nowrap">...</span>
            <span className="text-text-muted">→</span>
            <span className="px-2 py-1 rounded bg-success/10 text-success border border-success/20 whitespace-nowrap">
              成功 ✓
            </span>
          </div>
          <p className="text-xs text-text-muted mt-2">
            每个 Provider 的重试次数由其自身的{" "}
            <code className="px-1 py-0.5 rounded bg-bg-secondary text-text-primary">
              max_retries
            </code>{" "}
            设置控制（默认 0 = 试一次就切换）
          </p>
        </div>

        {/* Other Limits */}
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          <div>
            <label className="block text-sm font-medium text-text-primary mb-1.5">
              Max Body Size (MB)
              <ModifiedBadge
                configKey={CONFIG_KEYS.MAX_BODY_SIZE}
                currentValue={getValue(
                  CONFIG_KEYS.MAX_BODY_SIZE,
                  DEFAULTS.MAX_BODY_SIZE_MB,
                )}
                getDefault={getDefault}
              />
            </label>
            <input
              type="number"
              className="input"
              min={FORM_CONSTRAINTS.MIN_POSITIVE}
              value={getValue(
                CONFIG_KEYS.MAX_BODY_SIZE,
                DEFAULTS.MAX_BODY_SIZE_MB,
              )}
              onChange={(e) =>
                handleChange(CONFIG_KEYS.MAX_BODY_SIZE, e.target.value)
              }
            />
            <p className="text-xs text-text-muted mt-1.5">
              最大允许的请求体大小
            </p>
          </div>
        </div>
      </div>
    </ConfigSection>
  );
}

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
