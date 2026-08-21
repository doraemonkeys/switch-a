package tokenanalytics

import (
	"context"
	"errors"
	"time"

	"github.com/doraemonkeys/switch-a/internal/analyticswindow"
)

// FailureStage identifies the single boundary that failed without exposing its
// storage cause to HTTP clients or logs.
type FailureStage string

const (
	FailureStageSnapshotOpen FailureStage = "snapshot_open"
	FailureStageSummary      FailureStage = "summary"
	FailureStageTimeSeries   FailureStage = "timeseries"
	FailureStageProviderRank FailureStage = "provider_rank"
	FailureStageModelRank    FailureStage = "model_rank"
	FailureStageResponseMap  FailureStage = "response_map"
)

const failureMessage = "token analytics request failed"

// Failure carries a stable public error while retaining its cause and optional
// resolved decision range for internal control flow and lifecycle diagnostics.
type Failure struct {
	Stage     FailureStage
	cause     error
	window    analyticswindow.Window
	hasWindow bool
}

// NewFailure creates one stage-qualified failure. Services should wrap a cause
// only at the boundary where it first enters the token analytics domain.
func NewFailure(stage FailureStage, cause error) *Failure {
	return &Failure{Stage: stage, cause: cause}
}

// NewFailureForWindow retains the exact resolved decision range after the
// service has discovered an all-period lower bound. The public error remains
// stable while adapters can reconstruct truthful lifecycle context.
func NewFailureForWindow(stage FailureStage, cause error, window analyticswindow.Window) *Failure {
	return &Failure{Stage: stage, cause: cause, window: window, hasWindow: window.HasResolvedStart()}
}

func (e *Failure) Error() string {
	return failureMessage
}

func (e *Failure) Unwrap() error {
	return e.cause
}

// IsFailureAt reports a stage without requiring callers to inspect safe error
// text or unwrap the storage cause.
func IsFailureAt(err error, stage FailureStage) bool {
	var failure *Failure
	return errors.As(err, &failure) && failure.Stage == stage
}

// ResolvedFailureWindow returns decision context only when the service reached a
// concrete lower bound. Unresolved failures deliberately have no timestamp.
func ResolvedFailureWindow(err error) (analyticswindow.Window, bool) {
	var failure *Failure
	if !errors.As(err, &failure) || !failure.hasWindow {
		return analyticswindow.Window{}, false
	}
	return failure.window, true
}

// SummaryRecord is the first snapshot result. EarliestMatchingTime belongs to
// the same filters as every count and completes an unresolved all-period query.
type SummaryRecord struct {
	Breakdown
	TotalRequests            int64
	ObservedRequests         int64
	ComparableRequests       int64
	PartialRequests          int64
	InvalidRequests          int64
	UnknownSemanticsRequests int64
	EarliestMatchingTime     *time.Time
}

// BucketRecord uses the UTC epoch-aligned key returned by SQLite. The service
// owns clipping and insertion of missing intervals while SQLite supplies the
// traffic classes needed by the wire contract.
type BucketRecord struct {
	AlignedStart time.Time
	Breakdown
	TotalRequests      int64
	ObservedRequests   int64
	ComparableRequests int64
}

// ProviderRankRecord resolves the label in the same snapshot as its aggregate.
type ProviderRankRecord struct {
	ProviderID    string
	ProviderLabel string
	Breakdown
	ComparableRequests int64
}

// ModelRankRecord preserves an empty model because it is a durable value, not
// an absent query filter.
type ModelRankRecord struct {
	Model string
	Breakdown
	ComparableRequests int64
}

// SnapshotReader opens the read boundary consumed by Service.
type SnapshotReader interface {
	OpenSnapshot(context.Context) (Snapshot, error)
}

// Snapshot provides the fixed, sequential aggregate query set for one SQLite
// read transaction. Close must release rows, transaction, and connection.
type Snapshot interface {
	ReadSummary(context.Context, Query) (SummaryRecord, error)
	ReadBuckets(context.Context, Query) ([]BucketRecord, error)
	ReadProviderRanks(context.Context, Query, int) ([]ProviderRankRecord, error)
	ReadModelRanks(context.Context, Query, int) ([]ModelRankRecord, error)
	Close() error
}
