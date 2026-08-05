import type {
  SemanticErrorEvidence,
  SemanticEvidenceAlternate,
  SemanticEvidenceDecision,
  SemanticEvidenceHealth,
  SemanticEvidenceIdentity,
  SemanticEvidenceResponse,
  SemanticEvidenceRetry,
  SemanticEvidenceRule,
  SemanticFieldName,
  SemanticRuleSnapshot,
} from "../evidence-types";
import {
  assertExactKeys,
  contractError,
  readArray,
  readBoolean,
  readCanonicalDecimal,
  readEnum,
  readInteger,
  readRecord,
  readString,
  readUUID,
  type JsonRecord,
} from "@/features/error-detection/contracts/contract";
import {
  MAX_INTERNAL_ERROR_RULE_COUNT,
  MAX_INTERNAL_ERROR_RULE_KEYWORDS,
  parseInternalErrorRuleSpec,
} from "@/features/error-detection/contracts/rule-decoder";

const SEMANTIC_SCHEMA_VERSION = 1 as const;
const RESPONSE_STATES = ["probing", "forwarding", "discarded"] as const;
const MATCH_TIMINGS = ["probing", "forwarding"] as const;
const BOUNDARY_REASONS = [
  "request_probe_memory_exhausted",
  "process_probe_memory_exhausted",
  "unsupported_response_protocol",
  "unsupported_content_encoding",
  "content_decoding_failed",
  "malformed_protocol_frame",
  "decoded_event_too_large",
  "semantic_field_too_large",
  "analysis_internal_error",
  "no_retry_candidate",
  "passthrough_only",
  "semantic_match",
  "client_visible_event",
  "probe_duration_elapsed",
  "upstream_eof_no_match",
  "upstream_read_failure",
  "client_cancelled",
] as const;
const RULE_ACTIONS = [
  "passthrough",
  "retry_only",
  "retry_then_switch",
] as const;
const SEMANTIC_FIELDS = ["type", "code", "message", "reason"] as const;
const ALTERNATE_OUTCOMES = [
  "not_requested",
  "reserved",
  "activated",
  "unavailable",
  "failed",
  "released",
] as const;
const SWITCH_MODES = ["replacement", "failover"] as const;
const SWITCH_REASONS = [
  "internal_error_rule_exhausted",
  "internal_error_provider_unavailable",
] as const;
const DECISION_VALUES = [
  "passthrough",
  "observe_only",
  "commit_current",
  "retry_same",
  "switch_provider",
  "abort_client",
] as const;
const DECISION_REASONS = [
  "action_passthrough",
  "observer_only",
  "retry_budget_available",
  "rule_retry_budget_exhausted",
  "global_attempt_budget_exhausted",
  "reserved_switch_attempt",
  "alternate_provider_unavailable",
  "alternate_reservation_failed",
  "provider_deleted",
  "provider_disabled",
  "api_removed",
  "routing_changed",
  "group_disabled",
  "auth_unavailable",
  "provider_lookup_error",
  "response_already_visible",
  "client_retry_requested",
  "client_cancelled",
] as const;
const HEALTH_VERDICTS = ["success", "failure", "neutral"] as const;
const HEALTH_CAUSES = [
  "normal_completion",
  "transport_failure",
  "http_status_failure",
  "semantic_retry_then_switch",
  "semantic_neutral",
  "client_cancelled",
  "incomplete",
] as const;

function parseIdentity(value: unknown, path: string): SemanticEvidenceIdentity {
  const identity = readRecord(value, path);
  assertExactKeys(identity, path, [
    "request_id",
    "operation_id",
    "provider_id",
    "logical_attempt",
    "provider_attempt",
    "credential_phase",
  ]);
  return Object.freeze({
    request_id: readString(identity.request_id, `${path}.request_id`),
    operation_id: readString(identity.operation_id, `${path}.operation_id`),
    provider_id: readString(identity.provider_id, `${path}.provider_id`),
    logical_attempt: readCanonicalDecimal(
      identity.logical_attempt,
      `${path}.logical_attempt`,
    ),
    provider_attempt: readCanonicalDecimal(
      identity.provider_attempt,
      `${path}.provider_attempt`,
    ),
    credential_phase: readString(
      identity.credential_phase,
      `${path}.credential_phase`,
    ),
  });
}

