import { describe, expect, it } from "vitest";
import { validatedDebugCaptureDownloadGrant } from "./downloadGrant";

const EXPORT_ID = "ce_AAAAAAAAAAAAAAAAAAAAAAAA";
const SESSION_ID = "cs_1_000000000000000000000000";
const DOWNLOAD_PATH = `/admin/api/debug-capture/exports/${EXPORT_ID}/download`;
const DOWNLOAD_TOKEN = "A".repeat(43);
const NOW = Date.parse("2026-08-01T00:00:00Z");

function grant(overrides: Record<string, unknown> = {}): unknown {
  return {
    export_id: EXPORT_ID,
    session_id: SESSION_ID,
    record_count: 1,
    expires_at: "2026-08-01T00:05:00Z",
    download_path: DOWNLOAD_PATH,
    download_token: DOWNLOAD_TOKEN,
    ...overrides,
  };
}

describe("validatedDebugCaptureDownloadGrant", () => {
  it("copies a fully validated grant", () => {
    expect(
      validatedDebugCaptureDownloadGrant(grant(), SESSION_ID, NOW),
    ).toEqual(grant());
  });

  it.each([
    ["null", null],
    ["array", []],
    ["string", "grant"],
    ["non-canonical export ID", grant({ export_id: "export-a" })],
    ["wrong session", grant({ session_id: "cs_2_other" })],
    ["string record count", grant({ record_count: "1" })],
    ["zero record count", grant({ record_count: 0 })],
    ["fractional record count", grant({ record_count: 1.5 })],
    ["unbounded record count", grant({ record_count: Number.MAX_VALUE })],
    ["invalid expiry", grant({ expires_at: "tomorrow" })],
    ["expired grant", grant({ expires_at: "2026-08-01T00:00:00Z" })],
    ["empty download token", grant({ download_token: "" })],
    ["wrong token length", grant({ download_token: "A".repeat(42) })],
    [
      "non-canonical token padding bits",
      grant({ download_token: `${"A".repeat(42)}B` }),
    ],
    [
      "same-origin absolute alias",
      grant({ download_path: `https://switch-a.test${DOWNLOAD_PATH}` }),
    ],
    [
      "cross-origin URL",
      grant({ download_path: `https://attacker.test${DOWNLOAD_PATH}` }),
    ],
    [
      "different export route",
      grant({
        download_path:
          "/admin/api/debug-capture/exports/ce_BBBBBBBBBBBBBBBBBBBBBBBB/download",
      }),
    ],
    ["query string", grant({ download_path: `${DOWNLOAD_PATH}?token=leak` })],
    [
      "fragment",
      grant({ download_path: `${DOWNLOAD_PATH}#single-use-secret` }),
    ],
  ])("rejects a %s", (_case, value) => {
    expect(
      validatedDebugCaptureDownloadGrant(value, SESSION_ID, NOW),
    ).toBeNull();
  });

  it("rejects accessor-bearing input without throwing", () => {
    const value = Object.create(null) as Record<string, unknown>;
    Object.defineProperty(value, "export_id", {
      enumerable: true,
      get: () => {
        throw new Error("hostile accessor");
      },
    });

    expect(
      validatedDebugCaptureDownloadGrant(value, SESSION_ID, NOW),
    ).toBeNull();
  });
});
