import { renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ApiClient, TokenUsageResponse } from "../api/client";
import { createMockApiClient, createWrapper } from "./test-utils";
import { useTokenUsage } from "./useTokenUsage";

const mockTokenUsageResponse: TokenUsageResponse = {
  summary: {
    total_tokens: "12451200",
    input_tokens: "8204110",
    output_tokens: "4247090",
    fresh_input_tokens: "4663710",
    cache_read_input_tokens: "3120400",
    cache_creation_input_tokens: "420000",
    unclassified_input_tokens: "0",
    standard_output_tokens: "3426690",
    reasoning_tokens: "820400",
    unclassified_output_tokens: "0",
    cache_hit_rate: 0.3803,
    reasoning_ratio: 0.1932,
  },
  timeseries: [
    {
      start: "2026-08-21T00:00:00Z",
      end: "2026-08-21T01:00:00Z",
      total_tokens: "1200000",
      input_tokens: "800000",
      output_tokens: "400000",
      fresh_input_tokens: "400000",
      cache_read_input_tokens: "350000",
      cache_creation_input_tokens: "50000",
      unclassified_input_tokens: "0",
      standard_output_tokens: "320000",
      reasoning_tokens: "80000",
      unclassified_output_tokens: "0",
      total_requests: 120,
      observed_requests: 118,
      comparable_requests: 118,
    },
  ],
  by_provider: [
    {
      provider_id: "p1",
      provider_name: "Anthropic Direct",
      total_tokens: "7420000",
      input_tokens: "5000000",
      output_tokens: "2420000",
      fresh_input_tokens: "2000000",
      cache_read_input_tokens: "2800000",
      cache_creation_input_tokens: "200000",
      unclassified_input_tokens: "0",
      standard_output_tokens: "2420000",
      reasoning_tokens: "0",
      unclassified_output_tokens: "0",
      request_count: 840,
      share: 0.596,
    },
  ],
  by_model: [
    {
      model: "claude-3-7-sonnet",
      total_tokens: "6120000",
      input_tokens: "4000000",
      output_tokens: "2120000",
      fresh_input_tokens: "1800000",
      cache_read_input_tokens: "2000000",
      cache_creation_input_tokens: "200000",
      unclassified_input_tokens: "0",
      standard_output_tokens: "2120000",
      reasoning_tokens: "0",
      unclassified_output_tokens: "0",
      request_count: 620,
      share: 0.492,
    },
  ],
  time_range: {
    start: "2026-08-20T16:00:00Z",
    end: "2026-08-21T16:00:00Z",
    granularity: "1h",
  },
  coverage: {
    total_requests: 1456,
    observed_requests: 1430,
    comparable_requests: 1430,
    without_usage_requests: 26,
    rate: 0.9821,
  },
  data_quality: {
    quality_rate: 1.0,
    partial_requests: 0,
    invalid_requests: 0,
    unknown_semantics_requests: 0,
  },
};

function setupMockApiClient(): ApiClient {
  const mockApi = createMockApiClient();
  mockApi.tokenUsage.get = vi.fn().mockResolvedValue(mockTokenUsageResponse);
  return mockApi;
}

describe("useTokenUsage", () => {
  let mockApi: ApiClient;

  beforeEach(() => {
    mockApi = setupMockApiClient();
  });

  it("fetches token usage report on mount", async () => {
    const { result } = renderHook(() => useTokenUsage(), {
      wrapper: createWrapper(mockApi),
    });

    expect(result.current.loading).toBe(true);

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    expect(result.current.data).toEqual(mockTokenUsageResponse);
    expect(result.current.error).toBeNull();
    expect(mockApi.tokenUsage.get).toHaveBeenCalledWith({});
  });

  it("refetches when controlled parameters change", async () => {
    const { result, rerender } = renderHook(
      ({ params }) => useTokenUsage(params),
      {
        initialProps: { params: {} },
        wrapper: createWrapper(mockApi),
      },
    );

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    rerender({ params: { period: "7d", granularity: "6h" } });

    await waitFor(() => {
      expect(mockApi.tokenUsage.get).toHaveBeenCalledWith({
        period: "7d",
        granularity: "6h",
      });
    });
  });

  it("surfaces fetch errors", async () => {
    mockApi.tokenUsage.get = vi
      .fn()
      .mockRejectedValue(new Error("API network failure"));

    const { result } = renderHook(() => useTokenUsage(), {
      wrapper: createWrapper(mockApi),
    });

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    expect(result.current.error?.message).toBe("API network failure");
    expect(result.current.data).toBeNull();
  });
});
