import { describe, it, expect, beforeEach } from "vitest";
import { createApiClient } from "./client";
import { createMockStorage, createMockHttpClient } from "./test-mocks";

describe("createApiClient groups API", () => {
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

  it("should list groups", async () => {
    const groups = [{ id: "1", name: "Primary" }];
    mockHttpClient.mockResponse({
      ok: true,
      status: 200,
      json: () => Promise.resolve(groups),
    });

    const result = await api.groups.list();

    expect(result).toEqual(groups);
  });

  it("should get group by id", async () => {
    mockHttpClient.mockResponse({
      ok: true,
      status: 200,
      json: () => Promise.resolve({ id: "1", name: "Primary" }),
    });

    await api.groups.get("1");

    expect(mockHttpClient.fetch).toHaveBeenCalledWith(
      "https://test-api.example.com/groups/1",
      expect.any(Object),
    );
  });

  it("should create group", async () => {
    const input = { name: "New Group" };
    mockHttpClient.mockResponse({
      ok: true,
      status: 201,
      json: () => Promise.resolve({ id: "1", ...input }),
    });

    await api.groups.create(input);

    expect(mockHttpClient.fetch).toHaveBeenCalledWith(
      "https://test-api.example.com/groups",
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify(input),
      }),
    );
  });

  it("should update group", async () => {
    const input = { name: "Updated Group" };
    mockHttpClient.mockResponse({
      ok: true,
      status: 200,
      json: () => Promise.resolve({ id: "1", ...input }),
    });

    await api.groups.update("1", input);

    expect(mockHttpClient.fetch).toHaveBeenCalledWith(
      "https://test-api.example.com/groups/1",
      expect.objectContaining({
        method: "PUT",
        body: JSON.stringify(input),
      }),
    );
  });

  it("should delete group", async () => {
    mockHttpClient.mockResponse({ ok: true, status: 204 });

    await api.groups.delete("1");

    expect(mockHttpClient.fetch).toHaveBeenCalledWith(
      "https://test-api.example.com/groups/1",
      expect.objectContaining({ method: "DELETE" }),
    );
  });

  it("should enable group", async () => {
    const mockGroup = { id: "1", name: "Primary", enabled: true };
    mockHttpClient.mockResponse({
      ok: true,
      status: 200,
      json: () => Promise.resolve(mockGroup),
    });

    const result = await api.groups.enable("1");

    expect(mockHttpClient.fetch).toHaveBeenCalledWith(
      "https://test-api.example.com/groups/1/enable",
      expect.objectContaining({ method: "POST" }),
    );
    expect(result).toEqual(mockGroup);
  });

  it("should disable group", async () => {
    const mockGroup = { id: "1", name: "Primary", enabled: false };
    mockHttpClient.mockResponse({
      ok: true,
      status: 200,
      json: () => Promise.resolve(mockGroup),
    });

    const result = await api.groups.disable("1");

    expect(mockHttpClient.fetch).toHaveBeenCalledWith(
      "https://test-api.example.com/groups/1/disable",
      expect.objectContaining({ method: "POST" }),
    );
    expect(result).toEqual(mockGroup);
  });
});
