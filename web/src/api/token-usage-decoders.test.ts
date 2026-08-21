import { describe, expect, it } from "vitest";
import { parseTokenUsageResponse } from "./token-usage-decoders";
import type { TokenUsageResponse } from "./token-usage-types";

function createValidTokenUsageResponse(): TokenUsageResponse {
  return {
    summary: {
      total_tokens: "12451200",
      input_tokens: "8204110",
      output_tokens: "4247090",
      fresh_input_tokens: "4663710",
      cache_read_input_tokens: "3120400",
      cache_creation_input_tokens: "420000",
      unclassified_input_tokens: "0",
      standard_output_tokens: "3426690",
      reasoning_tokens: "820400",
      unclassified_output_tokens: "0",
      cache_hit_rate: 3120400 / 8204110,
      reasoning_ratio: 820400 / 4247090,
    },
    timeseries: [
      {
        start: "2026-08-21T00:00:00Z",
        end: "2026-08-21T01:00:00Z",
        total_tokens: "1200000",
        input_tokens: "800000",
        output_tokens: "400000",
        fresh_input_tokens: "400000",
        cache_read_input_tokens: "350000",
        cache_creation_input_tokens: "50000",
        unclassified_input_tokens: "0",
        standard_output_tokens: "320000",
        reasoning_tokens: "80000",
        unclassified_output_tokens: "0",
        total_requests: 120,
        observed_requests: 118,
        comparable_requests: 118,
      },
    ],
    by_provider: [
      {
        provider_id: "p1",
        provider_name: "Anthropic Direct",
        total_tokens: "7420000",
        input_tokens: "5000000",
        output_tokens: "2420000",
        fresh_input_tokens: "2000000",
        cache_read_input_tokens: "2800000",
        cache_creation_input_tokens: "200000",
        unclassified_input_tokens: "0",
        standard_output_tokens: "2420000",
        reasoning_tokens: "0",
        unclassified_output_tokens: "0",
        request_count: 840,
        share: 7420000 / 12451200,
      },
    ],
    by_model: [
      {
        model: "claude-3-7-sonnet",
        total_tokens: "6120000",
        input_tokens: "4000000",
        output_tokens: "2120000",
        fresh_input_tokens: "1800000",
        cache_read_input_tokens: "2000000",
        cache_creation_input_tokens: "200000",
        unclassified_input_tokens: "0",
        standard_output_tokens: "2120000",
        reasoning_tokens: "0",
        unclassified_output_tokens: "0",
        request_count: 620,
        share: 6120000 / 12451200,
      },
    ],
    time_range: {
      start: "2026-08-20T16:00:00Z",
      end: "2026-08-21T16:00:00Z",
      granularity: "1h",
    },
    coverage: {
      total_requests: 1456,
      observed_requests: 1430,
      comparable_requests: 1430,
      without_usage_requests: 26,
      rate: 1430 / 1456,
    },
    data_quality: {
      quality_rate: 1.0,
      partial_requests: 0,
      invalid_requests: 0,
      unknown_semantics_requests: 0,
    },
  };
}

