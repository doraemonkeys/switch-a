import { describe, it, expect, beforeEach } from "vitest";
import { createApiClient } from "./client";
import { createMockStorage, createMockHttpClient } from "./test-mocks";

describe("createApiClient status API", () => {
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

  it("should get system status", async () => {
    const status = {
      providers: [
        {
          id: "1",
          name: "Provider 1",
          enabled: true,
          current_requests: 0,
          health: null,
        },
      ],
    };
    mockHttpClient.mockResponse({
      ok: true,
      status: 200,
      json: () => Promise.resolve(status),
    });

    const result = await api.status.get();

    expect(result).toEqual(status);
  });

  it("should get health states", async () => {
    const health = [{ provider_id: "1", available: true }];
    mockHttpClient.mockResponse({
      ok: true,
      status: 200,
      json: () => Promise.resolve(health),
    });

    const result = await api.status.health();

    expect(result).toEqual(health);
    expect(mockHttpClient.fetch).toHaveBeenCalledWith(
      "https://test-api.example.com/health",
      expect.any(Object),
    );
  });
});
