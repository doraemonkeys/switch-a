import type { DisguiseState } from "@/api/client-disguise/types";
import {
  changeProfileSelection,
  effectiveProfile,
  profileSelection,
  selectedProfile,
  type LoginDraft,
} from "../loginDraft";
import {
  availableReferences,
  captureLabel,
  environmentKey,
  referenceHead,
  sourceName,
} from "./profileCatalog";

export function ReferenceFollow({
  state,
  draft,
  change,
}: {
  state: DisguiseState;
  draft: LoginDraft;
  change: (draft: LoginDraft) => void;
}) {
  const selection = profileSelection(draft);
  const references = availableReferences(
    state,
    draft.environment,
    selection.reference,
  );
  const baseline = selectedProfile(draft, state);
  const effective = effectiveProfile(draft, state);
  const head = referenceHead(state, draft.environment, selection.reference);
  let status = "采用来源当前快照，并跟随该环境后续的有效采样。";
  if (!selection.reference) status = "未设置参考来源，保留当前快照。";
  else if (!head)
    status = "来源暂无该环境的有效跟随快照，保留当前快照并等待后续采样。";
  else if (effective?.id !== head.id)
    status = "来源当前版本较旧，保留当前快照，避免降级。";
  return (
    <div className="cd-reference-follow">
      <label className="cd-field cd-reference-field">
        参考来源
        <select
          aria-label="参考来源"
          value={selection.reference}
          onChange={(event) =>
            change(
              changeProfileSelection(draft, { reference: event.target.value }),
            )
          }
        >
          <option value="">暂不跟随来源</option>
          {selection.reference &&
            !references.some((source) => source.id === selection.reference) && (
              <option value={selection.reference}>
                来源不可用：{selection.reference}
              </option>
            )}
          {references.map((source) => {
            const sampled = state.profiles.some(
              (profile) =>
                profile.source_id === source.id &&
                environmentKey(profile.tuple) === draft.environment,
            );
            return (
              <option key={source.id} value={source.id}>
                {source.name}
                {!sampled && " · 暂无该环境采样"}
              </option>
            );
          })}
        </select>
      </label>
      <p className="cd-field-help">{status}</p>
      {effective && (
        <div className="cd-follow-preview">
          <span>当前采用</span>
          <strong>{effective.client_version}</strong>
          <small>
            {sourceName(effective.source_id, state.references)} ·{" "}
            {captureLabel(effective)}
          </small>
        </div>
      )}
      {baseline && effective?.id !== baseline.id && (
        <p className="cd-field-help">
          原选快照：{baseline.client_version}。切回固定快照可继续使用原选择。
        </p>
      )}
    </div>
  );
}
