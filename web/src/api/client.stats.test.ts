import { describe, it, expect, beforeEach } from "vitest";
import { createApiClient } from "./client";
import { createMockStorage, createMockHttpClient } from "./test-mocks";

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

  it("should get stats without params", async () => {
    const statsResponse = {
      time_range: {
        start: "2025-01-01T00:00:00Z",
        end: "2025-01-02T00:00:00Z",
      },
      providers: [],
      time_series: [],
    };
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

  it("should get stats with period param", async () => {
    mockHttpClient.mockResponse({
      ok: true,
      status: 200,
      json: () => Promise.resolve({}),
    });

    await api.stats.get({ period: "7d" });

    expect(mockHttpClient.fetch).toHaveBeenCalledWith(
      "https://test-api.example.com/stats?period=7d",
      expect.any(Object),
    );
  });

  it("should get stats with granularity param", async () => {
    mockHttpClient.mockResponse({
      ok: true,
      status: 200,
      json: () => Promise.resolve({}),
    });

    await api.stats.get({ granularity: "1h" });

    expect(mockHttpClient.fetch).toHaveBeenCalledWith(
      "https://test-api.example.com/stats?granularity=1h",
      expect.any(Object),
    );
  });

  it("should get stats with period and granularity", async () => {
    mockHttpClient.mockResponse({
      ok: true,
      status: 200,
      json: () => Promise.resolve({}),
    });

    await api.stats.get({ period: "7d", granularity: "1h" });

    expect(mockHttpClient.fetch).toHaveBeenCalledWith(
      "https://test-api.example.com/stats?period=7d&granularity=1h",
      expect.any(Object),
    );
  });
});
