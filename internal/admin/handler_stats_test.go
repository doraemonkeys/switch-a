package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"switch-a/internal/model"
)

var errTest = errors.New("test error")

type nilTimeSeriesStore struct {
	*mockStore
}

func (s *nilTimeSeriesStore) GetLogTimeSeries(_ context.Context, _ time.Time, _ time.Time, _ time.Duration) ([]model.TimeSeriesPoint, error) {
	return nil, nil
}

type timeSeriesErrorStore struct {
	*mockStore
}

func (s *timeSeriesErrorStore) GetLogTimeSeries(_ context.Context, _ time.Time, _ time.Time, _ time.Duration) ([]model.TimeSeriesPoint, error) {
	return nil, errTest
}

func TestGetStats_Success(t *testing.T) {
	h, st, _ := testHandler()

	now := time.Now().UTC()
	st.providers["p1"] = &model.Provider{ID: "p1", Name: "Provider 1", Enabled: true}
	st.providers["p2"] = &model.Provider{ID: "p2", Name: "Provider 2", Enabled: true}
	st.providers["p3"] = &model.Provider{ID: "p3", Name: "Provider 3", Enabled: false}
	st.healthStates["p1"] = &model.HealthState{ProviderID: "p1", Available: true}
	st.healthStates["p2"] = &model.HealthState{ProviderID: "p2", Available: false}

	st.logs = []model.RequestLog{
		{
			ProviderID:       "p1",
			APIType:          "claude",
			SemanticsVersion: model.RequestSemanticsVersionNormalizedV1,
			ServiceOutcome:   lifecycleServiceOutcomePtr(model.ServiceOutcomeCompleted),
			ClientAction:     lifecycleClientActionPtr(model.ClientActionNone),
			LatencyMs:        100,
			CreatedAt:        now.Add(-1 * time.Hour),
		},
		{
			ProviderID:       "p1",
			APIType:          "claude",
			SemanticsVersion: model.RequestSemanticsVersionNormalizedV1,
			ServiceOutcome:   lifecycleServiceOutcomePtr(model.ServiceOutcomeCompleted),
			ClientAction:     lifecycleClientActionPtr(model.ClientActionNone),
			LatencyMs:        200,
			CreatedAt:        now.Add(-2 * time.Hour),
		},
		{
			ProviderID:       "p2",
			APIType:          "codex",
			SemanticsVersion: model.RequestSemanticsVersionNormalizedV1,
			ServiceOutcome:   lifecycleServiceOutcomePtr(model.ServiceOutcomeInterrupted),
			ClientAction:     lifecycleClientActionPtr(model.ClientActionReconnectRequired),
			LatencyMs:        300,
			CreatedAt:        now.Add(-3 * time.Hour),
		},
		{
			ProviderID:       "legacy-provider",
			APIType:          "claude",
			SemanticsVersion: model.RequestSemanticsVersionLegacyPreAssessment,
			LatencyMs:        999,
			CreatedAt:        now.Add(-4 * time.Hour),
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/api/stats", nil)
	w := httptest.NewRecorder()

	h.GetStats(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp StatsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.TotalRequests != 3 {
		t.Fatalf("TotalRequests = %d, want 3", resp.TotalRequests)
	}
	if resp.AvgLatencyMs != 200 {
		t.Fatalf("AvgLatencyMs = %d, want 200", resp.AvgLatencyMs)
	}
	if resp.OutcomeCounts[model.ServiceOutcomeCompleted] != 2 {
		t.Fatalf("completed count = %d, want 2", resp.OutcomeCounts[model.ServiceOutcomeCompleted])
	}
	if resp.OutcomeCounts[model.ServiceOutcomeInterrupted] != 1 {
		t.Fatalf("interrupted count = %d, want 1", resp.OutcomeCounts[model.ServiceOutcomeInterrupted])
	}
	if resp.Providers.Total != 3 || resp.Providers.Healthy != 1 || resp.Providers.Unhealthy != 1 || resp.Providers.Disabled != 1 {
		t.Fatalf("provider stats = %+v, want total=3 healthy=1 unhealthy=1 disabled=1", resp.Providers)
	}
	if resp.RequestsByAPIType["claude"] != 2 {
		t.Fatalf("RequestsByAPIType[claude] = %d, want 2", resp.RequestsByAPIType["claude"])
	}
	if len(resp.RequestsByProviderOutcome) != 2 {
		t.Fatalf("len(RequestsByProviderOutcome) = %d, want 2", len(resp.RequestsByProviderOutcome))
	}
	if resp.RequestsByProviderOutcome[0].ID != "p1" || resp.RequestsByProviderOutcome[0].TotalRequests != 2 {
		t.Fatalf("first provider outcome stats = %+v, want p1 total_requests=2", resp.RequestsByProviderOutcome[0])
	}
}

func TestGetStats_ResponseUsesOutcomeFields(t *testing.T) {
	h, st, _ := testHandler()

	st.logs = []model.RequestLog{{
		ProviderID:       "p1",
		APIType:          "claude",
		SemanticsVersion: model.RequestSemanticsVersionNormalizedV1,
		ServiceOutcome:   lifecycleServiceOutcomePtr(model.ServiceOutcomeCompleted),
		CreatedAt:        time.Now().UTC().Add(-time.Hour),
	}}

	req := httptest.NewRequest(http.MethodGet, "/admin/api/stats", nil)
	w := httptest.NewRecorder()

	h.GetStats(w, req)

	var raw map[string]any
	if err := json.NewDecoder(w.Body).Decode(&raw); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if _, ok := raw["success_count"]; ok {
		t.Fatal("response should not expose success_count")
	}
	if _, ok := raw["fail_count"]; ok {
		t.Fatal("response should not expose fail_count")
	}
	if _, ok := raw["success_rate"]; ok {
		t.Fatal("response should not expose success_rate")
	}
	if _, ok := raw["timeseries"]; ok {
		t.Fatal("response should not expose timeseries")
	}
	if _, ok := raw["outcome_counts"]; !ok {
		t.Fatal("response should expose outcome_counts")
	}
	outcomeTimeSeries, ok := raw["outcome_timeseries"]
	if !ok {
		t.Fatal("response should expose outcome_timeseries even without granularity")
	}
	points, ok := outcomeTimeSeries.([]any)
	if !ok {
		t.Fatalf("outcome_timeseries should decode as an array, got %T", outcomeTimeSeries)
	}
	if len(points) != 0 {
		t.Fatalf("len(outcome_timeseries) = %d, want 0 without granularity", len(points))
	}
}

func TestGetStats_PeriodParameter(t *testing.T) {
	tests := []struct {
		name     string
		period   string
		wantCode int
	}{
		{"default", "", http.StatusOK},
		{"24h", "24h", http.StatusOK},
		{"7d", "7d", http.StatusOK},
		{"30d", "30d", http.StatusOK},
		{"all", "all", http.StatusOK},
		{"invalid", "invalid", http.StatusBadRequest},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, _, _ := testHandler()
			url := "/admin/api/stats"
			if tc.period != "" {
				url += "?period=" + tc.period
			}

			req := httptest.NewRequest(http.MethodGet, url, nil)
			w := httptest.NewRecorder()
			h.GetStats(w, req)

			if w.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d", w.Code, tc.wantCode)
			}
		})
	}
}

