import { useState, useCallback, useRef, useEffect } from "react";
import type {
  ImportConfigRequest,
  ImportPreviewResponse,
  ImportResult,
  ExportedConfig,
} from "../api/types";

type ImportStep = "select" | "preview" | "result";

const IMPORT_SUMMARY_SECTIONS = [
  { key: "providers", label: "Providers" },
  { key: "groups", label: "Groups" },
  { key: "routing_policies", label: "Routing Policies" },
  { key: "settings", label: "Settings" },
] as const;

const REQUIRED_IMPORT_ARRAY_FIELDS = [
  "providers",
  "groups",
  "routing_policies",
] as const;

interface ConfigImportModalProps {
  isOpen: boolean;
  onClose: () => void;
  onPreview: (data: ImportConfigRequest) => Promise<ImportPreviewResponse>;
  onImport: (data: ImportConfigRequest) => Promise<ImportResult>;
  importing: boolean;
}

// Icons
const UploadIcon = () => (
  <svg
    className="w-12 h-12 text-text-muted"
    fill="none"
    stroke="currentColor"
    viewBox="0 0 24 24"
  >
    <path
      strokeLinecap="round"
      strokeLinejoin="round"
      strokeWidth={1.5}
      d="M7 16a4 4 0 01-.88-7.903A5 5 0 1115.9 6L16 6a5 5 0 011 9.9M15 13l-3-3m0 0l-3 3m3-3v12"
    />
  </svg>
);

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

const CloseButton = ({
  onClick,
  disabled,
}: {
  onClick: () => void;
  disabled?: boolean;
}) => (
  <button
    onClick={onClick}
    disabled={disabled}
    className="text-text-secondary hover:text-text-primary transition-colors disabled:opacity-50 cursor-pointer disabled:cursor-not-allowed"
    aria-label="Close"
  >
    <svg
      className="w-5 h-5"
      fill="none"
      stroke="currentColor"
      viewBox="0 0 24 24"
    >
      <path
        strokeLinecap="round"
        strokeLinejoin="round"
        strokeWidth={2}
        d="M6 18L18 6M6 6l12 12"
      />
    </svg>
  </button>
);

// Change count badge component
function ChangeBadge({
  label,
  add,
  update,
  deleteCount,
}: {
  label: string;
  add: number;
  update: number;
  deleteCount: number;
}) {
  const hasChanges = add > 0 || update > 0 || deleteCount > 0;
  return (
    <div
      className={`p-4 rounded-lg border ${hasChanges ? "bg-bg-tertiary border-border-light" : "bg-bg-tertiary/50 border-border-dark"}`}
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
      </div>
    </div>
  );
}

// Applied count badge for result
function AppliedBadge({
  label,
  added,
  updated,
}: {
  label: string;
  added: number;
  updated: number;
}) {
  const total = added + updated;
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
        {total === 0 && <span className="text-sm text-text-muted">无变更</span>}
      </div>
    </div>
  );
}

// Step: Select File
function SelectStep({
  isDragOver,
  onDrop,
  onDragOver,
  onDragLeave,
  onClick,
  fileInputRef,
  onInputChange,
}: {
  isDragOver: boolean;
  onDrop: (e: React.DragEvent) => void;
  onDragOver: (e: React.DragEvent) => void;
  onDragLeave: (e: React.DragEvent) => void;
  onClick: () => void;
  fileInputRef: React.RefObject<HTMLInputElement | null>;
  onInputChange: (e: React.ChangeEvent<HTMLInputElement>) => void;
}) {
  return (
    <div
      className={`border-2 border-dashed rounded-xl p-8 text-center transition-colors cursor-pointer
        ${isDragOver ? "border-primary bg-primary/5" : "border-border-light hover:border-primary/50 hover:bg-bg-tertiary/50"}`}
      onDrop={onDrop}
      onDragOver={onDragOver}
      onDragLeave={onDragLeave}
      onClick={onClick}
    >
      <input
        ref={fileInputRef}
        type="file"
        accept=".json,application/json"
        onChange={onInputChange}
        className="hidden"
      />
      <div className="flex flex-col items-center gap-3">
        <UploadIcon />
        <div>
          <p className="text-text-primary font-medium">
            拖拽文件到这里，或点击选择
          </p>
          <p className="text-sm text-text-muted mt-1">
            支持 .json 格式的配置文件
          </p>
        </div>
      </div>
    </div>
  );
}

