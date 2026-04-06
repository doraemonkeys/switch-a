import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ApiClient, StatsResponse } from "../api/client";
import { createMockApiClient, createWrapper } from "./test-utils";
import { useStats } from "./useStats";

const mockStatsResponse: StatsResponse = {
  total_requests: 1000,
  avg_latency_ms: 150,
  outcome_counts: {
    completed: 910,
    interrupted: 40,
    never_started: 20,
    abandoned_by_client: 15,
    unknown: 15,
  },
  providers: {
    total: 6,
    healthy: 4,
    unhealthy: 1,
    disabled: 1,
  },
  requests_by_api_type: {
    claude: 500,
    codex: 300,
    gemini: 200,
  },
  requests_by_provider_outcome: [
    {
      id: "provider-1",
      name: "Provider 1",
      total_requests: 400,
      outcome_counts: { completed: 380, interrupted: 20 },
    },
  ],
  time_range: {
    start: "2026-04-01T00:00:00Z",
    end: "2026-04-02T00:00:00Z",
  },
  outcome_timeseries: [
    {
      time: "2026-04-01T00:00:00Z",
      total_requests: 100,
      avg_latency_ms: 140,
      outcome_counts: { completed: 90, interrupted: 5, unknown: 5 },
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

  it("fetches normalized stats on mount", async () => {
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

  it("supports stats param updates", async () => {
    const { result } = renderHook(() => useStats(), {
      wrapper: createWrapper(mockApi),
    });

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    act(() => {
      result.current.setParams({ period: "7d", granularity: "1h" });
    });

    await waitFor(() => {
      expect(mockApi.stats.get).toHaveBeenCalledWith({
        period: "7d",
        granularity: "1h",
      });
    });
  });

  it("exposes outcome time series data", async () => {
    const { result } = renderHook(
      () => useStats({ period: "24h", granularity: "1h" }),
      {
        wrapper: createWrapper(mockApi),
      },
    );

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    expect(result.current.stats?.outcome_timeseries).toHaveLength(1);
    expect(result.current.stats?.outcome_timeseries?.[0].total_requests).toBe(
      100,
    );
  });

  it("surfaces fetch errors", async () => {
    mockApi.stats.get = vi.fn().mockRejectedValue(new Error("Network error"));

    const { result } = renderHook(() => useStats(), {
      wrapper: createWrapper(mockApi),
    });

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    expect(result.current.error?.message).toBe("Network error");
    expect(result.current.stats).toBeNull();
  });
});
