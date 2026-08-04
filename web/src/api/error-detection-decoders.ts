import type {
  InternalErrorAPIErrorResponse,
  InternalErrorRuleETag,
  InternalErrorRuleListResponse,
  InternalErrorRuleResponse,
  InternalErrorRuleStatsResponse,
  SemanticFieldName,
  TestMessageAnalysisReason,
  TestMessageExtractedError,
  TestMessageMatch,
  TestMessageResponse,
  TestMessageWinner,
} from "@/features/error-detection/contracts";
import type { InternalErrorNotFoundDetails } from "@/features/error-detection/contracts/types";
import { INTERNAL_ERROR_SCHEMA_VERSION } from "@/features/error-detection/contracts";
import {
  assertExactKeys,
  contractError,
  readArray,
  readCanonicalDecimal,
  readEnum,
  readInteger,
  readRecord,
  readString,
  readUTCInstant,
  readUUID,
  utf8ByteLength,
  type JsonRecord,
} from "@/features/error-detection/contracts/contract";
import {
  MAX_INTERNAL_ERROR_RULE_COUNT,
  MAX_INTERNAL_ERROR_RULE_KEYWORDS,
  parseInternalErrorRule,
  parseInternalErrorRuleSpec,
} from "@/features/error-detection/contracts/rule-decoder";

export { parseInternalErrorRuleSpec };

const MAX_TEST_MESSAGE_ERRORS = 32;
const MAX_MATCHED_KEYWORD_BYTES = 128;
const MAX_SEMANTIC_SHORT_FIELD_BYTES = 256;
const MAX_SEMANTIC_MESSAGE_BYTES = 4 * 1_024;

const SEMANTIC_FIELDS = ["type", "code", "message", "reason"] as const;
const ANALYSIS_REASONS = [
  "request_probe_memory_exhausted",
  "process_probe_memory_exhausted",
  "unsupported_response_protocol",
  "unsupported_content_encoding",
  "content_decoding_failed",
  "malformed_protocol_frame",
  "decoded_event_too_large",
  "semantic_field_too_large",
  "analysis_internal_error",
] as const satisfies readonly TestMessageAnalysisReason[];
const RULE_ETAG = /^"internal-error-rules\/(0|[1-9]\d*)"$/;

function assertSchemaVersion(value: unknown, path: string): void {
  if (value !== INTERNAL_ERROR_SCHEMA_VERSION) {
    contractError(path, `must equal ${INTERNAL_ERROR_SCHEMA_VERSION}`);
  }
}

function readBoundedString(
  value: unknown,
  path: string,
  maximumBytes: number,
  allowEmpty = false,
): string {
  const parsed = readString(value, path, allowEmpty);
  if (utf8ByteLength(parsed) > maximumBytes) {
    contractError(path, `must not exceed ${maximumBytes} UTF-8 bytes`);
  }
  return parsed;
}

function parseEnvelopeRevision(value: JsonRecord, path: string): string {
  assertSchemaVersion(value.schema_version, `${path}.schema_version`);
  return readCanonicalDecimal(
    value.rule_set_revision,
    `${path}.rule_set_revision`,
  );
}

function parseNotFoundDetails(
  details: JsonRecord,
  path: string,
): InternalErrorNotFoundDetails {
  const hasRuleID = Object.prototype.hasOwnProperty.call(details, "rule_id");
  const hasProviderID = Object.prototype.hasOwnProperty.call(
    details,
    "provider_id",
  );
  if (hasRuleID === hasProviderID) {
    contractError(path, "must contain exactly one of rule_id or provider_id");
  }
  if (hasRuleID) {
    assertExactKeys(details, path, ["rule_id"]);
    return Object.freeze({
      rule_id: readUUID(details.rule_id, `${path}.rule_id`),
    });
  }
  assertExactKeys(details, path, ["provider_id"]);
  return Object.freeze({
    provider_id: readString(details.provider_id, `${path}.provider_id`),
  });
}

