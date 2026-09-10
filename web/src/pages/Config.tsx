import { useState } from "react";
import { LoaderCircle } from "lucide-react";
import { useConfig } from "../hooks/useConfig";
import { useConfigExport } from "../hooks/useConfigExport";
import { useToast } from "../hooks/useToast";
import { ConfigForm } from "../components/ConfigForm";
import { ConfigImportModal } from "../components/ConfigImportModal";
import { ConfigHeader } from "../features/runtime-config/ConfigHeader";
import { downloadJsonFile } from "../lib/jsonDownload";

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

  if (loading && Object.keys(remoteConfig).length === 0) {
    return (
      <div className="config-page config-loading" role="status">
        <LoaderCircle className="config-spinner" size={24} />
        正在加载配置…
      </div>
    );
  }

  return (
    <div className="config-page">
      <ConfigHeader
        exporting={exporting}
        onExport={handleExport}
        onImport={() => setImportModalOpen(true)}
      />
      {error && (
        <div className="config-error" role="alert">
          <span>{error.message}</span>
          <button
            type="button"
            className="config-button config-button-secondary"
            onClick={() => void refetch()}
          >
            重新加载
          </button>
        </div>
      )}
      {Object.keys(remoteConfig).length > 0 && (
        <ConfigForm
          initialConfig={remoteConfig}
          defaults={defaults}
          onSave={updateConfig}
          saving={saving}
        />
      )}
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
