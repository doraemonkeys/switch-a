import { useState, useCallback } from "react";
import { useApi } from "../api";
import type {
  ExportedConfig,
  ImportConfigRequest,
  ImportPreviewResponse,
  ImportResult,
} from "../api/client";

interface UseConfigExportResult {
  /** Export the current configuration */
  exportConfig: () => Promise<ExportedConfig>;
  /** Preview import changes (dry run) */
  previewImport: (data: ImportConfigRequest) => Promise<ImportPreviewResponse>;
  /** Execute actual import */
  importConfig: (data: ImportConfigRequest) => Promise<ImportResult>;
  /** Loading state for export operation */
  exporting: boolean;
  /** Loading state for import/preview operation */
  importing: boolean;
  /** Error from the last operation */
  error: Error | null;
  /** Last exported config (available after exportConfig) */
  exportedConfig: ExportedConfig | null;
  /** Last preview result (available after previewImport) */
  preview: ImportPreviewResponse | null;
  /** Last import result (available after importConfig) */
  importResult: ImportResult | null;
  /** Reset all state */
  reset: () => void;
}

/**
 * Hook for configuration export/import operations
 */
export function useConfigExport(): UseConfigExportResult {
  const api = useApi();
  const [exporting, setExporting] = useState(false);
  const [importing, setImporting] = useState(false);
  const [error, setError] = useState<Error | null>(null);
  const [exportedConfig, setExportedConfig] = useState<ExportedConfig | null>(
    null,
  );
  const [preview, setPreview] = useState<ImportPreviewResponse | null>(null);
  const [importResult, setImportResult] = useState<ImportResult | null>(null);

  const exportConfig = useCallback(async (): Promise<ExportedConfig> => {
    setExporting(true);
    setError(null);
    try {
      const config = await api.config.export();
      setExportedConfig(config);
      return config;
    } catch (err) {
      const error =
        err instanceof Error ? err : new Error("Failed to export config");
      setError(error);
      throw error;
    } finally {
      setExporting(false);
    }
  }, [api]);

  const previewImport = useCallback(
    async (data: ImportConfigRequest): Promise<ImportPreviewResponse> => {
      setImporting(true);
      setError(null);
      setPreview(null);
      try {
        const result = await api.config.importPreview(data);
        setPreview(result);
        return result;
      } catch (err) {
        const error =
          err instanceof Error ? err : new Error("Failed to preview import");
        setError(error);
        throw error;
      } finally {
        setImporting(false);
      }
    },
    [api],
  );

  const importConfig = useCallback(
    async (data: ImportConfigRequest): Promise<ImportResult> => {
      setImporting(true);
      setError(null);
      setImportResult(null);
      try {
        const result = await api.config.import(data);
        setImportResult(result);
        return result;
      } catch (err) {
        const error =
          err instanceof Error ? err : new Error("Failed to import config");
        setError(error);
        throw error;
      } finally {
        setImporting(false);
      }
    },
    [api],
  );

  const reset = useCallback(() => {
    setError(null);
    setExportedConfig(null);
    setPreview(null);
    setImportResult(null);
  }, []);

  return {
    exportConfig,
    previewImport,
    importConfig,
    exporting,
    importing,
    error,
    exportedConfig,
    preview,
    importResult,
    reset,
  };
}