export function parseInternalErrorRuleListResponse(
  value: unknown,
): InternalErrorRuleListResponse {
  const path = "internal error rule list";
  const envelope = readRecord(value, path);
  assertExactKeys(envelope, path, [
    "schema_version",
    "rule_set_revision",
    "rules",
  ]);
  const rawRules = readArray(envelope.rules, `${path}.rules`);
  if (rawRules.length > MAX_INTERNAL_ERROR_RULE_COUNT) {
    contractError(
      `${path}.rules`,
      `must not exceed ${MAX_INTERNAL_ERROR_RULE_COUNT} rules`,
    );
  }
  const ids = new Set<string>();
  const rules = rawRules.map((rule, index) => {
    const parsed = parseInternalErrorRule(rule, `${path}.rules[${index}]`);
    if (parsed.position !== index) {
      contractError(`${path}.rules[${index}].position`, `must equal ${index}`);
    }
    if (ids.has(parsed.id)) {
      contractError(`${path}.rules[${index}].id`, "must be unique");
    }
    ids.add(parsed.id);
    return parsed;
  });
  return Object.freeze({
    schema_version: INTERNAL_ERROR_SCHEMA_VERSION,
    rule_set_revision: parseEnvelopeRevision(envelope, path),
    rules: Object.freeze(rules),
  });
}

export function parseInternalErrorRuleResponse(
  value: unknown,
): InternalErrorRuleResponse {
  const path = "internal error rule response";
  const envelope = readRecord(value, path);
  assertExactKeys(envelope, path, [
    "schema_version",
    "rule_set_revision",
    "rule",
  ]);
  return Object.freeze({
    schema_version: INTERNAL_ERROR_SCHEMA_VERSION,
    rule_set_revision: parseEnvelopeRevision(envelope, path),
    rule: parseInternalErrorRule(envelope.rule, `${path}.rule`),
  });
}

export function parseInternalErrorRuleStatsResponse(
  value: unknown,
): InternalErrorRuleStatsResponse {
  const path = "internal error rule stats";
  const envelope = readRecord(value, path);
  assertExactKeys(envelope, path, [
    "schema_version",
    "rule_set_revision",
    "stats",
  ]);
  const rawStats = readArray(envelope.stats, `${path}.stats`);
  if (rawStats.length > MAX_INTERNAL_ERROR_RULE_COUNT) {
    contractError(
      `${path}.stats`,
      `must not exceed ${MAX_INTERNAL_ERROR_RULE_COUNT} items`,
    );
  }
  const ids = new Set<string>();
  const stats = rawStats.map((value, index) => {
    const itemPath = `${path}.stats[${index}]`;
    const item = readRecord(value, itemPath);
    assertExactKeys(item, itemPath, ["rule_id", "hit_count", "last_hit_at"]);
    const ruleID = readUUID(item.rule_id, `${itemPath}.rule_id`);
    if (ids.has(ruleID)) {
      contractError(`${itemPath}.rule_id`, "must be unique");
    }
    ids.add(ruleID);
    return Object.freeze({
      rule_id: ruleID,
      hit_count: readCanonicalDecimal(item.hit_count, `${itemPath}.hit_count`),
      last_hit_at:
        item.last_hit_at === null
          ? null
          : readUTCInstant(item.last_hit_at, `${itemPath}.last_hit_at`),
    });
  });
  return Object.freeze({
    schema_version: INTERNAL_ERROR_SCHEMA_VERSION,
    rule_set_revision: parseEnvelopeRevision(envelope, path),
    stats: Object.freeze(stats),
  });
}

function parseMatch(value: unknown, path: string): TestMessageMatch {
  const match = readRecord(value, path);
  assertExactKeys(match, path, [
    "rule_id",
    "matched_keywords",
    "matched_keyword_indexes",
    "matched_fields",
  ]);
  const rawKeywords = readArray(
    match.matched_keywords,
    `${path}.matched_keywords`,
  );
  if (
    rawKeywords.length === 0 ||
    rawKeywords.length > MAX_INTERNAL_ERROR_RULE_KEYWORDS
  ) {
    contractError(
      `${path}.matched_keywords`,
      `must contain between 1 and ${MAX_INTERNAL_ERROR_RULE_KEYWORDS} items`,
    );
  }
  const keywords = rawKeywords.map((keyword, index) =>
    readBoundedString(
      keyword,
      `${path}.matched_keywords[${index}]`,
      MAX_MATCHED_KEYWORD_BYTES,
    ),
  );
  const indexes = readArray(
    match.matched_keyword_indexes,
    `${path}.matched_keyword_indexes`,
  ).map((index, itemIndex) =>
    readInteger(
      index,
      `${path}.matched_keyword_indexes[${itemIndex}]`,
      0,
      MAX_INTERNAL_ERROR_RULE_KEYWORDS - 1,
    ),
  );
  if (indexes.length !== keywords.length) {
    contractError(path, "must pair every matched keyword with its rule index");
  }
  if (
    indexes.some(
      (index, itemIndex) => itemIndex > 0 && index <= indexes[itemIndex - 1],
    )
  ) {
    contractError(
      `${path}.matched_keyword_indexes`,
      "must be strictly ascending",
    );
  }
  const fields = readArray(match.matched_fields, `${path}.matched_fields`).map(
    (field, index) =>
      readEnum(field, SEMANTIC_FIELDS, `${path}.matched_fields[${index}]`),
  );
  if (fields.length === 0) {
    contractError(`${path}.matched_fields`, "must not be empty");
  }
  if (
    fields.some(
      (field, index) =>
        index > 0 &&
        SEMANTIC_FIELDS.indexOf(field) <=
          SEMANTIC_FIELDS.indexOf(fields[index - 1] as SemanticFieldName),
    )
  ) {
    contractError(
      `${path}.matched_fields`,
      "must be unique and in canonical order",
    );
  }
  return Object.freeze({
    rule_id: readUUID(match.rule_id, `${path}.rule_id`),
    matched_keywords: Object.freeze(keywords),
    matched_keyword_indexes: Object.freeze(indexes),
    matched_fields: Object.freeze(fields),
  });
}

