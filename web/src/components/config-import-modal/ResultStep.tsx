import type { ImportMode, ImportResult } from "../../api/types";
import { IMPORT_SUMMARY_SECTIONS } from "./constants";
import { getVisibleSummaryKeys } from "./helpers";
import { ReauthenticationNotice } from "./ReauthenticationNotice";
const CheckCircleIcon = () => (
  <svg className="w-16 h-16 text-success" fill="none" viewBox="0 0 24 24">
    <path
      stroke="currentColor"
      strokeLinecap="round"
      strokeLinejoin="round"
      strokeWidth={2}
      d="M9 12l2 2 4-4m6 2a9 9 0 11-18 0 9 9 0 0118 0z"
    />
  </svg>
);

function AppliedBadge({
  label,
  added,
  updated,
  deleted,
}: {
  label: string;
  added: number;
  updated: number;
  deleted: number;
}) {
  const total = added + updated + deleted;
  return (
    <div className="flex items-center justify-between py-2">
      <span className="text-text-secondary">{label}</span>
      <div className="flex items-center gap-3">
        {added > 0 && (
          <span className="text-sm text-success">+{added} 新增</span>
        )}
        {updated > 0 && (
          <span className="text-sm text-primary">{updated} 更新</span>
        )}
        {deleted > 0 && (
          <span className="text-sm text-danger">-{deleted} 删除</span>
        )}
        {total === 0 && <span className="text-sm text-text-muted">无变更</span>}
      </div>
    </div>
  );
}

export function ResultStep({
  result,
  mode,
}: {
  result: ImportResult;
  mode: ImportMode;
}) {
  const visibleSummaryKeys = getVisibleSummaryKeys(mode);
  const reauthenticationRequirements =
    result.credential_reauthentication_requirements ?? [];

  return (
    <div className="space-y-6">
      <div className="flex flex-col items-center gap-3 py-4">
        <CheckCircleIcon />
        <div className="text-center">
          <h3 className="text-lg font-medium text-text-primary">导入成功</h3>
          <p className="text-sm text-text-secondary mt-1">
            {reauthenticationRequirements.length > 0
              ? "配置已更新；需重新认证的 ChatGPT Provider 将在连接后生效"
              : "配置已更新，变更立即生效"}
          </p>
        </div>
      </div>

      <div className="bg-bg-tertiary rounded-lg p-4 divide-y divide-border-dark">
        {visibleSummaryKeys.map((key) => {
          const applied = result.applied[key];
          if (!applied) return null;
          return (
            <AppliedBadge
              key={key}
              label={IMPORT_SUMMARY_SECTIONS[key].label}
              added={applied.added}
              updated={applied.updated}
              deleted={applied.deleted}
            />
          );
        })}
      </div>

      <ReauthenticationNotice
        requirements={reauthenticationRequirements}
        imported
      />
    </div>
  );
}
