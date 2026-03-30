import type { ImportStep } from "./types";
export function ModalFooter({
  step,
  importing,
  previewing,
  canPreview,
  canConfirmImport,
  onClose,
  onBackToSelect,
  onPreviewImport,
  onConfirmImport,
}: {
  step: ImportStep;
  importing: boolean;
  previewing: boolean;
  canPreview: boolean;
  canConfirmImport: boolean;
  onClose: () => void;
  onBackToSelect: () => void;
  onPreviewImport: () => void;
  onConfirmImport: () => void;
}) {
  return (
    <div className="p-6 border-t border-border-light flex justify-end gap-3 shrink-0">
      {step === "select" && (
        <>
          <button
            type="button"
            onClick={onClose}
            disabled={importing}
            className="px-4 py-2 text-text-secondary hover:text-text-primary transition-colors cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed"
          >
            取消
          </button>
          <button
            type="button"
            onClick={onPreviewImport}
            disabled={!canPreview || importing || previewing}
            className="btn btn-primary disabled:opacity-50 disabled:cursor-not-allowed"
          >
            {previewing ? (
              <span className="flex items-center gap-2">
                <span className="w-4 h-4 border-2 border-white/30 border-t-white rounded-full animate-spin" />
                预览中...
              </span>
            ) : (
              "预览变更"
            )}
          </button>
        </>
      )}

      {step === "preview" && (
        <>
          <button
            type="button"
            onClick={onBackToSelect}
            disabled={importing}
            className="px-4 py-2 text-text-secondary hover:text-text-primary transition-colors cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed"
          >
            返回
          </button>
          <button
            type="button"
            onClick={onConfirmImport}
            disabled={importing || !canConfirmImport}
            className="btn btn-primary disabled:opacity-50 disabled:cursor-not-allowed"
          >
            {importing ? (
              <span className="flex items-center gap-2">
                <span className="w-4 h-4 border-2 border-white/30 border-t-white rounded-full animate-spin" />
                导入中...
              </span>
            ) : (
              "确认导入"
            )}
          </button>
        </>
      )}

      {step === "result" && (
        <button type="button" onClick={onClose} className="btn btn-primary">
          完成
        </button>
      )}
    </div>
  );
}
