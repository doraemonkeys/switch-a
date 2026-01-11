package admin

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"switch-a/internal/model"

	"go.uber.org/zap"
)

// Config Tests

func TestGetConfig(t *testing.T) {
	h, st, _ := testHandler()

	st.config["sticky_enabled"] = "true"
	st.config["max_retries"] = "3"

	req := httptest.NewRequest(http.MethodGet, "/admin/api/config", nil)
	w := httptest.NewRecorder()

	h.GetConfig(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var config map[string]string
	if err := json.NewDecoder(w.Body).Decode(&config); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if config["sticky_enabled"] != "true" {
		t.Errorf("sticky_enabled = %q, want %q", config["sticky_enabled"], "true")
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

	body := `{"max_retries": "5", "sticky_ttl": "600"}`

	req := httptest.NewRequest(http.MethodPut, "/admin/api/config", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.UpdateConfig(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	if st.config["max_retries"] != "5" {
		t.Errorf("max_retries = %q, want %q", st.config["max_retries"], "5")
	}
	if st.config["sticky_ttl"] != "600" {
		t.Errorf("sticky_ttl = %q, want %q", st.config["sticky_ttl"], "600")
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
	st := &configErrorStore{
		config:   make(map[string]string),
		setErr:   errors.New("database error"),
		getErr:   nil,
		afterSet: false,
	}

	h := NewHandler(Config{
		Store:       st,
		Concurrency: &mockConcurrencyTracker{},
		Logger:      logger,
	})

	body := `{"max_retries": "5"}`

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
	st := &configErrorStore{
		config:   make(map[string]string),
		setErr:   nil,
		getErr:   errors.New("database error"),
		afterSet: true,
	}

	h := NewHandler(Config{
		Store:       st,
		Concurrency: &mockConcurrencyTracker{},
		Logger:      logger,
	})

	body := `{"max_retries": "5"}`

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
			name:    "max_retries negative",
			body:    `{"max_retries": "-5"}`,
			wantMsg: "must be a non-negative integer",
		},
		{
			name:    "trust_proxy_headers invalid",
			body:    `{"trust_proxy_headers": "maybe"}`,
			wantMsg: "must be 'true' or 'false'",
		},
		{
			name:    "sticky_enabled invalid",
			body:    `{"sticky_enabled": "yes"}`,
			wantMsg: "must be 'true' or 'false'",
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
			name:  "max_retries zero",
			body:  `{"max_retries": "0"}`,
			key:   "max_retries",
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
