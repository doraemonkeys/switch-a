import { CloseButton } from "./config-import-modal/common";
import { ModalFooter } from "./config-import-modal/ModalFooter";
import { PreviewStep } from "./config-import-modal/PreviewStep";
import { ResultStep } from "./config-import-modal/ResultStep";
import { SelectStep } from "./config-import-modal/SelectStep";
import type { ConfigImportModalProps } from "./config-import-modal/types";
import { useConfigImportModalState } from "./config-import-modal/useConfigImportModalState";

export function ConfigImportModal(props: ConfigImportModalProps) {
  const { isOpen, onClose, importing } = props;
  const {
    step,
    error,
    isDragOver,
    selectedFile,
    parsedConfig,
    mode,
    selectionCatalog,
    selectedGroupIds,
    selectedProviderIds,
    preview,
    previewMode,
    previewing,
    result,
    resultMode,
    canPreview,
    canConfirmImport,
    hasAnyChanges,
    fileInputRef,
    handleDrop,
    handleDragOver,
    handleDragLeave,
    handleInputChange,
    handlePreviewImport,
    handleConfirmImport,
    handleBackToSelect,
    handleModeChange,
    handleToggleGroup,
    handleToggleProvider,
  } = useConfigImportModalState(props);

  if (!isOpen) {
    return null;
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/50 backdrop-blur-sm">
      <div className="bg-bg-secondary w-full max-w-3xl rounded-xl shadow-2xl border border-border-light max-h-[90vh] overflow-hidden flex flex-col">
        <div className="p-6 border-b border-border-light flex justify-between items-center shrink-0">
          <div>
            <h2 className="text-xl font-bold text-text-primary">导入配置</h2>
            <p className="text-sm text-text-secondary mt-0.5">
              {step === "select" && "选择导入模式与范围"}
              {step === "preview" && "确认变更内容"}
              {step === "result" && "导入完成"}
            </p>
          </div>
          <CloseButton onClick={onClose} disabled={importing} />
        </div>

        <div className="p-6 overflow-y-auto grow">
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
              selectedFile={selectedFile}
              parsedConfig={parsedConfig}
              mode={mode}
              onModeChange={handleModeChange}
              catalog={selectionCatalog}
              selectedGroupIds={selectedGroupIds}
              selectedProviderIds={selectedProviderIds}
              onToggleGroup={handleToggleGroup}
              onToggleProvider={handleToggleProvider}
              importing={importing}
              previewing={previewing}
            />
          )}

          {step === "preview" && preview && selectedFile && (
            <PreviewStep
              selectedFile={selectedFile}
              preview={preview}
              mode={previewMode}
              hasAnyChanges={hasAnyChanges}
              importing={importing}
              onBackToSelect={handleBackToSelect}
            />
          )}

          {step === "result" && result && (
            <ResultStep result={result} mode={resultMode} />
          )}
        </div>

        <ModalFooter
          step={step}
          importing={importing}
          previewing={previewing}
          canPreview={canPreview}
          canConfirmImport={canConfirmImport}
          onClose={onClose}
          onBackToSelect={handleBackToSelect}
          onPreviewImport={handlePreviewImport}
          onConfirmImport={handleConfirmImport}
        />
      </div>
    </div>
  );
}
