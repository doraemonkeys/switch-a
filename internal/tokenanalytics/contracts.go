// Package tokenanalytics defines the wire-independent token reporting domain.
package tokenanalytics

import (
	"time"

	"github.com/doraemonkeys/switch-a/internal/analyticswindow"
)

const (
	// TopRankLimit bounds ranking work inside the shared read snapshot.
	TopRankLimit = 10
)

// Query carries an already validated window and exact optional filters.
// Pointers distinguish an absent filter from a present persisted empty value.
type Query struct {
	Window     analyticswindow.Window
	ProviderID *string
	Model      *string
	APIType    *string
}

// Breakdown keeps every token quantity exact until the endpoint serializes it.
// The seven classified segments must sum to TotalTokens, which must also equal
// InputTokens plus OutputTokens.
type Breakdown struct {
	TotalTokens              int64
	InputTokens              int64
	OutputTokens             int64
	FreshInputTokens         int64
	CacheReadInputTokens     int64
	CacheCreationInputTokens int64
	UnclassifiedInputTokens  int64
	StandardOutputTokens     int64
	ReasoningTokens          int64
	UnclassifiedOutputTokens int64
}

// Aggregate adds ratios derived from one conserving breakdown. Zero
// denominators map to numeric zero in the service rather than NaN.
type Aggregate struct {
	Breakdown
	CacheHitRate   float64
	ReasoningRatio float64
}

// Coverage distinguishes traffic in the query window from traffic whose token
// semantics can participate in canonical aggregates.
type Coverage struct {
	TotalRequests        int64
	ObservedRequests     int64
	ComparableRequests   int64
	WithoutUsageRequests int64
	Rate                 float64
}

// DataQuality classifies only requests with at least one observed core token
// field. Its three non-comparable classes are mutually exclusive.
type DataQuality struct {
	QualityRate              float64
	PartialRequests          int64
	InvalidRequests          int64
	UnknownSemanticsRequests int64
}

// Bucket is one clipped interval in the time series. Traffic counts retain the
// distinction between all, observed, and canonically comparable requests.
type Bucket struct {
	Start time.Time
	End   time.Time
	Breakdown
	TotalRequests      int64
	ObservedRequests   int64
	ComparableRequests int64
}

// ProviderRank order is TotalTokens descending, ComparableRequests descending,
// then ProviderID ascending. Label already contains the deleted-provider
// fallback selected inside the repository snapshot, and Share is derived by the
// service from the report's canonical total.
type ProviderRank struct {
	ProviderID    string
	ProviderLabel string
	Breakdown
	ComparableRequests int64
	Share              float64
}

// ModelRank order is TotalTokens descending, ComparableRequests descending,
// then Model ascending. Empty persisted model values remain empty, and Share is
// derived by the service from the report's canonical total.
type ModelRank struct {
	Model string
	Breakdown
	ComparableRequests int64
	Share              float64
}

// TimeRange is the exact UTC half-open interval represented by the report.
type TimeRange struct {
	Start time.Time
	End   time.Time
}

// Report deliberately mirrors the endpoint's seven fixed sections without
// carrying JSON representation decisions into the domain.
type Report struct {
	Summary     Aggregate
	TimeSeries  []Bucket
	ByProvider  []ProviderRank
	ByModel     []ModelRank
	TimeRange   TimeRange
	Coverage    Coverage
	DataQuality DataQuality
}
