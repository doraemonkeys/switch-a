import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { createApiClient } from "./client";
import { parseAPICatalog } from "./api-catalog";
import { createMockHttpClient, createMockStorage } from "./test-mocks";

function loadCatalogFixture(): unknown {
  return JSON.parse(
    readFileSync(
      resolve(process.cwd(), "../contracts/internal-error/v1/api-catalog.json"),
      "utf8",
    ),
  ) as unknown;
}

describe("apiCatalog client", () => {
  it("authenticates the canonical endpoint and decodes unknown JSON", async () => {
    const httpClient = createMockHttpClient();
    const storage = createMockStorage();
    const wireCatalog = loadCatalogFixture();
    storage.data.set("admin_token", "catalog-token");
    httpClient.mockResponse({
      json: () => Promise.resolve(wireCatalog),
    });
    const client = createApiClient({
      baseUrl: "https://admin.example.test/admin/api",
      httpClient,
      storage,
    });

    await expect(client.apiCatalog.get()).resolves.toEqual(
      parseAPICatalog(wireCatalog),
    );
    expect(httpClient.fetch).toHaveBeenCalledWith(
      "https://admin.example.test/admin/api/api-catalog",
      expect.objectContaining({
        headers: expect.objectContaining({
          Authorization: "Bearer catalog-token",
        }),
      }),
    );
  });

  it("rejects malformed server JSON instead of trusting its generic type", async () => {
    const httpClient = createMockHttpClient();
    httpClient.mockResponse({
      json: () => Promise.resolve({ schema_version: 1 }),
    });
    const client = createApiClient({
      baseUrl: "https://admin.example.test/admin/api",
      httpClient,
      storage: createMockStorage(),
    });

    await expect(client.apiCatalog.get()).rejects.toThrow(
      "custom_api_type_prefix",
    );
  });
});
