import type {
  TokenBreakdownDTO,
  TokenBucketDTO,
  TokenCoverageDTO,
  TokenDataQualityDTO,
  TokenModelRankDTO,
  TokenProviderRankDTO,
  TokenSummaryDTO,
  TokenTimeRangeDTO,
  TokenUsageResponse,
} from "./token-usage-types";

type JsonRecord = Record<string, unknown>;

const BREAKDOWN_FIELDS = [
  "total_tokens",
  "input_tokens",
  "output_tokens",
  "fresh_input_tokens",
  "cache_read_input_tokens",
  "cache_creation_input_tokens",
  "unclassified_input_tokens",
  "standard_output_tokens",
  "reasoning_tokens",
  "unclassified_output_tokens",
] as const;

const SUPPORTED_GRANULARITIES = new Set(["5m", "15m", "1h", "6h", "1d"]);
const RATIO_TOLERANCE = 1e-12;
const RFC3339_PATTERN =
  /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.\d+)?(Z|([+-])(\d{2}):(\d{2}))$/;

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

function assertCountValue(
  value: unknown,
  label: string,
): asserts value is number {
  assertContract(
    typeof value === "number" &&
      Number.isFinite(value) &&
      Number.isSafeInteger(value) &&
      value >= 0,
    `${label} must be a finite non-negative integer`,
  );
}

function assertRatioValue(
  value: unknown,
  label: string,
): asserts value is number {
  assertContract(
    typeof value === "number" &&
      Number.isFinite(value) &&
      value >= 0 &&
      value <= 1,
    `${label} must be a ratio between 0 and 1`,
  );
}

function assertDerivedRatio(
  actual: number,
  numerator: number | bigint,
  denominator: number | bigint,
  label: string,
): void {
  const numeratorNumber = Number(numerator);
  const denominatorNumber = Number(denominator);
  const expected =
    denominatorNumber === 0 ? 0 : numeratorNumber / denominatorNumber;
  assertContract(
    Math.abs(actual - expected) <= RATIO_TOLERANCE,
    `${label} is inconsistent with its counts`,
  );
}

function assertStringValue(
  value: unknown,
  label: string,
): asserts value is string {
  assertContract(typeof value === "string", `${label} must be a string`);
}

function assertDecimalString(
  value: unknown,
  label: string,
): asserts value is string {
  assertContract(
    typeof value === "string" && /^\d+$/.test(value),
    `${label} must be a non-negative decimal string`,
  );
}

function parseRFC3339Timestamp(value: unknown, label: string): number {
  assertStringValue(value, label);
  const match = RFC3339_PATTERN.exec(value);
  assertContract(match, `${label} must be an RFC3339 timestamp`);

  const year = Number(match[1]);
  const month = Number(match[2]);
  const day = Number(match[3]);
  const hour = Number(match[4]);
  const minute = Number(match[5]);
  const second = Number(match[6]);
  const offsetHour = match[9] === undefined ? 0 : Number(match[9]);
  const offsetMinute = match[10] === undefined ? 0 : Number(match[10]);

  const calendarDate = new Date(0);
  calendarDate.setUTCFullYear(year, month - 1, day);
  calendarDate.setUTCHours(0, 0, 0, 0);
  const hasValidCalendarDate =
    calendarDate.getUTCFullYear() === year &&
    calendarDate.getUTCMonth() === month - 1 &&
    calendarDate.getUTCDate() === day;

  assertContract(
    hasValidCalendarDate &&
      hour <= 23 &&
      minute <= 59 &&
      second <= 59 &&
      offsetHour <= 23 &&
      offsetMinute <= 59,
    `${label} must be an RFC3339 timestamp`,
  );

  const timestamp = Date.parse(value);
  assertContract(
    Number.isFinite(timestamp),
    `${label} must be an RFC3339 timestamp`,
  );
  return timestamp;
}

