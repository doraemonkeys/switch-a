import { describe, expect, it } from "vitest";
import type { DebugCaptureRecordDetail } from "@/api";
import { toDebugCaptureMessageSource } from "./message-source";

function detailWith(
  responseHeaders: Record<string, string[]>,
  body: string,
): DebugCaptureRecordDetail {
  return {
    summary: {
      record_id: "record-a",
      session_id: "session-a",
      provider: {
        id: "provider-a",
        name: "Provider A",
        api_type: "codex",
        target_url: "https://provider.example",
      },
    },
    http: {
      request: {
        method: "POST",
        url: "https://provider.example/v1/responses",
        host: "provider.example",
        headers: {},
        content_length: 0,
      },
      request_body: {
        data_base64: "",
        preview_bytes: 0,
        captured_bytes: 0,
        truncated: false,
        checksum_sha256: "",
      },
      response: {
        status_code: 500,
        protocol: "HTTP/2",
        headers: responseHeaders,
        content_length: body.length,
      },
      response_body: {
        data_base64: body,
        preview_bytes: 12,
        captured_bytes: 12,
        truncated: false,
        checksum_sha256: "checksum",
      },
    },
    snapshot_state: "final",
    gateway_trace: null,
  } as DebugCaptureRecordDetail;
}

describe("toDebugCaptureMessageSource", () => {
  it("decodes textual identity responses for direct form input", () => {
    const source = toDebugCaptureMessageSource(
      detailWith({ "Content-Type": ["application/json"] }, "eyJlcnJvciI6e319"),
    );

    expect(source?.content_type).toBe("application/json");
    expect(source?.content_encoding).toBe("identity");
    expect(source?.body).toEqual({ encoding: "utf8", value: '{"error":{}}' });
  });

  it("keeps compressed responses as base64 wire bytes", () => {
    const source = toDebugCaptureMessageSource(
      detailWith(
        {
          "Content-Type": ["application/json"],
          "Content-Encoding": ["gzip"],
        },
        "H4sIAAAAAAA=",
      ),
    );

    expect(source?.body).toEqual({
      encoding: "base64",
      value: "H4sIAAAAAAA=",
    });
  });
});
