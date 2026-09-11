import { useId } from "react";

const CONTENT_TYPE_PRESETS = [
  { id: "json", label: "JSON response", value: "application/json" },
  { id: "sse", label: "SSE stream", value: "text/event-stream" },
  { id: "text", label: "Plain text", value: "text/plain" },
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
  const encodingListID = useId();
  const bodyID = useId();
  const helpID = useId();
  const preset = CONTENT_TYPE_PRESETS.find(
    (candidate) => candidate.value === contentType.split(";")[0].trim(),
  );
  const compressed = contentEncoding !== "identity" && contentEncoding !== "";

  return (
    <div className="detection-payload">
      <div>
        <div
          className="detection-format-buttons"
          role="group"
          aria-label="Common response formats"
        >
          {CONTENT_TYPE_PRESETS.map((choice) => (
            <button
              key={choice.id}
              type="button"
              aria-pressed={preset?.id === choice.id}
              disabled={busy}
              onClick={() => onContentTypeChange(choice.value)}
            >
              {choice.label}
            </button>
          ))}
        </div>
        <label className="block space-y-1 text-xs text-text-secondary">
          <span>Content-Type</span>
          <input
            className="input font-mono text-xs"
            aria-label="Content-Type"
            required
            value={contentType}
            disabled={busy}
            onChange={(event) => onContentTypeChange(event.target.value)}
            placeholder="application/json"
          />
        </label>
      </div>

      <div>
        <div className="detection-body-toolbar">
          <label htmlFor={bodyID} className="text-sm font-medium">
            Response body
          </label>
          <button
            type="button"
            className="text-xs text-primary"
            disabled={busy || bodyEncoding !== "utf8"}
            onClick={() =>
              onBodyChange(UTF8_BODY_EXAMPLES[preset?.id ?? "json"])
            }
          >
            Insert example
          </button>
        </div>
        <textarea
          id={bodyID}
          className="input font-mono text-xs"
          aria-label="Response body"
          aria-describedby={helpID}
          value={body}
          disabled={busy}
          onChange={(event) => onBodyChange(event.target.value)}
          spellCheck={false}
          placeholder={
            bodyEncoding === "base64"
              ? "Paste Base64-encoded response bytes…"
              : '{\n  "error": {\n    "message": "Paste an upstream response here…"\n  }\n}'
          }
        />
        <p id={helpID} className="mt-2 text-xs text-text-secondary">
          {bodyEncoding === "base64"
            ? "Paste the original response bytes encoded as Base64."
            : "Paste the response body without HTTP headers."}
        </p>
      </div>

      <details
        className="detection-transport"
        open={compressed || bodyEncoding === "base64"}
      >
        <summary>
          Transport settings{" "}
          <span className="text-text-muted">
            · {contentEncoding || "No encoding"} /{" "}
            {bodyEncoding === "utf8" ? "UTF-8 text" : "Base64"}
          </span>
        </summary>
        <div className="mt-4 grid gap-4 sm:grid-cols-2">
          <label className="space-y-1 text-xs text-text-secondary">
            <span>Content-Encoding</span>
            <input
              className="input font-mono text-xs"
              aria-label="Content-Encoding"
              list={encodingListID}
              required
              value={contentEncoding}
              disabled={busy}
              onChange={(event) => onContentEncodingChange(event.target.value)}
            />
            <datalist id={encodingListID}>
              <option value="identity" />
              <option value="gzip" />
              <option value="br" />
            </datalist>
            <span className="block pt-1 text-xs">
              Use the original response header.
            </span>
          </label>
          <fieldset
            aria-label="Body encoding"
            disabled={busy}
            className="space-y-2 text-xs text-text-secondary"
          >
            <legend className="mb-2">Body encoding</legend>
            <label className="flex items-center gap-2">
              <input
                type="radio"
                name={bodyID}
                checked={bodyEncoding === "utf8"}
                onChange={() => onBodyEncodingChange("utf8")}
              />
              Direct text
            </label>
            <label className="flex items-center gap-2">
              <input
                type="radio"
                name={bodyID}
                checked={bodyEncoding === "base64"}
                onChange={() => onBodyEncodingChange("base64")}
              />
              Base64 bytes
            </label>
          </fieldset>
        </div>
        {compressed && bodyEncoding !== "base64" && (
          <p
            role="status"
            className="mt-3 rounded-md bg-warning-light p-3 text-xs text-warning-dark"
          >
            This header declares compressed content. Use Base64 for the original
            compressed bytes, or identity for an already decompressed body.
          </p>
        )}
      </details>
    </div>
  );
}
