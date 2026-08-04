import { describe, expect, it } from "vitest";
import { parseAPICatalog } from "@/api/api-catalog";
import type { Provider } from "@/api/types";
import apiCatalogFixture from "../../../../../contracts/internal-error/v1/api-catalog.json";
import type { InternalErrorRule } from "../contracts";
import {
  ENGLISH_INTERNAL_ERROR_PRESET_COPY,
  INTERNAL_ERROR_RULE_PRESETS,
  applyRulePreset,
  changeRuleDraftAction,
  createEmptyRuleDraft,
  createRetryDraftAction,
  moveRuleIDs,
  parseGlobalMaxAttempts,
  validateRuleDraft,
} from ".";

const catalog = parseAPICatalog(apiCatalogFixture);
const provider = {
  id: "provider-codex",
  name: "Codex primary",
} as Provider;

function validDraft() {
  return {
    ...createEmptyRuleDraft(),
    name: "  Capacity detector  ",
    api_type: "codex",
    keywords_text:
      " Server_Is_Overloaded \nserver_is_overloaded\n At Capacity ",
    action: createRetryDraftAction("retry_then_switch"),
  } as const;
}

function rule(id: string, position: number): InternalErrorRule {
  return {
    id,
    position,
    name: id,
    enabled: true,
    target: { kind: "global" },
    api_type: null,
    keywords: ["error"],
    match_mode: "any",
    action: { type: "passthrough" },
    created_at: "2026-08-03T01:00:00Z",
    updated_at: "2026-08-03T01:00:00Z",
  };
}

describe("rule draft model", () => {
  it("normalizes a valid draft into the strict RuleSpec union", () => {
    const result = validateRuleDraft(validDraft(), catalog, [provider]);

    expect(result).toEqual({
      valid: true,
      value: {
        name: "Capacity detector",
        enabled: true,
        target: { kind: "global" },
        api_type: "codex",
        keywords: ["server_is_overloaded", "at capacity"],
        match_mode: "any",
        action: {
          type: "retry_then_switch",
          max_retries: 2,
          backoff: {
            initial_delay: "250ms",
            max_delay: "2s",
            multiplier: 2,
            jitter: true,
          },
        },
      },
    });
  });

  it("allows a disabled custom rule but rejects enabling it", () => {
    const customDraft = { ...validDraft(), api_type: "custom:private" };

    expect(validateRuleDraft(customDraft, catalog, [provider])).toMatchObject({
      valid: false,
      errors: { api_type: expect.stringContaining("Custom API types") },
    });
    expect(
      validateRuleDraft({ ...customDraft, enabled: false }, catalog, [
        provider,
      ]),
    ).toMatchObject({ valid: true, value: { api_type: "custom:private" } });
  });

  it("reports stale provider, retry, backoff, and keyword boundaries", () => {
    const result = validateRuleDraft(
      {
        ...validDraft(),
        target: { kind: "provider", provider_id: "deleted" },
        keywords_text: "bad\u0000keyword",
        action: {
          type: "retry_only",
          max_retries: "11",
          backoff: {
            initial_delay: "-1s",
            max_delay: "0s",
            multiplier: 2,
            jitter: false,
          },
        },
      },
      catalog,
      [provider],
    );

    expect(result).toEqual({
      valid: false,
      errors: {
        keywords: "Keywords cannot contain Unicode control characters.",
        target:
          "The selected provider no longer exists. Choose another provider.",
        max_retries: "Retry count must be between 0 and 10.",
      },
    });
  });

  it("keeps preset copy separate and changes only normative draft fields", () => {
    const draft = {
      ...createEmptyRuleDraft(),
      name: "My rule",
      enabled: false,
    };
    const preset = INTERNAL_ERROR_RULE_PRESETS[0];
    const applied = applyRulePreset(draft, preset);

    expect(ENGLISH_INTERNAL_ERROR_PRESET_COPY[preset.id].name).toBe(
      "Codex capacity",
    );
    expect(applied).toMatchObject({
      name: "My rule",
      enabled: false,
      target: { kind: "global" },
      api_type: "codex",
      match_mode: "any",
      action: { type: "retry_then_switch", max_retries: "2" },
    });
  });

  it("preserves retry timing when only exhaustion behavior changes", () => {
    const retry = createRetryDraftAction("retry_only");
    expect(changeRuleDraftAction(retry, "retry_then_switch")).toEqual({
      ...retry,
      type: "retry_then_switch",
    });
    expect(changeRuleDraftAction(retry, "passthrough")).toEqual({
      type: "passthrough",
    });
  });

  it("builds an exact complete permutation for an adjacent move", () => {
    const rules = [rule("first", 0), rule("second", 1), rule("third", 2)];
    expect(moveRuleIDs(rules, "second", -1)).toEqual([
      "second",
      "first",
      "third",
    ]);
    expect(moveRuleIDs(rules, "first", -1)).toEqual([
      "first",
      "second",
      "third",
    ]);
  });

  it.each([
    ["0", 0],
    ["3", 3],
    ["03", null],
    ["-1", null],
    [undefined, null],
  ])("parses global_max_attempts %s as %s", (value, expected) => {
    expect(parseGlobalMaxAttempts(value)).toBe(expected);
  });
});
