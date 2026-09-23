import { ArrowUpRight, ChevronDown } from "lucide-react";
import { Link, useSearchParams } from "react-router";
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
  const hasUserAgentSample = Boolean(sampledUserAgent(profile));
  const [params] = useSearchParams();
  const referenceParams = new URLSearchParams(params);
  referenceParams.set("view", "references");
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
          <dt>普通请求 UA</dt>
          <dd>{hasUserAgentSample ? "使用采样 UA" : "按所选环境调整原 UA"}</dd>
        </div>
        <div>
          <dt>推理请求版本</dt>
          <dd>
            {version ? `${version} · ${versionOrigin}` : "沿用原请求版本"}
          </dd>
        </div>
        <div>
          <dt>账号请求 UA</dt>
          <dd>
            {hasUserAgentSample
              ? "使用所选快照的 UA 与版本规则"
              : "使用全局「GPT 账号请求」回退配置"}
          </dd>
        </div>
      </dl>
      <p className="cd-field-help">
        Browser Use 请求自动保留 codex-browser-use 身份，共享所选平台、架构和
        Codex 版本，保留原请求的终端信息和调用方版本。指定参考客户端后， 其
        Browser Use UA 也会单独采集，不会替换普通请求的 UA。
      </p>
      {!hasUserAgentSample && (
        <>
          <p className="cd-field-help cd-partial-profile">
            该快照未采集完整 UA；可识别的 Codex UA
            仍会应用所选客户端身份、平台、架构和已知版本。
            未采集的系统版本与终端信息沿用原请求；跨平台时不沿用原系统版本。{" "}
            如需使用采样 UA，请在{" "}
            <Link
              className="cd-text-link"
              to={`/client-disguise?${referenceParams}`}
            >
              Reference library
            </Link>{" "}
            采集或导入包含完整 User-Agent 的样本，再选择对应快照。
          </p>
          <p className="cd-field-help cd-partial-profile">
            Token 刷新和额度查询使用全局回退
            UA，可能与推理请求不同。在全局配置的 「认证与账号 → GPT
            账号请求」中查看。{" "}
            <Link
              className="cd-text-link"
              to="/config"
              target="_blank"
              rel="noreferrer"
            >
              查看全局配置（新标签页）
            </Link>
          </p>
        </>
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