function assertBreakdownConservation(
  breakdown: TokenBreakdownDTO,
  context: string,
): void {
  const total = BigInt(breakdown.total_tokens);
  const input = BigInt(breakdown.input_tokens);
  const output = BigInt(breakdown.output_tokens);
  const inputSegments =
    BigInt(breakdown.fresh_input_tokens) +
    BigInt(breakdown.cache_read_input_tokens) +
    BigInt(breakdown.cache_creation_input_tokens) +
    BigInt(breakdown.unclassified_input_tokens);
  const outputSegments =
    BigInt(breakdown.standard_output_tokens) +
    BigInt(breakdown.reasoning_tokens) +
    BigInt(breakdown.unclassified_output_tokens);

  assertContract(
    total === input + output,
    `${context} must conserve total_tokens as input_tokens + output_tokens`,
  );
  assertContract(
    input === inputSegments,
    `${context} must conserve input_tokens across input segments`,
  );
  assertContract(
    output === outputSegments,
    `${context} must conserve output_tokens across output segments`,
  );
}

function parseBreakdown(value: unknown, context: string): TokenBreakdownDTO {
  assertContract(isRecord(value), `${context} must be an object`);
  for (const field of BREAKDOWN_FIELDS) {
    assertContract(hasOwn(value, field), `${context}.${field} is required`);
    assertDecimalString(value[field], `${context}.${field}`);
  }

  const breakdown = value as unknown as TokenBreakdownDTO;
  assertBreakdownConservation(breakdown, context);
  return breakdown;
}

function parseSummary(value: unknown, context: string): TokenSummaryDTO {
  assertContract(isRecord(value), `${context} must be an object`);
  parseBreakdown(value, context);

  assertContract(
    hasOwn(value, "cache_hit_rate"),
    `${context}.cache_hit_rate is required`,
  );
  assertRatioValue(value.cache_hit_rate, `${context}.cache_hit_rate`);

  assertContract(
    hasOwn(value, "reasoning_ratio"),
    `${context}.reasoning_ratio is required`,
  );
  assertRatioValue(value.reasoning_ratio, `${context}.reasoning_ratio`);

  const summary = value as unknown as TokenSummaryDTO;
  assertDerivedRatio(
    summary.cache_hit_rate,
    BigInt(summary.cache_read_input_tokens),
    BigInt(summary.input_tokens),
    `${context}.cache_hit_rate`,
  );
  assertDerivedRatio(
    summary.reasoning_ratio,
    BigInt(summary.reasoning_tokens),
    BigInt(summary.output_tokens),
    `${context}.reasoning_ratio`,
  );

  return summary;
}

function parseBucket(value: unknown, context: string): TokenBucketDTO {
  assertContract(isRecord(value), `${context} must be an object`);
  parseBreakdown(value, context);

  assertContract(hasOwn(value, "start"), `${context}.start is required`);
  const start = parseRFC3339Timestamp(value.start, `${context}.start`);

  assertContract(hasOwn(value, "end"), `${context}.end is required`);
  const end = parseRFC3339Timestamp(value.end, `${context}.end`);
  assertContract(start < end, `${context} must have start before end`);

  assertContract(
    hasOwn(value, "total_requests"),
    `${context}.total_requests is required`,
  );
  assertCountValue(value.total_requests, `${context}.total_requests`);

  assertContract(
    hasOwn(value, "observed_requests"),
    `${context}.observed_requests is required`,
  );
  assertCountValue(value.observed_requests, `${context}.observed_requests`);

  assertContract(
    hasOwn(value, "comparable_requests"),
    `${context}.comparable_requests is required`,
  );
  assertCountValue(value.comparable_requests, `${context}.comparable_requests`);

  assertContract(
    value.observed_requests <= value.total_requests,
    `${context}.observed_requests cannot exceed total_requests`,
  );
  assertContract(
    value.comparable_requests <= value.observed_requests,
    `${context}.comparable_requests cannot exceed observed_requests`,
  );

  return value as unknown as TokenBucketDTO;
}

