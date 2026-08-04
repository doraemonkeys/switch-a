import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it, vi } from "vitest";
import type {
  InternalErrorRuleSpec,
  TestMessageInput,
} from "@/features/error-detection/contracts";
import { createApiClient } from "./client";
import { parseInternalErrorRuleETag } from "./error-detection-decoders";
import { createMockHttpClient, createMockStorage } from "./test-mocks";

const BASE_URL = "https://acceptance.example.test/admin/api";

function loadFixture<T>(name: string): T {
  return JSON.parse(
    readFileSync(
      resolve(process.cwd(), `../contracts/internal-error/v1/${name}`),
      "utf8",
    ),
  ) as T;
}

interface MutationCase {
  readonly rule_id?: string;
  readonly if_match: string;
  readonly request?: {
    readonly schema_version: number;
    readonly rule: InternalErrorRuleSpec;
  };
  readonly etag: string;
  readonly location?: string;
  readonly response?: unknown;
}

interface MutationFixture {
  readonly create: MutationCase & {
    readonly request: {
      readonly schema_version: number;
      readonly rule: InternalErrorRuleSpec;
    };
    readonly response: {
      readonly rule_set_revision: string;
      readonly rule: { readonly id: string };
    };
    readonly location: string;
  };
  readonly update: MutationCase & {
    readonly rule_id: string;
    readonly request: {
      readonly schema_version: number;
      readonly rule: InternalErrorRuleSpec;
    };
    readonly response: unknown;
  };
  readonly delete: MutationCase & {
    readonly rule_id: string;
  };
}

interface ReorderFixture {
  readonly if_match: string;
  readonly request: {
    readonly schema_version: number;
    readonly ordered_rule_ids: readonly string[];
  };
  readonly etag: string;
  readonly response: unknown;
}

interface TestMessageFixture {
  readonly complete: {
    readonly if_match: string;
    readonly request: { readonly schema_version: number } & TestMessageInput;
    readonly response: unknown;
  };
}

describe("V5C error-detection wire acceptance", () => {
  it("executes the full CRUD, reorder, stats, and runtime-analysis contract from shared fixtures", async () => {
    const ruleList = loadFixture<{
      readonly rule_set_revision: string;
      readonly rules: readonly [
        { readonly id: string },
        ...Array<{ readonly id: string }>,
      ];
    }>("rule-list.json");
    const ruleStats = loadFixture<unknown>("rule-stats.json");
    const mutations = loadFixture<MutationFixture>("rule-mutations.json");
    const reorder = loadFixture<ReorderFixture>("reorder.json");
    const testMessage = loadFixture<TestMessageFixture>("test-message.json");
    const httpClient = createMockHttpClient();
    const api = createApiClient({
      baseUrl: BASE_URL,
      httpClient,
      storage: createMockStorage(),
    });

    httpClient.mockResponse({
      status: 200,
      headers: new Headers({ ETag: '"internal-error-rules/7"' }),
      json: () => Promise.resolve(ruleList),
    });
    await api.errorDetection.rules.list();

    httpClient.mockResponse({
      status: 200,
      headers: new Headers({ ETag: mutations.create.etag }),
      json: () => Promise.resolve(mutations.create.response),
    });
    await api.errorDetection.rules.get(mutations.create.response.rule.id);

    httpClient.mockResponse({
      status: 201,
      headers: new Headers({
        ETag: mutations.create.etag,
        Location: mutations.create.location,
      }),
      json: () => Promise.resolve(mutations.create.response),
    });
    await api.errorDetection.rules.create(
      mutations.create.request.rule,
      parseInternalErrorRuleETag(mutations.create.if_match),
    );

    httpClient.mockResponse({
      status: 200,
      headers: new Headers({ ETag: mutations.update.etag }),
      json: () => Promise.resolve(mutations.update.response),
    });
    await api.errorDetection.rules.update(
      mutations.update.rule_id,
      mutations.update.request.rule,
      parseInternalErrorRuleETag(mutations.update.if_match),
    );

    httpClient.mockResponse({
      status: 204,
      headers: new Headers({ ETag: mutations.delete.etag }),
    });
    await api.errorDetection.rules.delete(
      mutations.delete.rule_id,
      parseInternalErrorRuleETag(mutations.delete.if_match),
    );

    httpClient.mockResponse({
      status: 200,
      headers: new Headers({ ETag: reorder.etag }),
      json: () => Promise.resolve(reorder.response),
    });
    await api.errorDetection.rules.reorder(
      reorder.request.ordered_rule_ids,
      parseInternalErrorRuleETag(reorder.if_match),
    );

    httpClient.mockResponse({
      status: 200,
      json: () => Promise.resolve(ruleStats),
    });
    await expect(api.errorDetection.stats.get()).resolves.toEqual(ruleStats);

    const { schema_version: schemaVersion, ...testInput } =
      testMessage.complete.request;
    expect(schemaVersion).toBe(1);
    httpClient.mockResponse({
      status: 200,
      json: () => Promise.resolve(testMessage.complete.response),
    });
    await api.errorDetection.testMessage(
      testInput,
      parseInternalErrorRuleETag(testMessage.complete.if_match),
    );

    const requests = vi.mocked(httpClient.fetch).mock.calls as unknown as Array<
      readonly [string, RequestInit]
    >;

    expect(requests[0]?.[0]).toBe(`${BASE_URL}/internal-error-rules`);
    expect(requests[1]?.[0]).toBe(
      `${BASE_URL}/internal-error-rules/${mutations.create.response.rule.id}`,
    );
    expect(requests[2]).toEqual([
      `${BASE_URL}/internal-error-rules`,
      expect.objectContaining({
        method: "POST",
        headers: expect.objectContaining({
          "If-Match": mutations.create.if_match,
        }),
        body: JSON.stringify(mutations.create.request),
      }),
    ]);
    expect(requests[3]).toEqual([
      `${BASE_URL}/internal-error-rules/${mutations.update.rule_id}`,
      expect.objectContaining({
        method: "PUT",
        headers: expect.objectContaining({
          "If-Match": mutations.update.if_match,
        }),
        body: JSON.stringify(mutations.update.request),
      }),
    ]);
    expect(requests[4]).toEqual([
      `${BASE_URL}/internal-error-rules/${mutations.delete.rule_id}`,
      expect.objectContaining({
        method: "DELETE",
        headers: expect.objectContaining({
          "If-Match": mutations.delete.if_match,
        }),
      }),
    ]);
    expect(requests[5]).toEqual([
      `${BASE_URL}/internal-error-rules/reorder`,
      expect.objectContaining({
        method: "POST",
        headers: expect.objectContaining({ "If-Match": reorder.if_match }),
        body: JSON.stringify(reorder.request),
      }),
    ]);
    expect(requests[6]?.[0]).toBe(`${BASE_URL}/internal-error-rule-stats`);
    expect(requests[7]).toEqual([
      `${BASE_URL}/internal-error-rules/test-message`,
      expect.objectContaining({
        method: "POST",
        headers: expect.objectContaining({
          "If-Match": testMessage.complete.if_match,
        }),
        body: JSON.stringify(testMessage.complete.request),
      }),
    ]);
  });
});
