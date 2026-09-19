import { ArrowUpRight, ChevronDown } from "lucide-react";
import type {
  DisguiseState,
  ProfileRevision,
} from "@/api/client-disguise/types";
import type { LoginDraft } from "./loginDraft";
import {
  captureLabel,
  environmentLabel,
  sourceName,
} from "./profiles/profileCatalog";
import {
  featureFields,
  requestVersion,
  sampledUserAgent,
  UNOBSERVED,
} from "./profiles/profileFeatures";

export function ProfileSummary({
  profile,
  state,
  draft,
}: {
  profile: ProfileRevision;
  state: DisguiseState;
  draft: LoginDraft;
}) {
  const official =
    draft.versionSource === "official_stable"
      ? state.official_version?.release.version
      : "";
  const version = official || requestVersion(profile);
  const versionOrigin = official ? "官方稳定版" : "来自快照";
  return (
    <div
      className="cd-profile-preview cd-profile-result"
      aria-label="生效预览"
      aria-live="polite"
    >
      <dl className="cd-profile-effective">
        <div>
          <dt>环境配置</dt>
          <dd>
            {environmentLabel(profile.tuple)} ·{" "}
            {draft.mode === "auto" ? "自动跟随" : "固定快照"}
          </dd>
        </div>
        <div>
          <dt>实际发送版本</dt>
          <dd>
            {version ? `${version} · ${versionOrigin}` : "沿用原请求版本"}
          </dd>
        </div>
      </dl>
      {!sampledUserAgent(profile) && (
        <p className="cd-field-help cd-partial-profile">
          部分特征：未采集完整 User-Agent。选择已知入口标识时，会同步调整原
          User-Agent 中可识别的 Codex 产品名，系统和终端信息沿用原请求。
          {official
            ? "版本字段使用官方稳定版，原 User-Agent 中可识别的 Codex 版本会同步更新。"
            : "客户端版本沿用原请求。"}
        </p>
      )}
      <details className="cd-profile-details">
        <summary>
          快照详情与完整 ID <ChevronDown size={14} aria-hidden="true" />
        </summary>
        <dl className="cd-detail-grid">
          <div>
            <dt>快照 ID</dt>
            <dd aria-label="快照 ID">{profile.id}</dd>
          </div>
          <div>
            <dt>采集版本</dt>
            <dd>{profile.client_version}</dd>
          </div>
          <div>
            <dt>来源</dt>
            <dd>
              {sourceName(profile.source_id, state.references)} ·{" "}
              {profile.source_id}
            </dd>
          </div>
          <div>
            <dt>采集时间</dt>
            <dd>{captureLabel(profile)}</dd>
          </div>
          {featureFields(profile).map((field) => (
            <div key={field.key}>
              <dt>{field.label}</dt>
              <dd>{field.value || UNOBSERVED}</dd>
            </div>
          ))}
        </dl>
        {profile.source_url && (
          <a
            className="cd-text-link"
            href={profile.source_url}
            target="_blank"
            rel="noreferrer"
          >
            查看采样依据 <ArrowUpRight size={13} aria-hidden="true" />
          </a>
        )}
        <p className="cd-field-help">传输层特征由高级设置中的独立样本决定。</p>
      </details>
    </div>
  );
}
