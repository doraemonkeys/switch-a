// Package tokenusageapi provides the focused administrative HTTP adapter for
// token analytics without widening the root admin storage interface.
package tokenusageapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/doraemonkeys/switch-a/internal/analyticswindow"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/tokenanalytics"
	"go.uber.org/zap"
)

const (
	MaxProviderIDRunes = 200
	MaxModelRunes      = 512
	MaxAPITypeRunes    = 200

	contentTypeJSON = "application/json"

	validationErrorCode = "VALIDATION_ERROR"
	internalErrorCode   = "INTERNAL_ERROR"

	validationErrorMessage = "Invalid token usage query"
	internalErrorMessage   = "Failed to get token usage"
)

type Analyzer interface {
	Analyze(context.Context, tokenanalytics.Query) (tokenanalytics.Report, error)
}

type Clock interface {
	Now() time.Time
}

type OperationIDGenerator interface {
	NewOperationID() string
}

type Config struct {
	Analyzer       Analyzer
	WindowResolver *analyticswindow.Resolver
	Clock          Clock
	OperationIDs   OperationIDGenerator
	Logger         *zap.Logger
}

type Handler struct {
	analyzer       Analyzer
	windowResolver *analyticswindow.Resolver
	clock          Clock
	operationIDs   OperationIDGenerator
	logger         *zap.Logger
}

func NewHandler(config Config) (*Handler, error) {
	if config.Analyzer == nil {
		return nil, fmt.Errorf("token usage analyzer is required")
	}
	if config.WindowResolver == nil {
		return nil, fmt.Errorf("token usage window resolver is required")
	}
	if config.Clock == nil {
		return nil, fmt.Errorf("token usage clock is required")
	}
	if config.OperationIDs == nil {
		config.OperationIDs = uuidOperationIDGenerator{}
	}
	if config.Logger == nil {
		config.Logger = zap.NewNop()
	}
	return &Handler{
		analyzer:       config.Analyzer,
		windowResolver: config.WindowResolver,
		clock:          config.Clock,
		operationIDs:   config.OperationIDs,
		logger:         config.Logger,
	}, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.GetTokenUsage(w, r)
}

// GetTokenUsage parses only transport concerns; all arithmetic and canonical
// token decisions remain inside tokenanalytics.Service.
func (h *Handler) GetTokenUsage(w http.ResponseWriter, r *http.Request) {
	query, err := h.parseQuery(r.URL.Query())
	if err != nil {
		writeValidationError(w, err)
		return
	}

	operationID := h.operationIDs.NewOperationID()
	startedAt := h.clock.Now()
	h.logStarted(operationID, query)

	report, err := h.analyzer.Analyze(r.Context(), query)
	if err != nil {
		duration := h.clock.Now().Sub(startedAt)
		h.logFailed(operationID, query, err, duration)
		if isValidationError(err) {
			writeValidationError(w, err)
			return
		}
		writeError(w, http.StatusInternalServerError, internalErrorCode, internalErrorMessage, nil)
		return
	}

	response := mapResponse(report, query.Window.GranularityName)
	duration := h.clock.Now().Sub(startedAt)
	h.logCompleted(operationID, query, report, duration)
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) parseQuery(values url.Values) (tokenanalytics.Query, error) {
	window, err := h.windowResolver.ResolveTokenUsage(values)
	if err != nil {
		return tokenanalytics.Query{}, err
	}
	providerID, err := optionalFilter(values, "provider_id", MaxProviderIDRunes)
	if err != nil {
		return tokenanalytics.Query{}, err
	}
	modelName, err := optionalFilter(values, "model", MaxModelRunes)
	if err != nil {
		return tokenanalytics.Query{}, err
	}
	apiType, err := optionalFilter(values, "api_type", MaxAPITypeRunes)
	if err != nil {
		return tokenanalytics.Query{}, err
	}
	return tokenanalytics.Query{Window: window, ProviderID: providerID, Model: modelName, APIType: apiType}, nil
}

type validationError struct {
	field  string
	reason string
}

func (e *validationError) Error() string {
	return "invalid token usage query"
}

func optionalFilter(values url.Values, field string, maxRunes int) (*string, error) {
	raw, present := values[field]
	if !present {
		return nil, nil
	}
	if len(raw) != 1 {
		return nil, &validationError{field: field, reason: "duplicate"}
	}
	value := raw[0]
	if strings.TrimSpace(value) == "" {
		return nil, &validationError{field: field, reason: "blank"}
	}
	if utf8.RuneCountInString(value) > maxRunes {
		return nil, &validationError{field: field, reason: "too_long"}
	}
	return &value, nil
}

