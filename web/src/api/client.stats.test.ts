import { beforeEach, describe, expect, it } from "vitest";
import { createApiClient } from "./client";
import { createMockHttpClient, createMockStorage } from "./test-mocks";
import type { StatsResponse } from "./types";

describe("createApiClient stats API", () => {
  let mockStorage: ReturnType<typeof createMockStorage>;
  let mockHttpClient: ReturnType<typeof createMockHttpClient>;
  let api: ReturnType<typeof createApiClient>;

  beforeEach(() => {
    mockStorage = createMockStorage();
    mockHttpClient = createMockHttpClient();
    api = createApiClient({
      storage: mockStorage,
      httpClient: mockHttpClient,
      baseUrl: "https://test-api.example.com",
    });
  });

  it("gets normalized stats without params", async () => {
    const statsResponse = {
      total_requests: 120,
      avg_latency_ms: 145,
      outcome_counts: {
        completed: 90,
        interrupted: 10,
        never_started: 5,
        abandoned_by_client: 8,
        unknown: 7,
      },
      providers: {
        total: 4,
        healthy: 3,
        unhealthy: 1,
        disabled: 0,
      },
      requests_by_api_type: { claude: 80, codex: 40 },
      requests_by_provider_outcome: [],
      time_range: {
        start: "2026-04-01T00:00:00Z",
        end: "2026-04-02T00:00:00Z",
      },
      outcome_timeseries: [
        {
          time: "2026-04-01T00:00:00Z",
          total_requests: 12,
          avg_latency_ms: 130,
          outcome_counts: {
            completed: 10,
            interrupted: 2,
          },
        },
      ],
    } satisfies StatsResponse;
    mockHttpClient.mockResponse({
      ok: true,
      status: 200,
      json: () => Promise.resolve(statsResponse),
    });

    const result = await api.stats.get();

    expect(result).toEqual(statsResponse);
    expect(mockHttpClient.fetch).toHaveBeenCalledWith(
      "https://test-api.example.com/stats",
      expect.any(Object),
    );
  });

  it("serializes period and granularity for stats", async () => {
    mockHttpClient.mockResponse({
      ok: true,
      status: 200,
      json: () =>
        Promise.resolve({
          total_requests: 0,
          avg_latency_ms: 0,
          outcome_counts: {},
          providers: {
            total: 0,
            healthy: 0,
            unhealthy: 0,
            disabled: 0,
          },
          requests_by_api_type: {},
          requests_by_provider_outcome: [],
          time_range: {
            start: "2026-04-01T00:00:00Z",
            end: "2026-04-02T00:00:00Z",
          },
          outcome_timeseries: [],
        }),
    });

    await api.stats.get({ period: "7d", granularity: "1h" });

    expect(mockHttpClient.fetch).toHaveBeenCalledWith(
      "https://test-api.example.com/stats?period=7d&granularity=1h",
      expect.any(Object),
    );
  });

  it("rejects stats responses that still expose outcome_timeseries[].requests", async () => {
    mockHttpClient.mockResponse({
      ok: true,
      status: 200,
      json: () =>
        Promise.resolve({
          total_requests: 120,
          avg_latency_ms: 145,
          outcome_counts: {
            completed: 90,
          },
          providers: {
            total: 4,
            healthy: 3,
            unhealthy: 1,
            disabled: 0,
          },
          requests_by_api_type: { claude: 80 },
          requests_by_provider_outcome: [],
          time_range: {
            start: "2026-04-01T00:00:00Z",
            end: "2026-04-02T00:00:00Z",
          },
          outcome_timeseries: [
            {
              time: "2026-04-01T00:00:00Z",
              requests: 12,
              avg_latency_ms: 130,
              outcome_counts: {
                completed: 10,
              },
            },
          ],
        }),
    });

    await expect(api.stats.get()).rejects.toThrow(
      "stats response.outcome_timeseries[0].total_requests is required",
    );
  });
});
