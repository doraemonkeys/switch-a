import type { ApiErrorDetails } from "./types";
import {
  parseInternalErrorAPIError,
  parseInternalErrorRuleETag,
  parseInternalErrorRuleListResponse,
  parseInternalErrorRuleResponse,
  parseInternalErrorRuleStatsResponse,
  parseTestMessageResponse,
  revisionFromInternalErrorRuleETag,
} from "./error-detection-decoders";
import {
  INTERNAL_ERROR_SCHEMA_VERSION,
  type DeletedInternalErrorRule,
  type InternalErrorRuleETag,
  type InternalErrorRuleListResponse,
  type InternalErrorRuleResponse,
  type InternalErrorRuleSpec,
  type InternalErrorRuleStatsResponse,
  type RevisionedInternalErrorResource,
  type TestMessageInput,
  type TestMessageResponse,
} from "@/features/error-detection/contracts";

const RULES_ENDPOINT = "/internal-error-rules";
const RULE_STATS_ENDPOINT = "/internal-error-rule-stats";
const TEST_MESSAGE_ENDPOINT = `${RULES_ENDPOINT}/test-message`;

export interface DecodedAPIError {
  readonly code: string;
  readonly message: string;
  readonly details?: ApiErrorDetails;
}

export type APIErrorDecoder = (value: unknown) => DecodedAPIError;

export type AuthenticatedResponseRequest = (
  endpoint: string,
  options?: RequestInit,
  errorDecoder?: APIErrorDecoder,
) => Promise<Response>;

function expectStatus(response: Response, expected: number, operation: string) {
  if (response.status !== expected) {
    throw new Error(
      `${operation} returned ${response.status}; expected ${expected}`,
    );
  }
}

async function readRevisioned<T extends { readonly rule_set_revision: string }>(
  response: Response,
  decoder: (value: unknown) => T,
): Promise<RevisionedInternalErrorResource<T>> {
  const value = decoder(await response.json());
  const etag = parseInternalErrorRuleETag(response.headers.get("ETag"));
  if (revisionFromInternalErrorRuleETag(etag) !== value.rule_set_revision) {
    throw new Error("rule response ETag and revision disagree");
  }
  return Object.freeze({ value, etag });
}

function mutationHeaders(etag: InternalErrorRuleETag): HeadersInit {
  return { "If-Match": parseInternalErrorRuleETag(etag) };
}

function createInternalErrorRulesApi(request: AuthenticatedResponseRequest) {
  return {
    list: async (): Promise<
      RevisionedInternalErrorResource<InternalErrorRuleListResponse>
    > => {
      const response = await request(
        RULES_ENDPOINT,
        undefined,
        parseInternalErrorAPIError,
      );
      expectStatus(response, 200, "list internal error rules");
      return readRevisioned(response, parseInternalErrorRuleListResponse);
    },
    get: async (
      ruleID: string,
    ): Promise<RevisionedInternalErrorResource<InternalErrorRuleResponse>> => {
      const response = await request(
        `${RULES_ENDPOINT}/${encodeURIComponent(ruleID)}`,
        undefined,
        parseInternalErrorAPIError,
      );
      expectStatus(response, 200, "get internal error rule");
      return readRevisioned(response, parseInternalErrorRuleResponse);
    },
    create: async (
      rule: InternalErrorRuleSpec,
      etag: InternalErrorRuleETag,
    ): Promise<RevisionedInternalErrorResource<InternalErrorRuleResponse>> => {
      const response = await request(
        RULES_ENDPOINT,
        {
          method: "POST",
          headers: mutationHeaders(etag),
          body: JSON.stringify({
            schema_version: INTERNAL_ERROR_SCHEMA_VERSION,
            rule,
          }),
        },
        parseInternalErrorAPIError,
      );
      expectStatus(response, 201, "create internal error rule");
      const result = await readRevisioned(
        response,
        parseInternalErrorRuleResponse,
      );
      const location = response.headers.get("Location");
      if (!location?.endsWith(`/${result.value.rule.id}`)) {
        throw new Error(
          "created rule response is missing its canonical Location",
        );
      }
      return result;
    },
    update: async (
      ruleID: string,
      rule: InternalErrorRuleSpec,
      etag: InternalErrorRuleETag,
    ): Promise<RevisionedInternalErrorResource<InternalErrorRuleResponse>> => {
      const response = await request(
        `${RULES_ENDPOINT}/${encodeURIComponent(ruleID)}`,
        {
          method: "PUT",
          headers: mutationHeaders(etag),
          body: JSON.stringify({
            schema_version: INTERNAL_ERROR_SCHEMA_VERSION,
            rule,
          }),
        },
        parseInternalErrorAPIError,
      );
      expectStatus(response, 200, "update internal error rule");
      return readRevisioned(response, parseInternalErrorRuleResponse);
    },
    delete: async (
      ruleID: string,
      etag: InternalErrorRuleETag,
    ): Promise<DeletedInternalErrorRule> => {
      const response = await request(
        `${RULES_ENDPOINT}/${encodeURIComponent(ruleID)}`,
        { method: "DELETE", headers: mutationHeaders(etag) },
        parseInternalErrorAPIError,
      );
      expectStatus(response, 204, "delete internal error rule");
      const responseETag = parseInternalErrorRuleETag(
        response.headers.get("ETag"),
      );
      return Object.freeze({
        rule_set_revision: revisionFromInternalErrorRuleETag(responseETag),
        etag: responseETag,
      });
    },
    reorder: async (
      orderedRuleIDs: readonly string[],
      etag: InternalErrorRuleETag,
    ): Promise<
      RevisionedInternalErrorResource<InternalErrorRuleListResponse>
    > => {
      const response = await request(
        `${RULES_ENDPOINT}/reorder`,
        {
          method: "POST",
          headers: mutationHeaders(etag),
          body: JSON.stringify({
            schema_version: INTERNAL_ERROR_SCHEMA_VERSION,
            ordered_rule_ids: orderedRuleIDs,
          }),
        },
        parseInternalErrorAPIError,
      );
      expectStatus(response, 200, "reorder internal error rules");
      return readRevisioned(response, parseInternalErrorRuleListResponse);
    },
  };
}

export function createErrorDetectionApi(request: AuthenticatedResponseRequest) {
  return {
    rules: createInternalErrorRulesApi(request),
    stats: {
      get: async (): Promise<InternalErrorRuleStatsResponse> => {
        const response = await request(
          RULE_STATS_ENDPOINT,
          undefined,
          parseInternalErrorAPIError,
        );
        expectStatus(response, 200, "get internal error rule stats");
        return parseInternalErrorRuleStatsResponse(await response.json());
      },
    },
    testMessage: async (
      input: TestMessageInput,
      etag?: InternalErrorRuleETag,
    ): Promise<TestMessageResponse> => {
      const response = await request(
        TEST_MESSAGE_ENDPOINT,
        {
          method: "POST",
          headers: etag ? mutationHeaders(etag) : undefined,
          body: JSON.stringify({
            schema_version: INTERNAL_ERROR_SCHEMA_VERSION,
            ...input,
          }),
        },
        parseInternalErrorAPIError,
      );
      expectStatus(response, 200, "test internal error message");
      return parseTestMessageResponse(await response.json());
    },
  };
}

export type ErrorDetectionApi = ReturnType<typeof createErrorDetectionApi>;
