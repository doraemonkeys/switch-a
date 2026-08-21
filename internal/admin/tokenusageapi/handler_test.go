package tokenusageapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/analyticswindow"
	"github.com/doraemonkeys/switch-a/internal/tokenanalytics"
	"go.uber.org/zap"
)

type analyzerStub struct {
	report tokenanalytics.Report
	err    error
	query  tokenanalytics.Query
	ctx    context.Context
	calls  int
	fn     func(context.Context, tokenanalytics.Query) (tokenanalytics.Report, error)
}

func (s *analyzerStub) Analyze(ctx context.Context, query tokenanalytics.Query) (tokenanalytics.Report, error) {
	s.calls++
	s.ctx = ctx
	s.query = query
	if s.fn != nil {
		return s.fn(ctx, query)
	}
	return s.report, s.err
}

type fixedClock struct {
	now time.Time
}

func (c fixedClock) Now() time.Time {
	return c.now
}

type operationIDStub struct {
	id    string
	calls int
}

func (s *operationIDStub) NewOperationID() string {
	s.calls++
	return s.id
}

func newTestHandler(t *testing.T, analyzer Analyzer, now time.Time) *Handler {
	t.Helper()
	clock := fixedClock{now: now}
	resolver := analyticswindow.NewResolver(clock)
	handler, err := NewHandler(Config{
		Analyzer:       analyzer,
		WindowResolver: &resolver,
		Clock:          clock,
		OperationIDs:   &operationIDStub{id: "operation-test"},
		Logger:         zap.NewNop(),
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	return handler
}

func TestHandlerDefaultsAndForwardsResolvedQuery(t *testing.T) {
	now := time.Date(2026, time.August, 21, 4, 5, 6, 7, time.UTC)
	analyzer := &analyzerStub{report: emptyReport(now.Add(-24*time.Hour), now)}
	handler := newTestHandler(t, analyzer, now)

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/admin/api/token-usage", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", recorder.Code, recorder.Body.String())
	}
	query := analyzer.query
	if query.Window.Period != analyticswindow.Period24Hours || query.Window.GranularityName != analyticswindow.Granularity1Hour ||
		!query.Window.Start.Equal(now.Add(-24*time.Hour)) || !query.Window.End.Equal(now) {
		t.Fatalf("forwarded default window = %+v", query.Window)
	}
	if query.ProviderID != nil || query.Model != nil || query.APIType != nil {
		t.Fatalf("omitted filters were not preserved: %+v", query)
	}
}

