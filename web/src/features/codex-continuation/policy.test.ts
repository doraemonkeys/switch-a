import { describe, expect, it } from "vitest";
import { parseCodexContinuation } from "./policy";

describe("Codex continuation contract", () => {
  it("supplies the creation and missing-field defaults", () => {
    expect(parseCodexContinuation(undefined)).toEqual({
      outbound: "any",
      inbound: "none",
    });
  });
  it("preserves configured directions", () => {
    expect(
      parseCodexContinuation({ outbound: "none", inbound: "same_identity" }),
    ).toEqual({ outbound: "none", inbound: "same_identity" });
  });
  it.each([null, "any", {}, { outbound: "any", inbound: "vendor" }])(
    "rejects malformed policies %j",
    (value) => {
      expect(() => parseCodexContinuation(value)).toThrow();
    },
  );
});
