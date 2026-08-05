import { MAX_RULE_RETRIES, calculateBackoffBaseDelays } from "../domain";
import {
  assertExactKeys,
  contractError,
  readArray,
  readBoolean,
  readEnum,
  readFiniteNumber,
  readInteger,
  readRecord,
  readString,
  readUTCInstant,
  readUUID,
  utf8ByteLength,
  type JsonRecord,
} from "./contract";
import type {
  InternalErrorRule,
  InternalErrorRuleAction,
  InternalErrorRuleSpec,
  InternalErrorRuleTarget,
  RuleBackoffPolicy,
} from "./types";

export const MAX_INTERNAL_ERROR_RULE_COUNT = 256;
export const MAX_INTERNAL_ERROR_RULE_KEYWORDS = 16;
const MAX_RULE_NAME_BYTES = 128;
const MAX_KEYWORD_BYTES = 128;
const MAX_KEYWORD_BYTES_PER_RULE = 2_048;

const RULE_SPEC_KEYS = [
  "name",
  "enabled",
  "target",
  "api_type",
  "keywords",
  "match_mode",
  "action",
] as const;

function parseTarget(value: unknown, path: string): InternalErrorRuleTarget {
  const target = readRecord(value, path);
  const kind = readEnum(
    target.kind,
    ["global", "provider"] as const,
    `${path}.kind`,
  );
  if (kind === "global") {
    assertExactKeys(target, path, ["kind"]);
    return Object.freeze({ kind });
  }
  assertExactKeys(target, path, ["kind", "provider_id"]);
  return Object.freeze({
    kind,
    provider_id: readString(target.provider_id, `${path}.provider_id`),
  });
}

function parseBackoff(value: unknown, path: string): RuleBackoffPolicy {
  const backoff = readRecord(value, path);
  assertExactKeys(backoff, path, [
    "initial_delay",
    "max_delay",
    "multiplier",
    "jitter",
  ]);
  const parsed = Object.freeze({
    initial_delay: readString(backoff.initial_delay, `${path}.initial_delay`),
    max_delay: readString(backoff.max_delay, `${path}.max_delay`),
    multiplier: readFiniteNumber(backoff.multiplier, `${path}.multiplier`),
    jitter: readBoolean(backoff.jitter, `${path}.jitter`),
  });
  const validation = calculateBackoffBaseDelays(parsed, 0);
  if (!validation.valid) {
    contractError(path, validation.error);
  }
  return parsed;
}

function parseAction(value: unknown, path: string): InternalErrorRuleAction {
  const action = readRecord(value, path);
  const type = readEnum(
    action.type,
    ["passthrough", "retry_only", "retry_then_switch"] as const,
    `${path}.type`,
  );
  if (type === "passthrough") {
    assertExactKeys(action, path, ["type"]);
    return Object.freeze({ type });
  }

  assertExactKeys(
    action,
    path,
    ["type", "max_retries", "backoff"],
    ["visible_response"],
  );
  const maxRetries = readInteger(
    action.max_retries,
    `${path}.max_retries`,
    0,
    MAX_RULE_RETRIES,
  );
  const backoff = parseBackoff(action.backoff, `${path}.backoff`);
  const validation = calculateBackoffBaseDelays(backoff, maxRetries);
  if (!validation.valid) {
    contractError(path, validation.error);
  }
  const parsed = {
    type,
    max_retries: maxRetries,
    backoff,
  } as const;
  if (action.visible_response === undefined) {
    return Object.freeze(parsed);
  }
  return Object.freeze({
    ...parsed,
    visible_response: readEnum(
      action.visible_response,
      ["disconnect_client", "commit_current"] as const,
      `${path}.visible_response`,
    ),
  });
}

function parseKeywords(value: unknown, path: string): readonly string[] {
  const values = readArray(value, path);
  if (values.length === 0 || values.length > MAX_INTERNAL_ERROR_RULE_KEYWORDS) {
    contractError(
      path,
      `must contain between 1 and ${MAX_INTERNAL_ERROR_RULE_KEYWORDS} items`,
    );
  }
  const seen = new Set<string>();
  let totalBytes = 0;
  const keywords = values.map((value, index) => {
    const keyword = readString(value, `${path}[${index}]`);
    const bytes = utf8ByteLength(keyword);
    if (bytes > MAX_KEYWORD_BYTES) {
      contractError(
        `${path}[${index}]`,
        `must not exceed ${MAX_KEYWORD_BYTES} UTF-8 bytes`,
      );
    }
    if (keyword.trim() !== keyword || /\p{Cc}/u.test(keyword)) {
      contractError(
        `${path}[${index}]`,
        "must be normalized and contain no control characters",
      );
    }
    if (seen.has(keyword)) {
      contractError(
        `${path}[${index}]`,
        "must not duplicate a normalized keyword",
      );
    }
    seen.add(keyword);
    totalBytes += bytes;
    return keyword;
  });
  if (totalBytes > MAX_KEYWORD_BYTES_PER_RULE) {
    contractError(
      path,
      `must not exceed ${MAX_KEYWORD_BYTES_PER_RULE} UTF-8 bytes in total`,
    );
  }
  return Object.freeze(keywords);
}

function parseRuleSpecFields(
  rule: JsonRecord,
  path: string,
): InternalErrorRuleSpec {
  const name = readString(rule.name, `${path}.name`);
  if (name.trim() !== name || utf8ByteLength(name) > MAX_RULE_NAME_BYTES) {
    contractError(
      `${path}.name`,
      `must be normalized and at most ${MAX_RULE_NAME_BYTES} UTF-8 bytes`,
    );
  }
  const apiType =
    rule.api_type === null
      ? null
      : readString(rule.api_type, `${path}.api_type`);
  return Object.freeze({
    name,
    enabled: readBoolean(rule.enabled, `${path}.enabled`),
    target: parseTarget(rule.target, `${path}.target`),
    api_type: apiType,
    keywords: parseKeywords(rule.keywords, `${path}.keywords`),
    match_mode: readEnum(
      rule.match_mode,
      ["any", "all"] as const,
      `${path}.match_mode`,
    ),
    action: parseAction(rule.action, `${path}.action`),
  });
}

export function parseInternalErrorRuleSpec(
  value: unknown,
  path = "internal error rule spec",
): InternalErrorRuleSpec {
  const rule = readRecord(value, path);
  assertExactKeys(rule, path, RULE_SPEC_KEYS);
  return parseRuleSpecFields(rule, path);
}

export function parseInternalErrorRule(
  value: unknown,
  path: string,
): InternalErrorRule {
  const rule = readRecord(value, path);
  assertExactKeys(rule, path, [
    ...RULE_SPEC_KEYS,
    "id",
    "position",
    "created_at",
    "updated_at",
  ]);
  return Object.freeze({
    ...parseRuleSpecFields(rule, path),
    id: readUUID(rule.id, `${path}.id`),
    position: readInteger(rule.position, `${path}.position`, 0),
    created_at: readUTCInstant(rule.created_at, `${path}.created_at`),
    updated_at: readUTCInstant(rule.updated_at, `${path}.updated_at`),
  });
}