function parseExtractedError(
  value: unknown,
  path: string,
): TestMessageExtractedError {
  const error = readRecord(value, path);
  assertExactKeys(
    error,
    path,
    ["frame_index", "matches"],
    ["type", "code", "message", "reason"],
  );
  const optionalFields = Object.fromEntries(
    SEMANTIC_FIELDS.flatMap((field) =>
      Object.prototype.hasOwnProperty.call(error, field)
        ? [
            [
              field,
              readBoundedString(
                error[field],
                `${path}.${field}`,
                field === "message"
                  ? MAX_SEMANTIC_MESSAGE_BYTES
                  : MAX_SEMANTIC_SHORT_FIELD_BYTES,
                true,
              ),
            ],
          ]
        : [],
    ),
  );
  return Object.freeze({
    frame_index: readInteger(error.frame_index, `${path}.frame_index`, 0),
    ...optionalFields,
    matches: Object.freeze(
      readArray(error.matches, `${path}.matches`).map((match, index) =>
        parseMatch(match, `${path}.matches[${index}]`),
      ),
    ),
  }) as TestMessageExtractedError;
}

function parseWinner(value: unknown, path: string): TestMessageWinner {
  const winner = readRecord(value, path);
  assertExactKeys(winner, path, [
    "error_index",
    "rule_id",
    "matched_keywords",
    "matched_keyword_indexes",
    "matched_fields",
  ]);
  return Object.freeze({
    error_index: readInteger(winner.error_index, `${path}.error_index`, 0),
    ...parseMatch(
      {
        rule_id: winner.rule_id,
        matched_keywords: winner.matched_keywords,
        matched_keyword_indexes: winner.matched_keyword_indexes,
        matched_fields: winner.matched_fields,
      },
      path,
    ),
  });
}

export function parseTestMessageResponse(value: unknown): TestMessageResponse {
  const path = "test message response";
  const response = readRecord(value, path);
  assertExactKeys(response, path, [
    "schema_version",
    "rule_set_revision",
    "response_protocol_id",
    "analysis_status",
    "analysis_reason",
    "errors",
    "decisive_error_index",
    "winner",
  ]);
  assertSchemaVersion(response.schema_version, `${path}.schema_version`);
  const status = readEnum(
    response.analysis_status,
    ["complete", "fail_open"] as const,
    `${path}.analysis_status`,
  );
  const reason =
    response.analysis_reason === null
      ? null
      : readEnum(
          response.analysis_reason,
          ANALYSIS_REASONS,
          `${path}.analysis_reason`,
        );
  if ((status === "complete") !== (reason === null)) {
    contractError(
      `${path}.analysis_reason`,
      "must be null only for complete analysis",
    );
  }
  const rawErrors = readArray(response.errors, `${path}.errors`);
  if (rawErrors.length > MAX_TEST_MESSAGE_ERRORS) {
    contractError(
      `${path}.errors`,
      `must not exceed ${MAX_TEST_MESSAGE_ERRORS} items`,
    );
  }
  const errors = rawErrors.map((error, index) =>
    parseExtractedError(error, `${path}.errors[${index}]`),
  );
  if (errors.length === 0 && response.decisive_error_index !== null) {
    contractError(
      `${path}.decisive_error_index`,
      "must be null when no errors were extracted",
    );
  }
  const decisiveErrorIndex =
    response.decisive_error_index === null
      ? null
      : readInteger(
          response.decisive_error_index,
          `${path}.decisive_error_index`,
          0,
          errors.length - 1,
        );
  const winner =
    response.winner === null
      ? null
      : parseWinner(response.winner, `${path}.winner`);
  if ((decisiveErrorIndex === null) !== (winner === null)) {
    contractError(path, "must emit decisive_error_index and winner together");
  }
  if (winner && winner.error_index !== decisiveErrorIndex) {
    contractError(
      `${path}.winner.error_index`,
      "must equal decisive_error_index",
    );
  }
  const protocolID =
    response.response_protocol_id === null
      ? null
      : readString(
          response.response_protocol_id,
          `${path}.response_protocol_id`,
        );
  if (status === "complete" && protocolID === null) {
    contractError(
      `${path}.response_protocol_id`,
      "must be present for complete analysis",
    );
  }
  return Object.freeze({
    schema_version: INTERNAL_ERROR_SCHEMA_VERSION,
    rule_set_revision: readCanonicalDecimal(
      response.rule_set_revision,
      `${path}.rule_set_revision`,
    ),
    response_protocol_id: protocolID,
    analysis_status: status,
    analysis_reason: reason,
    errors: Object.freeze(errors),
    decisive_error_index: decisiveErrorIndex,
    winner,
  });
}

