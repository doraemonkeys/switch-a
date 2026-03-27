import { beforeEach, describe, expect, it } from "vitest";
import { createApiClient } from "./client";
import { createMockHttpClient, createMockStorage } from "./test-mocks";

describe("createApiClient routing policies API", () => {
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

  it("should list routing policies", async () => {
    const policies = [
      {
        id: "policy-1",
        api_type: "codex",
        model_match_type: "prefix",
        model_match_value: "gpt-5",
        allowed_group_ids: ["primary"],
        allowed_vendors: ["openai"],
      },
    ];
    mockHttpClient.mockResponse({
      ok: true,
      status: 200,
      json: () => Promise.resolve(policies),
    });

    const result = await api.routingPolicies.list();

    expect(result).toEqual(policies);
    expect(mockHttpClient.fetch).toHaveBeenCalledWith(
      "https://test-api.example.com/routing-policies",
      expect.any(Object),
    );
  });

  it("should create a routing policy", async () => {
    const input = {
      api_type: "codex",
      model_match_type: "exact" as const,
      model_match_value: "gpt-5.1-codex",
      allowed_group_ids: ["primary"],
      allowed_vendors: ["openai"],
    };
    mockHttpClient.mockResponse({
      ok: true,
      status: 201,
      json: () => Promise.resolve({ id: "policy-1", ...input }),
    });

    await api.routingPolicies.create(input);

    expect(mockHttpClient.fetch).toHaveBeenCalledWith(
      "https://test-api.example.com/routing-policies",
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify(input),
      }),
    );
  });

  it("should update a routing policy", async () => {
    const input = {
      api_type: "codex",
      model_match_type: null,
      model_match_value: null,
      allowed_group_ids: ["primary"],
      allowed_vendors: ["openai"],
    };
    mockHttpClient.mockResponse({
      ok: true,
      status: 200,
      json: () => Promise.resolve({ id: "policy-1", ...input }),
    });

    await api.routingPolicies.update("policy-1", input);

    expect(mockHttpClient.fetch).toHaveBeenCalledWith(
      "https://test-api.example.com/routing-policies/policy-1",
      expect.objectContaining({
        method: "PUT",
        body: JSON.stringify(input),
      }),
    );
  });

  it("should delete a routing policy", async () => {
    mockHttpClient.mockResponse({ ok: true, status: 204 });

    await api.routingPolicies.delete("policy-1");

    expect(mockHttpClient.fetch).toHaveBeenCalledWith(
      "https://test-api.example.com/routing-policies/policy-1",
      expect.objectContaining({ method: "DELETE" }),
    );
  });
});
