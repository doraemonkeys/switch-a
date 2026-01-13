import { describe, it, expect, vi, beforeEach } from "vitest";
import { renderHook, waitFor, act } from "@testing-library/react";
import { useStats } from "./useStats";
import type { ApiClient, StatsResponse } from "../api/client";
import { createMockApiClient, createWrapper } from "./test-utils";

const mockStatsResponse: StatsResponse = {
  total_requests: 1000,
  success_count: 950,
  fail_count: 50,
  success_rate: 0.95,
  avg_latency_ms: 150,
  providers: {
    total: 5,
    healthy: 4,
    unhealthy: 1,
    disabled: 0,
  },
  requests_by_api_type: {
    claude: 500,
    codex: 300,
    gemini: 200,
  },
  requests_by_provider: [
    { id: "1", name: "Provider 1", count: 400, success_rate: 0.98 },
    { id: "2", name: "Provider 2", count: 300, success_rate: 0.92 },
  ],
  time_range: {
    start: "2024-01-01T00:00:00Z",
    end: "2024-01-02T00:00:00Z",
  },
};

const mockStatsWithTimeseries: StatsResponse = {
  ...mockStatsResponse,
  timeseries: [
    {
      time: "2024-01-01T00:00:00Z",
      requests: 100,
      success_count: 95,
      fail_count: 5,
      success_rate: 0.95,
      avg_latency_ms: 140,
    },
    {
      time: "2024-01-01T01:00:00Z",
      requests: 120,
      success_count: 115,
      fail_count: 5,
      success_rate: 0.958,
      avg_latency_ms: 155,
    },
  ],
};

function setupMockApiClient(): ApiClient {
  const mockApi = createMockApiClient();
  mockApi.stats.get = vi.fn().mockResolvedValue(mockStatsResponse);
  return mockApi;
}

describe("useStats", () => {
  let mockApi: ApiClient;

  beforeEach(() => {
    mockApi = setupMockApiClient();
  });

  it("should fetch stats on mount", async () => {
    const { result } = renderHook(() => useStats(), {
      wrapper: createWrapper(mockApi),
    });

    expect(result.current.loading).toBe(true);

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    expect(result.current.stats).toEqual(mockStatsResponse);
    expect(result.current.error).toBeNull();
    expect(mockApi.stats.get).toHaveBeenCalledWith({});
  });

  it("should fetch stats with initial params", async () => {
    const { result } = renderHook(
      () => useStats({ period: "7d", granularity: "1h" }),
      {
        wrapper: createWrapper(mockApi),
      },
    );

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    expect(mockApi.stats.get).toHaveBeenCalledWith({
      period: "7d",
      granularity: "1h",
    });
  });

  it("should handle error", async () => {
    mockApi.stats.get = vi.fn().mockRejectedValue(new Error("Network error"));

    const { result } = renderHook(() => useStats(), {
      wrapper: createWrapper(mockApi),
    });

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    expect(result.current.error).toBeInstanceOf(Error);
    expect(result.current.error?.message).toBe("Network error");
    expect(result.current.stats).toBeNull();
  });

  it("should handle non-Error rejection", async () => {
    mockApi.stats.get = vi.fn().mockRejectedValue("string error");

    const { result } = renderHook(() => useStats(), {
      wrapper: createWrapper(mockApi),
    });

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    expect(result.current.error?.message).toBe("Failed to fetch stats");
  });

  it("should refetch stats", async () => {
    const { result } = renderHook(() => useStats(), {
      wrapper: createWrapper(mockApi),
    });

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    await act(async () => {
      await result.current.refetch();
    });

    expect(mockApi.stats.get).toHaveBeenCalledTimes(2);
  });

  it("should update params and refetch", async () => {
    const { result } = renderHook(() => useStats(), {
      wrapper: createWrapper(mockApi),
    });

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    act(() => {
      result.current.setParams({ period: "30d" });
    });

    await waitFor(() => {
      expect(mockApi.stats.get).toHaveBeenCalledWith({ period: "30d" });
    });

    expect(result.current.params).toEqual({ period: "30d" });
  });

  it("should handle timeseries data", async () => {
    mockApi.stats.get = vi.fn().mockResolvedValue(mockStatsWithTimeseries);

    const { result } = renderHook(
      () => useStats({ period: "24h", granularity: "1h" }),
      {
        wrapper: createWrapper(mockApi),
      },
    );

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    expect(result.current.stats?.timeseries).toHaveLength(2);
    expect(result.current.stats?.timeseries?.[0].requests).toBe(100);
  });
});
