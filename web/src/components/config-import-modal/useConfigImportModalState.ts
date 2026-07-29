import { useCallback, useEffect, useRef, useState } from "react";
import type * as React from "react";
import type {
  ExportedConfig,
  ImportConfigRequest,
  ImportMode,
  ImportPreviewResponse,
  ImportResult,
} from "../../api/types";
import { REQUIRED_IMPORT_ARRAY_FIELDS } from "./constants";
import {
  buildImportRequest,
  buildImportScope,
  getSelectionCatalog,
  hasVisibleChanges,
  isJsonFile,
  toggleSelectedId,
} from "./helpers";
import type { ConfigImportModalProps, ImportStep } from "./types";

async function parseImportFile(file: File): Promise<ExportedConfig> {
  try {
    const text = await file.text();
    const json = JSON.parse(text) as ExportedConfig;

    for (const field of REQUIRED_IMPORT_ARRAY_FIELDS) {
      if (!Array.isArray(json[field])) {
        throw new Error(`配置文件缺少 ${field} 数组`);
      }
    }

    if (typeof json.settings !== "object" || json.settings === null) {
      throw new Error("配置文件缺少 settings 对象");
    }

    return json;
  } catch (err) {
    if (err instanceof SyntaxError) {
      throw new Error("JSON 格式无效，请检查文件内容", { cause: err });
    }
    throw err;
  }
}

function useConfigImportModalData() {
  const [step, setStep] = useState<ImportStep>("select");
  const [error, setError] = useState<string | null>(null);
  const [isDragOver, setIsDragOver] = useState(false);
  const [selectedFile, setSelectedFile] = useState<File | null>(null);
  const [parsedConfig, setParsedConfig] = useState<ExportedConfig | null>(null);
  const [mode, setMode] = useState<ImportMode>("full");
  const [selectedGroupIds, setSelectedGroupIds] = useState<string[]>([]);
  const [selectedProviderIds, setSelectedProviderIds] = useState<string[]>([]);
  const [preview, setPreview] = useState<ImportPreviewResponse | null>(null);
  const [previewRequest, setPreviewRequest] =
    useState<ImportConfigRequest | null>(null);
  const [previewing, setPreviewing] = useState(false);
  const [result, setResult] = useState<ImportResult | null>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const previewRequestVersionRef = useRef(0);

  const cancelPreviewRequest = useCallback(() => {
    previewRequestVersionRef.current += 1;
    setPreviewing(false);
  }, []);

  const clearImportProgress = useCallback(() => {
    cancelPreviewRequest();
    setPreview(null);
    setPreviewRequest(null);
    setResult(null);
  }, [cancelPreviewRequest]);

  const resetImportedState = useCallback(() => {
    setStep("select");
    setMode("full");
    setSelectedGroupIds([]);
    setSelectedProviderIds([]);
    clearImportProgress();
  }, [clearImportProgress]);

  const resetModalState = useCallback(() => {
    setError(null);
    setIsDragOver(false);
    setSelectedFile(null);
    setParsedConfig(null);
    resetImportedState();
    if (fileInputRef.current) {
      fileInputRef.current.value = "";
    }
  }, [resetImportedState]);

  return {
    step,
    setStep,
    error,
    setError,
    isDragOver,
    setIsDragOver,
    selectedFile,
    setSelectedFile,
    parsedConfig,
    setParsedConfig,
    mode,
    setMode,
    selectedGroupIds,
    setSelectedGroupIds,
    selectedProviderIds,
    setSelectedProviderIds,
    preview,
    setPreview,
    previewRequest,
    setPreviewRequest,
    previewing,
    setPreviewing,
    result,
    setResult,
    fileInputRef,
    previewRequestVersionRef,
    cancelPreviewRequest,
    clearImportProgress,
    resetImportedState,
    resetModalState,
  };
}

type ConfigImportModalStateData = ReturnType<typeof useConfigImportModalData>;

function useModalLifecycle({
  cancelPreviewRequest,
  importing,
  isOpen,
  onClose,
  resetModalState,
}: {
  cancelPreviewRequest: () => void;
  importing: boolean;
  isOpen: boolean;
  onClose: () => void;
  resetModalState: () => void;
}) {
  useEffect(() => {
    if (!isOpen) {
      cancelPreviewRequest();
      const timer = setTimeout(resetModalState, 200);
      return () => clearTimeout(timer);
    }
  }, [cancelPreviewRequest, isOpen, resetModalState]);

  useEffect(() => {
    if (!isOpen) {
      return;
    }

    const handleEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape" && !importing) {
        onClose();
      }
    };

    document.addEventListener("keydown", handleEscape);
    return () => document.removeEventListener("keydown", handleEscape);
  }, [importing, isOpen, onClose]);
}

