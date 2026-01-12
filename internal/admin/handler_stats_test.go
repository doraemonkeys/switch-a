package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"switch-a/internal/model"
)

var errTest = errors.New("test error")

func TestGetStats_Success(t *testing.T) {
	h, st, _ := testHandler()

	// Setup test data
	now := time.Now().UTC()
	st.providers["p1"] = &model.Provider{ID: "p1", Name: "Provider 1", Enabled: true}
	st.providers["p2"] = &model.Provider{ID: "p2", Name: "Provider 2", Enabled: true}
	st.providers["p3"] = &model.Provider{ID: "p3", Name: "Provider 3", Enabled: false}
	st.healthStates["p1"] = &model.HealthState{ProviderID: "p1", Available: true}
	st.healthStates["p2"] = &model.HealthState{ProviderID: "p2", Available: false}

	// Add some logs
	st.logs = []model.RequestLog{
		{ProviderID: "p1", APIType: "claude", Success: true, LatencyMs: 100, CreatedAt: now.Add(-1 * time.Hour)},
		{ProviderID: "p1", APIType: "claude", Success: true, LatencyMs: 200, CreatedAt: now.Add(-2 * time.Hour)},
		{ProviderID: "p2", APIType: "codex", Success: false, LatencyMs: 300, CreatedAt: now.Add(-3 * time.Hour)},
		{ProviderID: "p2", APIType: "codex", Success: true, LatencyMs: 150, CreatedAt: now.Add(-4 * time.Hour)},
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/api/stats", nil)
	w := httptest.NewRecorder()

	h.GetStats(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp StatsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Check overall statistics
	if resp.TotalRequests != 4 {
		t.Errorf("TotalRequests = %d, want 4", resp.TotalRequests)
	}
	if resp.SuccessCount != 3 {
		t.Errorf("SuccessCount = %d, want 3", resp.SuccessCount)
	}
	if resp.FailCount != 1 {
		t.Errorf("FailCount = %d, want 1", resp.FailCount)
	}
	if resp.SuccessRate < 0.74 || resp.SuccessRate > 0.76 {
		t.Errorf("SuccessRate = %f, want ~0.75", resp.SuccessRate)
	}

	// Check provider statistics
	if resp.Providers.Total != 3 {
		t.Errorf("Providers.Total = %d, want 3", resp.Providers.Total)
	}
	if resp.Providers.Healthy != 1 {
		t.Errorf("Providers.Healthy = %d, want 1", resp.Providers.Healthy)
	}
	if resp.Providers.Unhealthy != 1 {
		t.Errorf("Providers.Unhealthy = %d, want 1", resp.Providers.Unhealthy)
	}
	if resp.Providers.Disabled != 1 {
		t.Errorf("Providers.Disabled = %d, want 1", resp.Providers.Disabled)
	}

	// Check requests by API type
	if resp.RequestsByAPIType["claude"] != 2 {
		t.Errorf("RequestsByAPIType[claude] = %d, want 2", resp.RequestsByAPIType["claude"])
	}
	if resp.RequestsByAPIType["codex"] != 2 {
		t.Errorf("RequestsByAPIType[codex] = %d, want 2", resp.RequestsByAPIType["codex"])
	}

	// Check requests by provider
	if len(resp.RequestsByProvider) != 2 {
		t.Errorf("len(RequestsByProvider) = %d, want 2", len(resp.RequestsByProvider))
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
		{"1h", "1h", http.StatusBadRequest},
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
				t.Errorf("status = %d, want %d", w.Code, tc.wantCode)
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
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp StatsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Check that empty data returns zero values
	if resp.TotalRequests != 0 {
		t.Errorf("TotalRequests = %d, want 0", resp.TotalRequests)
	}
	if resp.SuccessRate != 0 {
		t.Errorf("SuccessRate = %f, want 0", resp.SuccessRate)
	}
	if resp.Providers.Total != 0 {
		t.Errorf("Providers.Total = %d, want 0", resp.Providers.Total)
	}
	if len(resp.RequestsByAPIType) != 0 {
		t.Errorf("len(RequestsByAPIType) = %d, want 0", len(resp.RequestsByAPIType))
	}
	if len(resp.RequestsByProvider) != 0 {
		t.Errorf("len(RequestsByProvider) = %d, want 0", len(resp.RequestsByProvider))
	}
}

func TestGetStats_StoreError(t *testing.T) {
	h, st, _ := testHandler()

	// Test log stats error
	st.logsErr = errTest

	req := httptest.NewRequest(http.MethodGet, "/admin/api/stats", nil)
	w := httptest.NewRecorder()

	h.GetStats(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestGetStats_ProviderListError(t *testing.T) {
	h, st, _ := testHandler()

	// Set list error
	st.listErr = errTest

	req := httptest.NewRequest(http.MethodGet, "/admin/api/stats", nil)
	w := httptest.NewRecorder()

	h.GetStats(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestGetStats_HealthStateError(t *testing.T) {
	h, st, _ := testHandler()

	// Setup providers but make health state fetch fail
	st.providers["p1"] = &model.Provider{ID: "p1", Name: "Provider 1", Enabled: true}
	st.healthErr = errTest

	req := httptest.NewRequest(http.MethodGet, "/admin/api/stats", nil)
	w := httptest.NewRecorder()

	h.GetStats(w, req)

	// Should still succeed (partial failure design)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp StatsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Provider should be counted as healthy (default when health state unavailable)
	if resp.Providers.Total != 1 {
		t.Errorf("Providers.Total = %d, want 1", resp.Providers.Total)
	}
}

func TestGetStats_TimeRange(t *testing.T) {
	h, st, _ := testHandler()

	// Setup test data
	now := time.Now().UTC()
	st.providers["p1"] = &model.Provider{ID: "p1", Name: "Provider 1", Enabled: true}

	// Add logs within 24h period
	st.logs = []model.RequestLog{
		{ProviderID: "p1", APIType: "claude", Success: true, CreatedAt: now.Add(-1 * time.Hour)},
		{ProviderID: "p1", APIType: "claude", Success: true, CreatedAt: now.Add(-23 * time.Hour)},
		// This log should be outside 24h period
		{ProviderID: "p1", APIType: "claude", Success: false, CreatedAt: now.Add(-25 * time.Hour)},
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/api/stats?period=24h", nil)
	w := httptest.NewRecorder()

	h.GetStats(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp StatsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Only 2 logs should be counted (within 24h)
	if resp.TotalRequests != 2 {
		t.Errorf("TotalRequests = %d, want 2", resp.TotalRequests)
	}

	// Check time range is set
	if resp.TimeRange.Start.IsZero() {
		t.Error("TimeRange.Start should not be zero")
	}
	if resp.TimeRange.End.IsZero() {
		t.Error("TimeRange.End should not be zero")
	}
}

func TestGetStats_ProviderNameMapping(t *testing.T) {
	h, st, _ := testHandler()

	// Setup test data with a provider name
	now := time.Now().UTC()
	st.providers["p1"] = &model.Provider{ID: "p1", Name: "My Provider", Enabled: true}
	st.logs = []model.RequestLog{
		{ProviderID: "p1", APIType: "claude", Success: true, CreatedAt: now.Add(-1 * time.Hour)},
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/api/stats", nil)
	w := httptest.NewRecorder()

	h.GetStats(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp StatsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Check that provider name is mapped correctly
	if len(resp.RequestsByProvider) != 1 {
		t.Fatalf("len(RequestsByProvider) = %d, want 1", len(resp.RequestsByProvider))
	}
	if resp.RequestsByProvider[0].Name != "My Provider" {
		t.Errorf("RequestsByProvider[0].Name = %q, want %q", resp.RequestsByProvider[0].Name, "My Provider")
	}
}

func TestValidPeriods(t *testing.T) {
	// Test that ValidPeriods contains expected values
	expectedPeriods := []string{"24h", "7d", "30d", "all"}
	for _, p := range expectedPeriods {
		if !ValidPeriods[p] {
			t.Errorf("ValidPeriods should contain %q", p)
		}
	}

	// Test that invalid periods are not included
	invalidPeriods := []string{"1h", "12h", "1d", "90d", ""}
	for _, p := range invalidPeriods {
		if ValidPeriods[p] {
			t.Errorf("ValidPeriods should not contain %q", p)
		}
	}
}

func TestPeriodToDuration(t *testing.T) {
	tests := []struct {
		period string
		want   time.Duration
	}{
		{"24h", 24 * time.Hour},
		{"7d", 7 * 24 * time.Hour},
		{"30d", 30 * 24 * time.Hour},
		{"all", 0},
	}

	for _, tc := range tests {
		t.Run(tc.period, func(t *testing.T) {
			got := periodToDuration(tc.period)
			if got != tc.want {
				t.Errorf("periodToDuration(%q) = %v, want %v", tc.period, got, tc.want)
			}
		})
	}
}
