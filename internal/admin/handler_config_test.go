package admin

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"switch-a/internal/defaults"
	"switch-a/internal/model"

	"go.uber.org/zap"
)

// Config Tests

func TestGetConfig(t *testing.T) {
	h, st, _ := testHandler()

	st.config["sticky_mode"] = "model"
	st.config["global_max_attempts"] = "3"

	req := httptest.NewRequest(http.MethodGet, "/admin/api/config", nil)
	w := httptest.NewRecorder()

	h.GetConfig(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp ConfigResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Check user-modified values
	if resp.Values["sticky_mode"] != "model" {
		t.Errorf("Values[sticky_mode] = %q, want %q", resp.Values["sticky_mode"], "model")
	}
	if resp.Values["global_max_attempts"] != "3" {
		t.Errorf("Values[global_max_attempts] = %q, want %q", resp.Values["global_max_attempts"], "3")
	}

	// Check that defaults are present
	if resp.Defaults == nil {
		t.Fatal("Defaults should not be nil")
	}
	if _, ok := resp.Defaults["sticky_mode"]; !ok {
		t.Error("Defaults should contain sticky_mode")
	}
	if got := resp.Defaults[defaults.ConfigKeyWebSocketProbeClientModel]; got != "true" {
		t.Errorf(
			"Defaults[%s] = %q, want %q",
			defaults.ConfigKeyWebSocketProbeClientModel,
			got,
			"true",
		)
	}
}

func TestGetConfig_FiltersStaleKeys(t *testing.T) {
	h, st, _ := testHandler()

	// Simulate stale config entries from previous versions
	st.config["sticky_mode"] = "model"
	st.config["max_retries"] = "10"         // Invalid: should be filtered out
	st.config["invalid_key"] = "some_value" // Invalid: should be filtered out

	req := httptest.NewRequest(http.MethodGet, "/admin/api/config", nil)
	w := httptest.NewRecorder()

	h.GetConfig(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp ConfigResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Valid key should be present
	if resp.Values["sticky_mode"] != "model" {
		t.Errorf("Values[sticky_mode] = %q, want %q", resp.Values["sticky_mode"], "model")
	}

	// Invalid keys should be filtered out
	if _, ok := resp.Values["max_retries"]; ok {
		t.Error("Values should not contain stale key 'max_retries'")
	}
	if _, ok := resp.Values["invalid_key"]; ok {
		t.Error("Values should not contain invalid key 'invalid_key'")
	}
}

func TestGetConfig_Error(t *testing.T) {
	h, st, _ := testHandler()
	st.configErr = errors.New("database error")

	req := httptest.NewRequest(http.MethodGet, "/admin/api/config", nil)
	w := httptest.NewRecorder()

	h.GetConfig(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestUpdateConfig(t *testing.T) {
	h, st, _ := testHandler()

	body := `{"global_max_attempts": "5", "sticky_ttl": "600", "websocket_probe_client_model": "false"}`

	req := httptest.NewRequest(http.MethodPut, "/admin/api/config", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.UpdateConfig(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	// Verify store was updated
	if st.config["global_max_attempts"] != "5" {
		t.Errorf("global_max_attempts = %q, want %q", st.config["global_max_attempts"], "5")
	}
	if st.config["sticky_ttl"] != "600" {
		t.Errorf("sticky_ttl = %q, want %q", st.config["sticky_ttl"], "600")
	}
	if got := st.config[defaults.ConfigKeyWebSocketProbeClientModel]; got != "false" {
		t.Errorf(
			"%s = %q, want %q",
			defaults.ConfigKeyWebSocketProbeClientModel,
			got,
			"false",
		)
	}

	// Verify response format
	var resp ConfigResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Values["global_max_attempts"] != "5" {
		t.Errorf("Values[global_max_attempts] = %q, want %q", resp.Values["global_max_attempts"], "5")
	}
	if resp.Defaults == nil {
		t.Fatal("Defaults should not be nil")
	}
}

func TestUpdateConfig_InvalidJSON(t *testing.T) {
	h, _, _ := testHandler()

	req := httptest.NewRequest(http.MethodPut, "/admin/api/config", bytes.NewBufferString("invalid"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.UpdateConfig(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestUpdateConfig_SetError(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	st := newConfigErrorStore()
	st.setErr = errors.New("database error")

	h := NewHandler(Config{
		Store:       st,
		Concurrency: &mockConcurrencyTracker{},
		Logger:      logger,
	})

	body := `{"global_max_attempts": "5"}`

	req := httptest.NewRequest(http.MethodPut, "/admin/api/config", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.UpdateConfig(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestUpdateConfig_GetAfterSetError(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	st := newConfigErrorStore()
	st.getErr = errors.New("database error")
	st.afterSet = true

	h := NewHandler(Config{
		Store:       st,
		Concurrency: &mockConcurrencyTracker{},
		Logger:      logger,
	})

	body := `{"global_max_attempts": "5"}`

	req := httptest.NewRequest(http.MethodPut, "/admin/api/config", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.UpdateConfig(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestUpdateConfig_InvalidValues(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantMsg string
	}{
		{
			name:    "circuit_failure negative",
			body:    `{"circuit_failure": "-1"}`,
			wantMsg: "must be a positive integer",
		},
		{
			name:    "circuit_failure not number",
			body:    `{"circuit_failure": "abc"}`,
			wantMsg: "must be a valid integer",
		},
		{
			name:    "sticky_ttl zero",
			body:    `{"sticky_ttl": "0"}`,
			wantMsg: "must be a positive integer",
		},
		{
			name:    "global_max_attempts negative",
			body:    `{"global_max_attempts": "-5"}`,
			wantMsg: "must be a non-negative integer",
		},
		{
			name:    "trust_proxy_headers invalid",
			body:    `{"trust_proxy_headers": "maybe"}`,
			wantMsg: "must be 'true' or 'false'",
		},
		{
			name:    "websocket_probe_client_model invalid",
			body:    `{"websocket_probe_client_model": "maybe"}`,
			wantMsg: "must be 'true' or 'false'",
		},
		{
			name:    "sticky_mode invalid",
			body:    `{"sticky_mode": "yes"}`,
			wantMsg: "must be 'off', 'api_type', or 'model'",
		},
		{
			name:    "auth_mode invalid",
			body:    `{"auth_mode": "invalid"}`,
			wantMsg: "must be 'auto', 'bearer', or 'x-api-key'",
		},
		{
			name:    "inter_group_strategy invalid",
			body:    `{"inter_group_strategy": "invalid"}`,
			wantMsg: "must be 'priority', 'random', or 'weight'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, _, _ := testHandler()

			req := httptest.NewRequest(http.MethodPut, "/admin/api/config", bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			h.UpdateConfig(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d; body: %s", w.Code, http.StatusBadRequest, w.Body.String())
			}
			if !bytes.Contains(w.Body.Bytes(), []byte(tt.wantMsg)) {
				t.Errorf("body = %s, want to contain %q", w.Body.String(), tt.wantMsg)
			}
		})
	}
}

func TestUpdateConfig_ValidValues(t *testing.T) {
	tests := []struct {
		name  string
		body  string
		key   string
		value string
	}{
		{
			name:  "trust_proxy_headers true",
			body:  `{"trust_proxy_headers": "true"}`,
			key:   "trust_proxy_headers",
			value: "true",
		},
		{
			name:  "trust_proxy_headers false",
			body:  `{"trust_proxy_headers": "false"}`,
			key:   "trust_proxy_headers",
			value: "false",
		},
		{
			name:  "trust_proxy_headers 1",
			body:  `{"trust_proxy_headers": "1"}`,
			key:   "trust_proxy_headers",
			value: "1",
		},
		{
			name:  "global_max_attempts zero",
			body:  `{"global_max_attempts": "0"}`,
			key:   "global_max_attempts",
			value: "0",
		},
		{
			name:  "auth_mode auto",
			body:  `{"auth_mode": "auto"}`,
			key:   "auth_mode",
			value: "auto",
		},
		{
			name:  "inter_group_strategy priority",
			body:  `{"inter_group_strategy": "priority"}`,
			key:   "inter_group_strategy",
			value: "priority",
		},
		{
			name:  "user_header any string",
			body:  `{"user_header": "X-Custom-User"}`,
			key:   "user_header",
			value: "X-Custom-User",
		},
		{
			name:  "sticky_mode model",
			body:  `{"sticky_mode": "model"}`,
			key:   "sticky_mode",
			value: "model",
		},
		{
			name:  "websocket_probe_client_model false",
			body:  `{"websocket_probe_client_model": "false"}`,
			key:   defaults.ConfigKeyWebSocketProbeClientModel,
			value: "false",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, st, _ := testHandler()

			req := httptest.NewRequest(http.MethodPut, "/admin/api/config", bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			h.UpdateConfig(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
			}
			if st.config[tt.key] != tt.value {
				t.Errorf("%s = %q, want %q", tt.key, st.config[tt.key], tt.value)
			}

			// Verify response format includes both defaults and values
			var resp ConfigResponse
			if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
				t.Fatalf("failed to decode response: %v", err)
			}
			if resp.Defaults == nil {
				t.Error("Defaults should not be nil")
			}
			if resp.Values[tt.key] != tt.value {
				t.Errorf("Values[%s] = %q, want %q", tt.key, resp.Values[tt.key], tt.value)
			}
		})
	}
}

// Health Tests

func TestGetHealth(t *testing.T) {
	h, st, _ := testHandler()

	st.healthStates["p1"] = &model.HealthState{ProviderID: "p1", Available: true}
	st.healthStates["p2"] = &model.HealthState{ProviderID: "p2", Available: false}

	req := httptest.NewRequest(http.MethodGet, "/admin/api/health", nil)
	w := httptest.NewRecorder()

	h.GetHealth(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var states []model.HealthState
	if err := json.NewDecoder(w.Body).Decode(&states); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(states) != 2 {
		t.Errorf("len(states) = %d, want 2", len(states))
	}
}

func TestGetHealth_Error(t *testing.T) {
	h, st, _ := testHandler()
	st.healthErr = errors.New("database error")

	req := httptest.NewRequest(http.MethodGet, "/admin/api/health", nil)
	w := httptest.NewRecorder()

	h.GetHealth(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

// Status Tests

func TestGetStatus(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	st := newMockStore()
	concurrency := &mockConcurrencyTracker{
		counts: map[string]int64{"p1": 5, "p2": 3},
	}

	h := NewHandler(Config{
		Store:       st,
		Concurrency: concurrency,
		Logger:      logger,
	})

	st.providers["p1"] = &model.Provider{ID: "p1", Name: "Provider 1", Enabled: true}
	st.providers["p2"] = &model.Provider{ID: "p2", Name: "Provider 2", Enabled: false}
	st.healthStates["p1"] = &model.HealthState{ProviderID: "p1", Available: true, SuccessCount: 100}

	req := httptest.NewRequest(http.MethodGet, "/admin/api/status", nil)
	w := httptest.NewRecorder()

	h.GetStatus(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var status SystemStatus
	if err := json.NewDecoder(w.Body).Decode(&status); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(status.Providers) != 2 {
		t.Errorf("len(Providers) = %d, want 2", len(status.Providers))
	}

	// Check that concurrency counts are included
	var p1Status *ProviderStatus
	for i := range status.Providers {
		if status.Providers[i].ID == "p1" {
			p1Status = &status.Providers[i]
			break
		}
	}

	if p1Status == nil {
		t.Fatal("p1 not found in status")
	}

	if p1Status.CurrentRequests != 5 {
		t.Errorf("p1 CurrentRequests = %d, want 5", p1Status.CurrentRequests)
	}
}

func TestGetStatus_Error(t *testing.T) {
	h, st, _ := testHandler()
	st.listErr = errors.New("database error")

	req := httptest.NewRequest(http.MethodGet, "/admin/api/status", nil)
	w := httptest.NewRecorder()

	h.GetStatus(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

// Logs Tests

func TestGetLogs(t *testing.T) {
	h, st, _ := testHandler()

	for i := 0; i < 50; i++ {
		st.logs = append(st.logs, model.RequestLog{ID: uint(i + 1)})
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/api/logs?limit=10&offset=5", nil)
	w := httptest.NewRecorder()

	h.GetLogs(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp LogsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(resp.Logs) != 10 {
		t.Errorf("len(Logs) = %d, want 10", len(resp.Logs))
	}
	if resp.Limit != 10 {
		t.Errorf("Limit = %d, want 10", resp.Limit)
	}
	if resp.Offset != 5 {
		t.Errorf("Offset = %d, want 5", resp.Offset)
	}
}

func TestGetLogs_Defaults(t *testing.T) {
	h, st, _ := testHandler()

	for i := 0; i < 50; i++ {
		st.logs = append(st.logs, model.RequestLog{ID: uint(i + 1)})
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/api/logs", nil)
	w := httptest.NewRecorder()

	h.GetLogs(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp LogsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Limit != 100 {
		t.Errorf("Limit = %d, want 100", resp.Limit)
	}
	if resp.Offset != 0 {
		t.Errorf("Offset = %d, want 0", resp.Offset)
	}
}

func TestGetLogs_MaxLimit(t *testing.T) {
	h, _, _ := testHandler()

	req := httptest.NewRequest(http.MethodGet, "/admin/api/logs?limit=5000", nil)
	w := httptest.NewRecorder()

	h.GetLogs(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp LogsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Limit != 1000 {
		t.Errorf("Limit = %d, want 1000 (capped)", resp.Limit)
	}
}

func TestGetLogs_Error(t *testing.T) {
	h, st, _ := testHandler()
	st.logsErr = errors.New("database error")

	req := httptest.NewRequest(http.MethodGet, "/admin/api/logs", nil)
	w := httptest.NewRecorder()

	h.GetLogs(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestGetLogs_WithFilters(t *testing.T) {
	h, st, _ := testHandler()
	now := time.Now()

	// Add test logs with different properties
	st.logs = []model.RequestLog{
		{ID: 1, ProviderID: "p1", APIType: "claude", Success: true, UserID: "user1", LatencyMs: 100, CreatedAt: now},
		{ID: 2, ProviderID: "p1", APIType: "claude", Success: false, UserID: "user1", LatencyMs: 500, CreatedAt: now},
		{ID: 3, ProviderID: "p2", APIType: "codex", Success: true, UserID: "user2", LatencyMs: 200, CreatedAt: now},
	}

	// Test provider_id filter
	req := httptest.NewRequest(http.MethodGet, "/admin/api/logs?provider_id=p1", nil)
	w := httptest.NewRecorder()
	h.GetLogs(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp LogsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if len(resp.Logs) != 2 {
		t.Errorf("expected 2 logs for p1, got %d", len(resp.Logs))
	}
	if resp.Total != 2 {
		t.Errorf("expected total=2, got %d", resp.Total)
	}
}

func TestGetLogs_SortBy(t *testing.T) {
	h, st, _ := testHandler()
	now := time.Now()

	st.logs = []model.RequestLog{
		{ID: 1, ProviderID: "p1", LatencyMs: 100, CreatedAt: now},
		{ID: 2, ProviderID: "p2", LatencyMs: 500, CreatedAt: now},
		{ID: 3, ProviderID: "p3", LatencyMs: 200, CreatedAt: now},
	}

	// Test sort_by=latency_ms&sort_order=desc
	req := httptest.NewRequest(http.MethodGet, "/admin/api/logs?sort_by=latency_ms&sort_order=desc", nil)
	w := httptest.NewRecorder()
	h.GetLogs(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp LogsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp.SortBy != "latency_ms" {
		t.Errorf("expected sort_by=latency_ms, got %s", resp.SortBy)
	}
	if resp.SortOrder != "desc" {
		t.Errorf("expected sort_order=desc, got %s", resp.SortOrder)
	}
}

func TestGetLogs_InvalidSuccess(t *testing.T) {
	h, _, _ := testHandler()

	req := httptest.NewRequest(http.MethodGet, "/admin/api/logs?success=invalid", nil)
	w := httptest.NewRecorder()
	h.GetLogs(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestGetLogs_InvalidSortBy(t *testing.T) {
	h, _, _ := testHandler()

	req := httptest.NewRequest(http.MethodGet, "/admin/api/logs?sort_by=invalid", nil)
	w := httptest.NewRecorder()
	h.GetLogs(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestGetLogs_InvalidSortOrder(t *testing.T) {
	h, _, _ := testHandler()

	req := httptest.NewRequest(http.MethodGet, "/admin/api/logs?sort_order=invalid", nil)
	w := httptest.NewRecorder()
	h.GetLogs(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestGetLogs_InvalidStartTime(t *testing.T) {
	h, _, _ := testHandler()

	req := httptest.NewRequest(http.MethodGet, "/admin/api/logs?start_time=invalid", nil)
	w := httptest.NewRecorder()
	h.GetLogs(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestGetLogs_InvalidEndTime(t *testing.T) {
	h, _, _ := testHandler()

	req := httptest.NewRequest(http.MethodGet, "/admin/api/logs?end_time=invalid", nil)
	w := httptest.NewRecorder()
	h.GetLogs(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestGetLogs_InvalidMinLatency(t *testing.T) {
	h, _, _ := testHandler()

	req := httptest.NewRequest(http.MethodGet, "/admin/api/logs?min_latency=invalid", nil)
	w := httptest.NewRecorder()
	h.GetLogs(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestGetLogs_NegativeMinLatency(t *testing.T) {
	h, _, _ := testHandler()

	req := httptest.NewRequest(http.MethodGet, "/admin/api/logs?min_latency=-100", nil)
	w := httptest.NewRecorder()
	h.GetLogs(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestGetLogs_ValidTimeRange(t *testing.T) {
	h, st, _ := testHandler()
	now := time.Now().UTC()

	st.logs = []model.RequestLog{
		{ID: 1, ProviderID: "p1", CreatedAt: now.Add(-2 * time.Hour)},
		{ID: 2, ProviderID: "p2", CreatedAt: now.Add(-1 * time.Hour)},
		{ID: 3, ProviderID: "p3", CreatedAt: now},
	}

	// Use UTC format to avoid URL encoding issues with timezone offset
	startTime := now.Add(-90 * time.Minute).Format(time.RFC3339)
	endTime := now.Add(-30 * time.Minute).Format(time.RFC3339)

	req := httptest.NewRequest(http.MethodGet, "/admin/api/logs", nil)
	q := req.URL.Query()
	q.Set("start_time", startTime)
	q.Set("end_time", endTime)
	req.URL.RawQuery = q.Encode()

	w := httptest.NewRecorder()
	h.GetLogs(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp LogsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if len(resp.Logs) != 1 {
		t.Errorf("expected 1 log in time range, got %d", len(resp.Logs))
	}
}
