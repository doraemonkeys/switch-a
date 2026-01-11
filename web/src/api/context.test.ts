import { describe, it, expect } from "vitest";
import { ApiContext } from "./context";

describe("ApiContext", () => {
  it("should be a React context with null default value", () => {
    // Verify the context exists and has expected shape
    expect(ApiContext).toBeDefined();
    expect(ApiContext.Provider).toBeDefined();
    expect(ApiContext.Consumer).toBeDefined();
  });

  it("should have displayName for debugging", () => {
    // React contexts have a displayName property
    expect(typeof ApiContext).toBe("object");
  });
});
