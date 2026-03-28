import { describe, it, expect, beforeEach } from "vitest";
import { createApiClient } from "./client";
import { createMockStorage, createMockHttpClient } from "./test-mocks";

describe("createApiClient logs API", () => {
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

  it("should list logs without params", async () => {
    const logsResponse = {
      logs: [{ id: 1, provider_id: "1" }],
      total: 1,
      limit: 20,
      offset: 0,
    };
    mockHttpClient.mockResponse({
      ok: true,
      status: 200,
      json: () => Promise.resolve(logsResponse),
    });

    const result = await api.logs.list();

    expect(result).toEqual(logsResponse);
    expect(mockHttpClient.fetch).toHaveBeenCalledWith(
      "https://test-api.example.com/logs",
      expect.any(Object),
    );
  });

  it("should list logs with limit param", async () => {
    mockHttpClient.mockResponse({
      ok: true,
      status: 200,
      json: () => Promise.resolve([]),
    });

    await api.logs.list({ limit: 50 });

    expect(mockHttpClient.fetch).toHaveBeenCalledWith(
      "https://test-api.example.com/logs?limit=50",
      expect.any(Object),
    );
  });

  it("should list logs with offset param", async () => {
    mockHttpClient.mockResponse({
      ok: true,
      status: 200,
      json: () => Promise.resolve([]),
    });

    await api.logs.list({ offset: 100 });

    expect(mockHttpClient.fetch).toHaveBeenCalledWith(
      "https://test-api.example.com/logs?offset=100",
      expect.any(Object),
    );
  });

  it("should list logs with both limit and offset", async () => {
    mockHttpClient.mockResponse({
      ok: true,
      status: 200,
      json: () => Promise.resolve([]),
    });

    await api.logs.list({ limit: 50, offset: 100 });

    expect(mockHttpClient.fetch).toHaveBeenCalledWith(
      "https://test-api.example.com/logs?limit=50&offset=100",
      expect.any(Object),
    );
  });

  it("should filter logs by success=false", async () => {
    mockHttpClient.mockResponse({
      ok: true,
      status: 200,
      json: () => Promise.resolve({ logs: [], total: 0, limit: 20, offset: 0 }),
    });

    await api.logs.list({ success: false });

    expect(mockHttpClient.fetch).toHaveBeenCalledWith(
      "https://test-api.example.com/logs?success=false",
      expect.any(Object),
    );
  });

  it("should filter logs by success=true", async () => {
    mockHttpClient.mockResponse({
      ok: true,
      status: 200,
      json: () => Promise.resolve({ logs: [], total: 0, limit: 20, offset: 0 }),
    });

    await api.logs.list({ success: true });

    expect(mockHttpClient.fetch).toHaveBeenCalledWith(
      "https://test-api.example.com/logs?success=true",
      expect.any(Object),
    );
  });

  it("should serialize websocket lifecycle filters", async () => {
    mockHttpClient.mockResponse({
      ok: true,
      status: 200,
      json: () => Promise.resolve({ logs: [], total: 0, limit: 20, offset: 0 }),
    });

    await api.logs.list({
      session_committed: true,
      client_visible: false,
      sticky_written: false,
      probe_outcome: "transport_failed",
      terminal_cause: "client_disconnect",
      commit_source: "upstream_message",
      recovery_action: "reconnect_required",
    });

    expect(mockHttpClient.fetch).toHaveBeenCalledWith(
      "https://test-api.example.com/logs?session_committed=true&client_visible=false&sticky_written=false&probe_outcome=transport_failed&terminal_cause=client_disconnect&commit_source=upstream_message&recovery_action=reconnect_required",
      expect.any(Object),
    );
  });
});
