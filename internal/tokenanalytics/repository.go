package tokenanalytics

import (
	"context"
	"errors"
	"time"

	"github.com/doraemonkeys/switch-a/internal/analyticswindow"
)

// FailureStage identifies the boundary that failed without exposing its
// underlying storage or validation error to HTTP clients.
type FailureStage string

const (
	FailureStageSnapshotOpen FailureStage = "snapshot_open"
	FailureStageSummary      FailureStage = "summary"
	FailureStageTimeSeries   FailureStage = "timeseries"
	FailureStageProviderRank FailureStage = "provider_rank"
	FailureStageModelRank    FailureStage = "model_rank"
	FailureStageResponseMap  FailureStage = "response_map"
)

// FailureCode is a bounded, non-sensitive explanation suitable for structured
// diagnostics. The underlying error remains available through Unwrap only.
type FailureCode string

const (
	FailureCodeRepository              FailureCode = "repository_error"
	FailureCodeSnapshotUnavailable     FailureCode = "snapshot_unavailable"
	FailureCodeSummaryValidation       FailureCode = "summary_validation_error"
	FailureCodeWindowResolution        FailureCode = "window_resolution_error"
	FailureCodeBucketSetValidation     FailureCode = "bucket_set_validation_error"
	FailureCodeBucketKeyRejected       FailureCode = "bucket_key_rejected"
	FailureCodeProviderRankValidation  FailureCode = "provider_rank_validation_error"
	FailureCodeModelRankValidation     FailureCode = "model_rank_validation_error"
	FailureCodeSnapshotClose           FailureCode = "snapshot_close_error"
	FailureCodeUnexpectedAnalyzerError FailureCode = "unexpected_analyzer_error"
)

const failureMessage = "token analytics request failed"

// Failure carries a stable public error while retaining safe diagnostic context,
// its underlying cause, and an optional resolved decision range.
type Failure struct {
	Stage     FailureStage
	code      FailureCode
	cause     error
	window    analyticswindow.Window
	hasWindow bool
}

// NewFailure creates one stage-qualified failure. Services should wrap an
// underlying error only where it first enters the token analytics domain.
func NewFailure(stage FailureStage, code FailureCode, cause error) *Failure {
	return &Failure{Stage: stage, code: normalizeFailureCode(code), cause: cause}
}

// NewFailureForWindow retains the exact resolved decision range after the
// service has discovered an all-period lower bound. The public error remains
// stable while adapters can reconstruct truthful lifecycle context.
func NewFailureForWindow(stage FailureStage, code FailureCode, cause error, window analyticswindow.Window) *Failure {
	return &Failure{
		Stage:     stage,
		code:      normalizeFailureCode(code),
		cause:     cause,
		window:    window,
		hasWindow: window.HasResolvedStart(),
	}
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

// FailureCodeOf returns a bounded diagnostic for every analyzer failure. Errors
// outside this domain deliberately collapse to one safe adapter-facing code.
func FailureCodeOf(err error) FailureCode {
	var failure *Failure
	if !errors.As(err, &failure) {
		return FailureCodeUnexpectedAnalyzerError
	}
	return normalizeFailureCode(failure.code)
}

func normalizeFailureCode(code FailureCode) FailureCode {
	switch code {
	case FailureCodeRepository,
		FailureCodeSnapshotUnavailable,
		FailureCodeSummaryValidation,
		FailureCodeWindowResolution,
		FailureCodeBucketSetValidation,
		FailureCodeBucketKeyRejected,
		FailureCodeProviderRankValidation,
		FailureCodeModelRankValidation,
		FailureCodeSnapshotClose,
		FailureCodeUnexpectedAnalyzerError:
		return code
	default:
		return FailureCodeUnexpectedAnalyzerError
	}
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
