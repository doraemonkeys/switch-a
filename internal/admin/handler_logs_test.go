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

func lifecycleBoolPtr(v bool) *bool {
	return &v
}

func lifecycleTerminalCausePtr(v model.TerminalCause) *model.TerminalCause {
	return &v
}

func lifecycleProbeOutcomePtr(v model.WebSocketProbeOutcome) *model.WebSocketProbeOutcome {
	return &v
}

func TestGetLog_Success(t *testing.T) {
	h, st, _ := testHandler()

	now := time.Now()
	committed := true
	probeOutcome := model.WebSocketProbeOutcomeUnsupported
	st.logs = []model.RequestLog{
		{
			ID:               1,
			RequestID:        "req-123",
			ProviderID:       "provider-1",
			APIType:          "claude",
			Model:            "claude-3",
			StatusCode:       200,
			Success:          true,
			IsWebSocket:      true,
			StickyWritten:    lifecycleBoolPtr(true),
			SessionCommitted: &committed,
			ProbeOutcome:     &probeOutcome,
			TerminalCause:    lifecycleTerminalCausePtr(model.TerminalCleanClose),
			CreatedAt:        now,
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/api/logs/1", nil)
	setPathValue(req, "id", "1")
	w := httptest.NewRecorder()

	h.GetLog(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var log model.RequestLog
	if err := json.NewDecoder(w.Body).Decode(&log); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if log.ID != 1 {
		t.Errorf("log.ID = %d, want 1", log.ID)
	}
	if log.RequestID != "req-123" {
		t.Errorf("log.RequestID = %q, want %q", log.RequestID, "req-123")
	}
	if log.ProviderID != "provider-1" {
		t.Errorf("log.ProviderID = %q, want %q", log.ProviderID, "provider-1")
	}
	if log.SessionCommitted == nil || !*log.SessionCommitted {
		t.Fatalf("log.SessionCommitted = %v, want true", log.SessionCommitted)
	}
	if log.StickyWritten == nil || !*log.StickyWritten {
		t.Fatalf("log.StickyWritten = %v, want true", log.StickyWritten)
	}
	if log.ProbeOutcome == nil || *log.ProbeOutcome != model.WebSocketProbeOutcomeUnsupported {
		t.Fatalf("log.ProbeOutcome = %v, want %q", log.ProbeOutcome, model.WebSocketProbeOutcomeUnsupported)
	}
	if log.TerminalCause == nil || *log.TerminalCause != model.TerminalCleanClose {
		t.Fatalf("log.TerminalCause = %v, want %q", log.TerminalCause, model.TerminalCleanClose)
	}
}

func TestGetLog_NotFound(t *testing.T) {
	h, st, _ := testHandler()
	st.logs = []model.RequestLog{} // Empty logs

	req := httptest.NewRequest(http.MethodGet, "/admin/api/logs/999", nil)
	setPathValue(req, "id", "999")
	w := httptest.NewRecorder()

	h.GetLog(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d; body: %s", w.Code, http.StatusNotFound, w.Body.String())
	}

	var errResp model.ErrorResponse
	if err := json.NewDecoder(w.Body).Decode(&errResp); err != nil {
		t.Fatalf("failed to decode error response: %v", err)
	}
	if errResp.Code != ErrCodeNotFound {
		t.Errorf("error code = %q, want %q", errResp.Code, ErrCodeNotFound)
	}
}

func TestGetLog_EmptyID(t *testing.T) {
	h, _, _ := testHandler()

	req := httptest.NewRequest(http.MethodGet, "/admin/api/logs/", nil)
	// Not setting path value simulates empty ID
	w := httptest.NewRecorder()

	h.GetLog(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	var errResp model.ErrorResponse
	if err := json.NewDecoder(w.Body).Decode(&errResp); err != nil {
		t.Fatalf("failed to decode error response: %v", err)
	}
	if errResp.Code != ErrCodeValidation {
		t.Errorf("error code = %q, want %q", errResp.Code, ErrCodeValidation)
	}
}

func TestGetLog_InvalidID(t *testing.T) {
	h, _, _ := testHandler()

	tests := []struct {
		name string
		id   string
	}{
		{"non-numeric", "abc"},
		{"negative", "-1"},
		{"decimal", "1.5"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/admin/api/logs/"+tt.id, nil)
			setPathValue(req, "id", tt.id)
			w := httptest.NewRecorder()

			h.GetLog(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
			}

			var errResp model.ErrorResponse
			if err := json.NewDecoder(w.Body).Decode(&errResp); err != nil {
				t.Fatalf("failed to decode error response: %v", err)
			}
			if errResp.Code != ErrCodeValidation {
				t.Errorf("error code = %q, want %q", errResp.Code, ErrCodeValidation)
			}
		})
	}
}

func TestGetLog_InternalError(t *testing.T) {
	h, st, _ := testHandler()
	st.logsErr = errors.New("database error")

	req := httptest.NewRequest(http.MethodGet, "/admin/api/logs/1", nil)
	setPathValue(req, "id", "1")
	w := httptest.NewRecorder()

	h.GetLog(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestGetLog_WithAttempts(t *testing.T) {
	h, st, _ := testHandler()

	now := time.Now()
	st.logs = []model.RequestLog{
		{
			ID:         1,
			RequestID:  "req-with-attempts",
			ProviderID: "provider-1",
			APIType:    "claude",
			Model:      "claude-3",
			StatusCode: 200,
			Success:    true,
			RetryCount: 2,
			CreatedAt:  now,
		},
	}

	// Add attempts to the mock store
	st.attempts = map[string][]model.RequestAttempt{
		"req-with-attempts": {
			{
				ID:         1,
				RequestID:  "req-with-attempts",
				ProviderID: "provider-1",
				Attempt:    1,
				StatusCode: 503,
				Error:      "service unavailable",
				LatencyMs:  100,
				CreatedAt:  now,
			},
			{
				ID:         2,
				RequestID:  "req-with-attempts",
				ProviderID: "provider-2",
				Attempt:    2,
				StatusCode: 429,
				Error:      "rate limited",
				LatencyMs:  50,
				CreatedAt:  now.Add(100 * time.Millisecond),
			},
			{
				ID:         3,
				RequestID:  "req-with-attempts",
				ProviderID: "provider-3",
				Attempt:    3,
				StatusCode: 200,
				Error:      "",
				LatencyMs:  200,
				CreatedAt:  now.Add(200 * time.Millisecond),
			},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/api/logs/1", nil)
	setPathValue(req, "id", "1")
	w := httptest.NewRecorder()

	h.GetLog(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var log model.RequestLog
	if err := json.NewDecoder(w.Body).Decode(&log); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(log.Attempts) != 3 {
		t.Fatalf("expected 3 attempts, got %d", len(log.Attempts))
	}
	if log.Attempts[0].ProviderID != "provider-1" {
		t.Errorf("first attempt provider_id = %q, want %q", log.Attempts[0].ProviderID, "provider-1")
	}
}

func TestGetLog_WithoutRequestID(t *testing.T) {
	h, st, _ := testHandler()

	now := time.Now()
	// Log without RequestID should not attempt to fetch attempts
	st.logs = []model.RequestLog{
		{
			ID:         1,
			RequestID:  "", // Empty request ID
			ProviderID: "provider-1",
			APIType:    "claude",
			StatusCode: 200,
			Success:    true,
			CreatedAt:  now,
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/api/logs/1", nil)
	setPathValue(req, "id", "1")
	w := httptest.NewRecorder()

	h.GetLog(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var log model.RequestLog
	if err := json.NewDecoder(w.Body).Decode(&log); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Should have no attempts since RequestID is empty
	if len(log.Attempts) != 0 {
		t.Errorf("expected no attempts for empty request_id, got %d", len(log.Attempts))
	}
}

func TestGetLog_AttemptsErrorDoesNotFailRequest(t *testing.T) {
	h, st, _ := testHandler()

	now := time.Now()
	st.logs = []model.RequestLog{
		{
			ID:         1,
			RequestID:  "req-with-error",
			ProviderID: "provider-1",
			APIType:    "claude",
			StatusCode: 200,
			Success:    true,
			CreatedAt:  now,
		},
	}

	// Set attempts error to simulate database failure for attempts
	st.attemptsErr = errors.New("database error")

	req := httptest.NewRequest(http.MethodGet, "/admin/api/logs/1", nil)
	setPathValue(req, "id", "1")
	w := httptest.NewRecorder()

	h.GetLog(w, req)

	// Should still return 200 OK even if attempts fetch fails
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var log model.RequestLog
	if err := json.NewDecoder(w.Body).Decode(&log); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Should have no attempts since fetch failed
	if len(log.Attempts) != 0 {
		t.Errorf("expected no attempts on error, got %d", len(log.Attempts))
	}
}

// Tests for GetLogs handler

func TestGetLogs_Success(t *testing.T) {
	h, st, _ := testHandler()

	now := time.Now()
	st.logs = []model.RequestLog{
		{
			ID:           1,
			ProviderID:   "provider-1",
			APIType:      "claude",
			Success:      true,
			IsWebSocket:  true,
			ProbeOutcome: lifecycleProbeOutcomePtr(model.WebSocketProbeOutcomeBypassed),
			CreatedAt:    now,
		},
		{ID: 2, ProviderID: "provider-2", APIType: "codex", Success: false, CreatedAt: now.Add(-time.Hour)},
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/api/logs", nil)
	w := httptest.NewRecorder()

	h.GetLogs(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp LogsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(resp.Logs) != 2 {
		t.Errorf("expected 2 logs, got %d", len(resp.Logs))
	}
	if resp.Total != 2 {
		t.Errorf("expected total 2, got %d", resp.Total)
	}
	if resp.Limit != DefaultLogsLimit {
		t.Errorf("expected limit %d, got %d", DefaultLogsLimit, resp.Limit)
	}
	if resp.Offset != 0 {
		t.Errorf("expected offset 0, got %d", resp.Offset)
	}
	if resp.Logs[0].ProbeOutcome == nil || *resp.Logs[0].ProbeOutcome != model.WebSocketProbeOutcomeBypassed {
		t.Fatalf("resp.Logs[0].ProbeOutcome = %v, want %q", resp.Logs[0].ProbeOutcome, model.WebSocketProbeOutcomeBypassed)
	}
}

func TestGetLogs_WithQueryFilters(t *testing.T) {
	h, st, _ := testHandler()

	now := time.Now()
	committed := true
	uncommitted := false
	st.logs = []model.RequestLog{
		{
			ID:               1,
			ProviderID:       "provider-1",
			APIType:          "claude",
			Success:          true,
			UserID:           "user-1",
			IsWebSocket:      true,
			StickyWritten:    lifecycleBoolPtr(true),
			SessionCommitted: &committed,
			ProbeOutcome:     lifecycleProbeOutcomePtr(model.WebSocketProbeOutcomeBypassed),
			TerminalCause:    lifecycleTerminalCausePtr(model.TerminalCleanClose),
			CreatedAt:        now,
		},
		{
			ID:               2,
			ProviderID:       "provider-2",
			APIType:          "codex",
			Success:          false,
			UserID:           "user-2",
			IsWebSocket:      true,
			StickyWritten:    lifecycleBoolPtr(false),
			SessionCommitted: &uncommitted,
			ProbeOutcome:     lifecycleProbeOutcomePtr(model.WebSocketProbeOutcomeTransportFailed),
			TerminalCause:    lifecycleTerminalCausePtr(model.TerminalUpstreamSemanticError),
			CreatedAt:        now.Add(-time.Hour),
		},
		{
			ID:               3,
			ProviderID:       "provider-1",
			APIType:          "claude",
			Success:          true,
			UserID:           "user-1",
			IsWebSocket:      true,
			StickyWritten:    lifecycleBoolPtr(false),
			SessionCommitted: &committed,
			ProbeOutcome:     lifecycleProbeOutcomePtr(model.WebSocketProbeOutcomeObservedUsableModel),
			TerminalCause:    lifecycleTerminalCausePtr(model.TerminalClientDisconnect),
			CreatedAt:        now.Add(-2 * time.Hour),
		},
	}

	tests := []struct {
		name          string
		query         string
		expectedCount int
	}{
		{"filter by provider_id", "?provider_id=provider-1", 2},
		{"filter by api_type", "?api_type=codex", 1},
		{"filter by success true", "?success=true", 2},
		{"filter by success false", "?success=false", 1},
		{"filter by user_id", "?user_id=user-1", 2},
		{"filter by sticky_written", "?sticky_written=true", 1},
		{"filter by session_committed true", "?session_committed=true", 2},
		{"filter by session_committed false", "?session_committed=false", 1},
		{"filter by probe_outcome", "?probe_outcome=transport_failed", 1},
		{"filter by terminal_cause", "?terminal_cause=client_disconnect", 1},
		{"multiple filters", "?provider_id=provider-1&api_type=claude", 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/admin/api/logs"+tt.query, nil)
			w := httptest.NewRecorder()

			h.GetLogs(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
			}

			var resp LogsResponse
			if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
				t.Fatalf("failed to decode response: %v", err)
			}

			if len(resp.Logs) != tt.expectedCount {
				t.Errorf("expected %d logs, got %d", tt.expectedCount, len(resp.Logs))
			}
		})
	}
}

func TestGetLogs_Pagination(t *testing.T) {
	h, st, _ := testHandler()

	now := time.Now()
	// Create 5 logs
	for i := 1; i <= 5; i++ {
		st.logs = append(st.logs, model.RequestLog{
			ID:         uint(i),
			ProviderID: "provider-1",
			APIType:    "claude",
			Success:    true,
			CreatedAt:  now.Add(-time.Duration(i) * time.Hour),
		})
	}

	tests := []struct {
		name          string
		query         string
		expectedCount int
		expectedTotal int64
	}{
		{"default limit", "", 5, 5},
		{"limit 2", "?limit=2", 2, 5},
		{"limit 2 offset 2", "?limit=2&offset=2", 2, 5},
		{"offset beyond results", "?offset=10", 0, 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/admin/api/logs"+tt.query, nil)
			w := httptest.NewRecorder()

			h.GetLogs(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
			}

			var resp LogsResponse
			if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
				t.Fatalf("failed to decode response: %v", err)
			}

			if len(resp.Logs) != tt.expectedCount {
				t.Errorf("expected %d logs, got %d", tt.expectedCount, len(resp.Logs))
			}
			if resp.Total != tt.expectedTotal {
				t.Errorf("expected total %d, got %d", tt.expectedTotal, resp.Total)
			}
		})
	}
}

func TestGetLogs_MaxLimitEnforced(t *testing.T) {
	h, _, _ := testHandler()

	// Request limit above maximum
	req := httptest.NewRequest(http.MethodGet, "/admin/api/logs?limit=5000", nil)
	w := httptest.NewRecorder()

	h.GetLogs(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp LogsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Limit should be capped at MaxLogsLimit
	if resp.Limit != MaxLogsLimit {
		t.Errorf("expected limit to be capped at %d, got %d", MaxLogsLimit, resp.Limit)
	}
}

func TestGetLogs_SortParams(t *testing.T) {
	h, st, _ := testHandler()

	now := time.Now()
	st.logs = []model.RequestLog{
		{ID: 1, ProviderID: "provider-1", Success: true, CreatedAt: now},
	}

	tests := []struct {
		name          string
		query         string
		expectedSort  string
		expectedOrder string
	}{
		{"default sort", "", "created_at", "desc"},
		{"sort by latency_ms", "?sort_by=latency_ms", "latency_ms", "desc"},
		{"sort asc", "?sort_order=asc", "created_at", "asc"},
		{"sort by latency_ms asc", "?sort_by=latency_ms&sort_order=asc", "latency_ms", "asc"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/admin/api/logs"+tt.query, nil)
			w := httptest.NewRecorder()

			h.GetLogs(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
			}

			var resp LogsResponse
			if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
				t.Fatalf("failed to decode response: %v", err)
			}

			if resp.SortBy != tt.expectedSort {
				t.Errorf("expected sort_by %q, got %q", tt.expectedSort, resp.SortBy)
			}
			if resp.SortOrder != tt.expectedOrder {
				t.Errorf("expected sort_order %q, got %q", tt.expectedOrder, resp.SortOrder)
			}
		})
	}
}

func TestGetLogs_InvalidParams(t *testing.T) {
	h, _, _ := testHandler()

	tests := []struct {
		name  string
		query string
	}{
		{"invalid limit non-numeric", "?limit=abc"},
		{"invalid limit negative", "?limit=-1"},
		{"invalid limit zero", "?limit=0"},
		{"invalid offset non-numeric", "?offset=xyz"},
		{"invalid offset negative", "?offset=-5"},
		{"invalid success", "?success=maybe"},
		{"invalid is_sse", "?is_sse=maybe"},
		{"invalid is_websocket", "?is_websocket=maybe"},
		{"invalid sticky_written", "?sticky_written=maybe"},
		{"invalid session_committed", "?session_committed=maybe"},
		{"invalid probe_outcome", "?probe_outcome=not_real"},
		{"invalid terminal_cause", "?terminal_cause=not_real"},
		{"invalid start_time", "?start_time=not-a-date"},
		{"invalid end_time", "?end_time=invalid"},
		{"invalid min_latency non-numeric", "?min_latency=slow"},
		{"invalid min_latency negative", "?min_latency=-100"},
		{"invalid min_retry_count non-numeric", "?min_retry_count=many"},
		{"invalid min_retry_count negative", "?min_retry_count=-1"},
		{"invalid has_retries", "?has_retries=maybe"},
		{"invalid sort_by", "?sort_by=invalid_field"},
		{"invalid sort_order", "?sort_order=random"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/admin/api/logs"+tt.query, nil)
			w := httptest.NewRecorder()

			h.GetLogs(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d; body: %s", w.Code, http.StatusBadRequest, w.Body.String())
			}

			var errResp model.ErrorResponse
			if err := json.NewDecoder(w.Body).Decode(&errResp); err != nil {
				t.Fatalf("failed to decode error response: %v", err)
			}
			if errResp.Code != ErrCodeValidation {
				t.Errorf("error code = %q, want %q", errResp.Code, ErrCodeValidation)
			}
		})
	}
}

func TestGetLogs_StoreErrors(t *testing.T) {
	h, st, _ := testHandler()

	t.Run("ListLogs error", func(t *testing.T) {
		st.logsErr = errors.New("database error")

		req := httptest.NewRequest(http.MethodGet, "/admin/api/logs", nil)
		w := httptest.NewRecorder()

		h.GetLogs(w, req)

		if w.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
		}
	})
}

// Tests for parseLogFilter helper function

func TestParseLogFilter_Defaults(t *testing.T) {
	filter, errMsg := parseLogFilter(map[string][]string{})

	if errMsg != "" {
		t.Errorf("unexpected error: %s", errMsg)
	}
	if filter.Limit != DefaultLogsLimit {
		t.Errorf("expected limit %d, got %d", DefaultLogsLimit, filter.Limit)
	}
	if filter.Offset != 0 {
		t.Errorf("expected offset 0, got %d", filter.Offset)
	}
	if filter.SortBy != "created_at" {
		t.Errorf("expected sort_by 'created_at', got %q", filter.SortBy)
	}
	if filter.SortOrder != "desc" {
		t.Errorf("expected sort_order 'desc', got %q", filter.SortOrder)
	}
	if filter.StickyWritten != nil {
		t.Errorf("expected sticky_written to be nil, got %v", filter.StickyWritten)
	}
	if filter.SessionCommitted != nil {
		t.Errorf("expected session_committed to be nil, got %v", filter.SessionCommitted)
	}
	if filter.ProbeOutcome != "" {
		t.Errorf("expected empty probe_outcome, got %q", filter.ProbeOutcome)
	}
	if filter.TerminalCause != "" {
		t.Errorf("expected empty terminal_cause, got %q", filter.TerminalCause)
	}
}

func TestParseLogFilter_AllParams(t *testing.T) {
	startTime := "2024-01-01T00:00:00Z"
	endTime := "2024-12-31T23:59:59Z"

	query := map[string][]string{
		"limit":             {"50"},
		"offset":            {"10"},
		"provider_id":       {"provider-1"},
		"api_type":          {"claude"},
		"success":           {"true"},
		"is_sse":            {"false"},
		"is_websocket":      {"true"},
		"sticky_written":    {"true"},
		"session_committed": {"false"},
		"probe_outcome":     {"transport_failed"},
		"terminal_cause":    {"upstream_semantic_error"},
		"user_id":           {"user-123"},
		"start_time":        {startTime},
		"end_time":          {endTime},
		"min_latency":       {"100"},
		"min_retry_count":   {"1"},
		"has_retries":       {"true"},
		"sort_by":           {"latency_ms"},
		"sort_order":        {"asc"},
	}

	filter, errMsg := parseLogFilter(query)

	if errMsg != "" {
		t.Errorf("unexpected error: %s", errMsg)
	}
	if filter.Limit != 50 {
		t.Errorf("expected limit 50, got %d", filter.Limit)
	}
	if filter.Offset != 10 {
		t.Errorf("expected offset 10, got %d", filter.Offset)
	}
	if filter.ProviderID != "provider-1" {
		t.Errorf("expected provider_id 'provider-1', got %q", filter.ProviderID)
	}
	if filter.APIType != "claude" {
		t.Errorf("expected api_type 'claude', got %q", filter.APIType)
	}
	if filter.Success == nil || *filter.Success != true {
		t.Error("expected success true")
	}
	if filter.IsSSE == nil || *filter.IsSSE != false {
		t.Error("expected is_sse false")
	}
	if filter.IsWebSocket == nil || *filter.IsWebSocket != true {
		t.Error("expected is_websocket true")
	}
	if filter.StickyWritten == nil || *filter.StickyWritten != true {
		t.Error("expected sticky_written true")
	}
	if filter.SessionCommitted == nil || *filter.SessionCommitted != false {
		t.Error("expected session_committed false")
	}
	if filter.ProbeOutcome != model.WebSocketProbeOutcomeTransportFailed {
		t.Errorf("expected probe_outcome %q, got %q", model.WebSocketProbeOutcomeTransportFailed, filter.ProbeOutcome)
	}
	if filter.TerminalCause != model.TerminalUpstreamSemanticError {
		t.Errorf("expected terminal_cause %q, got %q", model.TerminalUpstreamSemanticError, filter.TerminalCause)
	}
	if filter.UserID != "user-123" {
		t.Errorf("expected user_id 'user-123', got %q", filter.UserID)
	}
	if filter.StartTime == nil {
		t.Error("expected start_time to be set")
	}
	if filter.EndTime == nil {
		t.Error("expected end_time to be set")
	}
	if filter.MinLatency == nil || *filter.MinLatency != 100 {
		t.Error("expected min_latency 100")
	}
	if filter.MinRetryCount == nil || *filter.MinRetryCount != 1 {
		t.Error("expected min_retry_count 1")
	}
	if filter.HasRetries == nil || *filter.HasRetries != true {
		t.Error("expected has_retries true")
	}
	if filter.SortBy != "latency_ms" {
		t.Errorf("expected sort_by 'latency_ms', got %q", filter.SortBy)
	}
	if filter.SortOrder != "asc" {
		t.Errorf("expected sort_order 'asc', got %q", filter.SortOrder)
	}
}

func TestParseLogFilter_BoolParsing(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		expected bool
	}{
		{"true lowercase", "true", true},
		{"TRUE uppercase", "TRUE", true},
		{"1", "1", true},
		{"false lowercase", "false", false},
		{"FALSE uppercase", "FALSE", false},
		{"0", "0", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			query := map[string][]string{"success": {tt.value}}
			filter, errMsg := parseLogFilter(query)

			if errMsg != "" {
				t.Errorf("unexpected error: %s", errMsg)
			}
			if filter.Success == nil {
				t.Fatal("expected success to be set")
			}
			if *filter.Success != tt.expected {
				t.Errorf("expected success %v, got %v", tt.expected, *filter.Success)
			}
		})
	}
}

func TestParseLogFilter_TerminalCause(t *testing.T) {
	query := map[string][]string{"terminal_cause": {string(model.TerminalClientDisconnect)}}
	filter, errMsg := parseLogFilter(query)

	if errMsg != "" {
		t.Errorf("unexpected error: %s", errMsg)
	}
	if filter.TerminalCause != model.TerminalClientDisconnect {
		t.Errorf("expected terminal_cause %q, got %q", model.TerminalClientDisconnect, filter.TerminalCause)
	}
}

func TestParseLogFilter_ProbeOutcome(t *testing.T) {
	query := map[string][]string{"probe_outcome": {"unsupported"}}
	filter, errMsg := parseLogFilter(query)

	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	if filter.ProbeOutcome != model.WebSocketProbeOutcomeUnsupported {
		t.Errorf("expected probe_outcome %q, got %q", model.WebSocketProbeOutcomeUnsupported, filter.ProbeOutcome)
	}

	query = map[string][]string{"probe_outcome": {"bogus"}}
	filter, errMsg = parseLogFilter(query)

	if errMsg == "" {
		t.Fatal("expected validation error for invalid probe_outcome")
	}
	if filter.ProbeOutcome != "" {
		t.Errorf("expected empty probe_outcome on error, got %q", filter.ProbeOutcome)
	}
}

func TestParseLogFilter_LimitCapping(t *testing.T) {
	query := map[string][]string{"limit": {"5000"}}
	filter, errMsg := parseLogFilter(query)

	if errMsg != "" {
		t.Errorf("unexpected error: %s", errMsg)
	}
	if filter.Limit != MaxLogsLimit {
		t.Errorf("expected limit to be capped at %d, got %d", MaxLogsLimit, filter.Limit)
	}
}

func TestParseLogFilter_TimeFormat(t *testing.T) {
	// Valid RFC3339 time
	query := map[string][]string{
		"start_time": {"2024-06-15T14:30:00Z"},
		"end_time":   {"2024-06-15T15:30:00+01:00"},
	}
	filter, errMsg := parseLogFilter(query)

	if errMsg != "" {
		t.Errorf("unexpected error: %s", errMsg)
	}
	if filter.StartTime == nil {
		t.Error("expected start_time to be parsed")
	}
	if filter.EndTime == nil {
		t.Error("expected end_time to be parsed")
	}
}

func TestParseLogFilter_EdgeCases(t *testing.T) {
	t.Run("empty string values are ignored", func(t *testing.T) {
		query := map[string][]string{
			"provider_id": {""},
			"api_type":    {""},
			"user_id":     {""},
		}
		filter, errMsg := parseLogFilter(query)

		if errMsg != "" {
			t.Errorf("unexpected error: %s", errMsg)
		}
		if filter.ProviderID != "" {
			t.Errorf("expected empty provider_id, got %q", filter.ProviderID)
		}
	})

	t.Run("first value of multi-value param is used", func(t *testing.T) {
		query := map[string][]string{
			"limit": {"10", "20", "30"},
		}
		filter, errMsg := parseLogFilter(query)

		if errMsg != "" {
			t.Errorf("unexpected error: %s", errMsg)
		}
		if filter.Limit != 10 {
			t.Errorf("expected limit 10, got %d", filter.Limit)
		}
	})

	t.Run("zero min_latency is valid", func(t *testing.T) {
		query := map[string][]string{"min_latency": {"0"}}
		filter, errMsg := parseLogFilter(query)

		if errMsg != "" {
			t.Errorf("unexpected error: %s", errMsg)
		}
		if filter.MinLatency == nil || *filter.MinLatency != 0 {
			t.Error("expected min_latency 0")
		}
	})

	t.Run("zero min_retry_count is valid", func(t *testing.T) {
		query := map[string][]string{"min_retry_count": {"0"}}
		filter, errMsg := parseLogFilter(query)

		if errMsg != "" {
			t.Errorf("unexpected error: %s", errMsg)
		}
		if filter.MinRetryCount == nil || *filter.MinRetryCount != 0 {
			t.Error("expected min_retry_count 0")
		}
	})
}
