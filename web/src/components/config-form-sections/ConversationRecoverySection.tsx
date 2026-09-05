import {
  CONFIG_KEYS,
  DEFAULTS,
  CONVERSATION_RECOVERY_POLICY_OPTIONS,
} from "../../config";
import { ConfigSection } from "../ConfigSection";
import { ModifiedBadge } from "./ModifiedBadge";
import type { SectionProps } from "./types";

export function ConversationRecoverySection({
  getValue,
  handleChange,
  getDefault,
}: SectionProps) {
  const currentPolicy = getValue(
    CONFIG_KEYS.CONVERSATION_RECOVERY_POLICY,
    DEFAULTS.CONVERSATION_RECOVERY_POLICY,
  );

  return (
    <ConfigSection
      title="Codex 对话恢复"
      description="允许换账号时，按现有粘性和选路策略选择可用账号，并原样传递对话状态。设置从后续请求或 WebSocket 重连开始生效。"
      icon="🔄"
    >
      <label
        htmlFor={CONFIG_KEYS.CONVERSATION_RECOVERY_POLICY}
        className="block text-sm font-medium text-text-primary mb-1.5"
      >
        对话恢复策略
        <ModifiedBadge
          configKey={CONFIG_KEYS.CONVERSATION_RECOVERY_POLICY}
          currentValue={currentPolicy}
          getDefault={getDefault}
        />
      </label>
      <select
        id={CONFIG_KEYS.CONVERSATION_RECOVERY_POLICY}
        className="input"
        value={currentPolicy}
        aria-describedby="conversation-recovery-warning"
        onChange={(e) =>
          handleChange(CONFIG_KEYS.CONVERSATION_RECOVERY_POLICY, e.target.value)
        }
      >
        {CONVERSATION_RECOVERY_POLICY_OPTIONS.map((option) => (
          <option key={option.value} value={option.value}>
            {option.label}
          </option>
        ))}
      </select>
      <p
        id="conversation-recovery-warning"
        className="text-xs text-warning mt-1.5"
      >
        切回固定原账号后，已跨账号续聊的对话可能无法继续。
      </p>
    </ConfigSection>
  );
}
