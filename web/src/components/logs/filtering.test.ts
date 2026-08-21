import { describe, expect, it } from "vitest";
import type { LogFilter } from "../../api/types";
import { createClearedLogFilterPatch, isLogFilterActive } from "./filtering";

describe("log table filter metadata", () => {
  it.each<LogFilter>([
    { user_id: "user-42" },
    { min_latency: 250 },
    { min_latency: 0 },
    { is_sse: false },
  ])("treats table-scoping values as active: %o", (filter) => {
    expect(isLogFilterActive(filter)).toBe(true);
  });

  it("does not treat pagination and sorting as table scope", () => {
    expect(
      isLogFilterActive({
        limit: 100,
        offset: 200,
        sort_by: "latency_ms",
        sort_order: "asc",
      }),
    ).toBe(false);
  });

  it("clears every key used by active-filter detection", () => {
    const activeFilters: LogFilter = {
      provider_id: "provider-1",
      user_id: "user-42",
      min_latency: 250,
      is_websocket: false,
      commit_source: "semantic_event",
    };

    const cleared = { ...activeFilters, ...createClearedLogFilterPatch() };

    expect(isLogFilterActive(cleared)).toBe(false);
    expect(cleared.user_id).toBeUndefined();
    expect(cleared.min_latency).toBeUndefined();
  });
});
