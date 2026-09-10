import { Check, LoaderCircle, Save, RotateCcw } from "lucide-react";

export function ConfigSaveBar({
  changeCount,
  busy,
  onReset,
}: {
  changeCount: number;
  busy: boolean;
  onReset: () => void;
}) {
  return (
    <footer className="config-savebar">
      <div className="config-save-status" role="status">
        {changeCount > 0 ? (
          <span className="config-pending-dot" />
        ) : (
          <Check size={18} />
        )}
        <div>
          <strong>
            {changeCount > 0 ? `${changeCount} 项修改未保存` : "所有修改已保存"}
          </strong>
          <p>保存后对后续请求生效，现有 WebSocket 连接需重连。</p>
        </div>
      </div>
      <div className="config-save-actions">
        <button
          type="button"
          className="config-button config-button-secondary"
          disabled={!changeCount || busy}
          onClick={onReset}
        >
          <RotateCcw size={15} />
          撤销修改
        </button>
        <button
          type="submit"
          className="config-button config-button-primary"
          disabled={!changeCount || busy}
        >
          {busy ? (
            <LoaderCircle className="config-spinner" size={16} />
          ) : (
            <Save size={16} />
          )}
          {busy ? "正在保存" : "保存修改"}
        </button>
      </div>
    </footer>
  );
}
