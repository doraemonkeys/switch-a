import { Download, Upload, LoaderCircle, ChevronDown } from "lucide-react";

export function ConfigHeader({
  exporting,
  onExport,
  onImport,
}: {
  exporting: boolean;
  onExport: () => void;
  onImport: () => void;
}) {
  return (
    <header className="config-page-header">
      <div>
        <h2>配置管理</h2>
        <p>调整网关的运行方式。</p>
      </div>
      <div className="config-transfer">
        <div className="config-transfer-actions">
          <button
            type="button"
            className="config-button config-button-secondary"
            onClick={onExport}
            disabled={exporting}
          >
            {exporting ? (
              <LoaderCircle className="config-spinner" size={16} />
            ) : (
              <Download size={16} />
            )}
            {exporting ? "正在导出" : "导出备份"}
          </button>
          <button
            type="button"
            className="config-button config-button-secondary"
            onClick={onImport}
          >
            <Upload size={16} />
            导入配置
          </button>
        </div>
        <details className="config-transfer-help">
          <summary>
            备份与恢复说明
            <ChevronDown size={13} aria-hidden="true" />
          </summary>
          <div>
            <p>
              备份包含运行配置、客户端伪装身份与档案、Key
              绑定、映射、粘性路由绑定及对话归属。
            </p>
            <p>
              ChatGPT
              登录凭据不会导出。导入新环境后需重新认证；同账号重新认证会复用恢复的设备身份。仅设置导入不更改身份状态。
            </p>
          </div>
        </details>
      </div>
    </header>
  );
}
