package tokenusageapi

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/analyticswindow"
	"github.com/doraemonkeys/switch-a/internal/tokenanalytics"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

type sequenceClock struct {
	mu     sync.Mutex
	values []time.Time
	index  int
}

func (c *sequenceClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.index >= len(c.values) {
		return c.values[len(c.values)-1]
	}
	value := c.values[c.index]
	c.index++
	return value
}

func TestObservabilityCorrelatesStartAndCompletionWithDecisionContext(t *testing.T) {
	startedAt := time.Date(2026, time.August, 21, 1, 0, 0, 0, time.UTC)
	clock := &sequenceClock{values: []time.Time{startedAt, startedAt.Add(17 * time.Millisecond)}}
	resolver := analyticswindow.NewResolver(clock)
	ids := &operationIDStub{id: "stable-operation-id"}
	core, observed := observer.New(zap.InfoLevel)
	report := emptyReport(startedAt.Add(-24*time.Hour), startedAt)
	report.TimeSeries = []tokenanalytics.Bucket{{}, {}}
	report.ByProvider = []tokenanalytics.ProviderRank{{ProviderID: "provider-a"}}
	report.ByModel = []tokenanalytics.ModelRank{{Model: "model-a"}}
	report.Coverage = tokenanalytics.Coverage{TotalRequests: 10, ObservedRequests: 8, ComparableRequests: 6}
	report.DataQuality = tokenanalytics.DataQuality{PartialRequests: 1, InvalidRequests: 1, UnknownSemanticsRequests: 0}
	handler, err := NewHandler(Config{
		Analyzer:       &analyzerStub{report: report},
		WindowResolver: &resolver,
		Clock:          clock,
		OperationIDs:   ids,
		Logger:         zap.New(core),
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	recorder := httptest.NewRecorder()
	handler.GetTokenUsage(recorder, httptest.NewRequest(http.MethodGet,
		"/admin/api/token-usage?as_of=2026-08-21T01%3A00%3A00Z&provider_id=provider-a", nil))

	if recorder.Code != http.StatusOK || ids.calls != 1 {
		t.Fatalf("status/id calls = %d/%d, want 200/1", recorder.Code, ids.calls)
	}
	entries := observed.All()
	if len(entries) != 2 || entries[0].Message != queryStartedMessage || entries[1].Message != queryCompletedMessage {
		t.Fatalf("log messages = %+v, want start then complete", entries)
	}
	for _, entry := range entries {
		fields := entry.ContextMap()
		if fields["operation"] != operationName || fields["operation_id"] != "stable-operation-id" ||
			fields["period"] != analyticswindow.Period24Hours || fields["granularity"] != analyticswindow.Granularity1Hour ||
			fields["provider_id"] != "provider-a" {
			t.Fatalf("common fields = %+v", fields)
		}
		if _, exists := fields["model"]; exists {
			t.Fatalf("absent model was logged: %+v", fields)
		}
		if _, exists := fields["api_type"]; exists {
			t.Fatalf("absent api_type was logged: %+v", fields)
		}
		if fields["window_start_state"] != analyticswindow.StartResolved.String() {
			t.Fatalf("window_start_state = %v, want resolved: %+v", fields["window_start_state"], fields)
		}
		if _, exists := fields["window_start"]; !exists {
			t.Fatalf("window_start missing: %+v", fields)
		}
		if _, exists := fields["window_end"]; !exists {
			t.Fatalf("window_end missing: %+v", fields)
		}
	}
	completed := entries[1].ContextMap()
	wantCompletion := map[string]any{
		"bucket_count":               int64(2),
		"provider_rank_count":        int64(1),
		"model_rank_count":           int64(1),
		"total_requests":             int64(10),
		"observed_requests":          int64(8),
		"comparable_requests":        int64(6),
		"non_comparable_requests":    int64(4),
		"partial_requests":           int64(1),
		"invalid_requests":           int64(1),
		"unknown_semantics_requests": int64(0),
	}
	for key, want := range wantCompletion {
		if got := completed[key]; got != want {
			t.Errorf("completion field %s = %#v, want %#v", key, got, want)
		}
	}
	if got := completed["duration"]; got != 17*time.Millisecond {
		t.Errorf("duration = %#v, want %d", got, 17*time.Millisecond)
	}
}

func TestObservabilityAllPeriodLogsTruthfulLowerBoundLifecycle(t *testing.T) {
	t.Parallel()

	end := time.Date(2026, time.August, 21, 1, 0, 0, 0, time.UTC)
	earliest := end.Add(-72 * time.Hour)
	overflowStart := end.Add(-(analyticswindow.MaxBucketCount + 1) * 24 * time.Hour)
	overflowWindow := analyticswindow.Window{
		Period:          analyticswindow.PeriodAll,
		GranularityName: analyticswindow.Granularity1Day,
		Granularity:     24 * time.Hour,
		Start:           overflowStart,
		End:             end,
		StartResolution: analyticswindow.StartResolved,
	}
	tests := []struct {
		name       string
		analyzer   *analyzerStub
		wantStatus int
		wantEvent  string
		wantStart  *time.Time
	}{
		{
			name: "non-empty completion",
			analyzer: &analyzerStub{report: tokenanalytics.Report{
				TimeRange: tokenanalytics.TimeRange{Start: earliest, End: end},
			}},
			wantStatus: http.StatusOK,
			wantEvent:  queryCompletedMessage,
			wantStart:  &earliest,
		},
		{
			name: "empty completion",
			analyzer: &analyzerStub{report: tokenanalytics.Report{
				TimeRange: tokenanalytics.TimeRange{Start: end, End: end},
			}},
			wantStatus: http.StatusOK,
			wantEvent:  queryCompletedMessage,
			wantStart:  &end,
		},
		{
			name: "data-dependent overflow",
			analyzer: &analyzerStub{err: tokenanalytics.NewFailureForWindow(
				tokenanalytics.FailureStageResponseMap,
				tokenanalytics.FailureCodeWindowResolution,
				&analyticswindow.ValidationError{Field: "granularity", Reason: "too_many_buckets"},
				overflowWindow,
			)},
			wantStatus: http.StatusBadRequest,
			wantEvent:  queryFailedMessage,
			wantStart:  &overflowStart,
		},
		{
			name:       "summary failure",
			analyzer:   &analyzerStub{err: tokenanalytics.NewFailure(tokenanalytics.FailureStageSummary, tokenanalytics.FailureCodeRepository, errors.New("storage"))},
			wantStatus: http.StatusInternalServerError,
			wantEvent:  queryFailedMessage,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			startedAt := end.Add(time.Hour)
			clock := &sequenceClock{values: []time.Time{startedAt, startedAt.Add(time.Millisecond)}}
			resolver := analyticswindow.NewResolver(clock)
			core, observed := observer.New(zap.InfoLevel)
			handler, err := NewHandler(Config{
				Analyzer:       test.analyzer,
				WindowResolver: &resolver,
				Clock:          clock,
				OperationIDs:   &operationIDStub{id: "all-period-operation"},
				Logger:         zap.New(core),
			})
			if err != nil {
				t.Fatalf("NewHandler() error = %v", err)
			}

			recorder := httptest.NewRecorder()
			target := "/admin/api/token-usage?period=all&as_of=" + url.QueryEscape(end.Format(time.RFC3339Nano))
			handler.GetTokenUsage(recorder, httptest.NewRequest(http.MethodGet, target, nil))

			entries := observed.All()
			if recorder.Code != test.wantStatus || len(entries) != 2 ||
				entries[0].Message != queryStartedMessage || entries[1].Message != test.wantEvent {
				t.Fatalf("status/logs = %d/%+v, want %d start/%s", recorder.Code, entries, test.wantStatus, test.wantEvent)
			}

			startedFields := entries[0].ContextMap()
			if startedFields["window_start_state"] != analyticswindow.StartUnresolved.String() {
				t.Fatalf("start state = %+v, want unresolved", startedFields)
			}
			if _, exists := startedFields["window_start"]; exists {
				t.Fatalf("unresolved start emitted a timestamp: %+v", startedFields)
			}
			if got := startedFields["window_end"]; got != end {
				t.Fatalf("start window_end = %v, want %v", got, end)
			}

			finalFields := entries[1].ContextMap()
			if test.wantStart == nil {
				if finalFields["window_start_state"] != analyticswindow.StartUnresolved.String() {
					t.Fatalf("final state = %+v, want unresolved", finalFields)
				}
				if _, exists := finalFields["window_start"]; exists {
					t.Fatalf("unresolved failure emitted a timestamp: %+v", finalFields)
				}
				return
			}
			if finalFields["window_start_state"] != analyticswindow.StartResolved.String() ||
				finalFields["window_start"] != *test.wantStart {
				t.Fatalf("final lower bound = %+v, want resolved %v", finalFields, *test.wantStart)
			}
		})
	}
}

func TestObservabilityFailureCodesRemainDistinguishable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		err       error
		wantStage tokenanalytics.FailureStage
		wantCode  tokenanalytics.FailureCode
	}{
		{
			name:      "repository stage",
			err:       tokenanalytics.NewFailure(tokenanalytics.FailureStageTimeSeries, tokenanalytics.FailureCodeRepository, errors.New("private repository detail")),
			wantStage: tokenanalytics.FailureStageTimeSeries,
			wantCode:  tokenanalytics.FailureCodeRepository,
		},
		{
			name:      "rejected bucket key",
			err:       tokenanalytics.NewFailure(tokenanalytics.FailureStageResponseMap, tokenanalytics.FailureCodeBucketKeyRejected, errors.New("private bucket detail")),
			wantStage: tokenanalytics.FailureStageResponseMap,
			wantCode:  tokenanalytics.FailureCodeBucketKeyRejected,
		},
		{
			name:      "snapshot close",
			err:       tokenanalytics.NewFailure(tokenanalytics.FailureStageResponseMap, tokenanalytics.FailureCodeSnapshotClose, errors.New("private close detail")),
			wantStage: tokenanalytics.FailureStageResponseMap,
			wantCode:  tokenanalytics.FailureCodeSnapshotClose,
		},
		{
			name:      "unexpected analyzer",
			err:       errors.New("private unexpected detail"),
			wantStage: tokenanalytics.FailureStageResponseMap,
			wantCode:  tokenanalytics.FailureCodeUnexpectedAnalyzerError,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			startedAt := time.Date(2026, time.August, 21, 1, 0, 0, 0, time.UTC)
			clock := &sequenceClock{values: []time.Time{startedAt, startedAt.Add(time.Millisecond)}}
			resolver := analyticswindow.NewResolver(clock)
			core, observed := observer.New(zap.InfoLevel)
			handler, err := NewHandler(Config{
				Analyzer:       &analyzerStub{err: test.err},
				WindowResolver: &resolver,
				Clock:          clock,
				OperationIDs:   &operationIDStub{id: "failure-code-operation"},
				Logger:         zap.New(core),
			})
			if err != nil {
				t.Fatalf("NewHandler() error = %v", err)
			}

			recorder := httptest.NewRecorder()
			handler.GetTokenUsage(recorder, httptest.NewRequest(http.MethodGet, "/admin/api/token-usage", nil))

			entries := observed.All()
			if recorder.Code != http.StatusInternalServerError || len(entries) != 2 {
				t.Fatalf("status/log count = %d/%d, want 500/2", recorder.Code, len(entries))
			}
			fields := entries[1].ContextMap()
			if fields["operation_id"] != "failure-code-operation" ||
				fields["failure_stage"] != string(test.wantStage) ||
				fields["failure_code"] != string(test.wantCode) {
				t.Fatalf("failure fields = %+v, want operation/stage/code %q/%q/%q", fields, "failure-code-operation", test.wantStage, test.wantCode)
			}
			if serialized := fmt.Sprint(entries); strings.Contains(serialized, "private") {
				t.Fatalf("structured logs leaked underlying cause: %s", serialized)
			}
			if strings.Contains(recorder.Body.String(), "private") {
				t.Fatalf("public error leaked underlying cause: %s", recorder.Body.String())
			}
			assertErrorEnvelope(t, recorder, internalErrorCode, internalErrorMessage)
		})
	}
}

