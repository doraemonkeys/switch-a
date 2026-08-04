import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { InternalErrorContractError } from "@/features/error-detection/contracts/contract";
import {
  parseInternalErrorAPIError,
  parseInternalErrorRuleETag,
  parseInternalErrorRuleListResponse,
  parseTestMessageResponse,
  revisionFromInternalErrorRuleETag,
} from "./error-detection-decoders";

function fixture(name: string): unknown {
  return JSON.parse(
    readFileSync(
      resolve(process.cwd(), `../contracts/internal-error/v1/${name}`),
      "utf8",
    ),
  );
}

describe("internal error response decoders", () => {
  it("rejects unknown envelope fields and schema versions", () => {
    const ruleList = fixture("rule-list.json") as Record<string, unknown>;
    expect(() =>
      parseInternalErrorRuleListResponse({ ...ruleList, future: true }),
    ).toThrow(InternalErrorContractError);
    expect(() =>
      parseInternalErrorRuleListResponse({ ...ruleList, schema_version: 2 }),
    ).toThrow("schema_version must equal 1");
  });

  it("rejects fields outside target and action discriminators", () => {
    const ruleList = fixture("rule-list.json") as {
      rules: Array<Record<string, unknown>>;
      [key: string]: unknown;
    };
    const globalTarget = structuredClone(ruleList);
    globalTarget.rules[1].target = {
      kind: "global",
      provider_id: "provider-codex",
    };
    expect(() => parseInternalErrorRuleListResponse(globalTarget)).toThrow(
      "target.provider_id is not allowed",
    );

    const passthrough = structuredClone(ruleList);
    passthrough.rules[1].action = {
      type: "passthrough",
      max_retries: 1,
    };
    expect(() => parseInternalErrorRuleListResponse(passthrough)).toThrow(
      "action.max_retries is not allowed",
    );
  });

  it("rejects non-canonical revisions and non-dense positions", () => {
    const ruleList = fixture("rule-list.json") as {
      rule_set_revision: string;
      rules: Array<Record<string, unknown>>;
      [key: string]: unknown;
    };
    const revision = structuredClone(ruleList);
    revision.rule_set_revision = "07";
    expect(() => parseInternalErrorRuleListResponse(revision)).toThrow(
      "canonical unsigned decimal",
    );

    const positions = structuredClone(ruleList);
    positions.rules[1].position = 5;
    expect(() => parseInternalErrorRuleListResponse(positions)).toThrow(
      "position must equal 1",
    );
  });

  it("rejects incoherent Test Message terminal states", () => {
    const testMessage = fixture("test-message.json") as {
      complete: { response: Record<string, unknown> };
    };
    const unknownReason = structuredClone(testMessage.complete.response);
    unknownReason.analysis_status = "fail_open";
    unknownReason.analysis_reason = "future_reason";
    expect(() => parseTestMessageResponse(unknownReason)).toThrow(
      "analysis_reason must be one of",
    );

    const missingProtocol = structuredClone(testMessage.complete.response);
    missingProtocol.response_protocol_id = null;
    expect(() => parseTestMessageResponse(missingProtocol)).toThrow(
      "must be present for complete analysis",
    );

    const mismatchedWinner = structuredClone(testMessage.complete.response);
    mismatchedWinner.decisive_error_index = null;
    expect(() => parseTestMessageResponse(mismatchedWinner)).toThrow(
      "must emit decisive_error_index and winner together",
    );

    const emptyErrors = structuredClone(testMessage.complete.response);
    emptyErrors.errors = [];
    expect(() => parseTestMessageResponse(emptyErrors)).toThrow(
      "must be null when no errors were extracted",
    );

    const oversizedField = structuredClone(testMessage.complete.response) as {
      errors: Array<Record<string, unknown>>;
    };
    oversizedField.errors[0].message = "x".repeat(4_097);
    expect(() => parseTestMessageResponse(oversizedField)).toThrow(
      "message must not exceed 4096 UTF-8 bytes",
    );
  });

  it("preserves absent versus explicitly empty semantic fields", () => {
    const testMessage = fixture("test-message.json") as {
      complete: { response: { errors: Array<Record<string, unknown>> } };
    };
    const absent = structuredClone(testMessage.complete.response);
    delete absent.errors[0].message;
    expect("message" in parseTestMessageResponse(absent).errors[0]).toBe(false);

    const explicitEmpty = structuredClone(testMessage.complete.response);
    explicitEmpty.errors[0].message = "";
    expect(parseTestMessageResponse(explicitEmpty).errors[0].message).toBe("");
  });

  it("rejects unrecognized error details instead of trusting them", () => {
    expect(() =>
      parseInternalErrorAPIError({
        code: "REVISION_MISMATCH",
        message: "changed",
        details: { current_revision: "10", supplied_revision: "9" },
      }),
    ).toThrow("details.supplied_revision is not allowed");
  });

  it("decodes both conflict variants and rejects ambiguous details", () => {
    expect(
      parseInternalErrorAPIError({
        code: "CONFLICT",
        message: "revision exhausted",
        details: {},
      }),
    ).toEqual({
      code: "CONFLICT",
      message: "revision exhausted",
      details: {},
    });
    expect(
      parseInternalErrorAPIError({
        code: "CONFLICT",
        message: "capacity reached",
        details: { limit: 256 },
      }),
    ).toEqual({
      code: "CONFLICT",
      message: "capacity reached",
      details: { limit: 256 },
    });

    for (const details of [{ future: true }, { limit: 256, future: true }]) {
      expect(() =>
        parseInternalErrorAPIError({
          code: "CONFLICT",
          message: "conflict",
          details,
        }),
      ).toThrow("details.future is not allowed");
    }
  });

  it("decodes strict rule and provider NOT_FOUND detail variants", () => {
    const errors = fixture("errors.json") as {
      cases: Array<{ name: string; body: unknown }>;
    };
    const ruleNotFound = errors.cases.find(({ name }) => name === "not found");
    expect(ruleNotFound).toBeDefined();
    expect(parseInternalErrorAPIError(ruleNotFound?.body)).toEqual(
      ruleNotFound?.body,
    );

    expect(
      parseInternalErrorAPIError({
        code: "NOT_FOUND",
        message: "provider not found",
        details: { provider_id: "provider-codex" },
      }),
    ).toEqual({
      code: "NOT_FOUND",
      message: "provider not found",
      details: { provider_id: "provider-codex" },
    });
  });

  it.each([
    {
      name: "missing discriminator",
      details: {},
      message: "must contain exactly one of rule_id or provider_id",
    },
    {
      name: "ambiguous discriminators",
      details: {
        rule_id: "99999999-9999-4999-8999-999999999999",
        provider_id: "provider-codex",
      },
      message: "must contain exactly one of rule_id or provider_id",
    },
    {
      name: "empty provider ID",
      details: { provider_id: "" },
      message: "details.provider_id must not be empty",
    },
    {
      name: "provider sibling key",
      details: { provider_id: "provider-codex", future: true },
      message: "details.future is not allowed",
    },
    {
      name: "rule sibling key",
      details: {
        rule_id: "99999999-9999-4999-8999-999999999999",
        future: true,
      },
      message: "details.future is not allowed",
    },
    {
      name: "uppercase rule ID",
      details: { rule_id: "99999999-9999-4999-8999-99999999999A" },
      message: "details.rule_id must be a lowercase UUIDv4",
    },
    {
      name: "non-v4 rule ID",
      details: { rule_id: "99999999-9999-5999-8999-999999999999" },
      message: "details.rule_id must be a lowercase UUIDv4",
    },
  ])("rejects $name NOT_FOUND details", ({ details, message }) => {
    expect(() =>
      parseInternalErrorAPIError({
        code: "NOT_FOUND",
        message: "resource not found",
        details,
      }),
    ).toThrow(message);
  });
});

describe("internal error rule ETags", () => {
  it("round-trips one canonical strong ETag", () => {
    const etag = parseInternalErrorRuleETag('"internal-error-rules/10"');
    expect(revisionFromInternalErrorRuleETag(etag)).toBe("10");
  });

  it.each([
    null,
    'W/"internal-error-rules/10"',
    "*",
    '"internal-error-rules/01"',
    '"internal-error-rules/9", "internal-error-rules/10"',
  ])("rejects invalid ETag %s", (etag) => {
    expect(() => parseInternalErrorRuleETag(etag)).toThrow(
      InternalErrorContractError,
    );
  });
});
