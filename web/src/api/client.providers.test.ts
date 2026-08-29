import { describe, it, expect, beforeEach } from "vitest";
import { createApiClient } from "./client";
import { createMockStorage, createMockHttpClient } from "./test-mocks";

const credentialSession = {
  id: "session-1",
  name: "Test key",
  kind: "api_key",
  version: 1,
  subject: { kind: "keyed_digest", value: "ZGlnaWVzdA==", key_version: "h1" },
  auth_state: {
    status: "active",
    status_reason: undefined,
    last_error: undefined,
    last_transition_at: undefined,
    email: undefined,
    account_id: undefined,
    plan_type: undefined,
    expires_at: undefined,
    last_refresh_at: undefined,
    usage_snapshot: undefined,
    refresh_fail_count: undefined,
    last_refresh_failure_at: undefined,
  },
};

function providerPayload(overrides: Record<string, unknown> = {}) {
  return {
    id: "1",
    name: "OpenAI",
    api_types: [
      {
        api_type: "claude",
        base_url: "https://test.example.com",
        credential_session_id: credentialSession.id,
      },
    ],
    auth_mode: "auto",
    credential_sessions: [credentialSession],
    usage_limit_policy: "switch_provider",
    group_id: null,
    weight: 1,
    priority: 0,
    concurrency: 0,
    max_retries: 0,
    backoff: undefined,
    vendor: "",
    failover_scope: "any",
    accept_failover: "any",
    enabled: true,
    health: undefined,
    usage_limit_policy_explicit: undefined,
    created_at: "2026-08-28T00:00:00Z",
    updated_at: "2026-08-28T00:00:00Z",
    ...overrides,
  };
}

function credentialSessionPayload(overrides: Record<string, unknown> = {}) {
  return {
    ...credentialSession,
    secret_data: "sk-current",
    referenced_route_target_ids: ["1"],
    route_references: [
      { provider_id: "1", provider_name: "OpenAI", api_type: "claude" },
    ],
    created_at: "2026-08-28T00:00:00Z",
    updated_at: "2026-08-28T00:00:00Z",
    ...overrides,
  };
}

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
    const providers = [providerPayload()];
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
    const provider = providerPayload();
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
      api_types: [
        {
          api_type: "claude",
          base_url: "https://test.example.com",
          credential_session_id: "session-1",
        },
      ],
      usage_limit_policy: "switch_provider" as const,
    };
    const created = providerPayload({ name: input.name });
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
      api_types: [
        {
          api_type: "claude",
          base_url: "https://test.example.com",
          credential_session_id: "session-1",
        },
      ],
      usage_limit_policy: "suspend" as const,
    };
    mockHttpClient.mockResponse({
      ok: true,
      status: 200,
      json: () =>
        Promise.resolve(
          providerPayload({
            name: input.name,
            usage_limit_policy: input.usage_limit_policy,
          }),
        ),
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
    const mockProvider = providerPayload({ name: "Test", enabled: true });
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
    const mockProvider = providerPayload({ name: "Test", enabled: false });
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

  it("should refresh a credential session", async () => {
    const refreshed = credentialSessionPayload();
    mockHttpClient.mockResponse({
      ok: true,
      status: 200,
      json: () => Promise.resolve(refreshed),
    });

    await api.credentialSessions.refresh("session/1");

    expect(mockHttpClient.fetch).toHaveBeenCalledWith(
      "https://test-api.example.com/credential-sessions/session%2F1/refresh",
      expect.objectContaining({ method: "POST" }),
    );
  });

  it("should reauthenticate a credential session from a verified login", async () => {
    const reauthenticated = credentialSessionPayload({
      kind: "chatgpt",
      version: 2,
      secret_data: undefined,
    });
    mockHttpClient.mockResponse({
      ok: true,
      status: 200,
      json: () => Promise.resolve(reauthenticated),
    });

    const result = await api.credentialSessions.reauthenticate("session/1", {
      expected_version: 1,
      credential_login_id: "login-1",
    });

    expect(result).toEqual(reauthenticated);
    expect(mockHttpClient.fetch).toHaveBeenCalledWith(
      "https://test-api.example.com/credential-sessions/session%2F1/reauthenticate",
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify({
          expected_version: 1,
          credential_login_id: "login-1",
        }),
      }),
    );
  });

  it("should expose API-key credential values returned by the admin resource", async () => {
    const session = credentialSessionPayload();
    mockHttpClient.mockResponse({
      ok: true,
      status: 200,
      json: () => Promise.resolve([session]),
    });

    const result = await api.credentialSessions.list();

    expect(result).toEqual([session]);
    expect(result[0].secret_data).toBe("sk-current");
  });

  it("should rename a credential session", async () => {
    const renamed = credentialSessionPayload({
      name: "Renamed key",
      version: 2,
    });
    mockHttpClient.mockResponse({
      ok: true,
      status: 200,
      json: () => Promise.resolve(renamed),
    });

    const result = await api.credentialSessions.rename("session/1", {
      expected_version: 1,
      name: "Renamed key",
    });

    expect(result.name).toBe("Renamed key");
    expect(mockHttpClient.fetch).toHaveBeenCalledWith(
      "https://test-api.example.com/credential-sessions/session%2F1/name",
      expect.objectContaining({
        method: "PATCH",
        body: JSON.stringify({ expected_version: 1, name: "Renamed key" }),
      }),
    );
  });

  it("should refresh credential-session usage", async () => {
    const refreshed = credentialSessionPayload();
    mockHttpClient.mockResponse({
      ok: true,
      status: 200,
      json: () => Promise.resolve(refreshed),
    });

    await api.credentialSessions.refreshUsage("session/1");

    expect(mockHttpClient.fetch).toHaveBeenCalledWith(
      "https://test-api.example.com/credential-sessions/session%2F1/refresh-usage",
      expect.objectContaining({ method: "POST" }),
    );
  });
});

describe("createApiClient provider export and batch API", () => {
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

  it("should export one provider as Codex auth.json", async () => {
    const authDocument = {
      auth_mode: "chatgpt",
      OPENAI_API_KEY: null,
      tokens: {
        id_token: "id-token",
        access_token: "access-token",
        refresh_token: "refresh-token",
        account_id: "account-123",
      },
      last_refresh: "2026-08-26T00:15:00Z",
    };
    mockHttpClient.mockResponse({
      ok: true,
      status: 200,
      json: () => Promise.resolve(authDocument),
    });

    const result = await api.credentialSessions.exportCodexAuth("gpt/session");

    expect(result).toEqual(authDocument);
    expect(mockHttpClient.fetch).toHaveBeenCalledWith(
      "https://test-api.example.com/credential-sessions/gpt%2Fsession/codex-auth",
      expect.any(Object),
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
