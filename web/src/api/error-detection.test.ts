import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { beforeEach, describe, expect, it } from "vitest";
import { createApiClient } from "./client";
import { parseInternalErrorRuleETag } from "./error-detection-decoders";
import { createMockHttpClient, createMockStorage } from "./test-mocks";

function fixture(name: string): unknown {
  return JSON.parse(
    readFileSync(
      resolve(process.cwd(), `../contracts/internal-error/v1/${name}`),
      "utf8",
    ),
  );
}

describe("error-detection API client", () => {
  const baseUrl = "https://admin.example.test/admin/api";
  let httpClient: ReturnType<typeof createMockHttpClient>;
  let api: ReturnType<typeof createApiClient>;

  beforeEach(() => {
    httpClient = createMockHttpClient();
    api = createApiClient({
      baseUrl,
      httpClient,
      storage: createMockStorage(),
    });
  });

  it("returns the list with its verified revision ETag", async () => {
    const ruleList = fixture("rule-list.json");
    httpClient.mockResponse({
      status: 200,
      headers: new Headers({ ETag: '"internal-error-rules/7"' }),
      json: () => Promise.resolve(ruleList),
    });

    await expect(api.errorDetection.rules.list()).resolves.toEqual({
      value: ruleList,
      etag: '"internal-error-rules/7"',
    });
    expect(httpClient.fetch).toHaveBeenCalledWith(
      `${baseUrl}/internal-error-rules`,
      expect.objectContaining({
        headers: expect.objectContaining({
          "Content-Type": "application/json",
        }),
      }),
    );
  });

  it("creates a rule with the previous ETag and exact versioned payload", async () => {
    const mutations = fixture("rule-mutations.json") as {
      create: {
        if_match: string;
        request: { rule: never };
        etag: string;
        location: string;
        response: unknown;
      };
    };
    const create = mutations.create;
    httpClient.mockResponse({
      status: 201,
      headers: new Headers({ ETag: create.etag, Location: create.location }),
      json: () => Promise.resolve(create.response),
    });

    const result = await api.errorDetection.rules.create(
      create.request.rule,
      parseInternalErrorRuleETag(create.if_match),
    );

    expect(result).toEqual({ value: create.response, etag: create.etag });
    expect(httpClient.fetch).toHaveBeenCalledWith(
      `${baseUrl}/internal-error-rules`,
      expect.objectContaining({
        method: "POST",
        headers: expect.objectContaining({
          "If-Match": create.if_match,
        }),
        body: JSON.stringify(create.request),
      }),
    );
  });

  it("sends the exact complete permutation for reorder", async () => {
    const reorder = fixture("reorder.json") as {
      if_match: string;
      request: { ordered_rule_ids: readonly string[] };
      etag: string;
      response: unknown;
    };
    httpClient.mockResponse({
      status: 200,
      headers: new Headers({ ETag: reorder.etag }),
      json: () => Promise.resolve(reorder.response),
    });

    await api.errorDetection.rules.reorder(
      reorder.request.ordered_rule_ids,
      parseInternalErrorRuleETag(reorder.if_match),
    );

    expect(httpClient.fetch).toHaveBeenCalledWith(
      `${baseUrl}/internal-error-rules/reorder`,
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify(reorder.request),
      }),
    );
  });

  it("derives the new revision from a bodyless delete response", async () => {
    const deleteFixture = (
      fixture("rule-mutations.json") as {
        delete: {
          rule_id: string;
          if_match: string;
          etag: string;
        };
      }
    ).delete;
    httpClient.mockResponse({
      status: 204,
      headers: new Headers({ ETag: deleteFixture.etag }),
    });

    await expect(
      api.errorDetection.rules.delete(
        deleteFixture.rule_id,
        parseInternalErrorRuleETag(deleteFixture.if_match),
      ),
    ).resolves.toEqual({
      rule_set_revision: "10",
      etag: deleteFixture.etag,
    });
  });

  it("decodes stats and sends Test Message with an optional revision", async () => {
    const stats = fixture("rule-stats.json");
    httpClient.mockResponse({
      status: 200,
      json: () => Promise.resolve(stats),
    });
    await expect(api.errorDetection.stats.get()).resolves.toEqual(stats);

    const testMessage = fixture("test-message.json") as {
      complete: {
        if_match: string;
        request: { schema_version: number; [key: string]: unknown };
        response: unknown;
      };
    };
    const { schema_version: schemaVersion, ...input } =
      testMessage.complete.request;
    expect(schemaVersion).toBe(1);
    httpClient.mockResponse({
      status: 200,
      json: () => Promise.resolve(testMessage.complete.response),
    });

    await expect(
      api.errorDetection.testMessage(
        input as never,
        parseInternalErrorRuleETag(testMessage.complete.if_match),
      ),
    ).resolves.toEqual(testMessage.complete.response);
    expect(httpClient.fetch).toHaveBeenLastCalledWith(
      `${baseUrl}/internal-error-rules/test-message`,
      expect.objectContaining({
        method: "POST",
        headers: expect.objectContaining({
          "If-Match": testMessage.complete.if_match,
        }),
        body: JSON.stringify(testMessage.complete.request),
      }),
    );
  });

  it("surfaces a validated revision conflict without trusting malformed errors", async () => {
    const revisionMismatch = (
      fixture("errors.json") as {
        cases: Array<{ name: string; status: number; body: unknown }>;
      }
    ).cases.find(({ name }) => name === "revision mismatch");
    expect(revisionMismatch).toBeDefined();
    httpClient.mockResponse({
      ok: false,
      status: revisionMismatch?.status,
      statusText: "Precondition Failed",
      json: () => Promise.resolve(revisionMismatch?.body),
    });
    await expect(api.errorDetection.rules.list()).rejects.toMatchObject({
      code: "REVISION_MISMATCH",
      details: { current_revision: "10" },
    });

    httpClient.mockResponse({
      ok: false,
      status: 412,
      statusText: "Precondition Failed",
      json: () =>
        Promise.resolve({
          code: "REVISION_MISMATCH",
          message: "changed",
          details: { current_revision: "10", untrusted: true },
        }),
    });
    await expect(api.errorDetection.rules.list()).rejects.toMatchObject({
      code: "UNKNOWN_ERROR",
      message: "Precondition Failed",
    });
  });

  it("rejects a successful response whose ETag disagrees with its body", async () => {
    const ruleList = fixture("rule-list.json");
    httpClient.mockResponse({
      status: 200,
      headers: new Headers({ ETag: '"internal-error-rules/8"' }),
      json: () => Promise.resolve(ruleList),
    });
    await expect(api.errorDetection.rules.list()).rejects.toThrow(
      "rule response ETag and revision disagree",
    );
  });

  it("rejects a fabricated mutation ETag before issuing a request", async () => {
    await expect(
      api.errorDetection.rules.delete("rule-id", "*" as never),
    ).rejects.toThrow("must be one strong canonical rule ETag");
    expect(httpClient.fetch).not.toHaveBeenCalled();
  });
});