func TestObservabilityFailureUsesStableStageAndRedactsSensitiveInputs(t *testing.T) {
	const (
		bodySentinel  = "BODY-CREDENTIAL-SENTINEL"
		authSentinel  = "AUTHORIZATION-SENTINEL"
		causeSentinel = "SQL-PARAMETER-SENTINEL"
	)
	startedAt := time.Date(2026, time.August, 21, 1, 0, 0, 0, time.UTC)
	clock := &sequenceClock{values: []time.Time{startedAt, startedAt.Add(9 * time.Millisecond)}}
	resolver := analyticswindow.NewResolver(clock)
	core, observed := observer.New(zap.InfoLevel)
	analyzerErr := tokenanalytics.NewFailure(tokenanalytics.FailureStageSummary, tokenanalytics.FailureCodeRepository, errors.New(causeSentinel))
	handler, err := NewHandler(Config{
		Analyzer:       &analyzerStub{err: analyzerErr},
		WindowResolver: &resolver,
		Clock:          clock,
		OperationIDs:   &operationIDStub{id: "failure-operation-id"},
		Logger:         zap.New(core),
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	request := httptest.NewRequest(http.MethodGet,
		"/admin/api/token-usage?as_of=2026-08-21T01%3A00%3A00Z", strings.NewReader(bodySentinel))
	request.Header.Set("Authorization", "Bearer "+authSentinel)

	recorder := httptest.NewRecorder()
	handler.GetTokenUsage(recorder, request)

	entries := observed.All()
	if recorder.Code != http.StatusInternalServerError || len(entries) != 2 || entries[0].Message != queryStartedMessage || entries[1].Message != queryFailedMessage {
		t.Fatalf("status/logs = %d/%+v", recorder.Code, entries)
	}
	startedFields := entries[0].ContextMap()
	failedFields := entries[1].ContextMap()
	if startedFields["operation_id"] != "failure-operation-id" || failedFields["operation_id"] != "failure-operation-id" {
		t.Fatalf("operation IDs = %v/%v", startedFields["operation_id"], failedFields["operation_id"])
	}
	if failedFields["failure_stage"] != string(tokenanalytics.FailureStageSummary) ||
		failedFields["failure_code"] != string(tokenanalytics.FailureCodeRepository) ||
		failedFields["failure_reason"] != failureReasonQueryFailed ||
		failedFields["duration"] != 9*time.Millisecond {
		t.Fatalf("failure fields = %+v", failedFields)
	}
	if _, exists := failedFields["error"]; exists {
		t.Fatalf("unbounded error field was logged: %+v", failedFields)
	}
	serialized := fmt.Sprint(entries)
	for _, sentinel := range []string{bodySentinel, authSentinel, causeSentinel} {
		if strings.Contains(serialized, sentinel) {
			t.Fatalf("sensitive sentinel %q leaked into logs: %s", sentinel, serialized)
		}
	}
}
