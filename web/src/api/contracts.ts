import type { LogsResponse, RequestLog, StatsResponse } from "./types";

type JsonRecord = Record<string, unknown>;

const SEMANTICS_VERSIONS = ["legacy_pre_assessment", "normalized_v1"] as const;
const COMPLETION_STATES = ["unknown", "incomplete", "completed"] as const;
const SERVICE_OUTCOMES = [
  "completed",
  "interrupted",
  "never_started",
  "abandoned_by_client",
  "unknown",
] as const;
const CLIENT_ACTIONS = [
  "none",
  "transparent_retry",
  "reconnect_required",
] as const;
const TERMINATION_ACTORS = [
  "client",
  "gateway",
  "upstream",
  "internal",
  "unknown",
] as const;
const TERMINATION_REASONS = [
  "provider_unavailable",
  "provider_configuration_error",
  "usage_limit_reached",
  "websocket_connection_limit_reached",
  "client_request_error",
  "client_disconnect",
  "timeout",
  "transport_error",
  "upstream_semantic_error",
  "upstream_handshake_rejected",
  "client_upgrade_rejected",
  "internal_error",
  "clean_close",
  "unknown",
] as const;
const REQUEST_ATTEMPT_PHASES = [
  "pre_accept",
  "post_upgrade_pre_visible",
  "visible",
] as const;
const REQUEST_ATTEMPT_SWITCH_MODES = [
  "initial",
  "replacement",
  "failover",
] as const;
// Must mirror internal/model.RequestAttemptOutcome; the HTTP normalization
// path (classifyHTTPAttemptOutcome) emits values the websocket-only legacy
// four never covered, and a mismatch here rejects the whole log detail parse.
const REQUEST_ATTEMPT_OUTCOMES = [
  "upstream_handshake_rejected",
  "upstream_transport_error",
  "upstream_semantic_error",
  "visible_session",
  "upstream_completed",
  "upstream_http_status_error",
  "upstream_incomplete",
  "gateway_error",
] as const;

