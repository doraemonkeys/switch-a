import { useId } from "react";
import {
  DEFAULT_CODEX_CONTINUATION,
  type CodexContinuationPolicy,
  type CodexContinuationBoundary,
} from "./policy";

export function ProviderContinuationFields({
  value,
  onChange,
}: {
  value?: CodexContinuationPolicy;
  onChange: (value: CodexContinuationPolicy) => void;
}) {
  const id = useId();
  const policy = value ?? DEFAULT_CODEX_CONTINUATION;
  return (
    <fieldset className="rounded-lg border border-border p-4 space-y-3">
      <legend className="px-1 font-medium text-text-primary">
        Codex 对话续接策略
      </legend>
      <p className="text-xs text-text-secondary">
        仅作用于 Codex HTTP 和 WebSocket。优先继续使用当前
        Provider；切换时同时检查来源的转出策略和目标的接入策略，故障转移时继续遵守
        Vendor Isolation。
      </p>
      <div className="grid grid-cols-2 gap-4">
        <div>
          <label htmlFor={`${id}-outbound`} className="block text-sm mb-1">
            对话转出
          </label>
          <select
            id={`${id}-outbound`}
            className="input"
            value={policy.outbound}
            onChange={(event) =>
              onChange({
                ...policy,
                outbound: event.target.value as CodexContinuationBoundary,
              })
            }
          >
            <option value="any">允许转出（含跨账号）</option>
            <option value="same_identity">仅允许转到同上游身份</option>
            <option value="none">不允许转出</option>
          </select>
        </div>
        <div>
          <label htmlFor={`${id}-inbound`} className="block text-sm mb-1">
            外部对话接入
          </label>
          <select
            id={`${id}-inbound`}
            className="input"
            value={policy.inbound}
            onChange={(event) =>
              onChange({
                ...policy,
                inbound: event.target.value as CodexContinuationBoundary,
              })
            }
          >
            <option value="none">不接受外部对话</option>
            <option value="same_identity">仅接受同上游身份的对话</option>
            <option value="any">接受外部对话（含跨账号）</option>
          </select>
        </div>
      </div>
      <p className="text-xs text-text-muted">
        新对话正常选路，当前 Provider
        已承接的对话可继续使用。跨账号时原样传递对话状态，能否恢复取决于上游。
      </p>
    </fieldset>
  );
}
