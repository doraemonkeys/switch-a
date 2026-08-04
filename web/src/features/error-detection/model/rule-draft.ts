import {
  findBuiltInAPIType,
  isValidCustomAPIType,
  type APICatalog,
} from "@/api/api-catalog";
import type { Provider } from "@/api/types";
import type {
  InternalErrorRule,
  InternalErrorRuleAction,
  InternalErrorRuleSpec,
  InternalErrorRuleTarget,
  RuleBackoffPolicy,
} from "../contracts";
import { calculateBackoffBaseDelays, MAX_RULE_RETRIES } from "../domain";

export const MAX_RULE_NAME_BYTES = 128;
export const MAX_KEYWORDS_PER_RULE = 16;
export const MAX_KEYWORD_BYTES = 128;
export const MAX_KEYWORD_BYTES_PER_RULE = 2_048;
export const DEFAULT_RULE_RETRY_COUNT = 2;

export const DEFAULT_RULE_BACKOFF: RuleBackoffPolicy = Object.freeze({
  initial_delay: "250ms",
  max_delay: "2s",
  multiplier: 2,
  jitter: true,
});

export type RuleDraftAction =
  | { readonly type: "passthrough" }
  | {
      readonly type: "retry_only" | "retry_then_switch";
      readonly max_retries: string;
      readonly backoff: RuleBackoffPolicy;
    };

export interface RuleDraft {
  readonly name: string;
  readonly enabled: boolean;
  readonly target: InternalErrorRuleTarget;
  readonly api_type: string | null;
  readonly keywords_text: string;
  readonly match_mode: "any" | "all";
  readonly action: RuleDraftAction;
}

export interface ErrorDetectionPrefill {
  readonly target?: InternalErrorRuleTarget;
  readonly api_type?: string | null;
}

export type RuleDraftField =
  "name" | "target" | "api_type" | "keywords" | "max_retries" | "backoff";

export type RuleDraftErrors = Partial<Record<RuleDraftField, string>>;

export type RuleDraftValidation =
  | { readonly valid: true; readonly value: InternalErrorRuleSpec }
  | { readonly valid: false; readonly errors: RuleDraftErrors };

interface ValidatedValue<T> {
  readonly value?: T;
  readonly error?: string;
}

const utf8Encoder = new TextEncoder();
const UNICODE_CONTROL = /\p{Cc}/u;
const CANONICAL_NON_NEGATIVE_INTEGER = /^(0|[1-9]\d*)$/;

function utf8Length(value: string): number {
  return utf8Encoder.encode(value).length;
}

function validateName(value: string): ValidatedValue<string> {
  const name = value.trim();
  if (name.length === 0) {
    return { error: "Name is required." };
  }
  if (utf8Length(name) > MAX_RULE_NAME_BYTES) {
    return {
      error: `Name must be at most ${MAX_RULE_NAME_BYTES} UTF-8 bytes.`,
    };
  }
  return { value: name };
}

function normalizeKeywords(value: string): ValidatedValue<readonly string[]> {
  const seen = new Set<string>();
  const keywords: string[] = [];
  for (const rawKeyword of value.split(/\r?\n/u)) {
    const keyword = rawKeyword.trim().toLowerCase();
    if (keyword.length === 0) continue;
    if (UNICODE_CONTROL.test(keyword)) {
      return { error: "Keywords cannot contain Unicode control characters." };
    }
    if (utf8Length(keyword) > MAX_KEYWORD_BYTES) {
      return {
        error: `Each keyword must be at most ${MAX_KEYWORD_BYTES} UTF-8 bytes.`,
      };
    }
    if (!seen.has(keyword)) {
      seen.add(keyword);
      keywords.push(keyword);
    }
  }

  if (keywords.length === 0) {
    return { error: "Enter at least one keyword, one per line." };
  }
  if (keywords.length > MAX_KEYWORDS_PER_RULE) {
    return { error: `Use at most ${MAX_KEYWORDS_PER_RULE} keywords.` };
  }
  const totalBytes = keywords.reduce(
    (total, keyword) => total + utf8Length(keyword),
    0,
  );
  if (totalBytes > MAX_KEYWORD_BYTES_PER_RULE) {
    return {
      error: `Keywords must total at most ${MAX_KEYWORD_BYTES_PER_RULE} UTF-8 bytes.`,
    };
  }
  return { value: Object.freeze(keywords) };
}

function validateTarget(
  target: InternalErrorRuleTarget,
  providers: readonly Provider[],
): string | undefined {
  if (target.kind === "global") return undefined;
  if (target.provider_id.length === 0) return "Choose a provider.";
  if (!providers.some((provider) => provider.id === target.provider_id)) {
    return "The selected provider no longer exists. Choose another provider.";
  }
  return undefined;
}

function validateAPIType(
  catalog: APICatalog,
  apiType: string | null,
  enabled: boolean,
): ValidatedValue<string | null> {
  const normalized = apiType?.trim() || null;
  if (normalized === null) return { value: null };

  const builtIn = findBuiltInAPIType(catalog, normalized);
  if (builtIn) {
    if (enabled && !builtIn.semantic_error_supported) {
      return {
        error: `${builtIn.label} does not support structured error detection.`,
      };
    }
    return { value: normalized };
  }
  if (isValidCustomAPIType(catalog, normalized)) {
    return enabled
      ? {
          error:
            "Custom API types do not support structured error detection. Disable the rule or choose a supported built-in API type.",
        }
      : { value: normalized };
  }
  return { error: "Choose an API type from the server catalog." };
}

