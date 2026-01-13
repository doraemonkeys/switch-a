import { useConfig } from "../hooks/useConfig";
import { ConfigForm } from "../components/ConfigForm";

export function Config() {
  const {
    defaults,
    config: remoteConfig,
    loading,
    error,
    saving,
    updateConfig,
  } = useConfig();

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
        </div>
      </div>

      <ConfigForm
        initialConfig={remoteConfig}
        defaults={defaults}
        onSave={updateConfig}
        saving={saving}
      />
    </div>
  );
}