func mapResponse(report tokenanalytics.Report, granularity string) ResponseDTO {
	timeSeries := make([]BucketDTO, len(report.TimeSeries))
	for index, bucket := range report.TimeSeries {
		timeSeries[index] = BucketDTO{
			Start:              formatTime(bucket.Start),
			End:                formatTime(bucket.End),
			BreakdownDTO:       mapBreakdown(bucket.Breakdown),
			TotalRequests:      bucket.TotalRequests,
			ObservedRequests:   bucket.ObservedRequests,
			ComparableRequests: bucket.ComparableRequests,
		}
	}
	providers := make([]ProviderRankDTO, len(report.ByProvider))
	for index, rank := range report.ByProvider {
		providers[index] = ProviderRankDTO{
			ProviderID:   rank.ProviderID,
			ProviderName: rank.ProviderLabel,
			BreakdownDTO: mapBreakdown(rank.Breakdown),
			RequestCount: rank.ComparableRequests,
			Share:        rank.Share,
		}
	}
	models := make([]ModelRankDTO, len(report.ByModel))
	for index, rank := range report.ByModel {
		models[index] = ModelRankDTO{
			Model:        rank.Model,
			BreakdownDTO: mapBreakdown(rank.Breakdown),
			RequestCount: rank.ComparableRequests,
			Share:        rank.Share,
		}
	}

	return ResponseDTO{
		Summary: SummaryDTO{
			BreakdownDTO:   mapBreakdown(report.Summary.Breakdown),
			CacheHitRate:   report.Summary.CacheHitRate,
			ReasoningRatio: report.Summary.ReasoningRatio,
		},
		TimeSeries: timeSeries,
		ByProvider: providers,
		ByModel:    models,
		TimeRange: TimeRangeDTO{
			Start:       formatTime(report.TimeRange.Start),
			End:         formatTime(report.TimeRange.End),
			Granularity: granularity,
		},
		Coverage: CoverageDTO{
			TotalRequests:        report.Coverage.TotalRequests,
			ObservedRequests:     report.Coverage.ObservedRequests,
			ComparableRequests:   report.Coverage.ComparableRequests,
			WithoutUsageRequests: report.Coverage.WithoutUsageRequests,
			Rate:                 report.Coverage.Rate,
		},
		DataQuality: DataQualityDTO{
			QualityRate:              report.DataQuality.QualityRate,
			PartialRequests:          report.DataQuality.PartialRequests,
			InvalidRequests:          report.DataQuality.InvalidRequests,
			UnknownSemanticsRequests: report.DataQuality.UnknownSemanticsRequests,
		},
	}
}

func mapBreakdown(breakdown tokenanalytics.Breakdown) BreakdownDTO {
	return BreakdownDTO{
		TotalTokens:              strconv.FormatInt(breakdown.TotalTokens, 10),
		InputTokens:              strconv.FormatInt(breakdown.InputTokens, 10),
		OutputTokens:             strconv.FormatInt(breakdown.OutputTokens, 10),
		FreshInputTokens:         strconv.FormatInt(breakdown.FreshInputTokens, 10),
		CacheReadInputTokens:     strconv.FormatInt(breakdown.CacheReadInputTokens, 10),
		CacheCreationInputTokens: strconv.FormatInt(breakdown.CacheCreationInputTokens, 10),
		UnclassifiedInputTokens:  strconv.FormatInt(breakdown.UnclassifiedInputTokens, 10),
		StandardOutputTokens:     strconv.FormatInt(breakdown.StandardOutputTokens, 10),
		ReasoningTokens:          strconv.FormatInt(breakdown.ReasoningTokens, 10),
		UnclassifiedOutputTokens: strconv.FormatInt(breakdown.UnclassifiedOutputTokens, 10),
	}
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func isValidationError(err error) bool {
	var windowErr *analyticswindow.ValidationError
	var filterErr *validationError
	return errors.As(err, &windowErr) || errors.As(err, &filterErr)
}

func writeValidationError(w http.ResponseWriter, err error) {
	details := make(map[string]string, 1)
	var windowErr *analyticswindow.ValidationError
	var filterErr *validationError
	if errors.As(err, &windowErr) {
		details[windowErr.Field] = windowErr.Reason
	} else if errors.As(err, &filterErr) {
		details[filterErr.field] = filterErr.reason
	}
	writeError(w, http.StatusBadRequest, validationErrorCode, validationErrorMessage, details)
}

func writeError(w http.ResponseWriter, status int, code, message string, details map[string]string) {
	writeJSON(w, status, model.ErrorResponse{Code: code, Message: message, Details: details})
}

func writeJSON(w http.ResponseWriter, status int, response any) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(response)
}
