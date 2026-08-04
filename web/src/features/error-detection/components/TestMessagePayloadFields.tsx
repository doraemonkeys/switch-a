import { useId, type ReactNode } from "react";
import { Check } from "lucide-react";

const CONTENT_TYPE_PRESETS = [
  {
    id: "json",
    label: "JSON response",
    value: "application/json",
    description: "普通 JSON 错误响应",
  },
  {
    id: "sse",
    label: "SSE stream",
    value: "text/event-stream",
    description: "流式事件（event/data）",
  },
  {
    id: "text",
    label: "Plain text",
    value: "text/plain",
    description: "纯文本响应",
  },
] as const;

const CONTENT_ENCODING_PRESETS = [
  {
    value: "identity",
    label: "No compression",
    description: "响应体没有压缩（最常见）",
  },
  {
    value: "gzip",
    label: "gzip",
    description: "HTTP Header 为 Content-Encoding: gzip",
  },
  {
    value: "br",
    label: "Brotli (br)",
    description: "HTTP Header 为 Content-Encoding: br",
  },
] as const;

const UTF8_BODY_EXAMPLES = {
  json: '{"error":{"type":"server_error","code":"upstream_error","message":"Upstream unavailable"}}',
  sse: [
    "event: error",
    'data: {"type":"server_error","code":"upstream_error","message":"Upstream unavailable"}',
    "",
    "",
  ].join("\n"),
  text: "upstream unavailable",
} as const;

type BodyEncoding = "utf8" | "base64";

export interface TestMessagePayloadFieldsProps {
  readonly contentType: string;
  readonly contentEncoding: string;
  readonly bodyEncoding: BodyEncoding;
  readonly body: string;
  readonly busy: boolean;
  readonly onContentTypeChange: (value: string) => void;
  readonly onContentEncodingChange: (value: string) => void;
  readonly onBodyEncodingChange: (value: BodyEncoding) => void;
  readonly onBodyChange: (value: string) => void;
}

function contentTypePresetFor(value: string) {
  return (
    CONTENT_TYPE_PRESETS.find((preset) => preset.value === value)?.id ??
    "custom"
  );
}

function bodyPlaceholderFor(
  bodyEncoding: BodyEncoding,
  contentTypePreset: string,
): string {
  if (bodyEncoding === "base64") return "粘贴 Base64 编码的原始 wire bytes";
  if (contentTypePreset === "sse") {
    return 'event: error\ndata: {"type":"..."}\n\n';
  }
  if (contentTypePreset === "text") return "粘贴纯文本响应";
  return '{"error": {"message": "..."}}';
}

function ChoiceButton({
  selected,
  disabled,
  onClick,
  children,
}: {
  readonly selected: boolean;
  readonly disabled: boolean;
  readonly onClick: () => void;
  readonly children: ReactNode;
}) {
  return (
    <button
      type="button"
      aria-pressed={selected}
      disabled={disabled}
      onClick={onClick}
      className={`rounded-lg border px-3 py-2 text-left transition-colors focus:outline-none focus:ring-2 focus:ring-primary-ring ${
        selected
          ? "border-primary bg-primary-light/60 text-primary"
          : "border-border bg-white text-text-secondary hover:border-primary/40 hover:bg-primary-light/30"
      }`}
    >
      <span className="flex items-center gap-1.5 text-xs font-semibold">
        {selected && <Check className="h-3.5 w-3.5" aria-hidden="true" />}
        {children}
      </span>
    </button>
  );
}

