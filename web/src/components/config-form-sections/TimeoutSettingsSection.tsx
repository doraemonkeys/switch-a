import { CONFIG_KEYS, DEFAULTS, FORM_CONSTRAINTS } from "../../config";
import { ConfigSection } from "../ConfigSection";
import { ModifiedBadge } from "./ModifiedBadge";
import type { SectionProps } from "./types";

export function TimeoutSettingsSection({
  getValue,
  handleChange,
  getDefault,
}: SectionProps) {
  return (
    <ConfigSection
      title="Timeout Settings"
      description="Configure connection, first-byte, and idle timeouts for upstream requests."
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
            Response Idle Timeout (seconds)
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
            响应开始后，若连续该时长未收到新数据则中断 (0 = 无限制)
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
