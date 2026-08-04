import type { RuleBackoffPolicy } from "../contracts";
import type { RuleDraft, RuleDraftAction } from "./rule-draft";

export type InternalErrorRulePresetID = "codex_capacity" | "anthropic_overload";

export interface InternalErrorRulePreset {
  readonly id: InternalErrorRulePresetID;
  readonly api_type: string;
  readonly keywords: readonly string[];
  readonly match_mode: "any" | "all";
  readonly action: RuleDraftAction;
}

export interface InternalErrorRulePresetCopy {
  readonly name: string;
  readonly description: string;
}

const PRESET_BACKOFF: RuleBackoffPolicy = Object.freeze({
  initial_delay: "250ms",
  max_delay: "2s",
  multiplier: 2,
  jitter: true,
});

// These API types are normative preset data from the product contract, not a
// catalog. Availability and semantic support are still checked against F0 data.
export const INTERNAL_ERROR_RULE_PRESETS: readonly InternalErrorRulePreset[] =
  Object.freeze([
    {
      id: "codex_capacity",
      api_type: "codex",
      keywords: Object.freeze([
        "server_is_overloaded",
        "our servers are currently overloaded at capacity",
      ]),
      match_mode: "any",
      action: {
        type: "retry_then_switch",
        max_retries: "2",
        backoff: PRESET_BACKOFF,
      },
    },
    {
      id: "anthropic_overload",
      api_type: "claude",
      keywords: Object.freeze(["overloaded_error"]),
      match_mode: "any",
      action: {
        type: "retry_then_switch",
        max_retries: "2",
        backoff: PRESET_BACKOFF,
      },
    },
  ]);

export const ENGLISH_INTERNAL_ERROR_PRESET_COPY: Readonly<
  Record<InternalErrorRulePresetID, InternalErrorRulePresetCopy>
> = Object.freeze({
  codex_capacity: {
    name: "Codex capacity",
    description:
      "Retry recognized Codex capacity errors, then switch provider.",
  },
  anthropic_overload: {
    name: "Anthropic overload",
    description: "Retry Anthropic overload envelopes, then switch provider.",
  },
});

export function applyRulePreset(
  draft: RuleDraft,
  preset: InternalErrorRulePreset,
): RuleDraft {
  return {
    ...draft,
    target: { kind: "global" },
    api_type: preset.api_type,
    keywords_text: preset.keywords.join("\n"),
    match_mode: preset.match_mode,
    action: preset.action,
  };
}