function parseProviderRank(
  value: unknown,
  context: string,
): TokenProviderRankDTO {
  assertContract(isRecord(value), `${context} must be an object`);
  parseBreakdown(value, context);

  assertContract(
    hasOwn(value, "provider_id"),
    `${context}.provider_id is required`,
  );
  assertStringValue(value.provider_id, `${context}.provider_id`);

  assertContract(
    hasOwn(value, "provider_name"),
    `${context}.provider_name is required`,
  );
  assertStringValue(value.provider_name, `${context}.provider_name`);

  assertContract(
    hasOwn(value, "request_count"),
    `${context}.request_count is required`,
  );
  assertCountValue(value.request_count, `${context}.request_count`);

  assertContract(hasOwn(value, "share"), `${context}.share is required`);
  assertRatioValue(value.share, `${context}.share`);

  return value as unknown as TokenProviderRankDTO;
}

function parseModelRank(value: unknown, context: string): TokenModelRankDTO {
  assertContract(isRecord(value), `${context} must be an object`);
  parseBreakdown(value, context);

  assertContract(hasOwn(value, "model"), `${context}.model is required`);
  assertStringValue(value.model, `${context}.model`);

  assertContract(
    hasOwn(value, "request_count"),
    `${context}.request_count is required`,
  );
  assertCountValue(value.request_count, `${context}.request_count`);

  assertContract(hasOwn(value, "share"), `${context}.share is required`);
  assertRatioValue(value.share, `${context}.share`);

  return value as unknown as TokenModelRankDTO;
}

function parseTimeRange(value: unknown, context: string): TokenTimeRangeDTO {
  assertContract(isRecord(value), `${context} must be an object`);

  assertContract(hasOwn(value, "start"), `${context}.start is required`);
  const start = parseRFC3339Timestamp(value.start, `${context}.start`);

  assertContract(hasOwn(value, "end"), `${context}.end is required`);
  const end = parseRFC3339Timestamp(value.end, `${context}.end`);
  assertContract(start <= end, `${context} must not end before it starts`);

  assertContract(
    hasOwn(value, "granularity"),
    `${context}.granularity is required`,
  );
  assertStringValue(value.granularity, `${context}.granularity`);
  assertContract(
    SUPPORTED_GRANULARITIES.has(value.granularity),
    `${context}.granularity is not supported`,
  );

  return value as unknown as TokenTimeRangeDTO;
}

function parseCoverage(value: unknown, context: string): TokenCoverageDTO {
  assertContract(isRecord(value), `${context} must be an object`);

  for (const field of [
    "total_requests",
    "observed_requests",
    "comparable_requests",
    "without_usage_requests",
  ] as const) {
    assertContract(hasOwn(value, field), `${context}.${field} is required`);
    assertCountValue(value[field], `${context}.${field}`);
  }

  assertContract(hasOwn(value, "rate"), `${context}.rate is required`);
  assertRatioValue(value.rate, `${context}.rate`);

  const coverage = value as unknown as TokenCoverageDTO;
  assertContract(
    coverage.observed_requests <= coverage.total_requests,
    `${context}.observed_requests cannot exceed total_requests`,
  );
  assertContract(
    coverage.comparable_requests <= coverage.observed_requests,
    `${context}.comparable_requests cannot exceed observed_requests`,
  );
  assertContract(
    coverage.without_usage_requests ===
      coverage.total_requests - coverage.observed_requests,
    `${context}.without_usage_requests must equal total_requests - observed_requests`,
  );
  assertDerivedRatio(
    coverage.rate,
    coverage.comparable_requests,
    coverage.total_requests,
    `${context}.rate`,
  );

  return coverage;
}

function parseDataQuality(
  value: unknown,
  context: string,
): TokenDataQualityDTO {
  assertContract(isRecord(value), `${context} must be an object`);

  assertContract(
    hasOwn(value, "quality_rate"),
    `${context}.quality_rate is required`,
  );
  assertRatioValue(value.quality_rate, `${context}.quality_rate`);

  for (const field of [
    "partial_requests",
    "invalid_requests",
    "unknown_semantics_requests",
  ] as const) {
    assertContract(hasOwn(value, field), `${context}.${field} is required`);
    assertCountValue(value[field], `${context}.${field}`);
  }

  return value as unknown as TokenDataQualityDTO;
}

