package tokenusageapi

// BreakdownDTO keeps token quantities as decimal strings so JavaScript clients
// never lose precision while request counts and ratios remain JSON numbers.
type BreakdownDTO struct {
	TotalTokens              string `json:"total_tokens"`
	InputTokens              string `json:"input_tokens"`
	OutputTokens             string `json:"output_tokens"`
	FreshInputTokens         string `json:"fresh_input_tokens"`
	CacheReadInputTokens     string `json:"cache_read_input_tokens"`
	CacheCreationInputTokens string `json:"cache_creation_input_tokens"`
	UnclassifiedInputTokens  string `json:"unclassified_input_tokens"`
	StandardOutputTokens     string `json:"standard_output_tokens"`
	ReasoningTokens          string `json:"reasoning_tokens"`
	UnclassifiedOutputTokens string `json:"unclassified_output_tokens"`
}

type SummaryDTO struct {
	BreakdownDTO
	CacheHitRate   float64 `json:"cache_hit_rate"`
	ReasoningRatio float64 `json:"reasoning_ratio"`
}

type BucketDTO struct {
	Start string `json:"start"`
	End   string `json:"end"`
	BreakdownDTO
	TotalRequests      int64 `json:"total_requests"`
	ObservedRequests   int64 `json:"observed_requests"`
	ComparableRequests int64 `json:"comparable_requests"`
}

type ProviderRankDTO struct {
	ProviderID   string `json:"provider_id"`
	ProviderName string `json:"provider_name"`
	BreakdownDTO
	RequestCount int64   `json:"request_count"`
	Share        float64 `json:"share"`
}

type ModelRankDTO struct {
	Model string `json:"model"`
	BreakdownDTO
	RequestCount int64   `json:"request_count"`
	Share        float64 `json:"share"`
}

type TimeRangeDTO struct {
	Start       string `json:"start"`
	End         string `json:"end"`
	Granularity string `json:"granularity"`
}

type CoverageDTO struct {
	TotalRequests        int64   `json:"total_requests"`
	ObservedRequests     int64   `json:"observed_requests"`
	ComparableRequests   int64   `json:"comparable_requests"`
	WithoutUsageRequests int64   `json:"without_usage_requests"`
	Rate                 float64 `json:"rate"`
}

type DataQualityDTO struct {
	QualityRate              float64 `json:"quality_rate"`
	PartialRequests          int64   `json:"partial_requests"`
	InvalidRequests          int64   `json:"invalid_requests"`
	UnknownSemanticsRequests int64   `json:"unknown_semantics_requests"`
}

// ResponseDTO has exactly the seven stable top-level sections consumed by the
// admin analytics panel.
type ResponseDTO struct {
	Summary     SummaryDTO        `json:"summary"`
	TimeSeries  []BucketDTO       `json:"timeseries"`
	ByProvider  []ProviderRankDTO `json:"by_provider"`
	ByModel     []ModelRankDTO    `json:"by_model"`
	TimeRange   TimeRangeDTO      `json:"time_range"`
	Coverage    CoverageDTO       `json:"coverage"`
	DataQuality DataQualityDTO    `json:"data_quality"`
}