func TestHandlerForwardsExactFiltersAndNormalizedAsOf(t *testing.T) {
	analyzer := &analyzerStub{}
	handler := newTestHandler(t, analyzer, time.Date(2030, time.January, 1, 0, 0, 0, 0, time.UTC))
	target := "/admin/api/token-usage?period=7d&granularity=6h&as_of=2026-08-21T08%3A30%3A45.123456789%2B08%3A00" +
		"&provider_id=provider-1&model=%20model%20&api_type=custom%3Adiagnostic&ignored=value"

	recorder := httptest.NewRecorder()
	handler.GetTokenUsage(recorder, httptest.NewRequest(http.MethodGet, target, nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", recorder.Code, recorder.Body.String())
	}
	wantEnd := time.Date(2026, time.August, 21, 0, 30, 45, 123456789, time.UTC)
	if !analyzer.query.Window.End.Equal(wantEnd) || !analyzer.query.Window.Start.Equal(wantEnd.Add(-7*24*time.Hour)) {
		t.Fatalf("normalized window = %+v", analyzer.query.Window)
	}
	if analyzer.query.ProviderID == nil || *analyzer.query.ProviderID != "provider-1" ||
		analyzer.query.Model == nil || *analyzer.query.Model != " model " ||
		analyzer.query.APIType == nil || *analyzer.query.APIType != "custom:diagnostic" {
		t.Fatalf("forwarded filters = %+v", analyzer.query)
	}
}

func TestHandlerRejectsEveryInvalidScalarBeforeStartingAnalysis(t *testing.T) {
	tooLongProvider := url.QueryEscape(strings.Repeat("界", MaxProviderIDRunes+1))
	tooLongModel := url.QueryEscape(strings.Repeat("m", MaxModelRunes+1))
	tooLongAPIType := url.QueryEscape(strings.Repeat("a", MaxAPITypeRunes+1))
	tests := []string{
		"period=", "period=invalid", "period=24h&period=7d",
		"granularity=", "granularity=10m", "period=7d&granularity=5m", "granularity=1h&granularity=6h",
		"as_of=", "as_of=invalid", "as_of=1500-01-01T00%3A00%3A00Z", "as_of=2300-01-01T00%3A00%3A00Z",
		"as_of=2026-08-21T00%3A00%3A00Z&as_of=2026-08-21T01%3A00%3A00Z",
		"provider_id=", "provider_id=p1&provider_id=p2", "provider_id=" + tooLongProvider,
		"model=", "model=m1&model=m2", "model=" + tooLongModel,
		"api_type=", "api_type=a1&api_type=a2", "api_type=" + tooLongAPIType,
	}
	for _, query := range tests {
		t.Run(query, func(t *testing.T) {
			analyzer := &analyzerStub{}
			ids := &operationIDStub{id: "must-not-be-generated"}
			clock := fixedClock{now: time.Date(2026, time.August, 21, 0, 0, 0, 0, time.UTC)}
			resolver := analyticswindow.NewResolver(clock)
			handler, err := NewHandler(Config{Analyzer: analyzer, WindowResolver: &resolver, Clock: clock, OperationIDs: ids})
			if err != nil {
				t.Fatalf("NewHandler() error = %v", err)
			}

			recorder := httptest.NewRecorder()
			handler.GetTokenUsage(recorder, httptest.NewRequest(http.MethodGet, "/admin/api/token-usage?"+query, nil))

			if recorder.Code != http.StatusBadRequest || analyzer.calls != 0 || ids.calls != 0 {
				t.Fatalf("status/analyzer/id calls = %d/%d/%d, want 400/0/0; body: %s", recorder.Code, analyzer.calls, ids.calls, recorder.Body.String())
			}
			assertErrorEnvelope(t, recorder, validationErrorCode, validationErrorMessage)
		})
	}
}

func TestHandlerReturnsStableOutOfRangeWindowError(t *testing.T) {
	analyzer := &analyzerStub{}
	handler := newTestHandler(t, analyzer, time.Date(2026, time.August, 21, 0, 0, 0, 0, time.UTC))
	recorder := httptest.NewRecorder()

	handler.GetTokenUsage(
		recorder,
		httptest.NewRequest(http.MethodGet, "/admin/api/token-usage?period=all&as_of=2300-01-01T00%3A00%3A00Z", nil),
	)

	var response struct {
		Code    string            `json:"code"`
		Message string            `json:"message"`
		Details map[string]string `json:"details"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if recorder.Code != http.StatusBadRequest || response.Code != validationErrorCode ||
		response.Message != validationErrorMessage || response.Details["as_of"] != "out_of_range" || analyzer.calls != 0 {
		t.Fatalf("status/response/analyzer calls = %d/%+v/%d", recorder.Code, response, analyzer.calls)
	}
}

func TestHandlerMapsDataDependentWindowValidationToBadRequest(t *testing.T) {
	cause := &analyticswindow.ValidationError{Field: "granularity", Reason: "too_many_buckets"}
	analyzer := &analyzerStub{err: tokenanalytics.NewFailure(tokenanalytics.FailureStageResponseMap, tokenanalytics.FailureCodeWindowResolution, cause)}
	handler := newTestHandler(t, analyzer, time.Date(2026, time.August, 21, 0, 0, 0, 0, time.UTC))

	recorder := httptest.NewRecorder()
	handler.GetTokenUsage(recorder, httptest.NewRequest(http.MethodGet, "/admin/api/token-usage?period=all", nil))

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", recorder.Code, recorder.Body.String())
	}
	assertErrorEnvelope(t, recorder, validationErrorCode, validationErrorMessage)
}

func TestHandlerPreservesContextCancellationAndSanitizesAnalyzerErrors(t *testing.T) {
	requestContext, cancel := context.WithCancel(context.Background())
	cancel()
	analyzer := &analyzerStub{fn: func(ctx context.Context, _ tokenanalytics.Query) (tokenanalytics.Report, error) {
		if !errors.Is(ctx.Err(), context.Canceled) {
			t.Fatalf("analyzer context error = %v, want canceled", ctx.Err())
		}
		return tokenanalytics.Report{}, errors.New("sensitive SQL and credential detail")
	}}
	handler := newTestHandler(t, analyzer, time.Date(2026, time.August, 21, 0, 0, 0, 0, time.UTC))
	request := httptest.NewRequest(http.MethodGet, "/admin/api/token-usage", nil).WithContext(requestContext)

	recorder := httptest.NewRecorder()
	handler.GetTokenUsage(recorder, request)

	if recorder.Code != http.StatusInternalServerError || strings.Contains(recorder.Body.String(), "sensitive") {
		t.Fatalf("status/body = %d/%s", recorder.Code, recorder.Body.String())
	}
	assertErrorEnvelope(t, recorder, internalErrorCode, internalErrorMessage)
}

func TestHandlerMapsDTOWithoutRecomputingDomainValues(t *testing.T) {
	const aboveJavaScriptSafeInteger int64 = 9007199254740993
	start := time.Date(2026, time.August, 20, 0, 0, 0, 123, time.FixedZone("offset", 8*60*60))
	end := start.Add(time.Hour)
	breakdown := tokenanalytics.Breakdown{
		TotalTokens: aboveJavaScriptSafeInteger, InputTokens: 11, OutputTokens: 22,
		FreshInputTokens: 1, CacheReadInputTokens: 2, CacheCreationInputTokens: 3,
		UnclassifiedInputTokens: 4, StandardOutputTokens: 5, ReasoningTokens: 6,
		UnclassifiedOutputTokens: 7,
	}
	report := tokenanalytics.Report{
		Summary:    tokenanalytics.Aggregate{Breakdown: breakdown, CacheHitRate: 0.123, ReasoningRatio: 0.456},
		TimeSeries: []tokenanalytics.Bucket{{Start: start, End: end, Breakdown: breakdown, TotalRequests: 9, ObservedRequests: 8, ComparableRequests: 7}},
		ByProvider: []tokenanalytics.ProviderRank{{ProviderID: "deleted", ProviderLabel: "deleted", Breakdown: breakdown, ComparableRequests: 6, Share: 0.321}},
		ByModel:    []tokenanalytics.ModelRank{{Model: "", Breakdown: breakdown, ComparableRequests: 5, Share: 0.654}},
		TimeRange:  tokenanalytics.TimeRange{Start: start, End: end},
		Coverage: tokenanalytics.Coverage{
			TotalRequests: 9, ObservedRequests: 8, ComparableRequests: 7, WithoutUsageRequests: 1, Rate: 0.777,
		},
		DataQuality: tokenanalytics.DataQuality{QualityRate: 0.875, PartialRequests: 1, InvalidRequests: 2, UnknownSemanticsRequests: 3},
	}
	analyzer := &analyzerStub{report: report}
	handler := newTestHandler(t, analyzer, end.UTC())

	recorder := httptest.NewRecorder()
	handler.GetTokenUsage(recorder, httptest.NewRequest(http.MethodGet, "/admin/api/token-usage", nil))

	if recorder.Code != http.StatusOK || recorder.Header().Get("Content-Type") != contentTypeJSON {
		t.Fatalf("status/content type = %d/%q", recorder.Code, recorder.Header().Get("Content-Type"))
	}
	decoder := json.NewDecoder(recorder.Body)
	decoder.UseNumber()
	var raw map[string]any
	if err := decoder.Decode(&raw); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	wantSections := []string{"summary", "timeseries", "by_provider", "by_model", "time_range", "coverage", "data_quality"}
	gotSections := make([]string, 0, len(raw))
	for key := range raw {
		gotSections = append(gotSections, key)
	}
	if len(gotSections) != len(wantSections) {
		t.Fatalf("top-level sections = %v, want exactly %v", gotSections, wantSections)
	}
	for _, key := range wantSections {
		if _, exists := raw[key]; !exists {
			t.Fatalf("missing top-level section %q", key)
		}
	}
	summary := raw["summary"].(map[string]any)
	if summary["total_tokens"] != "9007199254740993" || summary["input_tokens"] != "11" || summary["cache_hit_rate"] != json.Number("0.123") {
		t.Fatalf("summary mapping = %+v", summary)
	}
	bucket := raw["timeseries"].([]any)[0].(map[string]any)
	if bucket["total_requests"] != json.Number("9") || bucket["observed_requests"] != json.Number("8") || bucket["comparable_requests"] != json.Number("7") {
		t.Fatalf("bucket counts = %+v", bucket)
	}
	provider := raw["by_provider"].([]any)[0].(map[string]any)
	if provider["provider_name"] != "deleted" || provider["share"] != json.Number("0.321") || provider["request_count"] != json.Number("6") {
		t.Fatalf("provider mapping = %+v", provider)
	}
	model := raw["by_model"].([]any)[0].(map[string]any)
	if model["model"] != "" || model["share"] != json.Number("0.654") {
		t.Fatalf("model mapping = %+v", model)
	}
	timeRange := raw["time_range"].(map[string]any)
	if timeRange["start"] != start.UTC().Format(time.RFC3339Nano) || timeRange["granularity"] != analyticswindow.Granularity1Hour {
		t.Fatalf("time range = %+v", timeRange)
	}
}

func TestHandlerNormalizesEveryNilCollectionToArray(t *testing.T) {
	now := time.Date(2026, time.August, 21, 0, 0, 0, 0, time.UTC)
	analyzer := &analyzerStub{report: emptyReport(now.Add(-24*time.Hour), now)}
	handler := newTestHandler(t, analyzer, now)
	recorder := httptest.NewRecorder()
	handler.GetTokenUsage(recorder, httptest.NewRequest(http.MethodGet, "/admin/api/token-usage", nil))

	var raw map[string]json.RawMessage
	if err := json.NewDecoder(recorder.Body).Decode(&raw); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	for _, field := range []string{"timeseries", "by_provider", "by_model"} {
		if string(raw[field]) != "[]" {
			t.Fatalf("%s = %s, want []", field, raw[field])
		}
	}
}

func TestNewHandlerRequiresCoreDependencies(t *testing.T) {
	now := time.Date(2026, time.August, 21, 0, 0, 0, 0, time.UTC)
	clock := fixedClock{now: now}
	resolver := analyticswindow.NewResolver(clock)
	tests := []Config{
		{WindowResolver: &resolver, Clock: clock},
		{Analyzer: &analyzerStub{}, Clock: clock},
		{Analyzer: &analyzerStub{}, WindowResolver: &resolver},
	}
	for _, config := range tests {
		if _, err := NewHandler(config); err == nil {
			t.Fatal("NewHandler() error = nil, want dependency validation error")
		}
	}
}

func emptyReport(start, end time.Time) tokenanalytics.Report {
	return tokenanalytics.Report{TimeRange: tokenanalytics.TimeRange{Start: start, End: end}}
}

func assertErrorEnvelope(t *testing.T, recorder *httptest.ResponseRecorder, code, message string) {
	t.Helper()
	var response struct {
		Code    string            `json:"code"`
		Message string            `json:"message"`
		Details map[string]string `json:"details"`
	}
	if err := json.NewDecoder(strings.NewReader(recorder.Body.String())).Decode(&response); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if response.Code != code || response.Message != message {
		t.Fatalf("error response = %+v, want code/message %q/%q", response, code, message)
	}
}

func TestMapResponseDoesNotMutateReport(t *testing.T) {
	report := tokenanalytics.Report{TimeSeries: nil, ByProvider: nil, ByModel: nil}
	before := report
	_ = mapResponse(report, analyticswindow.Granularity1Hour)
	if !reflect.DeepEqual(report, before) {
		t.Fatal("mapResponse mutated the domain report")
	}
}
