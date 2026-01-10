import { CONFIG_DEFAULTS, FORM_CONSTRAINTS } from '../config'

export function Config() {
  return (
    <div className="space-y-6">
      {/* Page Header */}
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-2xl font-bold text-text-primary">Configuration</h2>
          <p className="text-text-secondary mt-1">运行时配置管理</p>
        </div>
        <span className="badge badge-success">
          <span className="w-2 h-2 bg-success rounded-full mr-1.5"></span>
          Synced
        </span>
      </div>

      <div className="card">
        <form className="space-y-8">
          {/* Auth Settings */}
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
                <select className="input">
                  <option value="auto">Auto (detect from request)</option>
                  <option value="bearer">Bearer Token</option>
                  <option value="x-api-key">X-API-Key</option>
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
                  defaultValue="X-User-ID"
                  placeholder="X-User-ID"
                />
                <p className="text-xs text-text-muted mt-1.5">
                  用于识别用户的请求头名称
                </p>
              </div>
            </div>
          </ConfigSection>

          {/* Sticky Session */}
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
                  defaultChecked
                />
                <div>
                  <span className="font-medium text-text-primary">Enable sticky session</span>
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
                  defaultValue={CONFIG_DEFAULTS.STICKY_TTL_SECONDS}
                  min={FORM_CONSTRAINTS.MIN_ZERO}
                />
              </div>
            </div>
          </ConfigSection>

          {/* Circuit Breaker */}
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
                  defaultValue={CONFIG_DEFAULTS.CIRCUIT_BREAKER.FAILURE_THRESHOLD}
                  min={FORM_CONSTRAINTS.MIN_POSITIVE}
                />
                <p className="text-xs text-text-muted mt-1.5">
                  触发熔断的失败次数
                </p>
              </div>
              <div>
                <label className="block text-sm font-medium text-text-primary mb-1.5">
                  Window (seconds)
                </label>
                <input
                  type="number"
                  className="input"
                  defaultValue={CONFIG_DEFAULTS.CIRCUIT_BREAKER.WINDOW_SECONDS}
                  min={FORM_CONSTRAINTS.MIN_POSITIVE}
                />
                <p className="text-xs text-text-muted mt-1.5">
                  检测窗口时长
                </p>
              </div>
              <div>
                <label className="block text-sm font-medium text-text-primary mb-1.5">
                  Disable Duration (seconds)
                </label>
                <input
                  type="number"
                  className="input"
                  defaultValue={
                    CONFIG_DEFAULTS.CIRCUIT_BREAKER.DISABLE_DURATION_SECONDS
                  }
                  min={FORM_CONSTRAINTS.MIN_POSITIVE}
                />
                <p className="text-xs text-text-muted mt-1.5">
                  熔断禁用时长
                </p>
              </div>
            </div>
          </ConfigSection>

          {/* Form Actions */}
          <div className="flex items-center justify-between pt-6 border-t border-border">
            <p className="text-sm text-text-muted">
              Changes take effect immediately after saving.
            </p>
            <div className="flex gap-3">
              <button type="button" className="btn btn-secondary">
                Reset
              </button>
              <button type="submit" className="btn btn-primary">
                <span>💾</span>
                Save Changes
              </button>
            </div>
          </div>
        </form>
      </div>
    </div>
  )
}

interface ConfigSectionProps {
  title: string
  description: string
  icon: string
  children: React.ReactNode
}

function ConfigSection({ title, description, icon, children }: ConfigSectionProps) {
  return (
    <fieldset className="space-y-4">
      <div className="flex items-start gap-3">
        <div className="w-10 h-10 rounded-xl bg-bg-secondary flex items-center justify-center shrink-0">
          <span className="text-lg">{icon}</span>
        </div>
        <div>
          <legend className="text-lg font-semibold text-text-primary">{title}</legend>
          <p className="text-sm text-text-secondary">{description}</p>
        </div>
      </div>
      <div className="ml-13">{children}</div>
    </fieldset>
  )
}
