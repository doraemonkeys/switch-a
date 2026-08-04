import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import {
  parseInternalErrorAPIError,
  parseInternalErrorRuleListResponse,
  parseInternalErrorRuleResponse,
  parseInternalErrorRuleSpec,
  parseInternalErrorRuleStatsResponse,
  parseTestMessageResponse,
} from "@/api/error-detection-decoders";

const FIXTURE_ROOT = resolve(process.cwd(), "../contracts/internal-error/v1");

function fixture(name: string): unknown {
  return JSON.parse(readFileSync(resolve(FIXTURE_ROOT, name), "utf8"));
}

describe("shared internal-error wire fixtures", () => {
  it("decodes the canonical rule list without reshaping it", () => {
    const value = fixture("rule-list.json");
    expect(parseInternalErrorRuleListResponse(value)).toEqual(value);
  });

  it("decodes create, update, and reorder requests and responses", () => {
    const mutations = fixture("rule-mutations.json") as {
      create: { request: { rule: unknown }; response: unknown };
      update: { request: { rule: unknown }; response: unknown };
    };
    const reorder = fixture("reorder.json") as { response: unknown };

    expect(parseInternalErrorRuleSpec(mutations.create.request.rule)).toEqual(
      mutations.create.request.rule,
    );
    expect(parseInternalErrorRuleResponse(mutations.create.response)).toEqual(
      mutations.create.response,
    );
    expect(parseInternalErrorRuleSpec(mutations.update.request.rule)).toEqual(
      mutations.update.request.rule,
    );
    expect(parseInternalErrorRuleResponse(mutations.update.response)).toEqual(
      mutations.update.response,
    );
    expect(parseInternalErrorRuleListResponse(reorder.response)).toEqual(
      reorder.response,
    );
  });

  it("decodes stats and both Test Message terminal states", () => {
    const stats = fixture("rule-stats.json");
    const testMessage = fixture("test-message.json") as {
      complete: { response: unknown };
      fail_open: { response: unknown };
    };

    expect(parseInternalErrorRuleStatsResponse(stats)).toEqual(stats);
    expect(parseTestMessageResponse(testMessage.complete.response)).toEqual(
      testMessage.complete.response,
    );
    expect(parseTestMessageResponse(testMessage.fail_open.response)).toEqual(
      testMessage.fail_open.response,
    );
  });

  it("decodes every canonical API error envelope", () => {
    const errors = fixture("errors.json") as {
      cases: readonly { body: unknown }[];
    };
    for (const errorCase of errors.cases) {
      expect(parseInternalErrorAPIError(errorCase.body)).toEqual(
        errorCase.body,
      );
    }
  });
});