function ResponseFormatSection({
  contentType,
  contentEncoding,
  busy,
  contentEncodingListID,
  fieldHelpID,
  onContentTypeChange,
  onContentEncodingChange,
}: Pick<
  TestMessagePayloadFieldsProps,
  | "contentType"
  | "contentEncoding"
  | "busy"
  | "onContentTypeChange"
  | "onContentEncodingChange"
> & { readonly contentEncodingListID: string; readonly fieldHelpID: string }) {
  const contentTypePreset = contentTypePresetFor(contentType);
  const contentEncodingPreset = CONTENT_ENCODING_PRESETS.some(
    (preset) => preset.value === contentEncoding,
  )
    ? contentEncoding
    : "custom";

  return (
    <>
      <section className="rounded-xl border border-border bg-white p-4">
        <div>
          <h3 className="text-sm font-semibold text-text-primary">
            1. Response format
          </h3>
          <p className="mt-1 text-xs leading-5 text-text-secondary">
            先选择响应的“外形”。Content-Type 决定系统按
            JSON、流式事件还是纯文本解析，通常直接照抄真实响应的 Header。
          </p>
        </div>

        <div
          className="mt-3 grid gap-2 sm:grid-cols-3"
          role="group"
          aria-label="Common response formats"
        >
          {CONTENT_TYPE_PRESETS.map((preset) => (
            <ChoiceButton
              key={preset.id}
              selected={contentTypePreset === preset.id}
              disabled={busy}
              onClick={() => onContentTypeChange(preset.value)}
            >
              <span>
                {preset.label}
                <span className="mt-0.5 block font-normal text-text-muted">
                  {preset.description}
                </span>
              </span>
            </ChoiceButton>
          ))}
        </div>

        <label className="mt-3 block space-y-1 text-sm text-text-secondary">
          <span>
            Content-Type{" "}
            <span className="font-normal text-text-muted">（响应格式）</span>
          </span>
          <input
            className="input font-mono"
            aria-label="Content-Type"
            aria-describedby={`${fieldHelpID}-content-type`}
            required
            value={contentType}
            disabled={busy}
            onChange={(event) => onContentTypeChange(event.target.value)}
            placeholder="例如 application/json"
          />
          <span
            id={`${fieldHelpID}-content-type`}
            className="block text-xs leading-5 text-text-muted"
          >
            只填媒体类型，不要把整个 Header 粘进来；有 charset
            参数时可一并保留。
          </span>
        </label>
      </section>

      <section className="rounded-xl border border-border bg-white p-4">
        <div>
          <h3 className="text-sm font-semibold text-text-primary">
            2. Transport compression
          </h3>
          <p className="mt-1 text-xs leading-5 text-text-secondary">
            Content-Encoding 是“传输时有没有压缩”，不是文字编码。拿不准时保持 No
            compression；只有真实响应 Header 明确写了 gzip 或 br 才选择它。
          </p>
        </div>

        <div
          className="mt-3 grid gap-2 sm:grid-cols-3"
          role="group"
          aria-label="Common content encodings"
        >
          {CONTENT_ENCODING_PRESETS.map((preset) => (
            <ChoiceButton
              key={preset.value}
              selected={contentEncodingPreset === preset.value}
              disabled={busy}
              onClick={() => onContentEncodingChange(preset.value)}
            >
              <span>
                {preset.label}
                <span className="mt-0.5 block font-normal text-text-muted">
                  {preset.description}
                </span>
              </span>
            </ChoiceButton>
          ))}
        </div>

        <label className="mt-3 block space-y-1 text-sm text-text-secondary">
          <span>Content-Encoding</span>
          <input
            className="input font-mono"
            aria-label="Content-Encoding"
            aria-describedby={`${fieldHelpID}-content-encoding`}
            list={contentEncodingListID}
            required
            value={contentEncoding}
            disabled={busy}
            onChange={(event) => onContentEncodingChange(event.target.value)}
            placeholder="identity"
          />
          <datalist id={contentEncodingListID}>
            <option value="identity" />
            <option value="gzip" />
            <option value="br" />
          </datalist>
          <span
            id={`${fieldHelpID}-content-encoding`}
            className="block text-xs leading-5 text-text-muted"
          >
            这是响应 Header 的值，不是 body 里要填写的文字；常见值为
            identity、gzip、br。
          </span>
        </label>
      </section>
    </>
  );
}

