import { beforeEach, describe, expect, it } from "vitest";
import { createApiClient } from "./client";
import { createMockHttpClient, createMockStorage } from "./test-mocks";
import type {
  ProviderImportCommitRequest,
  ProviderImportCommitResult,
  ProviderImportPreview,
} from "./provider-import-types";

describe("createApiClient provider imports API", () => {
  let mockHttpClient: ReturnType<typeof createMockHttpClient>;
  let api: ReturnType<typeof createApiClient>;

  beforeEach(() => {
    mockHttpClient = createMockHttpClient();
    api = createApiClient({
      storage: createMockStorage(),
      httpClient: mockHttpClient,
      baseUrl: "https://test-api.example.com",
    });
  });

  it("posts the sub2api document as the raw preview body", async () => {
    const source = JSON.stringify({
      accounts: [{ name: "account-a", priority: 2, concurrency: 3 }],
    });
    const preview: ProviderImportPreview = {
      import_id: "import-1",
      expires_at: "2026-07-30T20:00:00Z",
      create_defaults: {
        weight: 1,
        max_retries: 0,
        backoff: {
          initial_delay: "100ms",
          max_delay: "5s",
          multiplier: 2,
          jitter: false,
        },
      },
      items: [
        {
          candidate_id: "candidate-1",
          source_index: 0,
          status: "ready",
          name: "account-a",
          provider_id: "account-a",
          email: "a@example.com",
          account_id: "acct-1",
          plan_type: "plus",
          priority: 2,
          concurrency: 3,
          default_selected: true,
          warnings: [],
        },
      ],
      summary: {
        total: 1,
        ready: 1,
        existing: 0,
        duplicate: 0,
        invalid: 0,
        unsupported: 0,
      },
      warnings: [
        {
          code: "FIELD_IGNORED",
          message: "rate_multiplier is not imported",
        },
      ],
    };
    mockHttpClient.mockResponse({
      status: 201,
      json: () => Promise.resolve(preview),
    });

    const result = await api.providerImports.preview(source);

    expect(result).toEqual(preview);
    expect(mockHttpClient.fetch).toHaveBeenCalledWith(
      "https://test-api.example.com/provider-imports",
      expect.objectContaining({
        method: "POST",
        body: source,
      }),
    );
  });

  it("commits the reviewed candidate plan", async () => {
    const request: ProviderImportCommitRequest = {
      group_id: "openai-accounts",
      items: [
        {
          candidate_id: "candidate-1",
          action: "create",
          provider_id: "account-a",
          name: "Account A",
          priority: 2,
          weight: 1,
          concurrency: 3,
          max_retries: 2,
          backoff: {
            initial_delay: "500ms",
            max_delay: "5s",
            multiplier: 2,
            jitter: true,
          },
        },
        {
          candidate_id: "candidate-2",
          action: "update",
          provider_id: "provider-existing",
        },
      ],
    };
    const commitResult: ProviderImportCommitResult = {
      import_id: "import/session 1",
      summary: { created: 1, updated: 1, skipped: 0 },
      items: [
        {
          candidate_id: "candidate-1",
          outcome: "created",
          provider_id: "account-a",
          name: "Account A",
        },
        {
          candidate_id: "candidate-2",
          outcome: "updated",
          provider_id: "provider-existing",
          name: "Existing account",
        },
      ],
    };
    mockHttpClient.mockResponse({
      json: () => Promise.resolve(commitResult),
    });

    const result = await api.providerImports.commit(
      "import/session 1",
      request,
    );

    expect(result).toEqual(commitResult);
    expect(mockHttpClient.fetch).toHaveBeenCalledWith(
      "https://test-api.example.com/provider-imports/import%2Fsession%201/commit",
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify(request),
      }),
    );
  });

  it("discards an abandoned import session", async () => {
    mockHttpClient.mockResponse({ status: 204 });

    await expect(
      api.providerImports.discard("import/session 1"),
    ).resolves.toBeUndefined();
    expect(mockHttpClient.fetch).toHaveBeenCalledWith(
      "https://test-api.example.com/provider-imports/import%2Fsession%201",
      expect.objectContaining({ method: "DELETE" }),
    );
  });

  it("preserves an expired-preview status for recovery UX", async () => {
    mockHttpClient.mockResponse({
      ok: false,
      status: 410,
      statusText: "Gone",
      json: () =>
        Promise.resolve({
          code: "PROVIDER_IMPORT_EXPIRED",
          message: "Preview expired",
        }),
    });

    await expect(
      api.providerImports.commit("import-1", { group_id: null, items: [] }),
    ).rejects.toMatchObject({
      code: "PROVIDER_IMPORT_EXPIRED",
      message: "Preview expired",
      status: 410,
    });
  });

  it("preserves structured provider conflicts for stale-preview recovery", async () => {
    mockHttpClient.mockResponse({
      ok: false,
      status: 409,
      statusText: "Conflict",
      json: () =>
        Promise.resolve({
          code: "CONFLICT",
          message: "Provider state changed after preview",
          details: {
            conflicts: [
              {
                candidate_id: "candidate-1",
                kind: "credential_version_mismatch",
                provider_id: "provider-1",
                expected_credential_version: 1,
                current_credential_version: 2,
              },
            ],
          },
        }),
    });

    await expect(
      api.providerImports.commit("import-1", { group_id: null, items: [] }),
    ).rejects.toMatchObject({
      status: 409,
      details: {
        conflicts: [
          {
            candidate_id: "candidate-1",
            kind: "credential_version_mismatch",
            current_credential_version: 2,
          },
        ],
      },
    });
  });
});
