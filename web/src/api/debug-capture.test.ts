import { describe, expect, it, vi } from "vitest";
import {
  buildDebugCaptureRecordsQuery,
  createDebugCaptureApi,
  type AuthenticatedRequest,
} from "./debug-capture";

function createRequestMock() {
  return vi.fn().mockResolvedValue({}) as unknown as AuthenticatedRequest;
}

describe("Debug Capture API", () => {
  it("gets global status through the authenticated JSON transport", async () => {
    const request = createRequestMock();
    const api = createDebugCaptureApi(request);

    await api.status();

    expect(request).toHaveBeenCalledWith("/debug-capture/status");
  });

  it("starts a session with the explicit raw-payload acknowledgement", async () => {
    const request = createRequestMock();
    const api = createDebugCaptureApi(request);
    const input = {
      provider_ids: ["provider-a", "provider-b"],
      completed_records_per_provider: 10,
      retained_bytes_limit: 268_435_456,
      acknowledge_raw_payload_risk: true as const,
    };

    await api.start(input);

    expect(request).toHaveBeenCalledWith("/debug-capture/sessions", {
      method: "POST",
      body: JSON.stringify(input),
    });
  });

  it("encodes the session precondition when stopping", async () => {
    const request = createRequestMock();
    const api = createDebugCaptureApi(request);

    await api.stop("session/with spaces");

    expect(request).toHaveBeenCalledWith(
      "/debug-capture/sessions/session%2Fwith%20spaces",
      { method: "DELETE" },
    );
  });

  it("preserves the cursor and watermark as opaque query values", async () => {
    const request = createRequestMock();
    const api = createDebugCaptureApi(request);

    await api.listRecords("session-a", {
      limit: 50,
      cursor: "cursor+/=",
      snapshot_watermark: "watermark & stable",
    });

    expect(request).toHaveBeenCalledWith(
      "/debug-capture/sessions/session-a/records?limit=50&cursor=cursor%2B%2F%3D&snapshot_watermark=watermark+%26+stable",
    );
  });

  it("omits absent record query values", () => {
    expect(buildDebugCaptureRecordsQuery()).toBe("");
    expect(buildDebugCaptureRecordsQuery({ limit: 25 })).toBe("limit=25");
  });

  it("encodes both detail resource identifiers", async () => {
    const request = createRequestMock();
    const api = createDebugCaptureApi(request);

    await api.getRecord("session/a", "record/b");

    expect(request).toHaveBeenCalledWith(
      "/debug-capture/sessions/session%2Fa/records/record%2Fb",
    );
  });

  it("creates a selected-record export without downloading it", async () => {
    const request = createRequestMock();
    const api = createDebugCaptureApi(request);
    const selection = {
      scope: "records" as const,
      record_ids: ["record-a", "record-b"],
    };

    await api.createExport("session-a", selection);

    expect(request).toHaveBeenCalledTimes(1);
    expect(request).toHaveBeenCalledWith(
      "/debug-capture/sessions/session-a/exports",
      {
        method: "POST",
        body: JSON.stringify(selection),
      },
    );
    expect(api).not.toHaveProperty("download");
  });
});