function parseResponse(value: unknown, path: string): SemanticEvidenceResponse {
  const response = readRecord(value, path);
  assertExactKeys(response, path, [
    "protocol_id",
    "state",
    "match_timing",
    "boundary_reason",
    "elapsed_ms",
    "peak_probe_bytes",
    "raw_probe_bytes",
    "decoded_probe_bytes",
    "upstream_bytes_read",
    "client_body_bytes_written",
    "headers_committed",
    "visible_to_client",
  ]);
  return Object.freeze({
    // Protocol IDs come from the catalog; evidence validates identity, not a
    // duplicated frontend allow-list that could drift from that authority.
    protocol_id: readString(response.protocol_id, `${path}.protocol_id`),
    state: readEnum(response.state, RESPONSE_STATES, `${path}.state`),
    match_timing: readEnum(
      response.match_timing,
      MATCH_TIMINGS,
      `${path}.match_timing`,
    ),
    boundary_reason: readEnum(
      response.boundary_reason,
      BOUNDARY_REASONS,
      `${path}.boundary_reason`,
    ),
    elapsed_ms: readCanonicalDecimal(response.elapsed_ms, `${path}.elapsed_ms`),
    peak_probe_bytes: readCanonicalDecimal(
      response.peak_probe_bytes,
      `${path}.peak_probe_bytes`,
    ),
    raw_probe_bytes: readCanonicalDecimal(
      response.raw_probe_bytes,
      `${path}.raw_probe_bytes`,
    ),
    decoded_probe_bytes: readCanonicalDecimal(
      response.decoded_probe_bytes,
      `${path}.decoded_probe_bytes`,
    ),
    upstream_bytes_read: readCanonicalDecimal(
      response.upstream_bytes_read,
      `${path}.upstream_bytes_read`,
    ),
    client_body_bytes_written: readCanonicalDecimal(
      response.client_body_bytes_written,
      `${path}.client_body_bytes_written`,
    ),
    headers_committed: readBoolean(
      response.headers_committed,
      `${path}.headers_committed`,
    ),
    visible_to_client: readBoolean(
      response.visible_to_client,
      `${path}.visible_to_client`,
    ),
  });
}

function parseRuleSnapshot(value: unknown, path: string): SemanticRuleSnapshot {
  const snapshot = readRecord(value, path);
  assertExactKeys(snapshot, path, [
    "name",
    "enabled",
    "target",
    "api_type",
    "keywords",
    "match_mode",
    "action",
    "position",
  ]);
  const spec = parseInternalErrorRuleSpec(
    {
      name: snapshot.name,
      enabled: snapshot.enabled,
      target: snapshot.target,
      api_type: snapshot.api_type,
      keywords: snapshot.keywords,
      match_mode: snapshot.match_mode,
      action: snapshot.action,
    },
    path,
  );
  return Object.freeze({
    ...spec,
    position: readInteger(
      snapshot.position,
      `${path}.position`,
      0,
      MAX_INTERNAL_ERROR_RULE_COUNT - 1,
    ),
  });
}

function parseMatchingRuleIDs(
  value: unknown,
  winnerID: string,
  path: string,
): readonly string[] {
  const ids = readArray(value, path).map((id, index) =>
    readUUID(id, `${path}[${index}]`),
  );
  if (ids.length === 0 || ids.length > MAX_INTERNAL_ERROR_RULE_COUNT) {
    contractError(
      path,
      `must contain between 1 and ${MAX_INTERNAL_ERROR_RULE_COUNT} items`,
    );
  }
  if (ids[0] !== winnerID) {
    contractError(path, "must begin with winner_id");
  }
  if (new Set(ids).size !== ids.length) {
    contractError(path, "must contain unique rule IDs");
  }
  return Object.freeze(ids);
}