function useFileSelectionHandlers(state: ConfigImportModalStateData) {
  const {
    resetImportedState,
    setError,
    setIsDragOver,
    setParsedConfig,
    setSelectedFile,
  } = state;

  async function handleFileSelect(file: File) {
    setError(null);

    try {
      const config = await parseImportFile(file);
      setSelectedFile(file);
      setParsedConfig(config);
      resetImportedState();
    } catch (err) {
      setError(err instanceof Error ? err.message : "文件解析失败");
      setSelectedFile(null);
      setParsedConfig(null);
    }
  }

  function handleDrop(event: React.DragEvent) {
    event.preventDefault();
    setIsDragOver(false);

    const file = event.dataTransfer.files[0];
    if (!file || !isJsonFile(file)) {
      setError("请选择 JSON 文件");
      return;
    }

    void handleFileSelect(file);
  }

  function handleDragOver(event: React.DragEvent) {
    event.preventDefault();
    setIsDragOver(true);
  }

  function handleDragLeave(event: React.DragEvent) {
    event.preventDefault();
    setIsDragOver(false);
  }

  function handleInputChange(event: React.ChangeEvent<HTMLInputElement>) {
    const file = event.target.files?.[0];
    if (file) {
      void handleFileSelect(file);
    }
  }

  return {
    handleDrop,
    handleDragOver,
    handleDragLeave,
    handleInputChange,
  };
}

function useImportFlowHandlers(
  state: ConfigImportModalStateData,
  {
    canPreview,
    currentScope,
    onImport,
    onPreview,
  }: {
    canPreview: boolean;
    currentScope: ImportConfigRequest["import_scope"];
    onImport: ConfigImportModalProps["onImport"];
    onPreview: ConfigImportModalProps["onPreview"];
  },
) {
  const {
    clearImportProgress,
    parsedConfig,
    previewRequest,
    previewRequestVersionRef,
    previewing,
    setError,
    setMode,
    setPreview,
    setPreviewRequest,
    setPreviewing,
    setResult,
    setSelectedGroupIds,
    setSelectedProviderIds,
    setStep,
  } = state;

  async function handlePreviewImport() {
    if (!parsedConfig) {
      return;
    }
    if (previewing) {
      return;
    }

    if (!canPreview) {
      setError("按范围导入时至少选择一个 Group 或 Provider");
      return;
    }

    setError(null);

    const request = buildImportRequest(parsedConfig, currentScope);
    const requestVersion = previewRequestVersionRef.current + 1;
    previewRequestVersionRef.current = requestVersion;
    setPreviewing(true);

    try {
      const previewResult = await onPreview(request);
      if (previewRequestVersionRef.current !== requestVersion) {
        return;
      }
      setPreview(previewResult);
      setPreviewRequest(request);
      setResult(null);
      setStep("preview");
    } catch (err) {
      if (previewRequestVersionRef.current !== requestVersion) {
        return;
      }
      setError(err instanceof Error ? err.message : "预览失败");
    } finally {
      if (previewRequestVersionRef.current === requestVersion) {
        setPreviewing(false);
      }
    }
  }

  async function handleConfirmImport() {
    if (!previewRequest) {
      return;
    }

    setError(null);

    try {
      const importResult = await onImport(previewRequest);
      setResult(importResult);
      setStep("result");
    } catch (err) {
      setError(err instanceof Error ? err.message : "导入失败");
    }
  }

  function handleBackToSelect() {
    setStep("select");
    clearImportProgress();
    setError(null);
  }

  function handleModeChange(nextMode: ImportMode) {
    setMode(nextMode);
    setError(null);
  }

  function handleToggleGroup(groupId: string) {
    setSelectedGroupIds((currentIds) => toggleSelectedId(currentIds, groupId));
  }

  function handleToggleProvider(providerId: string) {
    setSelectedProviderIds((currentIds) =>
      toggleSelectedId(currentIds, providerId),
    );
  }

  return {
    handlePreviewImport,
    handleConfirmImport,
    handleBackToSelect,
    handleModeChange,
    handleToggleGroup,
    handleToggleProvider,
  };
}

export function useConfigImportModalState({
  isOpen,
  onClose,
  onPreview,
  onImport,
  importing,
}: ConfigImportModalProps) {
  const state = useConfigImportModalData();
  const {
    error,
    fileInputRef,
    isDragOver,
    mode,
    parsedConfig,
    preview,
    previewing,
    previewRequest,
    resetModalState,
    result,
    selectedFile,
    selectedGroupIds,
    selectedProviderIds,
    step,
  } = state;

  const selectionCatalog = getSelectionCatalog(parsedConfig);
  const currentScope = buildImportScope(
    mode,
    selectedGroupIds,
    selectedProviderIds,
  );
  const previewMode = previewRequest?.import_scope.mode ?? mode;
  const resultMode = previewRequest?.import_scope.mode ?? mode;
  const canPreview =
    parsedConfig != null &&
    (mode !== "selection" ||
      selectedGroupIds.length > 0 ||
      selectedProviderIds.length > 0);
  const hasAnyChanges = hasVisibleChanges(preview, previewMode);
  const previewWarnings = preview?.warnings ?? [];
  const canConfirmImport = hasAnyChanges && previewWarnings.length === 0;

  useModalLifecycle({
    cancelPreviewRequest: state.cancelPreviewRequest,
    importing,
    isOpen,
    onClose,
    resetModalState,
  });

  const fileSelectionHandlers = useFileSelectionHandlers(state);
  const importFlowHandlers = useImportFlowHandlers(state, {
    canPreview,
    currentScope,
    onImport,
    onPreview,
  });

  return {
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
    ...fileSelectionHandlers,
    ...importFlowHandlers,
  };
}
