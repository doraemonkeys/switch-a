package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/analyticswindow"
	"github.com/doraemonkeys/switch-a/internal/model"
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

type statsFixedClock struct {
	now time.Time
}

func (c statsFixedClock) Now() time.Time {
	return c.now
}

type statsArgumentStore struct {
	*mockStore
	statsCalls      int
	statsStart      time.Time
	statsEnd        time.Time
	timeSeriesCalls int
	timeSeriesStart time.Time
	timeSeriesEnd   time.Time
	granularity     time.Duration
}

func (s *statsArgumentStore) GetLogStats(ctx context.Context, start, end time.Time) (*model.LogStats, error) {
	s.statsCalls++
	s.statsStart = start
	s.statsEnd = end
	return s.mockStore.GetLogStats(ctx, start, end)
}

func (s *statsArgumentStore) GetLogTimeSeries(ctx context.Context, start, end time.Time, granularity time.Duration) ([]model.TimeSeriesPoint, error) {
	s.timeSeriesCalls++
	s.timeSeriesStart = start
	s.timeSeriesEnd = end
	s.granularity = granularity
	return s.mockStore.GetLogTimeSeries(ctx, start, end, granularity)
}

func setStatsClock(handler *Handler, now time.Time) {
	resolver := analyticswindow.NewResolver(statsFixedClock{now: now})
	handler.statsWindowResolver = &resolver
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
		earliestLog,
		endTime,
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

func TestGetStatsAsOfAlignsExactExclusiveEnd(t *testing.T) {
	handler, store, _ := testHandler()
	captured := &statsArgumentStore{mockStore: store}
	handler.store = captured
	setStatsClock(handler, time.Date(2030, time.January, 1, 0, 0, 0, 0, time.UTC))

	request := httptest.NewRequest(http.MethodGet, "/admin/api/stats?period=24h&granularity=1h&as_of=2026-08-21T08%3A30%3A45%2B08%3A00", nil)
	recorder := httptest.NewRecorder()
	handler.GetStats(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", recorder.Code, recorder.Body.String())
	}
	wantEnd := time.Date(2026, time.August, 21, 0, 30, 45, 0, time.UTC)
	wantStart := wantEnd.Add(-24 * time.Hour)
	if captured.statsCalls != 1 || !captured.statsStart.Equal(wantStart) || !captured.statsEnd.Equal(wantEnd) {
		t.Fatalf("GetLogStats arguments = calls %d [%s,%s), want [%s,%s)", captured.statsCalls, captured.statsStart, captured.statsEnd, wantStart, wantEnd)
	}
	if captured.timeSeriesCalls != 1 || !captured.timeSeriesStart.Equal(wantStart) || !captured.timeSeriesEnd.Equal(wantEnd) || captured.granularity != time.Hour {
		t.Fatalf("GetLogTimeSeries arguments = calls %d [%s,%s) %s", captured.timeSeriesCalls, captured.timeSeriesStart, captured.timeSeriesEnd, captured.granularity)
	}
	var response StatsResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !response.TimeRange.Start.Equal(wantStart) || !response.TimeRange.End.Equal(wantEnd) {
		t.Fatalf("response range = %+v, want [%s,%s)", response.TimeRange, wantStart, wantEnd)
	}
}

func TestGetStatsOmittedAsOfUsesInjectedClock(t *testing.T) {
	handler, store, _ := testHandler()
	captured := &statsArgumentStore{mockStore: store}
	handler.store = captured
	wantEnd := time.Date(2026, time.August, 21, 2, 3, 4, 0, time.UTC)
	setStatsClock(handler, wantEnd)

	recorder := httptest.NewRecorder()
	handler.GetStats(recorder, httptest.NewRequest(http.MethodGet, "/admin/api/stats", nil))

	if recorder.Code != http.StatusOK || !captured.statsEnd.Equal(wantEnd) || captured.timeSeriesCalls != 0 {
		t.Fatalf("status/end/timeseries = %d/%s/%d, want 200/%s/0", recorder.Code, captured.statsEnd, captured.timeSeriesCalls, wantEnd)
	}
}

func TestGetStatsRejectsInvalidAsOfWithoutStorageAccess(t *testing.T) {
	tests := []string{
		"/admin/api/stats?as_of=",
		"/admin/api/stats?as_of=not-a-time",
		"/admin/api/stats?as_of=2026-08-21T00%3A00%3A00Z&as_of=2026-08-21T01%3A00%3A00Z",
	}
	for _, target := range tests {
		t.Run(target, func(t *testing.T) {
			handler, store, _ := testHandler()
			captured := &statsArgumentStore{mockStore: store}
			handler.store = captured
			setStatsClock(handler, time.Date(2026, time.August, 21, 0, 0, 0, 0, time.UTC))

			recorder := httptest.NewRecorder()
			handler.GetStats(recorder, httptest.NewRequest(http.MethodGet, target, nil))

			if recorder.Code != http.StatusBadRequest || captured.statsCalls != 0 {
				t.Fatalf("status/storage calls = %d/%d, want 400/0; body: %s", recorder.Code, captured.statsCalls, recorder.Body.String())
			}
		})
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
