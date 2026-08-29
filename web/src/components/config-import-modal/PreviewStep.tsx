import type { ImportMode, ImportPreviewResponse } from "../../api/types";
import { IMPORT_SUMMARY_SECTIONS } from "./constants";
import { FileCard } from "./common";
import { getVisibleSummaryKeys } from "./helpers";
import { ReauthenticationNotice } from "./ReauthenticationNotice";
const WarningIcon = () => (
  <svg
    className="w-4 h-4 text-warning shrink-0"
    fill="none"
    viewBox="0 0 24 24"
  >
    <path
      stroke="currentColor"
      strokeLinecap="round"
      strokeLinejoin="round"
      strokeWidth={2}
      d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z"
    />
  </svg>
);

function ChangeBadge({
  label,
  add,
  update,
  deleteCount,
  unchanged,
}: {
  label: string;
  add: number;
  update: number;
  deleteCount: number;
  unchanged: number;
}) {
  const hasMutations = add > 0 || update > 0 || deleteCount > 0;
  return (
    <div
      className={`p-4 rounded-lg border ${hasMutations ? "bg-bg-tertiary border-border-light" : "bg-bg-tertiary/50 border-border-dark"}`}
    >
      <div className="text-sm font-medium text-text-secondary mb-2">
        {label}
      </div>
      <div className="flex gap-4 flex-wrap">
        <div className="flex items-center gap-1.5">
          <span
            className={`w-2 h-2 rounded-full ${add > 0 ? "bg-success" : "bg-text-muted/30"}`}
          />
          <span
            className={`text-sm ${add > 0 ? "text-success" : "text-text-muted"}`}
          >
            +{add} 新增
          </span>
        </div>
        <div className="flex items-center gap-1.5">
          <span
            className={`w-2 h-2 rounded-full ${update > 0 ? "bg-primary" : "bg-text-muted/30"}`}
          />
          <span
            className={`text-sm ${update > 0 ? "text-primary" : "text-text-muted"}`}
          >
            {update} 更新
          </span>
        </div>
        <div className="flex items-center gap-1.5">
          <span
            className={`w-2 h-2 rounded-full ${deleteCount > 0 ? "bg-danger" : "bg-text-muted/30"}`}
          />
          <span
            className={`text-sm ${deleteCount > 0 ? "text-danger" : "text-text-muted"}`}
          >
            -{deleteCount} 删除
          </span>
        </div>
        <div className="flex items-center gap-1.5">
          <span
            className={`w-2 h-2 rounded-full ${unchanged > 0 ? "bg-text-secondary" : "bg-text-muted/30"}`}
          />
          <span
            className={`text-sm ${unchanged > 0 ? "text-text-secondary" : "text-text-muted"}`}
          >
            {unchanged} 无变化
          </span>
        </div>
      </div>
    </div>
  );
}

export function PreviewStep({
  selectedFile,
  preview,
  mode,
  hasAnyChanges,
  importing,
  onBackToSelect,
}: {
  selectedFile: File;
  preview: ImportPreviewResponse;
  mode: ImportMode;
  hasAnyChanges: boolean;
  importing: boolean;
  onBackToSelect: () => void;
}) {
  const visibleSummaryKeys = getVisibleSummaryKeys(mode);
  const warnings = preview.warnings ?? [];

  return (
    <div className="space-y-4">
      <FileCard
        selectedFile={selectedFile}
        actionLabel="修改范围"
        onAction={onBackToSelect}
        disabled={importing}
      />

      <div className="space-y-3">
        <h3 className="text-sm font-medium text-text-secondary">变更预览</h3>
        <div className="grid gap-3">
          {visibleSummaryKeys.map((key) => {
            const change = preview.changes[key];
            return (
              <ChangeBadge
                key={key}
                label={IMPORT_SUMMARY_SECTIONS[key].label}
                add={change.add}
                update={change.update}
                deleteCount={change.delete}
                unchanged={change.unchanged}
              />
            );
          })}
        </div>
      </div>

      {warnings.length > 0 && (
        <div className="space-y-2">
          <h3 className="text-sm font-medium text-warning flex items-center gap-1.5">
            <WarningIcon />
            警告信息
          </h3>
          <div className="bg-warning/10 border border-warning/20 rounded-lg p-3 space-y-1.5">
            {warnings.map((warning, index) => (
              <p
                key={`${warning}-${index}`}
                className="text-sm text-warning/90 flex items-start gap-2"
              >
                <span className="text-warning/60">•</span>
                {warning}
              </p>
            ))}
          </div>
        </div>
      )}

      <ReauthenticationNotice
        requirements={preview.credential_reauthentication_requirements ?? []}
      />

      {!hasAnyChanges && (
        <div className="bg-bg-tertiary border border-border-light rounded-lg p-4 text-center">
          <p className="text-text-secondary">
            没有检测到任何变更，配置已是最新
          </p>
        </div>
      )}
    </div>
  );
}