export function parseInternalErrorAPIError(
  value: unknown,
): InternalErrorAPIErrorResponse {
  const path = "internal error API error";
  const response = readRecord(value, path);
  assertExactKeys(response, path, ["code", "message", "details"]);
  const code = readEnum(
    response.code,
    [
      "VALIDATION_ERROR",
      "NOT_FOUND",
      "CONFLICT",
      "REVISION_MISMATCH",
      "REQUEST_TOO_LARGE",
      "PRECONDITION_REQUIRED",
      "INTERNAL_ERROR",
    ] as const,
    `${path}.code`,
  );
  const message = readString(response.message, `${path}.message`);
  const details = readRecord(response.details, `${path}.details`);
  switch (code) {
    case "VALIDATION_ERROR":
      assertExactKeys(details, `${path}.details`, ["field"]);
      return Object.freeze({
        code,
        message,
        details: Object.freeze({
          field: readString(details.field, `${path}.details.field`),
        }),
      });
    case "NOT_FOUND":
      return Object.freeze({
        code,
        message,
        details: parseNotFoundDetails(details, `${path}.details`),
      });
    case "CONFLICT":
      if (!Object.prototype.hasOwnProperty.call(details, "limit")) {
        assertExactKeys(details, `${path}.details`, []);
        return Object.freeze({
          code,
          message,
          details: Object.freeze({}) as Record<string, never>,
        });
      }
      assertExactKeys(details, `${path}.details`, ["limit"]);
      return Object.freeze({
        code,
        message,
        details: Object.freeze({
          limit: readInteger(details.limit, `${path}.details.limit`, 0),
        }),
      });
    case "REVISION_MISMATCH":
      assertExactKeys(details, `${path}.details`, ["current_revision"]);
      return Object.freeze({
        code,
        message,
        details: Object.freeze({
          current_revision: readCanonicalDecimal(
            details.current_revision,
            `${path}.details.current_revision`,
          ),
        }),
      });
    case "REQUEST_TOO_LARGE":
      assertExactKeys(details, `${path}.details`, ["limit_bytes"]);
      return Object.freeze({
        code,
        message,
        details: Object.freeze({
          limit_bytes: readInteger(
            details.limit_bytes,
            `${path}.details.limit_bytes`,
            0,
          ),
        }),
      });
    case "PRECONDITION_REQUIRED":
    case "INTERNAL_ERROR":
      assertExactKeys(details, `${path}.details`, []);
      return Object.freeze({
        code,
        message,
        details: Object.freeze({}) as Record<string, never>,
      });
  }
}

export function parseInternalErrorRuleETag(
  value: string | null,
): InternalErrorRuleETag {
  if (value === null || !RULE_ETAG.test(value)) {
    contractError(
      "internal error rule ETag",
      "must be one strong canonical rule ETag",
    );
  }
  return value as InternalErrorRuleETag;
}

export function revisionFromInternalErrorRuleETag(
  etag: InternalErrorRuleETag,
): string {
  const match = RULE_ETAG.exec(etag);
  if (!match) {
    contractError(
      "internal error rule ETag",
      "must be one strong canonical rule ETag",
    );
  }
  return match[1];
}