describe("token-usage-decoders", () => {
  it("successfully decodes a complete canonical token usage payload", () => {
    const valid = createValidTokenUsageResponse();
    const result = parseTokenUsageResponse(valid);
    expect(result).toEqual(valid);
  });

  it("successfully decodes responses with empty lists", () => {
    const valid = createValidTokenUsageResponse();
    valid.timeseries = [];
    valid.by_provider = [];
    valid.by_model = [];
    const result = parseTokenUsageResponse(valid);
    expect(result.timeseries).toEqual([]);
    expect(result.by_provider).toEqual([]);
    expect(result.by_model).toEqual([]);
  });

  it("rejects non-object responses", () => {
    expect(() => parseTokenUsageResponse(null)).toThrow(
      "token usage response must be an object",
    );
    expect(() => parseTokenUsageResponse("not an object")).toThrow(
      "token usage response must be an object",
    );
    expect(() => parseTokenUsageResponse([])).toThrow(
      "token usage response must be an object",
    );
  });

  it("rejects missing top-level required fields", () => {
    const valid = createValidTokenUsageResponse();
    const missingSummary = { ...valid };
    delete (missingSummary as unknown as Record<string, unknown>).summary;
    expect(() => parseTokenUsageResponse(missingSummary)).toThrow(
      "token usage response.summary is required",
    );

    const missingTimeseries = { ...valid };
    delete (missingTimeseries as unknown as Record<string, unknown>).timeseries;
    expect(() => parseTokenUsageResponse(missingTimeseries)).toThrow(
      "token usage response.timeseries is required",
    );

    const missingCoverage = { ...valid };
    delete (missingCoverage as unknown as Record<string, unknown>).coverage;
    expect(() => parseTokenUsageResponse(missingCoverage)).toThrow(
      "token usage response.coverage is required",
    );

    const missingQuality = { ...valid };
    delete (missingQuality as unknown as Record<string, unknown>).data_quality;
    expect(() => parseTokenUsageResponse(missingQuality)).toThrow(
      "token usage response.data_quality is required",
    );
  });

  it("rejects non-decimal token strings in breakdowns", () => {
    const invalidTokenValues = [
      "-10",
      "12.5",
      "abc",
      "100a",
      "",
      " ",
      null,
      100,
    ];
    for (const val of invalidTokenValues) {
      const valid = createValidTokenUsageResponse();
      (valid.summary as unknown as Record<string, unknown>).total_tokens = val;
      expect(() => parseTokenUsageResponse(valid)).toThrow(
        "token usage response.summary.total_tokens must be a non-negative decimal string",
      );
    }
  });

  it("rejects non-number metrics and rates", () => {
    const valid = createValidTokenUsageResponse();
    (valid.summary as unknown as Record<string, unknown>).cache_hit_rate =
      "0.5";
    expect(() => parseTokenUsageResponse(valid)).toThrow(
      "token usage response.summary.cache_hit_rate must be a ratio between 0 and 1",
    );

    const valid2 = createValidTokenUsageResponse();
    (valid2.coverage as unknown as Record<string, unknown>).rate = NaN;
    expect(() => parseTokenUsageResponse(valid2)).toThrow(
      "token usage response.coverage.rate must be a ratio between 0 and 1",
    );

    const valid3 = createValidTokenUsageResponse();
    (valid3.data_quality as unknown as Record<string, unknown>).quality_rate =
      "invalid";
    expect(() => parseTokenUsageResponse(valid3)).toThrow(
      "token usage response.data_quality.quality_rate must be a ratio between 0 and 1",
    );
  });

  it("rejects malformed timeseries bucket entries", () => {
    const valid = createValidTokenUsageResponse();
    (valid.timeseries[0] as unknown as Record<string, unknown>).total_requests =
      "invalid";
    expect(() => parseTokenUsageResponse(valid)).toThrow(
      "token usage response.timeseries[0].total_requests must be a finite non-negative integer",
    );

    const valid2 = createValidTokenUsageResponse();
    (
      valid2.timeseries[0] as unknown as Record<string, unknown>
    ).fresh_input_tokens = -5;
    expect(() => parseTokenUsageResponse(valid2)).toThrow(
      "token usage response.timeseries[0].fresh_input_tokens must be a non-negative decimal string",
    );
  });

  it("rejects malformed provider rank entries", () => {
    const valid = createValidTokenUsageResponse();
    (valid.by_provider[0] as unknown as Record<string, unknown>).provider_id =
      123;
    expect(() => parseTokenUsageResponse(valid)).toThrow(
      "token usage response.by_provider[0].provider_id must be a string",
    );

    const valid2 = createValidTokenUsageResponse();
    (valid2.by_provider[0] as unknown as Record<string, unknown>).share = "0.5";
    expect(() => parseTokenUsageResponse(valid2)).toThrow(
      "token usage response.by_provider[0].share must be a ratio between 0 and 1",
    );
  });

  it("rejects malformed model rank entries", () => {
    const valid = createValidTokenUsageResponse();
    (valid.by_model[0] as unknown as Record<string, unknown>).model = null;
    expect(() => parseTokenUsageResponse(valid)).toThrow(
      "token usage response.by_model[0].model must be a string",
    );
  });

  it.each([
    {
      label: "summary total",
      mutate: (response: TokenUsageResponse) => {
        response.summary.total_tokens = "12451201";
      },
      message:
        "token usage response.summary must conserve total_tokens as input_tokens + output_tokens",
    },
    {
      label: "bucket input",
      mutate: (response: TokenUsageResponse) => {
        response.timeseries[0].fresh_input_tokens = "400001";
      },
      message:
        "token usage response.timeseries[0] must conserve input_tokens across input segments",
    },
    {
      label: "provider output",
      mutate: (response: TokenUsageResponse) => {
        response.by_provider[0].reasoning_tokens = "1";
      },
      message:
        "token usage response.by_provider[0] must conserve output_tokens across output segments",
    },
    {
      label: "model output",
      mutate: (response: TokenUsageResponse) => {
        response.by_model[0].standard_output_tokens = "2119999";
      },
      message:
        "token usage response.by_model[0] must conserve output_tokens across output segments",
    },
  ])("rejects non-conserving $label breakdowns", ({ mutate, message }) => {
    const response = createValidTokenUsageResponse();
    mutate(response);

    expect(() => parseTokenUsageResponse(response)).toThrow(message);
  });

  it("rejects negative, fractional, infinite, and unsafe request counts", () => {
    for (const invalidCount of [
      -1,
      1.5,
      Infinity,
      Number.MAX_SAFE_INTEGER + 1,
    ]) {
      const response = createValidTokenUsageResponse();
      (
        response.timeseries[0] as unknown as Record<string, unknown>
      ).observed_requests = invalidCount;

      expect(() => parseTokenUsageResponse(response)).toThrow(
        "token usage response.timeseries[0].observed_requests must be a finite non-negative integer",
      );
    }
  });

  it("rejects ratios outside the closed unit interval", () => {
    for (const invalidRatio of [-0.01, 1.01, Infinity]) {
      const response = createValidTokenUsageResponse();
      (response.summary as unknown as Record<string, unknown>).reasoning_ratio =
        invalidRatio;

      expect(() => parseTokenUsageResponse(response)).toThrow(
        "token usage response.summary.reasoning_ratio must be a ratio between 0 and 1",
      );
    }
  });

  it("rejects malformed, unsupported, and reversed report ranges", () => {
    const malformed = createValidTokenUsageResponse();
    malformed.time_range.start = "2026-02-31T16:00:00Z";
    expect(() => parseTokenUsageResponse(malformed)).toThrow(
      "token usage response.time_range.start must be an RFC3339 timestamp",
    );

    const unsupported = createValidTokenUsageResponse();
    (unsupported.time_range as unknown as Record<string, unknown>).granularity =
      "30m";
    expect(() => parseTokenUsageResponse(unsupported)).toThrow(
      "token usage response.time_range.granularity is not supported",
    );

    const reversed = createValidTokenUsageResponse();
    reversed.time_range.start = "2026-08-22T16:00:00Z";
    expect(() => parseTokenUsageResponse(reversed)).toThrow(
      "token usage response.time_range must not end before it starts",
    );
  });

  it("rejects reversed, out-of-range, and overlapping buckets", () => {
    const reversed = createValidTokenUsageResponse();
    reversed.timeseries[0].end = reversed.timeseries[0].start;
    expect(() => parseTokenUsageResponse(reversed)).toThrow(
      "token usage response.timeseries[0] must have start before end",
    );

    const outside = createValidTokenUsageResponse();
    outside.timeseries[0].start = "2026-08-20T15:00:00Z";
    outside.timeseries[0].end = "2026-08-20T16:00:00Z";
    expect(() => parseTokenUsageResponse(outside)).toThrow(
      "token usage response.timeseries[0] must be within time_range",
    );

    const overlapping = createValidTokenUsageResponse();
    overlapping.timeseries.push({
      ...overlapping.timeseries[0],
      start: "2026-08-21T00:30:00Z",
      end: "2026-08-21T01:30:00Z",
    });
    expect(() => parseTokenUsageResponse(overlapping)).toThrow(
      "token usage response.timeseries[1] must be ordered and non-overlapping",
    );
  });

  it("rejects impossible coverage count relationships", () => {
    const observed = createValidTokenUsageResponse();
    observed.coverage.observed_requests = 1457;
    expect(() => parseTokenUsageResponse(observed)).toThrow(
      "token usage response.coverage.observed_requests cannot exceed total_requests",
    );

    const comparable = createValidTokenUsageResponse();
    comparable.coverage.comparable_requests = 1431;
    expect(() => parseTokenUsageResponse(comparable)).toThrow(
      "token usage response.coverage.comparable_requests cannot exceed observed_requests",
    );

    const withoutUsage = createValidTokenUsageResponse();
    withoutUsage.coverage.without_usage_requests = 25;
    expect(() => parseTokenUsageResponse(withoutUsage)).toThrow(
      "token usage response.coverage.without_usage_requests must equal total_requests - observed_requests",
    );
  });

  it("rejects ratios that contradict their source counts", () => {
    const summary = createValidTokenUsageResponse();
    summary.summary.cache_hit_rate = 0;
    expect(() => parseTokenUsageResponse(summary)).toThrow(
      "token usage response.summary.cache_hit_rate is inconsistent with its counts",
    );

    const coverage = createValidTokenUsageResponse();
    coverage.coverage.rate = 0;
    expect(() => parseTokenUsageResponse(coverage)).toThrow(
      "token usage response.coverage.rate is inconsistent with its counts",
    );

    const quality = createValidTokenUsageResponse();
    quality.data_quality.quality_rate = 0;
    expect(() => parseTokenUsageResponse(quality)).toThrow(
      "token usage response.data_quality.quality_rate is inconsistent with its counts",
    );

    const rank = createValidTokenUsageResponse();
    rank.by_provider[0].share = 0;
    expect(() => parseTokenUsageResponse(rank)).toThrow(
      "token usage response.by_provider[0].share is inconsistent with its counts",
    );
  });

  it("rejects inconsistent observed-data quality partitions", () => {
    const response = createValidTokenUsageResponse();
    response.data_quality.partial_requests = 1;

    expect(() => parseTokenUsageResponse(response)).toThrow(
      "token usage response observed request quality partition is inconsistent",
    );
  });
});