function parseMatchedKeywords(
  rule: JsonRecord,
  snapshot: SemanticRuleSnapshot,
  path: string,
): Pick<
  SemanticEvidenceRule,
  "matched_keywords" | "matched_keyword_indexes" | "matched_fields"
> {
  const keywords = readArray(
    rule.matched_keywords,
    `${path}.matched_keywords`,
  ).map((keyword, index) =>
    readString(keyword, `${path}.matched_keywords[${index}]`),
  );
  const indexes = readArray(
    rule.matched_keyword_indexes,
    `${path}.matched_keyword_indexes`,
  ).map((index, itemIndex) =>
    readInteger(
      index,
      `${path}.matched_keyword_indexes[${itemIndex}]`,
      0,
      snapshot.keywords.length - 1,
    ),
  );
  if (
    keywords.length === 0 ||
    keywords.length > MAX_INTERNAL_ERROR_RULE_KEYWORDS ||
    keywords.length !== indexes.length
  ) {
    contractError(path, "must pair every matched keyword with its rule index");
  }
  indexes.forEach((keywordIndex, index) => {
    if (index > 0 && keywordIndex <= indexes[index - 1]) {
      contractError(
        `${path}.matched_keyword_indexes`,
        "must be strictly ascending",
      );
    }
    if (keywords[index] !== snapshot.keywords[keywordIndex]) {
      contractError(
        `${path}.matched_keywords[${index}]`,
        "must equal the winner keyword at its paired index",
      );
    }
  });
  const fields = readArray(rule.matched_fields, `${path}.matched_fields`).map(
    (field, index) =>
      readEnum(field, SEMANTIC_FIELDS, `${path}.matched_fields[${index}]`),
  );
  if (
    fields.length === 0 ||
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
  return {
    matched_keywords: Object.freeze(keywords),
    matched_keyword_indexes: Object.freeze(indexes),
    matched_fields: Object.freeze(fields),
  };
}

function parseRule(value: unknown, path: string): SemanticEvidenceRule {
  const rule = readRecord(value, path);
  assertExactKeys(rule, path, [
    "revision",
    "winner_id",
    "normalized_snapshot",
    "matching_rule_ids",
    "matched_keywords",
    "matched_keyword_indexes",
    "matched_fields",
  ]);
  const winnerID = readUUID(rule.winner_id, `${path}.winner_id`);
  const snapshot = parseRuleSnapshot(
    rule.normalized_snapshot,
    `${path}.normalized_snapshot`,
  );
  return Object.freeze({
    revision: readCanonicalDecimal(rule.revision, `${path}.revision`),
    winner_id: winnerID,
    normalized_snapshot: snapshot,
    matching_rule_ids: parseMatchingRuleIDs(
      rule.matching_rule_ids,
      winnerID,
      `${path}.matching_rule_ids`,
    ),
    ...parseMatchedKeywords(rule, snapshot, path),
  });
}

function parseRetry(
  value: unknown,
  path: string,
  snapshot: SemanticRuleSnapshot,
): SemanticEvidenceRetry {
  const retry = readRecord(value, path);
  assertExactKeys(retry, path, [
    "action",
    "global_attempts_started",
    "global_attempts_remaining",
    "global_attempts_unlimited",
    "rule_retries_scheduled",
    "rule_retry_limit",
  ]);
  const action = readEnum(retry.action, RULE_ACTIONS, `${path}.action`);
  if (action !== snapshot.action.type) {
    contractError(`${path}.action`, "must equal the winning rule action");
  }
  const unlimited = readBoolean(
    retry.global_attempts_unlimited,
    `${path}.global_attempts_unlimited`,
  );
  const remaining =
    retry.global_attempts_remaining === null
      ? null
      : readCanonicalDecimal(
          retry.global_attempts_remaining,
          `${path}.global_attempts_remaining`,
        );
  if (unlimited !== (remaining === null)) {
    contractError(
      `${path}.global_attempts_remaining`,
      "must be null exactly when the global budget is unlimited",
    );
  }
  const retryLimit = readInteger(
    retry.rule_retry_limit,
    `${path}.rule_retry_limit`,
    0,
  );
  const snapshotRetryLimit =
    snapshot.action.type === "passthrough" ? 0 : snapshot.action.max_retries;
  if (retryLimit !== snapshotRetryLimit) {
    contractError(
      `${path}.rule_retry_limit`,
      "must equal the winning rule limit",
    );
  }
  return Object.freeze({
    action,
    global_attempts_started: readCanonicalDecimal(
      retry.global_attempts_started,
      `${path}.global_attempts_started`,
    ),
    global_attempts_remaining: remaining,
    global_attempts_unlimited: unlimited,
    rule_retries_scheduled: readCanonicalDecimal(
      retry.rule_retries_scheduled,
      `${path}.rule_retries_scheduled`,
    ),
    rule_retry_limit: retryLimit,
  });
}

function readNullableEnum<const T extends readonly string[]>(
  value: unknown,
  allowed: T,
  path: string,
): T[number] | null {
  return value === null ? null : readEnum(value, allowed, path);
}

function parseAlternate(
  value: unknown,
  path: string,
): SemanticEvidenceAlternate {
  const alternate = readRecord(value, path);
  assertExactKeys(alternate, path, [
    "outcome",
    "provider_id",
    "switch_mode",
    "switch_reason",
  ]);
  return Object.freeze({
    outcome: readEnum(alternate.outcome, ALTERNATE_OUTCOMES, `${path}.outcome`),
    provider_id:
      alternate.provider_id === null
        ? null
        : readString(alternate.provider_id, `${path}.provider_id`),
    switch_mode: readNullableEnum(
      alternate.switch_mode,
      SWITCH_MODES,
      `${path}.switch_mode`,
    ),
    switch_reason: readNullableEnum(
      alternate.switch_reason,
      SWITCH_REASONS,
      `${path}.switch_reason`,
    ),
  });
}

function parseDecision(value: unknown, path: string): SemanticEvidenceDecision {
  const decision = readRecord(value, path);
  assertExactKeys(decision, path, ["value", "reason"]);
  return Object.freeze({
    value: readEnum(decision.value, DECISION_VALUES, `${path}.value`),
    reason: readEnum(decision.reason, DECISION_REASONS, `${path}.reason`),
  });
}

function parseHealth(value: unknown, path: string): SemanticEvidenceHealth {
  const health = readRecord(value, path);
  assertExactKeys(health, path, ["verdict", "cause", "circuit_opened"]);
  return Object.freeze({
    verdict: readEnum(health.verdict, HEALTH_VERDICTS, `${path}.verdict`),
    cause: readEnum(health.cause, HEALTH_CAUSES, `${path}.cause`),
    circuit_opened: readBoolean(
      health.circuit_opened,
      `${path}.circuit_opened`,
    ),
  });
}

export function parseSemanticError(
  value: unknown,
  path: string,
): SemanticErrorEvidence {
  const semantic = readRecord(value, path);
  assertExactKeys(semantic, path, [
    "schema_version",
    "identity",
    "response",
    "rule",
    "retry",
    "alternate",
    "decision",
    "health",
  ]);
  if (semantic.schema_version !== SEMANTIC_SCHEMA_VERSION) {
    contractError(
      `${path}.schema_version`,
      `must equal ${SEMANTIC_SCHEMA_VERSION}`,
    );
  }
  const rule = parseRule(semantic.rule, `${path}.rule`);
  return Object.freeze({
    schema_version: SEMANTIC_SCHEMA_VERSION,
    identity: parseIdentity(semantic.identity, `${path}.identity`),
    response: parseResponse(semantic.response, `${path}.response`),
    rule,
    retry: parseRetry(
      semantic.retry,
      `${path}.retry`,
      rule.normalized_snapshot,
    ),
    alternate: parseAlternate(semantic.alternate, `${path}.alternate`),
    decision: parseDecision(semantic.decision, `${path}.decision`),
    health: parseHealth(semantic.health, `${path}.health`),
  });
}