function validateAction(
  action: RuleDraftAction,
): ValidatedValue<InternalErrorRuleAction> {
  if (action.type === "passthrough") return { value: action };
  if (!CANONICAL_NON_NEGATIVE_INTEGER.test(action.max_retries)) {
    return { error: "Retry count must be a whole number." };
  }

  const maxRetries = Number(action.max_retries);
  if (maxRetries > MAX_RULE_RETRIES) {
    return { error: `Retry count must be between 0 and ${MAX_RULE_RETRIES}.` };
  }
  const backoff = calculateBackoffBaseDelays(action.backoff, maxRetries);
  if (!backoff.valid) return { error: backoff.error };
  return {
    value: {
      type: action.type,
      max_retries: maxRetries,
      backoff: Object.freeze({ ...action.backoff }),
    },
  };
}

export function createEmptyRuleDraft(
  prefill: ErrorDetectionPrefill = {},
): RuleDraft {
  return {
    name: "",
    enabled: true,
    target: prefill.target ?? { kind: "global" },
    api_type: prefill.api_type ?? null,
    keywords_text: "",
    match_mode: "any",
    action: { type: "passthrough" },
  };
}

export function ruleToDraft(rule: InternalErrorRule): RuleDraft {
  return {
    name: rule.name,
    enabled: rule.enabled,
    target: rule.target,
    api_type: rule.api_type,
    keywords_text: rule.keywords.join("\n"),
    match_mode: rule.match_mode,
    action:
      rule.action.type === "passthrough"
        ? { type: "passthrough" }
        : {
            type: rule.action.type,
            max_retries: String(rule.action.max_retries),
            backoff: rule.action.backoff,
          },
  };
}

export function ruleToSpec(rule: InternalErrorRule): InternalErrorRuleSpec {
  return {
    name: rule.name,
    enabled: rule.enabled,
    target: rule.target,
    api_type: rule.api_type,
    keywords: rule.keywords,
    match_mode: rule.match_mode,
    action: rule.action,
  };
}

export function createRetryDraftAction(
  type: "retry_only" | "retry_then_switch",
): RuleDraftAction {
  return {
    type,
    max_retries: String(DEFAULT_RULE_RETRY_COUNT),
    backoff: DEFAULT_RULE_BACKOFF,
  };
}

export function changeRuleDraftAction(
  current: RuleDraftAction,
  type: RuleDraftAction["type"],
): RuleDraftAction {
  if (type === "passthrough") return { type };
  if (current.type === "passthrough") return createRetryDraftAction(type);
  // Switching between retry policies changes exhaustion behavior only; keeping
  // timing and retry count avoids discarding unrelated draft work.
  return { ...current, type };
}

export function validateRuleDraft(
  draft: RuleDraft,
  catalog: APICatalog,
  providers: readonly Provider[],
): RuleDraftValidation {
  const name = validateName(draft.name);
  const keywords = normalizeKeywords(draft.keywords_text);
  const apiType = validateAPIType(catalog, draft.api_type, draft.enabled);
  const action = validateAction(draft.action);
  const targetError = validateTarget(draft.target, providers);
  const errors: RuleDraftErrors = {};

  if (name.error) errors.name = name.error;
  if (keywords.error) errors.keywords = keywords.error;
  if (apiType.error) errors.api_type = apiType.error;
  if (targetError) errors.target = targetError;
  if (action.error) {
    errors[action.error.startsWith("Retry count") ? "max_retries" : "backoff"] =
      action.error;
  }
  if (
    Object.keys(errors).length > 0 ||
    name.value === undefined ||
    keywords.value === undefined ||
    apiType.value === undefined ||
    action.value === undefined
  ) {
    return { valid: false, errors };
  }

  return {
    valid: true,
    value: {
      name: name.value,
      enabled: draft.enabled,
      target: draft.target,
      api_type: apiType.value,
      keywords: keywords.value,
      match_mode: draft.match_mode,
      action: action.value,
    },
  };
}

export function areRuleDraftsEqual(left: RuleDraft, right: RuleDraft): boolean {
  return JSON.stringify(left) === JSON.stringify(right);
}

export function moveRuleIDs(
  rules: readonly InternalErrorRule[],
  ruleID: string,
  direction: -1 | 1,
): readonly string[] {
  const ordered = [...rules].sort(
    (left, right) => left.position - right.position,
  );
  const currentIndex = ordered.findIndex((rule) => rule.id === ruleID);
  const targetIndex = currentIndex + direction;
  if (currentIndex < 0 || targetIndex < 0 || targetIndex >= ordered.length) {
    return Object.freeze(ordered.map((rule) => rule.id));
  }
  [ordered[currentIndex], ordered[targetIndex]] = [
    ordered[targetIndex],
    ordered[currentIndex],
  ];
  return Object.freeze(ordered.map((rule) => rule.id));
}