func TestGetStats_EmptyData(t *testing.T) {
	h, _, _ := testHandler()

	req := httptest.NewRequest(http.MethodGet, "/admin/api/stats", nil)
	w := httptest.NewRecorder()

	h.GetStats(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp StatsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.TotalRequests != 0 || resp.AvgLatencyMs != 0 {
		t.Fatalf("unexpected totals for empty response: %+v", resp)
	}
	if len(resp.OutcomeCounts) != 0 {
		t.Fatalf("OutcomeCounts = %+v, want empty map", resp.OutcomeCounts)
	}
	if len(resp.RequestsByProviderOutcome) != 0 {
		t.Fatalf("RequestsByProviderOutcome = %+v, want empty slice", resp.RequestsByProviderOutcome)
	}
}

func TestGetStats_StoreError(t *testing.T) {
	h, st, _ := testHandler()
	st.logsErr = errTest

	req := httptest.NewRequest(http.MethodGet, "/admin/api/stats", nil)
	w := httptest.NewRecorder()

	h.GetStats(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestGetStats_ProviderListError(t *testing.T) {
	h, st, _ := testHandler()
	st.listErr = errTest

	req := httptest.NewRequest(http.MethodGet, "/admin/api/stats", nil)
	w := httptest.NewRecorder()

	h.GetStats(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestGetStats_HealthStateError(t *testing.T) {
	h, st, _ := testHandler()

	st.providers["p1"] = &model.Provider{ID: "p1", Name: "Provider 1", Enabled: true}
	st.healthErr = errTest

	req := httptest.NewRequest(http.MethodGet, "/admin/api/stats", nil)
	w := httptest.NewRecorder()

	h.GetStats(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp StatsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Providers.Total != 1 || resp.Providers.Healthy != 1 {
		t.Fatalf("provider stats = %+v, want default healthy provider", resp.Providers)
	}
}

func TestGetStats_TimeRange(t *testing.T) {
	h, st, _ := testHandler()

	now := time.Now().UTC()
	st.providers["p1"] = &model.Provider{ID: "p1", Name: "Provider 1", Enabled: true}
	st.logs = []model.RequestLog{
		{
			ProviderID:       "p1",
			APIType:          "claude",
			SemanticsVersion: model.RequestSemanticsVersionNormalizedV1,
			ServiceOutcome:   lifecycleServiceOutcomePtr(model.ServiceOutcomeCompleted),
			CreatedAt:        now.Add(-1 * time.Hour),
		},
		{
			ProviderID:       "p1",
			APIType:          "claude",
			SemanticsVersion: model.RequestSemanticsVersionNormalizedV1,
			ServiceOutcome:   lifecycleServiceOutcomePtr(model.ServiceOutcomeCompleted),
			CreatedAt:        now.Add(-23 * time.Hour),
		},
		{
			ProviderID:       "p1",
			APIType:          "claude",
			SemanticsVersion: model.RequestSemanticsVersionLegacyPreAssessment,
			CreatedAt:        now.Add(-25 * time.Hour),
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/api/stats?period=24h", nil)
	w := httptest.NewRecorder()
	h.GetStats(w, req)

	var resp StatsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.TotalRequests != 2 {
		t.Fatalf("TotalRequests = %d, want 2", resp.TotalRequests)
	}
	if resp.TimeRange.Start.IsZero() || resp.TimeRange.End.IsZero() {
		t.Fatalf("time range should be populated, got %+v", resp.TimeRange)
	}
}

func TestGetStats_ProviderNameMapping(t *testing.T) {
	h, st, _ := testHandler()

	st.providers["p1"] = &model.Provider{ID: "p1", Name: "My Provider", Enabled: true}
	st.logs = []model.RequestLog{{
		ProviderID:       "p1",
		APIType:          "claude",
		SemanticsVersion: model.RequestSemanticsVersionNormalizedV1,
		ServiceOutcome:   lifecycleServiceOutcomePtr(model.ServiceOutcomeCompleted),
		CreatedAt:        time.Now().UTC().Add(-time.Hour),
	}}

	req := httptest.NewRequest(http.MethodGet, "/admin/api/stats", nil)
	w := httptest.NewRecorder()
	h.GetStats(w, req)

	var resp StatsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(resp.RequestsByProviderOutcome) != 1 {
		t.Fatalf("len(RequestsByProviderOutcome) = %d, want 1", len(resp.RequestsByProviderOutcome))
	}
	if resp.RequestsByProviderOutcome[0].Name != "My Provider" {
		t.Fatalf("provider name = %q, want %q", resp.RequestsByProviderOutcome[0].Name, "My Provider")
	}
}

func TestGetStats_WithGranularity(t *testing.T) {
	h, st, _ := testHandler()

	now := time.Now().UTC().Truncate(time.Hour)
	st.providers["p1"] = &model.Provider{ID: "p1", Name: "Provider 1", Enabled: true}
	st.logs = []model.RequestLog{
		{
			ProviderID:       "p1",
			APIType:          "claude",
			SemanticsVersion: model.RequestSemanticsVersionNormalizedV1,
			ServiceOutcome:   lifecycleServiceOutcomePtr(model.ServiceOutcomeCompleted),
			LatencyMs:        100,
			CreatedAt:        now.Add(-1 * time.Hour),
		},
		{
			ProviderID:       "p1",
			APIType:          "claude",
			SemanticsVersion: model.RequestSemanticsVersionNormalizedV1,
			ServiceOutcome:   lifecycleServiceOutcomePtr(model.ServiceOutcomeInterrupted),
			LatencyMs:        300,
			CreatedAt:        now.Add(-2 * time.Hour),
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/api/stats?granularity=1h", nil)
	w := httptest.NewRecorder()
	h.GetStats(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp StatsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(resp.OutcomeTimeSeries) == 0 {
		t.Fatal("OutcomeTimeSeries should not be empty when granularity is specified")
	}
}

func TestGetStats_WithGranularity_UsesOutcomeTimeSeriesWireContract(t *testing.T) {
	h, st, _ := testHandler()

	now := time.Now().UTC().Truncate(time.Hour)
	st.providers["p1"] = &model.Provider{ID: "p1", Name: "Provider 1", Enabled: true}
	st.logs = []model.RequestLog{
		{
			ProviderID:       "p1",
			APIType:          "claude",
			SemanticsVersion: model.RequestSemanticsVersionNormalizedV1,
			ServiceOutcome:   lifecycleServiceOutcomePtr(model.ServiceOutcomeCompleted),
			LatencyMs:        120,
			CreatedAt:        now.Add(-1 * time.Hour),
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/api/stats?granularity=1h", nil)
	w := httptest.NewRecorder()
	h.GetStats(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var raw map[string]any
	if err := json.NewDecoder(w.Body).Decode(&raw); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	outcomeTimeSeries, ok := raw["outcome_timeseries"]
	if !ok {
		t.Fatal("response should expose outcome_timeseries when granularity is specified")
	}
	points, ok := outcomeTimeSeries.([]any)
	if !ok {
		t.Fatalf("outcome_timeseries should decode as an array, got %T", outcomeTimeSeries)
	}
	if len(points) == 0 {
		t.Fatal("outcome_timeseries should not be empty when granularity is specified")
	}

	firstPoint, ok := points[0].(map[string]any)
	if !ok {
		t.Fatalf("outcome_timeseries[0] should decode as an object, got %T", points[0])
	}
	if _, ok := firstPoint["total_requests"]; !ok {
		t.Fatal("outcome_timeseries points should expose total_requests")
	}
	if _, ok := firstPoint["requests"]; ok {
		t.Fatal("outcome_timeseries points should not expose requests")
	}
}

func TestGetStats_GranularityValidation(t *testing.T) {
	tests := []struct {
		name        string
		granularity string
		wantCode    int
	}{
		{"valid_5m", "5m", http.StatusOK},
		{"valid_1h", "1h", http.StatusOK},
		{"invalid_10m", "10m", http.StatusBadRequest},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, _, _ := testHandler()

			req := httptest.NewRequest(http.MethodGet, "/admin/api/stats?granularity="+tc.granularity, nil)
			w := httptest.NewRecorder()
			h.GetStats(w, req)

			if w.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d", w.Code, tc.wantCode)
			}
		})
	}
}

func TestGetStats_GranularityLimits(t *testing.T) {
	tests := []struct {
		name        string
		period      string
		granularity string
		wantCode    int
	}{
		{"24h_5m_ok", "24h", "5m", http.StatusOK},
		{"7d_5m_error", "7d", "5m", http.StatusBadRequest},
		{"30d_6h_ok", "30d", "6h", http.StatusOK},
		{"all_6h_error", "all", "6h", http.StatusBadRequest},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, _, _ := testHandler()
			req := httptest.NewRequest(http.MethodGet, "/admin/api/stats?period="+tc.period+"&granularity="+tc.granularity, nil)
			w := httptest.NewRecorder()
			h.GetStats(w, req)

			if w.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d", w.Code, tc.wantCode)
			}
		})
	}
}

func TestGetStats_TimeSeriesZeroFill(t *testing.T) {
	h, st, _ := testHandler()

	now := time.Now().UTC().Truncate(time.Hour)
	st.providers["p1"] = &model.Provider{ID: "p1", Name: "Provider 1", Enabled: true}
	st.logs = []model.RequestLog{
		{
			ProviderID:       "p1",
			APIType:          "claude",
			SemanticsVersion: model.RequestSemanticsVersionNormalizedV1,
			ServiceOutcome:   lifecycleServiceOutcomePtr(model.ServiceOutcomeCompleted),
			LatencyMs:        100,
			CreatedAt:        now.Add(-1 * time.Hour),
		},
		{
			ProviderID:       "p1",
			APIType:          "claude",
			SemanticsVersion: model.RequestSemanticsVersionNormalizedV1,
			ServiceOutcome:   lifecycleServiceOutcomePtr(model.ServiceOutcomeCompleted),
			LatencyMs:        200,
			CreatedAt:        now.Add(-3 * time.Hour),
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/api/stats?period=24h&granularity=1h", nil)
	w := httptest.NewRecorder()
	h.GetStats(w, req)

	var resp StatsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(resp.OutcomeTimeSeries) != 24 {
		t.Fatalf("len(OutcomeTimeSeries) = %d, want 24", len(resp.OutcomeTimeSeries))
	}
	for i := 1; i < len(resp.OutcomeTimeSeries); i++ {
		if !resp.OutcomeTimeSeries[i].Time.After(resp.OutcomeTimeSeries[i-1].Time) {
			t.Fatalf("OutcomeTimeSeries not ordered at %d", i)
		}
	}
}

func TestGetStats_TimeSeriesError(t *testing.T) {
	h, st, _ := testHandler()

	st.providers["p1"] = &model.Provider{ID: "p1", Name: "Provider 1", Enabled: true}
	st.logsErr = errTest

	req := httptest.NewRequest(http.MethodGet, "/admin/api/stats?granularity=1h", nil)
	w := httptest.NewRecorder()
	h.GetStats(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestGetStats_TimeSeriesFetchError(t *testing.T) {
	h, st, _ := testHandler()
	h.store = &timeSeriesErrorStore{mockStore: st}

	st.providers["p1"] = &model.Provider{ID: "p1", Name: "Provider 1", Enabled: true}
	st.logs = []model.RequestLog{{
		ProviderID:       "p1",
		APIType:          "claude",
		SemanticsVersion: model.RequestSemanticsVersionNormalizedV1,
		ServiceOutcome:   lifecycleServiceOutcomePtr(model.ServiceOutcomeCompleted),
		CreatedAt:        time.Now().UTC().Add(-time.Hour),
	}}

	req := httptest.NewRequest(http.MethodGet, "/admin/api/stats?granularity=1h", nil)
	w := httptest.NewRecorder()
	h.GetStats(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusInternalServerError, w.Body.String())
	}
}

func TestGetStats_WithoutGranularity(t *testing.T) {
	h, st, _ := testHandler()

	st.providers["p1"] = &model.Provider{ID: "p1", Name: "Provider 1", Enabled: true}

	req := httptest.NewRequest(http.MethodGet, "/admin/api/stats", nil)
	w := httptest.NewRecorder()
	h.GetStats(w, req)

	var resp StatsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(resp.OutcomeTimeSeries) != 0 {
		t.Fatalf("OutcomeTimeSeries should be empty when granularity is omitted, got %d points", len(resp.OutcomeTimeSeries))
	}
}

func TestFormatGranularity_KnownAndFallbackValues(t *testing.T) {
	tests := []struct {
		name  string
		input time.Duration
		want  string
	}{
		{name: "5m", input: 5 * time.Minute, want: "5m"},
		{name: "15m", input: 15 * time.Minute, want: "15m"},
		{name: "1h", input: time.Hour, want: "1h"},
		{name: "6h", input: 6 * time.Hour, want: "6h"},
		{name: "1d", input: 24 * time.Hour, want: "1d"},
		{name: "fallback", input: 90 * time.Minute, want: (90 * time.Minute).String()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatGranularity(tt.input); got != tt.want {
				t.Fatalf("formatGranularity(%s) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestGetTimeSeriesStartTime_Branches(t *testing.T) {
	now := time.Date(2026, time.April, 6, 2, 30, 0, 0, time.UTC)
	startTime := now.Add(-24 * time.Hour)
	earliestLog := now.Add(-72 * time.Hour)

	if got := getTimeSeriesStartTime("24h", startTime, earliestLog, now); !got.Equal(startTime) {
		t.Fatalf("24h start time = %s, want %s", got, startTime)
	}
	if got := getTimeSeriesStartTime("all", startTime, earliestLog, now); !got.Equal(earliestLog) {
		t.Fatalf("all start time with earliest log = %s, want %s", got, earliestLog)
	}

	wantFallback := now.Add(-DefaultTimeSeriesRangeDays * 24 * time.Hour)
	if got := getTimeSeriesStartTime("all", startTime, time.Time{}, now); !got.Equal(wantFallback) {
		t.Fatalf("all fallback start time = %s, want %s", got, wantFallback)
	}
}

func TestBuildStatsResponse_AllPeriodUsesEarliestLog(t *testing.T) {
	h, _, _ := testHandler()

	startTime := time.Date(2026, time.April, 6, 0, 0, 0, 0, time.UTC)
	endTime := startTime.Add(24 * time.Hour)
	earliestLog := startTime.Add(-48 * time.Hour)
	logStats := &model.LogStats{
		TotalRequests: 2,
		AvgLatencyMs:  150,
		OutcomeCounts: map[model.ServiceOutcome]int64{
			model.ServiceOutcomeCompleted:   1,
			model.ServiceOutcomeInterrupted: 1,
		},
		ByAPIType: map[string]int64{
			"claude": 2,
		},
		ByProvider: []model.ProviderLogStats{{
			ProviderID: "p1",
			Count:      2,
			OutcomeCounts: map[model.ServiceOutcome]int64{
				model.ServiceOutcomeCompleted:   1,
				model.ServiceOutcomeInterrupted: 1,
			},
		}},
		EarliestLog: earliestLog,
	}

	resp := h.buildStatsResponse(
		logStats,
		ProviderStats{Total: 1, Healthy: 1},
		map[string]string{"p1": "Provider 1"},
		startTime,
		endTime,
		"all",
	)

	if !resp.TimeRange.Start.Equal(earliestLog) {
		t.Fatalf("TimeRange.Start = %s, want %s", resp.TimeRange.Start, earliestLog)
	}
	if len(resp.OutcomeTimeSeries) != 0 {
		t.Fatalf("OutcomeTimeSeries = %+v, want empty slice", resp.OutcomeTimeSeries)
	}
	if len(resp.RequestsByProviderOutcome) != 1 || resp.RequestsByProviderOutcome[0].Name != "Provider 1" {
		t.Fatalf("RequestsByProviderOutcome = %+v, want provider name mapping preserved", resp.RequestsByProviderOutcome)
	}
}

func TestGetStats_AllPeriodNormalizesNilOutcomeTimeSeries(t *testing.T) {
	h, st, _ := testHandler()
	h.store = &nilTimeSeriesStore{mockStore: st}

	earliestLog := time.Now().UTC().Add(-2 * time.Hour)
	st.providers["p1"] = &model.Provider{ID: "p1", Name: "Provider 1", Enabled: true}
	st.logs = []model.RequestLog{{
		ProviderID:       "p1",
		APIType:          "claude",
		SemanticsVersion: model.RequestSemanticsVersionNormalizedV1,
		ServiceOutcome:   lifecycleServiceOutcomePtr(model.ServiceOutcomeCompleted),
		CreatedAt:        earliestLog,
	}}

	req := httptest.NewRequest(http.MethodGet, "/admin/api/stats?period=all&granularity=1d", nil)
	w := httptest.NewRecorder()
	h.GetStats(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp StatsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !resp.TimeRange.Start.Equal(earliestLog) {
		t.Fatalf("TimeRange.Start = %s, want %s", resp.TimeRange.Start, earliestLog)
	}
	if len(resp.OutcomeTimeSeries) != 0 {
		t.Fatalf("OutcomeTimeSeries = %+v, want empty slice after nil store result", resp.OutcomeTimeSeries)
	}
}
