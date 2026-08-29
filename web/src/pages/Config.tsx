import { useState } from "react";
import { useConfig } from "../hooks/useConfig";
import { useConfigExport } from "../hooks/useConfigExport";
import { useToast } from "../hooks/useToast";
import { ConfigForm } from "../components/ConfigForm";
import { ConfigImportModal } from "../components/ConfigImportModal";
import { downloadJsonFile } from "../lib/jsonDownload";

// Download icon
const DownloadIcon = () => (
  <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24">
    <path
      stroke="currentColor"
      strokeLinecap="round"
      strokeLinejoin="round"
      strokeWidth={2}
      d="M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1m-4-4l-4 4m0 0l-4-4m4 4V4"
    />
  </svg>
);

// Upload icon
const UploadIcon = () => (
  <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24">
    <path
      stroke="currentColor"
      strokeLinecap="round"
      strokeLinejoin="round"
      strokeWidth={2}
      d="M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1m-4-8l-4-4m0 0L8 8m4-4v12"
    />
  </svg>
);

export function Config() {
  const {
    defaults,
    config: remoteConfig,
    loading,
    error,
    saving,
    updateConfig,
    refetch,
  } = useConfig();

  const { exportConfig, previewImport, importConfig, exporting, importing } =
    useConfigExport();

  const toast = useToast();

  const [importModalOpen, setImportModalOpen] = useState(false);

  // Handle export - download JSON file
  const handleExport = async () => {
    try {
      const config = await exportConfig();

      const date = new Date().toISOString().split("T")[0];
      downloadJsonFile(`switch-a-config-${date}.json`, config);

      toast.success("配置导出成功");
    } catch (err) {
      console.error("Export failed:", err);
      toast.error(err instanceof Error ? err.message : "导出配置失败");
    }
  };

  if (loading) {
    return (
      <div className="flex items-center justify-center min-h-[400px]">
        <div className="w-8 h-8 border-4 border-primary/30 border-t-primary rounded-full animate-spin"></div>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      {/* Page Header */}
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-2xl font-bold text-text-primary">
            Configuration
          </h2>
          <p className="text-text-secondary mt-1">运行时配置管理</p>
        </div>
        <div className="flex items-center gap-3">
          {error && (
            <span className="text-sm text-error bg-error/10 px-3 py-1 rounded-full">
              Error: {error.message}
            </span>
          )}

          {/* Export Button */}
          <button
            onClick={handleExport}
            disabled={exporting}
            className="btn btn-secondary flex items-center gap-2 disabled:opacity-50 disabled:cursor-not-allowed"
            title="导出所有配置；ChatGPT 登录信息不会导出，恢复后需重新认证"
          >
            {exporting ? (
              <span className="w-4 h-4 border-2 border-current/30 border-t-current rounded-full animate-spin" />
            ) : (
              <DownloadIcon />
            )}
            <span>导出</span>
          </button>

          {/* Import Button */}
          <button
            onClick={() => setImportModalOpen(true)}
            className="btn btn-secondary flex items-center gap-2"
            title="从 JSON 文件导入配置"
          >
            <UploadIcon />
            <span>导入</span>
          </button>
        </div>
      </div>

      <ConfigForm
        initialConfig={remoteConfig}
        defaults={defaults}
        onSave={updateConfig}
        saving={saving}
      />

      {/* Import Modal */}
      <ConfigImportModal
        isOpen={importModalOpen}
        onClose={() => setImportModalOpen(false)}
        onPreview={previewImport}
        onImport={async (data, ruleSetETag) => {
          const result = await importConfig(data, ruleSetETag);
          if (result.success) {
            toast.success("配置导入成功");
            await refetch();
          }
          return result;
        }}
        importing={importing}
      />
    </div>
  );
}
