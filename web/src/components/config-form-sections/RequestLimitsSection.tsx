import { CONFIG_KEYS, DEFAULTS, FORM_CONSTRAINTS } from "../../config";
import { ConfigSection } from "../ConfigSection";
import { ModifiedBadge } from "./ModifiedBadge";
import type { SectionProps } from "./types";

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
