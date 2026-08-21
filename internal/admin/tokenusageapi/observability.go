package tokenusageapi

import (
	"context"
	"errors"
	"time"

	"github.com/doraemonkeys/switch-a/internal/analyticswindow"
	"github.com/doraemonkeys/switch-a/internal/tokenanalytics"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

const (
	operationName = "token_usage_query"

	queryStartedMessage   = "token usage query started"
	queryCompletedMessage = "token usage query completed"
	queryFailedMessage    = "token usage query failed"

	failureReasonQueryFailed      = "query_failed"
	failureReasonValidation       = "validation"
	failureReasonContextCanceled  = "context_canceled"
	failureReasonDeadlineExceeded = "context_deadline_exceeded"
)

type uuidOperationIDGenerator struct{}

func (uuidOperationIDGenerator) NewOperationID() string {
	return uuid.NewString()
}

func (h *Handler) logStarted(operationID string, query tokenanalytics.Query) {
	h.logger.Info(queryStartedMessage, lifecycleFields(operationID, query, query.Window)...)
}

func (h *Handler) logCompleted(operationID string, query tokenanalytics.Query, report tokenanalytics.Report, duration time.Duration) {
	window := query.Window
	window.Start = report.TimeRange.Start
	window.End = report.TimeRange.End
	window.StartResolution = analyticswindow.StartResolved
	fields := lifecycleFields(operationID, query, window)
	fields = append(fields,
		zap.Int("bucket_count", len(report.TimeSeries)),
		zap.Int("provider_rank_count", len(report.ByProvider)),
		zap.Int("model_rank_count", len(report.ByModel)),
		zap.Int64("total_requests", report.Coverage.TotalRequests),
		zap.Int64("observed_requests", report.Coverage.ObservedRequests),
		zap.Int64("comparable_requests", report.Coverage.ComparableRequests),
		zap.Int64("non_comparable_requests", report.Coverage.TotalRequests-report.Coverage.ComparableRequests),
		zap.Int64("partial_requests", report.DataQuality.PartialRequests),
		zap.Int64("invalid_requests", report.DataQuality.InvalidRequests),
		zap.Int64("unknown_semantics_requests", report.DataQuality.UnknownSemanticsRequests),
		zap.Duration("duration", duration),
	)
	h.logger.Info(queryCompletedMessage, fields...)
}

func (h *Handler) logFailed(operationID string, query tokenanalytics.Query, err error, duration time.Duration) {
	stage := failureStage(err)
	window := query.Window
	if resolved, ok := tokenanalytics.ResolvedFailureWindow(err); ok {
		window = resolved
	}
	fields := lifecycleFields(operationID, query, window)
	fields = append(fields,
		zap.String("failure_stage", string(stage)),
		zap.String("failure_reason", failureReason(err)),
		zap.Duration("duration", duration),
		// Retain the stage while deliberately dropping the raw cause: the domain's
		// stable Error text is sufficient for correlation and cannot leak SQL.
		zap.Error(tokenanalytics.NewFailure(stage, nil)),
	)
	h.logger.Error(queryFailedMessage, fields...)
}

func lifecycleFields(operationID string, query tokenanalytics.Query, window analyticswindow.Window) []zap.Field {
	fields := []zap.Field{
		zap.String("operation", operationName),
		zap.String("operation_id", operationID),
		zap.String("period", query.Window.Period),
		zap.String("window_start_state", window.StartResolution.String()),
		zap.Time("window_end", window.End.UTC()),
		zap.String("granularity", query.Window.GranularityName),
	}
	if window.HasResolvedStart() {
		fields = append(fields, zap.Time("window_start", window.Start.UTC()))
	}
	if query.ProviderID != nil {
		fields = append(fields, zap.String("provider_id", *query.ProviderID))
	}
	if query.Model != nil {
		fields = append(fields, zap.String("model", *query.Model))
	}
	if query.APIType != nil {
		fields = append(fields, zap.String("api_type", *query.APIType))
	}
	return fields
}

func failureStage(err error) tokenanalytics.FailureStage {
	var failure *tokenanalytics.Failure
	if errors.As(err, &failure) {
		return failure.Stage
	}
	return tokenanalytics.FailureStageResponseMap
}

func failureReason(err error) string {
	if isValidationError(err) {
		return failureReasonValidation
	}
	if errors.Is(err, context.Canceled) {
		return failureReasonContextCanceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return failureReasonDeadlineExceeded
	}
	return failureReasonQueryFailed
}
