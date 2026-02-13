import { describe, it, expect, beforeEach } from "vitest";
import { createApiClient } from "./client";
import { createMockStorage, createMockHttpClient } from "./test-mocks";

describe("createApiClient config API", () => {
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

  it("should get config with defaults and values", async () => {
    const configResponse = {
      defaults: {
        sticky_ttl: "300",
        auth_mode: "auto",
        global_max_attempts: "3",
      },
      values: {
        global_max_attempts: "5",
      },
    };
    mockHttpClient.mockResponse({
      ok: true,
      status: 200,
      json: () => Promise.resolve(configResponse),
    });

    const result = await api.config.get();

    expect(result).toEqual(configResponse);
    expect(result.defaults.sticky_ttl).toBe("300");
    expect(result.values.global_max_attempts).toBe("5");
    expect(mockHttpClient.fetch).toHaveBeenCalledWith(
      "https://test-api.example.com/config",
      expect.any(Object),
    );
  });

  it("should update config and return new response", async () => {
    const updatedConfigResponse = {
      defaults: {
        sticky_ttl: "300",
        auth_mode: "auto",
      },
      values: {
        sticky_ttl: "600",
      },
    };
    mockHttpClient.mockResponse({
      ok: true,
      status: 200,
      json: () => Promise.resolve(updatedConfigResponse),
    });

    const result = await api.config.update({ sticky_ttl: "600" });

    expect(result).toEqual(updatedConfigResponse);
    expect(mockHttpClient.fetch).toHaveBeenCalledWith(
      "https://test-api.example.com/config",
      expect.objectContaining({
        method: "PUT",
        body: JSON.stringify({ sticky_ttl: "600" }),
      }),
    );
  });

  it("should export config", async () => {
    const exportedConfig = {
      version: "1.0",
      exported_at: "2025-01-13T10:00:00Z",
      providers: [
        {
          id: "provider-1",
          name: "OpenAI",
          api_key: "sk-***",
          api_types: [
            { api_type: "claude", base_url: "https://api.openai.com" },
          ],
          auth_mode: "bearer",
          weight: 1,
          priority: 1,
          concurrency: 10,
          max_retries: 3,
          enabled: true,
        },
      ],
      groups: [
        {
          id: "group-1",
          name: "Primary",
          strategy: "round_robin",
          priority: 1,
          weight: 1,
          enabled: true,
        },
      ],
      settings: { sticky_ttl: "300" },
    };
    mockHttpClient.mockResponse({
      ok: true,
      status: 200,
      json: () => Promise.resolve(exportedConfig),
    });

    const result = await api.config.export();

    expect(result).toEqual(exportedConfig);
    expect(mockHttpClient.fetch).toHaveBeenCalledWith(
      "https://test-api.example.com/config/export",
      expect.any(Object),
    );
  });

  it("should preview config import (dry run)", async () => {
    const importRequest = {
      providers: [
        {
          id: "provider-1",
          name: "OpenAI",
          api_key: "sk-new",
          api_types: [
            { api_type: "claude", base_url: "https://api.openai.com" },
          ],
          auth_mode: "bearer" as const,
          weight: 1,
          priority: 1,
          concurrency: 10,
          max_retries: 3,
          enabled: true,
        },
      ],
      groups: [],
      settings: { sticky_ttl: "600" },
    };
    const previewResponse = {
      dry_run: true,
      changes: {
        providers: { add: 0, update: 1, delete: 0 },
        groups: { add: 0, update: 0, delete: 0 },
        settings: { add: 0, update: 1, delete: 0 },
      },
      warnings: [],
    };
    mockHttpClient.mockResponse({
      ok: true,
      status: 200,
      json: () => Promise.resolve(previewResponse),
    });

    const result = await api.config.importPreview(importRequest);

    expect(result).toEqual(previewResponse);
    expect(mockHttpClient.fetch).toHaveBeenCalledWith(
      "https://test-api.example.com/config/import?dry_run=true",
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify(importRequest),
      }),
    );
  });

  it("should import config", async () => {
    const importRequest = {
      providers: [
        {
          id: "provider-1",
          name: "OpenAI",
          api_key: "sk-new",
          api_types: [
            { api_type: "claude", base_url: "https://api.openai.com" },
          ],
          auth_mode: "bearer" as const,
          weight: 1,
          priority: 1,
          concurrency: 10,
          max_retries: 3,
          enabled: true,
        },
      ],
      groups: [],
      settings: { sticky_ttl: "600" },
    };
    const importResult = {
      success: true,
      applied: {
        providers: { added: 0, updated: 1 },
        groups: { added: 0, updated: 0 },
        settings: { added: 0, updated: 1 },
      },
    };
    mockHttpClient.mockResponse({
      ok: true,
      status: 200,
      json: () => Promise.resolve(importResult),
    });

    const result = await api.config.import(importRequest);

    expect(result).toEqual(importResult);
    expect(mockHttpClient.fetch).toHaveBeenCalledWith(
      "https://test-api.example.com/config/import",
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify(importRequest),
      }),
    );
  });

  it("should handle import with warnings", async () => {
    const importRequest = {
      providers: [],
      groups: [],
      settings: {},
    };
    const previewResponse = {
      dry_run: true,
      changes: {
        providers: { add: 0, update: 0, delete: 0 },
        groups: { add: 0, update: 0, delete: 0 },
        settings: { add: 0, update: 0, delete: 0 },
      },
      warnings: ["No changes detected", "Empty configuration"],
    };
    mockHttpClient.mockResponse({
      ok: true,
      status: 200,
      json: () => Promise.resolve(previewResponse),
    });

    const result = await api.config.importPreview(importRequest);

    expect(result.warnings).toHaveLength(2);
    expect(result.warnings).toContain("No changes detected");
  });
});