function ResponseBodySection({
  bodyEncoding,
  body,
  contentTypePreset,
  contentEncoding,
  busy,
  fieldHelpID,
  onBodyEncodingChange,
  onBodyChange,
}: Pick<
  TestMessagePayloadFieldsProps,
  "bodyEncoding" | "body" | "busy" | "onBodyEncodingChange" | "onBodyChange"
> & {
  readonly contentTypePreset: string;
  readonly contentEncoding: string;
  readonly fieldHelpID: string;
}) {
  function insertBodyExample() {
    if (bodyEncoding !== "utf8") return;
    const exampleKey =
      contentTypePreset === "custom" ? "json" : contentTypePreset;
    onBodyChange(
      UTF8_BODY_EXAMPLES[exampleKey as keyof typeof UTF8_BODY_EXAMPLES],
    );
  }

  const showCompressionWarning =
    contentEncoding !== "identity" && bodyEncoding !== "base64";

  return (
    <section className="rounded-xl border border-border bg-white p-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h3 className="text-sm font-semibold text-text-primary">
            3. Response body
          </h3>
          <p className="mt-1 text-xs leading-5 text-text-secondary">
            粘贴真实响应体，不要粘贴 HTTP headers。默认直接粘贴文本；如果 body
            经过压缩，请改用 Base64 粘贴原始字节。
          </p>
        </div>
        <button
          type="button"
          className="btn btn-secondary btn-sm"
          disabled={busy || bodyEncoding !== "utf8"}
          onClick={insertBodyExample}
        >
          Insert example
        </button>
      </div>

      <div className="mt-3 grid gap-4 sm:grid-cols-[230px_1fr]">
        <fieldset
          aria-label="Body encoding"
          aria-describedby={`${fieldHelpID}-body-encoding`}
          disabled={busy}
          className="space-y-2 text-sm text-text-secondary"
        >
          <legend>Body encoding</legend>
          <label className="flex cursor-pointer items-start gap-2 rounded-lg border border-border bg-bg-secondary/50 p-3 has-[:checked]:border-primary has-[:checked]:bg-primary-light/50">
            <input
              type="radio"
              name={`${fieldHelpID}-body-encoding`}
              value="utf8"
              checked={bodyEncoding === "utf8"}
              disabled={busy}
              onChange={() => onBodyEncodingChange("utf8")}
              className="mt-0.5"
            />
            <span>
              <span className="block font-medium text-text-primary">
                Direct text
              </span>
              <span className="mt-0.5 block text-xs text-text-muted">
                UTF-8 字符串，适合普通 JSON / SSE
              </span>
            </span>
          </label>
          <label className="flex cursor-pointer items-start gap-2 rounded-lg border border-border bg-bg-secondary/50 p-3 has-[:checked]:border-primary has-[:checked]:bg-primary-light/50">
            <input
              type="radio"
              name={`${fieldHelpID}-body-encoding`}
              value="base64"
              checked={bodyEncoding === "base64"}
              disabled={busy}
              onChange={() => onBodyEncodingChange("base64")}
              className="mt-0.5"
            />
            <span>
              <span className="block font-medium text-text-primary">
                Base64 bytes
              </span>
              <span className="mt-0.5 block text-xs text-text-muted">
                压缩后的原始字节，不能直接粘贴乱码
              </span>
            </span>
          </label>
          <span
            id={`${fieldHelpID}-body-encoding`}
            className="block text-xs leading-5 text-text-muted"
          >
            这是后台请求的传输方式，不会改变真实响应的 Content-Encoding。
          </span>
        </fieldset>

        <label className="space-y-1 text-sm text-text-secondary">
          <span>Response body</span>
          <textarea
            className="input min-h-40 font-mono text-xs"
            aria-label="Response body"
            aria-describedby={`${fieldHelpID}-body`}
            value={body}
            disabled={busy}
            onChange={(event) => onBodyChange(event.target.value)}
            placeholder={bodyPlaceholderFor(bodyEncoding, contentTypePreset)}
          />
          <span
            id={`${fieldHelpID}-body`}
            className="block text-xs leading-5 text-text-muted"
          >
            示例只帮助确定格式，不保证一定命中规则；要验证规则，请优先粘贴真实失败响应。
          </span>
        </label>
      </div>

      {showCompressionWarning && (
        <p
          role="status"
          className="mt-4 rounded-lg bg-warning-light/60 px-3 py-2 text-xs text-warning-dark"
        >
          当前 Header 声明了压缩，但你仍在粘贴文本。请确认 body
          是否已经解压；要保留原始压缩字节，请切换到 Base64 bytes。
        </p>
      )}
    </section>
  );
}

export function TestMessagePayloadFields({
  contentType,
  contentEncoding,
  bodyEncoding,
  body,
  busy,
  onContentTypeChange,
  onContentEncodingChange,
  onBodyEncodingChange,
  onBodyChange,
}: TestMessagePayloadFieldsProps) {
  const contentEncodingListID = useId();
  const fieldHelpID = useId();
  const contentTypePreset = contentTypePresetFor(contentType);

  return (
    <>
      <ResponseFormatSection
        contentType={contentType}
        contentEncoding={contentEncoding}
        busy={busy}
        contentEncodingListID={contentEncodingListID}
        fieldHelpID={fieldHelpID}
        onContentTypeChange={onContentTypeChange}
        onContentEncodingChange={onContentEncodingChange}
      />
      <ResponseBodySection
        bodyEncoding={bodyEncoding}
        body={body}
        contentTypePreset={contentTypePreset}
        contentEncoding={contentEncoding}
        busy={busy}
        fieldHelpID={fieldHelpID}
        onBodyEncodingChange={onBodyEncodingChange}
        onBodyChange={onBodyChange}
      />
    </>
  );
}
