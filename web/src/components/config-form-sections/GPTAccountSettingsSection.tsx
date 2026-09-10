import { CONFIG_KEYS, DEFAULTS } from "../../config";
import { ConfigSection } from "../ConfigSection";
import { ModifiedBadge } from "./ModifiedBadge";
import type { SectionProps } from "./types";

const CLIENT_MODE_OPTIONS = [
  {
    value: DEFAULTS.GPT_ACCOUNT_FALLBACK_CLIENT,
    label: "保持当前行为（默认）",
  },
  { value: "official_stable", label: "使用官方稳定版本特征" },
] as const;

export function GPTAccountSettingsSection({
  getValue,
  handleChange,
  getDefault,
}: SectionProps) {
  const mode = getValue(
    CONFIG_KEYS.GPT_ACCOUNT_FALLBACK_CLIENT,
    DEFAULTS.GPT_ACCOUNT_FALLBACK_CLIENT,
  );
  return (
    <ConfigSection
      title="GPT Account Requests"
      description="GPT 登录、Token 刷新和额度查询的客户端特征。"
      icon="🔑"
    >
      <label
        htmlFor={CONFIG_KEYS.GPT_ACCOUNT_FALLBACK_CLIENT}
        className="block text-sm font-medium text-text-primary mb-1.5"
      >
        无伪装 UA 时的客户端特征
        <ModifiedBadge
          configKey={CONFIG_KEYS.GPT_ACCOUNT_FALLBACK_CLIENT}
          currentValue={mode}
          getDefault={getDefault}
        />
      </label>
      <select
        id={CONFIG_KEYS.GPT_ACCOUNT_FALLBACK_CLIENT}
        className="input max-w-md"
        value={mode}
        onChange={(event) =>
          handleChange(
            CONFIG_KEYS.GPT_ACCOUNT_FALLBACK_CLIENT,
            event.target.value,
          )
        }
      >
        {CLIENT_MODE_OPTIONS.map((option) => (
          <option key={option.value} value={option.value}>
            {option.label}
          </option>
        ))}
      </select>
      <p className="text-xs text-text-muted mt-1.5">
        {mode === DEFAULTS.GPT_ACCOUNT_FALLBACK_CLIENT
          ? "优先使用凭据绑定的客户端 UA；没有可用 UA 时使用 switch-a/版本。"
          : "无可用伪装 UA 时，使用内置 Codex Desktop 模板并同步官方稳定版本；首次同步完成前沿用模板版本。"}
      </p>
      <p className="text-xs text-text-muted mt-1.5">
        伪装配置中的 UA
        及版本规则始终优先。保存后从下一次账号操作生效；浏览器授权页面仍使用浏览器自身的
        UA。
      </p>
    </ConfigSection>
  );
}
