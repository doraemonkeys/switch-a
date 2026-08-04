import type { DebugCaptureMessageSourceState } from "@/features/debug-capture";

export interface TestMessageCaptureSourceProps {
  readonly captureSource: DebugCaptureMessageSourceState;
  readonly busy: boolean;
  readonly appliedRecordId: string | null;
  readonly onApply: () => void;
}

function captureOptionPlaceholder(
  source: DebugCaptureMessageSourceState,
): string {
  if (source.loading) return "Loading captured responses…";
  if (source.records.length === 0) return "No HTTP responses captured yet";
  return "Choose a captured response";
}

export function TestMessageCaptureSource({
  captureSource,
  busy,
  appliedRecordId,
  onApply,
}: TestMessageCaptureSourceProps) {
  return (
    <section className="mt-5 rounded-xl border border-primary/20 bg-primary-light/30 p-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h3 className="text-sm font-semibold text-text-primary">
            Recommended: use Debug Capture
          </h3>
          <p className="mt-1 text-xs leading-5 text-text-secondary">
            从系统已保存的真实上游响应开始，API、Content-Type、压缩方式和 body
            会一起带入，不需要手填协议字段。
          </p>
        </div>
        {captureSource.session_id && (
          <a
            className="text-xs font-medium text-primary underline underline-offset-2"
            href="/admin/debug-capture"
          >
            Open Debug Capture
          </a>
        )}
      </div>

      {!captureSource.session_id ? (
        <p className="mt-3 rounded-lg bg-white/70 px-3 py-2 text-xs text-text-secondary">
          当前没有可用的 Debug Capture 会话。先在 Debug Capture
          页面启动捕获并重现一次失败请求；也可以继续使用下面的手动输入。
        </p>
      ) : (
        <div className="mt-3 space-y-3">
          <div className="flex flex-wrap items-end gap-2">
            <label className="min-w-0 flex-1 space-y-1 text-sm text-text-secondary sm:min-w-[20rem]">
              <span>Captured response</span>
              <select
                className="input"
                aria-label="Debug Capture record"
                value={captureSource.selected_record_id ?? ""}
                disabled={busy || captureSource.loading}
                onChange={(event) =>
                  captureSource.select_record(event.target.value || null)
                }
              >
                <option value="">
                  {captureOptionPlaceholder(captureSource)}
                </option>
                {captureSource.records.map((record) => (
                  <option key={record.record_id} value={record.record_id}>
                    {record.provider.name} · {record.provider.api_type} ·
                    attempt {record.provider_attempt_index + 1}
                  </option>
                ))}
              </select>
            </label>
            <button
              type="button"
              className="btn btn-secondary btn-sm"
              disabled={busy || captureSource.loading}
              onClick={() => void captureSource.refresh()}
            >
              Refresh captures
            </button>
            <button
              type="button"
              className="btn btn-primary btn-sm"
              disabled={
                busy ||
                captureSource.selected_loading ||
                !captureSource.selected_source
              }
              onClick={onApply}
            >
              {captureSource.selected_loading
                ? "Loading…"
                : "Use this response"}
            </button>
          </div>

          {captureSource.error && (
            <p role="alert" className="text-xs text-danger">
              {captureSource.error.message}
            </p>
          )}
          {captureSource.selected_error && (
            <p role="alert" className="text-xs text-danger">
              {captureSource.selected_error.message}
            </p>
          )}
          {captureSource.selected_source && (
            <div className="rounded-lg border border-border bg-white/80 px-3 py-2 text-xs text-text-secondary">
              <div className="flex flex-wrap gap-x-4 gap-y-1">
                <span>{captureSource.selected_source.content_type}</span>
                <span>
                  Content-Encoding:{" "}
                  {captureSource.selected_source.content_encoding}
                </span>
                <span>
                  {captureSource.selected_source.body.encoding === "utf8"
                    ? "可读文本"
                    : "Base64 原始字节"}
                </span>
                {captureSource.selected_source.response_status !== null && (
                  <span>
                    HTTP {captureSource.selected_source.response_status}
                  </span>
                )}
              </div>
              {captureSource.selected_source.preview_truncated && (
                <p role="status" className="mt-1 text-warning-dark">
                  Debug Capture 只保留了响应体预览（
                  {captureSource.selected_source.preview_bytes} /{" "}
                  {captureSource.selected_source.captured_bytes}{" "}
                  bytes），分析结果可能不完整。
                </p>
              )}
              {appliedRecordId === captureSource.selected_source.record_id && (
                <p role="status" className="mt-1 font-medium text-success-dark">
                  已带入此响应；提交前仍可在下面调整字段。
                </p>
              )}
            </div>
          )}
        </div>
      )}
    </section>
  );
}
