import { beforeEach, describe, expect, it, vi } from "vitest";
import { createApiClient } from "./client";
import { createMockHttpClient, createMockStorage } from "./test-mocks";
import type { TokenUsageResponse } from "./types";

function createValidTokenUsagePayload(): TokenUsageResponse {
  return {
    summary: {
      total_tokens: "1000",
      input_tokens: "600",
      output_tokens: "400",
      fresh_input_tokens: "300",
      cache_read_input_tokens: "200",
      cache_creation_input_tokens: "100",
      unclassified_input_tokens: "0",
      standard_output_tokens: "300",
      reasoning_tokens: "100",
      unclassified_output_tokens: "0",
      cache_hit_rate: 1 / 3,
      reasoning_ratio: 0.25,
    },
    timeseries: [
      {
        start: "2026-08-21T00:00:00Z",
        end: "2026-08-21T01:00:00Z",
        total_tokens: "1000",
        input_tokens: "600",
        output_tokens: "400",
        fresh_input_tokens: "300",
        cache_read_input_tokens: "200",
        cache_creation_input_tokens: "100",
        unclassified_input_tokens: "0",
        standard_output_tokens: "300",
        reasoning_tokens: "100",
        unclassified_output_tokens: "0",
        total_requests: 10,
        observed_requests: 10,
        comparable_requests: 10,
      },
    ],
    by_provider: [
      {
        provider_id: "p1",
        provider_name: "Provider 1",
        total_tokens: "1000",
        input_tokens: "600",
        output_tokens: "400",
        fresh_input_tokens: "300",
        cache_read_input_tokens: "200",
        cache_creation_input_tokens: "100",
        unclassified_input_tokens: "0",
        standard_output_tokens: "300",
        reasoning_tokens: "100",
        unclassified_output_tokens: "0",
        request_count: 10,
        share: 1.0,
      },
    ],
    by_model: [
      {
        model: "model-1",
        total_tokens: "1000",
        input_tokens: "600",
        output_tokens: "400",
        fresh_input_tokens: "300",
        cache_read_input_tokens: "200",
        cache_creation_input_tokens: "100",
        unclassified_input_tokens: "0",
        standard_output_tokens: "300",
        reasoning_tokens: "100",
        unclassified_output_tokens: "0",
        request_count: 10,
        share: 1.0,
      },
    ],
    time_range: {
      start: "2026-08-21T00:00:00Z",
      end: "2026-08-22T00:00:00Z",
      granularity: "1h",
    },
    coverage: {
      total_requests: 10,
      observed_requests: 10,
      comparable_requests: 10,
      without_usage_requests: 0,
      rate: 1.0,
    },
    data_quality: {
      quality_rate: 1.0,
      partial_requests: 0,
      invalid_requests: 0,
      unknown_semantics_requests: 0,
    },
  };
}

describe("createApiClient tokenUsage API", () => {
  let mockStorage: ReturnType<typeof createMockStorage>;
  let mockHttpClient: ReturnType<typeof createMockHttpClient>;
  let onUnauthorized: () => void;
  let api: ReturnType<typeof createApiClient>;

  beforeEach(() => {
    mockStorage = createMockStorage();
    mockHttpClient = createMockHttpClient();
    onUnauthorized = vi.fn();
    api = createApiClient({
      storage: mockStorage,
      httpClient: mockHttpClient,
      baseUrl: "https://test-api.example.com",
      onUnauthorized,
    });
  });

  it("gets token usage report without query params", async () => {
    const payload = createValidTokenUsagePayload();
    mockHttpClient.mockResponse({
      ok: true,
      status: 200,
      json: () => Promise.resolve(payload),
    });

    const result = await api.tokenUsage.get();

    expect(result).toEqual(payload);
    expect(mockHttpClient.fetch).toHaveBeenCalledWith(
      "https://test-api.example.com/token-usage",
      expect.any(Object),
    );
  });

  it("serializes full token usage query parameters", async () => {
    const payload = createValidTokenUsagePayload();
    mockHttpClient.mockResponse({
      ok: true,
      status: 200,
      json: () => Promise.resolve(payload),
    });

    await api.tokenUsage.get({
      period: "7d",
      granularity: "6h",
      as_of: "2026-08-21T12:00:00Z",
      provider_id: "prov-123",
      model: "claude-3-7-sonnet",
      api_type: "claude",
    });

    expect(mockHttpClient.fetch).toHaveBeenCalledWith(
      "https://test-api.example.com/token-usage?period=7d&granularity=6h&as_of=2026-08-21T12%3A00%3A00Z&provider_id=prov-123&model=claude-3-7-sonnet&api_type=claude",
      expect.any(Object),
    );
  });

  it("supports as_of parameter in stats.get", async () => {
    mockHttpClient.mockResponse({
      ok: true,
      status: 200,
      json: () =>
        Promise.resolve({
          total_requests: 0,
          avg_latency_ms: 0,
          outcome_counts: {},
          providers: { total: 0, healthy: 0, unhealthy: 0, disabled: 0 },
          requests_by_api_type: {},
          requests_by_provider_outcome: [],
          time_range: {
            start: "2026-08-20T00:00:00Z",
            end: "2026-08-21T00:00:00Z",
          },
          outcome_timeseries: [],
        }),
    });

    await api.stats.get({
      period: "24h",
      granularity: "1h",
      as_of: "2026-08-21T12:00:00Z",
    });

    expect(mockHttpClient.fetch).toHaveBeenCalledWith(
      "https://test-api.example.com/stats?period=24h&granularity=1h&as_of=2026-08-21T12%3A00%3A00Z",
      expect.any(Object),
    );
  });

  it("handles 401 Unauthorized by clearing auth and calling callback", async () => {
    mockStorage.setItem("admin_token", "test-token");
    mockHttpClient.mockResponse({
      ok: false,
      status: 401,
      statusText: "Unauthorized",
      json: () =>
        Promise.resolve({ code: "UNAUTHORIZED", message: "Invalid token" }),
    });

    await expect(api.tokenUsage.get()).rejects.toThrow("Invalid token");
    expect(mockStorage.getItem("admin_token")).toBeNull();
    expect(onUnauthorized).toHaveBeenCalled();
  });
});
