import { act, renderHook } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { useAnalyticsWindow } from "./useAnalyticsWindow";

describe("useAnalyticsWindow", () => {
  it("creates a complete initial window with one generated as_of", () => {
    const now = vi.fn(() => new Date("2026-08-21T08:00:00.000Z"));
    const { result } = renderHook(() => useAnalyticsWindow({ now }));

    expect(result.current.window).toEqual({
      period: "24h",
      granularity: "1h",
      as_of: "2026-08-21T08:00:00.000Z",
    });
    expect(now).toHaveBeenCalledTimes(1);
  });

  it("preserves the shared as_of across semantic selector actions", () => {
    const now = vi.fn(() => new Date("2026-08-21T08:00:00.000Z"));
    const { result } = renderHook(() => useAnalyticsWindow({ now }));

    act(() => {
      result.current.applyIntent({ type: "period-selected", period: "7d" });
    });
    expect(result.current.window).toEqual({
      period: "7d",
      granularity: "6h",
      as_of: "2026-08-21T08:00:00.000Z",
    });

    act(() => {
      result.current.applyIntent({
        type: "granularity-selected",
        granularity: "1d",
      });
    });
    expect(result.current.window).toEqual({
      period: "7d",
      granularity: "1d",
      as_of: "2026-08-21T08:00:00.000Z",
    });
    expect(now).toHaveBeenCalledTimes(1);
  });

  it("atomically advances only the shared as_of on refresh", () => {
    const now = vi
      .fn<() => Date>()
      .mockReturnValueOnce(new Date("2026-08-21T08:00:00.000Z"))
      .mockReturnValueOnce(new Date("2026-08-21T09:30:00.000Z"));
    const { result } = renderHook(() => useAnalyticsWindow({ now }));

    act(() => {
      result.current.applyIntent({ type: "period-selected", period: "30d" });
      result.current.applyIntent({ type: "refresh-requested" });
    });

    expect(result.current.window).toEqual({
      period: "30d",
      granularity: "1d",
      as_of: "2026-08-21T09:30:00.000Z",
    });
  });
});
