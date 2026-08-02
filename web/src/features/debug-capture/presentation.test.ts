import { describe, expect, it } from "vitest";
import {
  formatBytes,
  formatCaptureValue,
  getContentType,
  isTextualContentType,
  presentBlobPreview,
} from "./presentation";

describe("Debug Capture presentation", () => {
  it("formats bounded memory values", () => {
    expect(formatBytes(0)).toBe("0 B");
    expect(formatBytes(1_536)).toBe("1.5 KiB");
    expect(formatBytes(16 * 1_024 * 1_024)).toBe("16 MiB");
  });

  it("formats stable enum values without changing their meaning", () => {
    expect(formatCaptureValue("active_partial")).toBe("Active Partial");
    expect(formatCaptureValue("preparation_error")).toBe("Preparation Error");
  });

  it("finds Content-Type case-insensitively", () => {
    expect(getContentType({ "content-type": ["application/json"] })).toBe(
      "application/json",
    );
    expect(getContentType({ Accept: ["application/json"] })).toBe("");
  });

  it("classifies only known textual media types", () => {
    expect(isTextualContentType("application/problem+json")).toBe(true);
    expect(isTextualContentType("text/event-stream")).toBe(true);
    expect(isTextualContentType("application/octet-stream")).toBe(false);
  });

  it("decodes valid UTF-8 but preserves binary previews as base64 text", () => {
    const preview = {
      data_base64: btoa("hello"),
      preview_bytes: 5,
      captured_bytes: 5,
      truncated: false,
      checksum_sha256: "checksum",
    };

    expect(presentBlobPreview(preview, true)).toBe("hello");
    expect(presentBlobPreview(preview, false)).toBe(btoa("hello"));
    expect(
      presentBlobPreview(
        { ...preview, data_base64: "not-valid-base64!" },
        true,
      ),
    ).toBe("not-valid-base64!");
  });
});
