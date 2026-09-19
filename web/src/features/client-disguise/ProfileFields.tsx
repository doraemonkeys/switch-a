import { SlidersHorizontal } from "lucide-react";
import type { DisguiseState } from "@/api/client-disguise/types";
import {
  changeEnvironment,
  changeProfileSelection,
  effectiveProfile,
  profileSelection,
  type LoginDraft,
} from "./loginDraft";
import { ProfileSummary } from "./ProfileSummary";
import { CLIENT_TYPES } from "./profiles/clientTypes";
import {
  profileEnvironments,
  environmentLabel,
} from "./profiles/profileCatalog";
import { ProfileMode } from "./profiles/ProfileMode";
import { ReferenceFollow } from "./profiles/ReferenceFollow";
import { SnapshotPicker } from "./profiles/SnapshotPicker";
import "./profiles/profile-editor.css";

export function ProfileFields({
  state,
  draft,
  change,
}: {
  state: DisguiseState;
  draft: LoginDraft;
  change: (draft: LoginDraft) => void;
}) {
  const environments = profileEnvironments(state.profiles);
  const selectedTuple = environments.find(
    ([key]) => key === draft.environment,
  )?.[1];
  const clientType = CLIENT_TYPES[selectedTuple?.client_type ?? ""];
  const selection = profileSelection(draft);
  const effective = effectiveProfile(draft, state);
  const officialVersion = state.official_version?.release.version;
  const syncFallback = officialVersion
    ? ""
    : "首次同步成功前，沿用快照的版本规则。";
  return (
    <section
      className="cd-editor-section cd-profile-editor"
      aria-labelledby="profile-heading"
    >
      <div className="cd-section-heading">
        <SlidersHorizontal size={17} aria-hidden="true" />
        <h3 id="profile-heading">环境配置</h3>
      </div>
      <p className="cd-description">
        选择该登录凭据向上游呈现的客户端环境和版本。
      </p>
      <label className="cd-field cd-reference-field">
        客户端环境
        <select
          aria-label="客户端环境"
          aria-describedby="client-environment-help"
          value={draft.environment}
          disabled={environments.length === 0}
          onChange={(event) =>
            change(changeEnvironment(draft, event.target.value, state))
          }
        >
          <option value="" disabled>
            选择客户端环境
          </option>
          {draft.environment &&
            !environments.some(([key]) => key === draft.environment) && (
              <option value={draft.environment}>
                环境不可用：{draft.environment}
              </option>
            )}
          {environments.map(([key, tuple]) => (
            <option key={key} value={key}>
              {environmentLabel(tuple)}
            </option>
          ))}
        </select>
        <span id="client-environment-help" className="cd-field-help">
          {clientType ? (
            <>
              {clientType.description} 内置 Originator：
              <code>{clientType.originator}</code>。
            </>
          ) : (
            "可选组合来自已有配置与采样。"
          )}
        </span>
      </label>
      {environments.length === 0 ? (
        <p className="cd-description">
          暂无环境快照，请先在 Reference library 中导入应用层样本。
        </p>
      ) : (
        draft.environment && (
          <>
            <ProfileMode draft={draft} change={change} />
            {draft.mode === "auto" ? (
              <ReferenceFollow state={state} draft={draft} change={change} />
            ) : (
              <SnapshotPicker
                key={draft.environment}
                state={state}
                environment={draft.environment}
                revisionID={selection.revisionID}
                select={(revisionID) =>
                  change(changeProfileSelection(draft, { revisionID }))
                }
              />
            )}
            {!effective && (
              <p className="cd-field-help">
                当前快照不可用，请选择可用环境，或在固定快照模式下重新选择。
              </p>
            )}
          </>
        )
      )}
      <label className="cd-field cd-profile-version">
        发送版本来源
        <select
          aria-label="发送版本来源"
          value={draft.versionSource}
          onChange={(event) =>
            change({
              ...draft,
              versionSource: event.target.value as LoginDraft["versionSource"],
            })
          }
        >
          <option value="">使用快照采集版本</option>
          <option value="official_stable">同步 Codex CLI 官方稳定版</option>
        </select>
        <span className="cd-field-help">
          {draft.versionSource === "official_stable"
            ? `版本号与 User-Agent 中的 Codex 版本独立更新，固定快照时环境特征仍保持不变。${syncFallback}`
            : "使用所选快照的版本规则；未采集 User-Agent 时保留原请求版本。"}
        </span>
      </label>
      {effective && (
        <ProfileSummary profile={effective} state={state} draft={draft} />
      )}
    </section>
  );
}
