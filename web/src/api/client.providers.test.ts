import { describe, it, expect, beforeEach } from "vitest";
import { createApiClient } from "./client";
import { createMockStorage, createMockHttpClient } from "./test-mocks";

describe("createApiClient providers API", () => {
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

  it("should list providers", async () => {
    const providers = [{ id: "1", name: "OpenAI" }];
    mockHttpClient.mockResponse({
      ok: true,
      status: 200,
      json: () => Promise.resolve(providers),
    });

    const result = await api.providers.list();

    expect(result).toEqual(providers);
    expect(mockHttpClient.fetch).toHaveBeenCalledWith(
      "https://test-api.example.com/providers",
      expect.any(Object),
    );
  });

  it("should get provider by id", async () => {
    const provider = { id: "1", name: "OpenAI" };
    mockHttpClient.mockResponse({
      ok: true,
      status: 200,
      json: () => Promise.resolve(provider),
    });

    const result = await api.providers.get("1");

    expect(result).toEqual(provider);
    expect(mockHttpClient.fetch).toHaveBeenCalledWith(
      "https://test-api.example.com/providers/1",
      expect.any(Object),
    );
  });

  it("should create provider", async () => {
    const input = {
      name: "Test",
      api_key: "key",
      api_types: [{ api_type: "claude", base_url: "https://test.example.com" }],
      usage_limit_policy: "switch_provider" as const,
    };
    const created = { id: "1", ...input };
    mockHttpClient.mockResponse({
      ok: true,
      status: 201,
      json: () => Promise.resolve(created),
    });

    const result = await api.providers.create(input);

    expect(result).toEqual(created);
    expect(mockHttpClient.fetch).toHaveBeenCalledWith(
      "https://test-api.example.com/providers",
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify(input),
      }),
    );
  });

  it("should update provider", async () => {
    const input = {
      name: "Updated",
      api_key: "key",
      api_types: [{ api_type: "claude", base_url: "https://test.example.com" }],
      usage_limit_policy: "suspend" as const,
    };
    mockHttpClient.mockResponse({
      ok: true,
      status: 200,
      json: () => Promise.resolve({ id: "1", ...input }),
    });

    await api.providers.update("1", input);

    expect(mockHttpClient.fetch).toHaveBeenCalledWith(
      "https://test-api.example.com/providers/1",
      expect.objectContaining({
        method: "PUT",
        body: JSON.stringify(input),
      }),
    );
  });

  it("should delete provider", async () => {
    mockHttpClient.mockResponse({ ok: true, status: 204 });

    await api.providers.delete("1");

    expect(mockHttpClient.fetch).toHaveBeenCalledWith(
      "https://test-api.example.com/providers/1",
      expect.objectContaining({ method: "DELETE" }),
    );
  });

  it("should enable provider", async () => {
    const mockProvider = { id: "1", name: "Test", enabled: true };
    mockHttpClient.mockResponse({
      ok: true,
      status: 200,
      json: () => Promise.resolve(mockProvider),
    });

    const result = await api.providers.enable("1");

    expect(mockHttpClient.fetch).toHaveBeenCalledWith(
      "https://test-api.example.com/providers/1/enable",
      expect.objectContaining({ method: "POST" }),
    );
    expect(result).toEqual(mockProvider);
  });

  it("should disable provider", async () => {
    const mockProvider = { id: "1", name: "Test", enabled: false };
    mockHttpClient.mockResponse({
      ok: true,
      status: 200,
      json: () => Promise.resolve(mockProvider),
    });

    const result = await api.providers.disable("1");

    expect(mockHttpClient.fetch).toHaveBeenCalledWith(
      "https://test-api.example.com/providers/1/disable",
      expect.objectContaining({ method: "POST" }),
    );
    expect(result).toEqual(mockProvider);
  });

  it("should reset provider", async () => {
    const mockHealthState = { provider_id: "1", available: true };
    mockHttpClient.mockResponse({
      ok: true,
      status: 200,
      json: () => Promise.resolve(mockHealthState),
    });

    const result = await api.providers.reset("1");

    expect(mockHttpClient.fetch).toHaveBeenCalledWith(
      "https://test-api.example.com/providers/1/reset",
      expect.objectContaining({ method: "POST" }),
    );
    expect(result).toEqual(mockHealthState);
  });

  it("should refresh provider credential", async () => {
    mockHttpClient.mockResponse({ ok: true, status: 204 });

    await api.providers.refreshCredential("1");

    expect(mockHttpClient.fetch).toHaveBeenCalledWith(
      "https://test-api.example.com/providers/1/refresh-credential",
      expect.objectContaining({ method: "POST" }),
    );
  });

  it("should refresh provider usage", async () => {
    mockHttpClient.mockResponse({ ok: true, status: 204 });

    await api.providers.refreshUsage("1");

    expect(mockHttpClient.fetch).toHaveBeenCalledWith(
      "https://test-api.example.com/providers/1/refresh-usage",
      expect.objectContaining({ method: "POST" }),
    );
  });

  it("should batch enable providers", async () => {
    const batchRequest = { action: "enable" as const, ids: ["1", "2", "3"] };
    const batchResponse = {
      success: true,
      affected: 3,
      results: [
        { id: "1", success: true },
        { id: "2", success: true },
        { id: "3", success: true },
      ],
    };
    mockHttpClient.mockResponse({
      ok: true,
      status: 200,
      json: () => Promise.resolve(batchResponse),
    });

    const result = await api.providers.batch(batchRequest);

    expect(mockHttpClient.fetch).toHaveBeenCalledWith(
      "https://test-api.example.com/providers/batch",
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify(batchRequest),
      }),
    );
    expect(result).toEqual(batchResponse);
  });

  it("should batch disable providers", async () => {
    const batchRequest = { action: "disable" as const, ids: ["1", "2"] };
    const batchResponse = {
      success: true,
      affected: 2,
      results: [
        { id: "1", success: true },
        { id: "2", success: true },
      ],
    };
    mockHttpClient.mockResponse({
      ok: true,
      status: 200,
      json: () => Promise.resolve(batchResponse),
    });

    const result = await api.providers.batch(batchRequest);

    expect(result).toEqual(batchResponse);
  });

  it("should batch reset providers", async () => {
    const batchRequest = { action: "reset" as const, ids: ["1"] };
    const batchResponse = {
      success: true,
      affected: 1,
      results: [{ id: "1", success: true }],
    };
    mockHttpClient.mockResponse({
      ok: true,
      status: 200,
      json: () => Promise.resolve(batchResponse),
    });

    const result = await api.providers.batch(batchRequest);

    expect(result).toEqual(batchResponse);
  });

  it("should batch delete providers", async () => {
    const batchRequest = { action: "delete" as const, ids: ["1", "2"] };
    const batchResponse = {
      success: true,
      affected: 2,
      results: [
        { id: "1", success: true },
        { id: "2", success: true },
      ],
    };
    mockHttpClient.mockResponse({
      ok: true,
      status: 200,
      json: () => Promise.resolve(batchResponse),
    });

    const result = await api.providers.batch(batchRequest);

    expect(result).toEqual(batchResponse);
  });

  it("should handle partial batch failure", async () => {
    const batchRequest = { action: "enable" as const, ids: ["1", "2"] };
    const batchResponse = {
      success: false,
      affected: 1,
      results: [
        { id: "1", success: true },
        { id: "2", success: false, error: "Provider not found" },
      ],
    };
    mockHttpClient.mockResponse({
      ok: true,
      status: 200,
      json: () => Promise.resolve(batchResponse),
    });

    const result = await api.providers.batch(batchRequest);

    expect(result.success).toBe(false);
    expect(result.affected).toBe(1);
    expect(result.results[1].error).toBe("Provider not found");
  });
});
