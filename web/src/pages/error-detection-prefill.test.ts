import { describe, expect, it } from "vitest";
import {
  getErrorDetectionPrefillKey,
  readErrorDetectionPrefill,
} from "./error-detection-prefill";

describe("error detection route prefill", () => {
  it("ignores incomplete or unrelated query state", () => {
    expect(readErrorDetectionPrefill(new URLSearchParams())).toBeUndefined();
    expect(
      readErrorDetectionPrefill(new URLSearchParams({ scope: "provider" })),
    ).toBeUndefined();
    expect(
      readErrorDetectionPrefill(
        new URLSearchParams({
          scope: "global",
          provider_id: "provider-a",
        }),
      ),
    ).toBeUndefined();
  });

  it("preserves decoded provider and API identifiers", () => {
    const prefill = readErrorDetectionPrefill(
      new URLSearchParams(
        "scope=provider&provider_id=provider+%26+west&api_type=custom%3Aprivate",
      ),
    );

    expect(prefill).toEqual({
      target: { kind: "provider", provider_id: "provider & west" },
      api_type: "custom:private",
    });
    expect(getErrorDetectionPrefillKey(prefill)).toBe(
      "provider:provider & west:custom:private",
    );
  });

  it("supports provider-scoped all-API drafts without an API parameter", () => {
    const prefill = readErrorDetectionPrefill(
      new URLSearchParams({
        scope: "provider",
        provider_id: "provider-a",
      }),
    );

    expect(prefill).toEqual({
      target: { kind: "provider", provider_id: "provider-a" },
    });
    expect(getErrorDetectionPrefillKey(prefill)).toBe("provider:provider-a:");
    expect(getErrorDetectionPrefillKey(undefined)).toBe("unscoped");
  });
});