/**
 * Rejecting internally contradictory reports keeps malformed telemetry in the
 * diagnostic error state instead of turning it into authoritative charts.
 */
export function parseTokenUsageResponse(value: unknown): TokenUsageResponse {
  assertContract(isRecord(value), "token usage response must be an object");

  assertContract(
    hasOwn(value, "summary"),
    "token usage response.summary is required",
  );
  const summary = parseSummary(value.summary, "token usage response.summary");

  assertContract(
    hasOwn(value, "timeseries"),
    "token usage response.timeseries is required",
  );
  assertContract(
    Array.isArray(value.timeseries),
    "token usage response.timeseries must be an array",
  );
  const timeseries = value.timeseries.map((bucket, index) =>
    parseBucket(bucket, `token usage response.timeseries[${index}]`),
  );

  assertContract(
    hasOwn(value, "by_provider"),
    "token usage response.by_provider is required",
  );
  assertContract(
    Array.isArray(value.by_provider),
    "token usage response.by_provider must be an array",
  );
  const byProvider = value.by_provider.map((rank, index) =>
    parseProviderRank(rank, `token usage response.by_provider[${index}]`),
  );

  assertContract(
    hasOwn(value, "by_model"),
    "token usage response.by_model is required",
  );
  assertContract(
    Array.isArray(value.by_model),
    "token usage response.by_model must be an array",
  );
  const byModel = value.by_model.map((rank, index) =>
    parseModelRank(rank, `token usage response.by_model[${index}]`),
  );

  assertContract(
    hasOwn(value, "time_range"),
    "token usage response.time_range is required",
  );
  const timeRange = parseTimeRange(
    value.time_range,
    "token usage response.time_range",
  );

  assertContract(
    hasOwn(value, "coverage"),
    "token usage response.coverage is required",
  );
  const coverage = parseCoverage(
    value.coverage,
    "token usage response.coverage",
  );

  assertContract(
    hasOwn(value, "data_quality"),
    "token usage response.data_quality is required",
  );
  const dataQuality = parseDataQuality(
    value.data_quality,
    "token usage response.data_quality",
  );

  assertContract(
    coverage.comparable_requests +
      dataQuality.partial_requests +
      dataQuality.invalid_requests +
      dataQuality.unknown_semantics_requests ===
      coverage.observed_requests,
    "token usage response observed request quality partition is inconsistent",
  );
  assertDerivedRatio(
    dataQuality.quality_rate,
    coverage.comparable_requests,
    coverage.observed_requests,
    "token usage response.data_quality.quality_rate",
  );

  for (const [index, rank] of byProvider.entries()) {
    assertDerivedRatio(
      rank.share,
      BigInt(rank.total_tokens),
      BigInt(summary.total_tokens),
      `token usage response.by_provider[${index}].share`,
    );
  }
  for (const [index, rank] of byModel.entries()) {
    assertDerivedRatio(
      rank.share,
      BigInt(rank.total_tokens),
      BigInt(summary.total_tokens),
      `token usage response.by_model[${index}].share`,
    );
  }

  const reportStart = Date.parse(timeRange.start);
  const reportEnd = Date.parse(timeRange.end);
  let previousBucketEnd = reportStart;
  for (const [index, bucket] of timeseries.entries()) {
    const bucketStart = Date.parse(bucket.start);
    const bucketEnd = Date.parse(bucket.end);
    assertContract(
      bucketStart >= reportStart && bucketEnd <= reportEnd,
      `token usage response.timeseries[${index}] must be within time_range`,
    );
    assertContract(
      bucketStart >= previousBucketEnd,
      `token usage response.timeseries[${index}] must be ordered and non-overlapping`,
    );
    previousBucketEnd = bucketEnd;
  }

  return {
    summary,
    timeseries,
    by_provider: byProvider,
    by_model: byModel,
    time_range: timeRange,
    coverage,
    data_quality: dataQuality,
  };
}
