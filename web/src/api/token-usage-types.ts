import type { StatsGranularity, StatsPeriod } from "./types";

/**
 * Token quantities are transmitted as decimal strings across the wire so JavaScript
 * clients never lose precision for large cumulative sums, while counts and ratios
 * remain standard JSON numbers.
 */
export interface TokenBreakdownDTO {
  total_tokens: string;
  input_tokens: string;
  output_tokens: string;
  fresh_input_tokens: string;
  cache_read_input_tokens: string;
  cache_creation_input_tokens: string;
  unclassified_input_tokens: string;
  standard_output_tokens: string;
  reasoning_tokens: string;
  unclassified_output_tokens: string;
}

export interface TokenSummaryDTO extends TokenBreakdownDTO {
  cache_hit_rate: number;
  reasoning_ratio: number;
}

export interface TokenBucketDTO extends TokenBreakdownDTO {
  start: string;
  end: string;
  total_requests: number;
  observed_requests: number;
  comparable_requests: number;
}

export interface TokenProviderRankDTO extends TokenBreakdownDTO {
  provider_id: string;
  provider_name: string;
  request_count: number;
  share: number;
}

export interface TokenModelRankDTO extends TokenBreakdownDTO {
  model: string;
  request_count: number;
  share: number;
}

export interface TokenTimeRangeDTO {
  start: string;
  end: string;
  granularity: StatsGranularity;
}

export interface TokenCoverageDTO {
  total_requests: number;
  observed_requests: number;
  comparable_requests: number;
  without_usage_requests: number;
  rate: number;
}

export interface TokenDataQualityDTO {
  quality_rate: number;
  partial_requests: number;
  invalid_requests: number;
  unknown_semantics_requests: number;
}

export interface TokenUsageResponse {
  summary: TokenSummaryDTO;
  timeseries: TokenBucketDTO[];
  by_provider: TokenProviderRankDTO[];
  by_model: TokenModelRankDTO[];
  time_range: TokenTimeRangeDTO;
  coverage: TokenCoverageDTO;
  data_quality: TokenDataQualityDTO;
}

export interface TokenUsageParams {
  period?: StatsPeriod;
  granularity?: StatsGranularity;
  as_of?: string;
  provider_id?: string;
  model?: string;
  api_type?: string;
}