function isRecord(value: unknown): value is JsonRecord {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function hasOwn(value: JsonRecord, key: string): boolean {
  return Object.prototype.hasOwnProperty.call(value, key);
}

function assertContract(
  condition: unknown,
  message: string,
): asserts condition {
  if (!condition) {
    throw new Error(message);
  }
}

function assertEnumValue<T extends readonly string[]>(
  value: unknown,
  allowedValues: T,
  label: string,
): asserts value is T[number] {
  assertContract(
    typeof value === "string" && allowedValues.includes(value),
    `${label} must be one of: ${allowedValues.join(", ")}`,
  );
}

function assertNullableEnumValue<T extends readonly string[]>(
  value: unknown,
  allowedValues: T,
  label: string,
): void {
  if (value === null) {
    return;
  }
  assertEnumValue(value, allowedValues, label);
}

function assertNumberValue(
  value: unknown,
  label: string,
): asserts value is number {
  assertContract(typeof value === "number", `${label} must be a number`);
}

function assertNullableNumberValue(value: unknown, label: string): void {
  if (value === null) {
    return;
  }
  assertNumberValue(value, label);
}

function assertStringValue(
  value: unknown,
  label: string,
): asserts value is string {
  assertContract(typeof value === "string", `${label} must be a string`);
}

function assertNullableStringValue(value: unknown, label: string): void {
  if (value === null) {
    return;
  }
  assertStringValue(value, label);
}

function assertBooleanValue(
  value: unknown,
  label: string,
): asserts value is boolean {
  assertContract(typeof value === "boolean", `${label} must be a boolean`);
}

function assertNullableBooleanValue(value: unknown, label: string): void {
  if (value === null) {
    return;
  }
  assertBooleanValue(value, label);
}

type ContractValueValidator = (value: unknown, label: string) => void;

interface OptionalFieldValidator {
  key: string;
  validate: ContractValueValidator;
}

function assertOptionalField(
  value: JsonRecord,
  context: string,
  validator: OptionalFieldValidator,
): void {
  if (!hasOwn(value, validator.key) || value[validator.key] === undefined) {
    return;
  }

  validator.validate(value[validator.key], `${context}.${validator.key}`);
}

const OPTIONAL_REQUEST_ATTEMPT_FIELDS: ReadonlyArray<OptionalFieldValidator> = [
  {
    key: "switch_mode",
    validate: (value, label) =>
      assertEnumValue(value, REQUEST_ATTEMPT_SWITCH_MODES, label),
  },
  {
    key: "provider_attempt",
    validate: assertNumberValue,
  },
  {
    key: "provider_switch_count",
    validate: assertNumberValue,
  },
  {
    key: "phase",
    validate: (value, label) =>
      assertNullableEnumValue(value, REQUEST_ATTEMPT_PHASES, label),
  },
  {
    key: "outcome",
    validate: (value, label) =>
      assertNullableEnumValue(value, REQUEST_ATTEMPT_OUTCOMES, label),
  },
  {
    key: "result_visible_to_client",
    validate: assertNullableBooleanValue,
  },
  {
    key: "body_snippet",
    validate: assertStringValue,
  },
  {
    key: "req_body_snippet",
    validate: assertStringValue,
  },
  {
    key: "switch_reason",
    validate: assertStringValue,
  },
  {
    key: "continuity_seeded",
    validate: assertBooleanValue,
  },
  {
    key: "continuity_origin_provider_id",
    validate: assertStringValue,
  },
  {
    key: "continuity_seed_age_ms",
    validate: assertNullableNumberValue,
  },
];

function assertOptionalRequestAttemptFields(
  value: JsonRecord,
  context: string,
): void {
  OPTIONAL_REQUEST_ATTEMPT_FIELDS.forEach((validator) => {
    assertOptionalField(value, context, validator);
  });
}

function parseRequestAttempt(value: unknown, context: string) {
  assertContract(isRecord(value), `${context} must be an object`);

  assertNumberValue(value.id, `${context}.id`);
  assertStringValue(value.request_id, `${context}.request_id`);
  assertStringValue(value.provider_id, `${context}.provider_id`);

  assertContract(
    hasOwn(value, "semantics_version"),
    `${context}.semantics_version is required`,
  );
  assertEnumValue(
    value.semantics_version,
    SEMANTICS_VERSIONS,
    `${context}.semantics_version`,
  );

  assertNumberValue(value.attempt, `${context}.attempt`);
  assertNumberValue(value.status_code, `${context}.status_code`);
  assertStringValue(value.error, `${context}.error`);

  assertContract(
    hasOwn(value, "attempt_evidence_json"),
    `${context}.attempt_evidence_json must be present`,
  );
  assertNullableStringValue(
    value.attempt_evidence_json,
    `${context}.attempt_evidence_json`,
  );

  assertNumberValue(value.latency_ms, `${context}.latency_ms`);
  assertStringValue(value.created_at, `${context}.created_at`);

  assertOptionalRequestAttemptFields(value, context);
}

function assertNormalizedRequestLogContract(
  value: JsonRecord,
  context: string,
): void {
  assertContract(
    hasOwn(value, "client_transport_status_code"),
    `${context}.client_transport_status_code is required for normalized_v1 rows`,
  );
  assertNumberValue(
    value.client_transport_status_code,
    `${context}.client_transport_status_code`,
  );

  assertContract(
    hasOwn(value, "completion_state"),
    `${context}.completion_state is required for normalized_v1 rows`,
  );
  assertEnumValue(
    value.completion_state,
    COMPLETION_STATES,
    `${context}.completion_state`,
  );

  assertContract(
    hasOwn(value, "service_outcome"),
    `${context}.service_outcome is required for normalized_v1 rows`,
  );
  assertEnumValue(
    value.service_outcome,
    SERVICE_OUTCOMES,
    `${context}.service_outcome`,
  );

  assertContract(
    hasOwn(value, "client_action"),
    `${context}.client_action is required for normalized_v1 rows`,
  );
  assertEnumValue(
    value.client_action,
    CLIENT_ACTIONS,
    `${context}.client_action`,
  );

  assertContract(
    hasOwn(value, "termination_actor"),
    `${context}.termination_actor must be present for normalized_v1 rows`,
  );
  assertNullableEnumValue(
    value.termination_actor,
    TERMINATION_ACTORS,
    `${context}.termination_actor`,
  );

  assertContract(
    hasOwn(value, "termination_reason"),
    `${context}.termination_reason must be present for normalized_v1 rows`,
  );
  assertNullableEnumValue(
    value.termination_reason,
    TERMINATION_REASONS,
    `${context}.termination_reason`,
  );

  assertContract(
    hasOwn(value, "session_evidence_json"),
    `${context}.session_evidence_json must be present for normalized_v1 rows`,
  );
  assertNullableStringValue(
    value.session_evidence_json,
    `${context}.session_evidence_json`,
  );
}

export function parseRequestLog(value: unknown, context: string): RequestLog {
  assertContract(isRecord(value), `${context} must be an object`);
  assertContract(
    hasOwn(value, "semantics_version"),
    `${context}.semantics_version is required`,
  );
  assertEnumValue(
    value.semantics_version,
    SEMANTICS_VERSIONS,
    `${context}.semantics_version`,
  );

  if (value.semantics_version === "normalized_v1") {
    assertNormalizedRequestLogContract(value, context);
  }

  if (hasOwn(value, "attempts")) {
    assertContract(
      Array.isArray(value.attempts),
      `${context}.attempts must be an array`,
    );
    value.attempts.forEach((attempt, index) => {
      parseRequestAttempt(attempt, `${context}.attempts[${index}]`);
    });
  }

  return value as unknown as RequestLog;
}

export function parseLogsResponse(value: unknown): LogsResponse {
  assertContract(isRecord(value), "logs response must be an object");
  assertContract(
    Array.isArray(value.logs),
    "logs response.logs must be an array",
  );

  value.logs.forEach((log, index) => {
    parseRequestLog(log, `logs response.logs[${index}]`);
  });

  return value as unknown as LogsResponse;
}

export function parseStatsResponse(value: unknown): StatsResponse {
  assertContract(isRecord(value), "stats response must be an object");
  assertContract(
    Array.isArray(value.outcome_timeseries),
    "stats response.outcome_timeseries must be an array",
  );

  value.outcome_timeseries.forEach((point, index) => {
    assertContract(
      isRecord(point),
      `stats response.outcome_timeseries[${index}] must be an object`,
    );
    assertStringValue(
      point.time,
      `stats response.outcome_timeseries[${index}].time`,
    );
    assertContract(
      hasOwn(point, "total_requests"),
      `stats response.outcome_timeseries[${index}].total_requests is required`,
    );
    assertNumberValue(
      point.total_requests,
      `stats response.outcome_timeseries[${index}].total_requests`,
    );
    assertNumberValue(
      point.avg_latency_ms,
      `stats response.outcome_timeseries[${index}].avg_latency_ms`,
    );
    assertContract(
      isRecord(point.outcome_counts),
      `stats response.outcome_timeseries[${index}].outcome_counts must be an object`,
    );
  });

  return value as unknown as StatsResponse;
}