// Step: Preview Changes
function PreviewStep({
  selectedFile,
  preview,
  hasAnyChanges,
  importing,
  onBackToSelect,
}: {
  selectedFile: File | null;
  preview: ImportPreviewResponse;
  hasAnyChanges: boolean;
  importing: boolean;
  onBackToSelect: () => void;
}) {
  return (
    <div className="space-y-4">
      {/* File info */}
      <div className="flex items-center gap-3 p-3 bg-bg-tertiary rounded-lg">
        <svg
          className="w-5 h-5 text-primary shrink-0"
          fill="none"
          viewBox="0 0 24 24"
        >
          <path
            stroke="currentColor"
            strokeLinecap="round"
            strokeLinejoin="round"
            strokeWidth={2}
            d="M9 12h6m-6 4h6m2 5H7a2 2 0 01-2-2V5a2 2 0 012-2h5.586a1 1 0 01.707.293l5.414 5.414a1 1 0 01.293.707V19a2 2 0 01-2 2z"
          />
        </svg>
        <div className="min-w-0 flex-1">
          <p className="text-sm text-text-primary font-medium truncate">
            {selectedFile?.name}
          </p>
          <p className="text-xs text-text-muted">
            {selectedFile && `${(selectedFile.size / 1024).toFixed(1)} KB`}
          </p>
        </div>
        <button
          onClick={onBackToSelect}
          disabled={importing}
          className="text-xs text-text-secondary hover:text-primary transition-colors disabled:opacity-50"
        >
          重新选择
        </button>
      </div>

      {/* Changes summary */}
      <div className="space-y-3">
        <h3 className="text-sm font-medium text-text-secondary">变更预览</h3>
        <div className="grid gap-3">
          {IMPORT_SUMMARY_SECTIONS.map((section) => {
            const change = preview.changes[section.key];
            return (
              <ChangeBadge
                key={section.key}
                label={section.label}
                add={change.add}
                update={change.update}
                deleteCount={change.delete}
              />
            );
          })}
        </div>
      </div>

      {/* Warnings */}
      {preview.warnings && preview.warnings.length > 0 && (
        <div className="space-y-2">
          <h3 className="text-sm font-medium text-warning flex items-center gap-1.5">
            <WarningIcon />
            警告信息
          </h3>
          <div className="bg-warning/10 border border-warning/20 rounded-lg p-3 space-y-1.5">
            {preview.warnings.map((warning, index) => (
              <p
                key={index}
                className="text-sm text-warning/90 flex items-start gap-2"
              >
                <span className="text-warning/60">•</span>
                {warning}
              </p>
            ))}
          </div>
        </div>
      )}

      {/* No changes warning */}
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

// Step: Result
function ResultStep({ result }: { result: ImportResult }) {
  return (
    <div className="space-y-6">
      <div className="flex flex-col items-center gap-3 py-4">
        <CheckCircleIcon />
        <div className="text-center">
          <h3 className="text-lg font-medium text-text-primary">导入成功</h3>
          <p className="text-sm text-text-secondary mt-1">
            配置已更新，变更立即生效
          </p>
        </div>
      </div>

      <div className="bg-bg-tertiary rounded-lg p-4 divide-y divide-border-dark">
        {IMPORT_SUMMARY_SECTIONS.map((section) => {
          const applied = result.applied[section.key];
          return (
            <AppliedBadge
              key={section.key}
              label={section.label}
              added={applied.added}
              updated={applied.updated}
            />
          );
        })}
      </div>
    </div>
  );
}

// Modal Footer
function ModalFooter({
  step,
  importing,
  hasAnyChanges,
  onClose,
  onBackToSelect,
  onConfirmImport,
}: {
  step: ImportStep;
  importing: boolean;
  hasAnyChanges: boolean;
  onClose: () => void;
  onBackToSelect: () => void;
  onConfirmImport: () => void;
}) {
  return (
    <div className="p-6 border-t border-border-light flex justify-end gap-3 shrink-0">
      {step === "select" && (
        <button
          type="button"
          onClick={onClose}
          className="px-4 py-2 text-text-secondary hover:text-text-primary transition-colors cursor-pointer"
        >
          取消
        </button>
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
            disabled={importing || !hasAnyChanges}
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

export function ConfigImportModal({
  isOpen,
  onClose,
  onPreview,
  onImport,
  importing,
}: ConfigImportModalProps) {
  const [step, setStep] = useState<ImportStep>("select");
  const [error, setError] = useState<string | null>(null);
  const [isDragOver, setIsDragOver] = useState(false);
  const [selectedFile, setSelectedFile] = useState<File | null>(null);
  const [parsedData, setParsedData] = useState<ImportConfigRequest | null>(
    null,
  );
  const [preview, setPreview] = useState<ImportPreviewResponse | null>(null);
  const [result, setResult] = useState<ImportResult | null>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);

  // Reset state when modal opens/closes
  useEffect(() => {
    if (!isOpen) {
      // Delay reset to allow close animation
      const timer = setTimeout(() => {
        setStep("select");
        setError(null);
        setIsDragOver(false);
        setSelectedFile(null);
        setParsedData(null);
        setPreview(null);
        setResult(null);
      }, 200);
      return () => clearTimeout(timer);
    }
  }, [isOpen]);

  // Handle Escape key
  const handleEscape = useCallback(
    (e: KeyboardEvent) => {
      if (e.key === "Escape" && !importing) onClose();
    },
    [onClose, importing],
  );

  useEffect(() => {
    if (isOpen) {
      document.addEventListener("keydown", handleEscape);
      return () => document.removeEventListener("keydown", handleEscape);
    }
  }, [isOpen, handleEscape]);

  // Parse and validate JSON file
  const parseFile = async (file: File): Promise<ImportConfigRequest | null> => {
    try {
      const text = await file.text();
      const json = JSON.parse(text) as ExportedConfig;

      // Basic validation
      for (const field of REQUIRED_IMPORT_ARRAY_FIELDS) {
        if (!Array.isArray(json[field])) {
          throw new Error(`配置文件缺少 ${field} 数组`);
        }
      }
      if (typeof json.settings !== "object" || json.settings === null) {
        throw new Error("配置文件缺少 settings 对象");
      }

      return {
        version: json.version,
        providers: json.providers,
        groups: json.groups,
        routing_policies: json.routing_policies,
        settings: json.settings,
      };
    } catch (err) {
      if (err instanceof SyntaxError) {
        throw new Error("JSON 格式无效，请检查文件内容");
      }
      throw err;
    }
  };

  // Handle file selection
  const handleFileSelect = async (file: File) => {
    setError(null);
    setSelectedFile(file);

    try {
      const data = await parseFile(file);
      if (!data) return;

      setParsedData(data);

      // Auto-preview
      const previewResult = await onPreview(data);
      setPreview(previewResult);
      setStep("preview");
    } catch (err) {
      setError(err instanceof Error ? err.message : "文件解析失败");
      setSelectedFile(null);
    }
  };

  // Handle drop
  const handleDrop = useCallback(
    (e: React.DragEvent) => {
      e.preventDefault();
      setIsDragOver(false);

      const file = e.dataTransfer.files[0];
      if (file && file.type === "application/json") {
        handleFileSelect(file);
      } else {
        setError("请选择 JSON 文件");
      }
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [onPreview],
  );

  // Handle drag events
  const handleDragOver = useCallback((e: React.DragEvent) => {
    e.preventDefault();
    setIsDragOver(true);
  }, []);

  const handleDragLeave = useCallback((e: React.DragEvent) => {
    e.preventDefault();
    setIsDragOver(false);
  }, []);

  // Handle file input change
  const handleInputChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (file) {
      handleFileSelect(file);
    }
  };

  // Handle import confirmation
  const handleConfirmImport = async () => {
    if (!parsedData) return;

    setError(null);
    try {
      const importResult = await onImport(parsedData);
      setResult(importResult);
      setStep("result");
    } catch (err) {
      setError(err instanceof Error ? err.message : "导入失败");
    }
  };

  // Handle back to select
  const handleBackToSelect = () => {
    setStep("select");
    setSelectedFile(null);
    setParsedData(null);
    setPreview(null);
    setError(null);
    if (fileInputRef.current) {
      fileInputRef.current.value = "";
    }
  };

  if (!isOpen) return null;

  const hasAnyChanges = !!(
    preview &&
    IMPORT_SUMMARY_SECTIONS.some((section) => {
      const change = preview.changes[section.key];
      return change.add > 0 || change.update > 0 || change.delete > 0;
    })
  );

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/50 backdrop-blur-sm">
      <div className="bg-bg-secondary w-full max-w-lg rounded-xl shadow-2xl border border-border-light max-h-[90vh] overflow-hidden flex flex-col">
        {/* Header */}
        <div className="p-6 border-b border-border-light flex justify-between items-center shrink-0">
          <div>
            <h2 className="text-xl font-bold text-text-primary">导入配置</h2>
            <p className="text-sm text-text-secondary mt-0.5">
              {step === "select" && "选择 JSON 配置文件"}
              {step === "preview" && "确认变更内容"}
              {step === "result" && "导入完成"}
            </p>
          </div>
          <CloseButton onClick={onClose} disabled={importing} />
        </div>

        {/* Content */}
        <div className="p-6 overflow-y-auto grow">
          {/* Error display */}
          {error && (
            <div className="mb-4 bg-red-500/10 border border-red-500/20 text-red-400 p-3 rounded-lg text-sm">
              {error}
            </div>
          )}

          {step === "select" && (
            <SelectStep
              isDragOver={isDragOver}
              onDrop={handleDrop}
              onDragOver={handleDragOver}
              onDragLeave={handleDragLeave}
              onClick={() => fileInputRef.current?.click()}
              fileInputRef={fileInputRef}
              onInputChange={handleInputChange}
            />
          )}

          {step === "preview" && preview && (
            <PreviewStep
              selectedFile={selectedFile}
              preview={preview}
              hasAnyChanges={hasAnyChanges}
              importing={importing}
              onBackToSelect={handleBackToSelect}
            />
          )}

          {step === "result" && result && <ResultStep result={result} />}
        </div>

        <ModalFooter
          step={step}
          importing={importing}
          hasAnyChanges={hasAnyChanges}
          onClose={onClose}
          onBackToSelect={handleBackToSelect}
          onConfirmImport={handleConfirmImport}
        />
      </div>
    </div>
  );
}
