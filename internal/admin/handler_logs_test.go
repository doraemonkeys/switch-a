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

type countLogsErrorStore struct {
	*mockStore
}

func (s *countLogsErrorStore) CountLogs(_ context.Context, _ model.LogFilter) (int64, error) {
	return 0, errors.New("count exploded")
}

func lifecycleBoolPtr(v bool) *bool {
	return &v
}

func lifecycleCommitSourcePtr(v model.CommitSource) *model.CommitSource {
	return &v
}

func lifecycleCompletionStatePtr(v model.CompletionState) *model.CompletionState {
	return &v
}

func lifecycleServiceOutcomePtr(v model.ServiceOutcome) *model.ServiceOutcome {
	return &v
}

func lifecycleClientActionPtr(v model.ClientAction) *model.ClientAction {
	return &v
}

func lifecycleTerminationActorPtr(v model.TerminationActor) *model.TerminationActor {
	return &v
}

func lifecycleTerminationReasonPtr(v model.TerminationReason) *model.TerminationReason {
	return &v
}

func lifecycleIntPtr(v int) *int {
	return &v
}

func TestGetLog_Success(t *testing.T) {
	h, st, _ := testHandler()

	now := time.Now()
	committed := true
	st.logs = []model.RequestLog{
		{
			ID:                        1,
			RequestID:                 "req-123",
			ProviderID:                "provider-1",
			APIType:                   "claude",
			Model:                     "claude-3",
			SemanticsVersion:          model.RequestSemanticsVersionNormalizedV1,
			ClientTransportStatusCode: lifecycleIntPtr(101),
			CompletionState:           lifecycleCompletionStatePtr(model.CompletionStateCompleted),
			ServiceOutcome:            lifecycleServiceOutcomePtr(model.ServiceOutcomeCompleted),
			ClientAction:              lifecycleClientActionPtr(model.ClientActionNone),
			IsWebSocket:               true,
			SessionCommitted:          &committed,
			ClientVisible:             lifecycleBoolPtr(true),
			CommitSource:              lifecycleCommitSourcePtr(model.CommitSemantic),
			CreatedAt:                 now,
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/api/logs/1", nil)
	setPathValue(req, "id", "1")
	w := httptest.NewRecorder()

	h.GetLog(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var log model.RequestLog
	if err := json.NewDecoder(w.Body).Decode(&log); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if log.SemanticsVersion != model.RequestSemanticsVersionNormalizedV1 {
		t.Fatalf("SemanticsVersion = %q, want %q", log.SemanticsVersion, model.RequestSemanticsVersionNormalizedV1)
	}
	if log.ClientTransportStatusCode == nil || *log.ClientTransportStatusCode != 101 {
		t.Fatalf("ClientTransportStatusCode = %v, want 101", log.ClientTransportStatusCode)
	}
	if log.ServiceOutcome == nil || *log.ServiceOutcome != model.ServiceOutcomeCompleted {
		t.Fatalf("ServiceOutcome = %v, want %q", log.ServiceOutcome, model.ServiceOutcomeCompleted)
	}
	if log.CommitSource == nil || *log.CommitSource != model.CommitSemantic {
		t.Fatalf("CommitSource = %v, want %q", log.CommitSource, model.CommitSemantic)
	}
}

func TestGetLog_AttemptsPreserveExplicitNullEvidence(t *testing.T) {
	h, st, _ := testHandler()

	st.logs = []model.RequestLog{{
		ID:               1,
		RequestID:        "req-123",
		ProviderID:       "provider-1",
		SemanticsVersion: model.RequestSemanticsVersionNormalizedV1,
		CreatedAt:        time.Now(),
	}}
	st.attempts["req-123"] = []model.RequestAttempt{{
		ID:               10,
		RequestID:        "req-123",
		ProviderID:       "provider-2",
		SemanticsVersion: model.RequestSemanticsVersionNormalizedV1,
		Attempt:          1,
		StatusCode:       502,
		Error:            "provider unavailable",
		LatencyMs:        75,
		CreatedAt:        time.Now(),
	}}

	req := httptest.NewRequest(http.MethodGet, "/admin/api/logs/1", nil)
	setPathValue(req, "id", "1")
	w := httptest.NewRecorder()

	h.GetLog(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var payload map[string]any
	if err := json.NewDecoder(w.Body).Decode(&payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	attemptsValue, ok := payload["attempts"]
	if !ok {
		t.Fatal("attempts missing from response payload")
	}
	attempts, ok := attemptsValue.([]any)
	if !ok || len(attempts) != 1 {
		t.Fatalf("attempts = %#v, want single attempt", attemptsValue)
	}

	attempt, ok := attempts[0].(map[string]any)
	if !ok {
		t.Fatalf("attempt payload = %#v, want object", attempts[0])
	}
	if got := attempt["semantics_version"]; got != string(model.RequestSemanticsVersionNormalizedV1) {
		t.Fatalf("semantics_version = %#v, want %q", got, model.RequestSemanticsVersionNormalizedV1)
	}

	value, ok := attempt["attempt_evidence_json"]
	if !ok {
		t.Fatal("attempt_evidence_json missing from attempt payload")
	}
	if value != nil {
		t.Fatalf("attempt_evidence_json = %#v, want nil", value)
	}
}

func TestGetLog_NotFound(t *testing.T) {
	h, st, _ := testHandler()
	st.logs = []model.RequestLog{}

	req := httptest.NewRequest(http.MethodGet, "/admin/api/logs/999", nil)
	setPathValue(req, "id", "999")
	w := httptest.NewRecorder()

	h.GetLog(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusNotFound, w.Body.String())
	}
}

func TestGetLog_InvalidID(t *testing.T) {
	h, _, _ := testHandler()

	req := httptest.NewRequest(http.MethodGet, "/admin/api/logs/not-a-number", nil)
	setPathValue(req, "id", "not-a-number")
	w := httptest.NewRecorder()

	h.GetLog(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestGetLog_AttemptsErrorDoesNotFailRequest(t *testing.T) {
	h, st, _ := testHandler()

	st.logs = []model.RequestLog{{
		ID:               1,
		RequestID:        "req-with-error",
		ProviderID:       "provider-1",
		SemanticsVersion: model.RequestSemanticsVersionNormalizedV1,
		CreatedAt:        time.Now(),
	}}
	st.attemptsErr = errors.New("database error")

	req := httptest.NewRequest(http.MethodGet, "/admin/api/logs/1", nil)
	setPathValue(req, "id", "1")
	w := httptest.NewRecorder()

	h.GetLog(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}
}

func TestGetLog_ErrorBranches(t *testing.T) {
	t.Run("missing id", func(t *testing.T) {
		h, _, _ := testHandler()

		req := httptest.NewRequest(http.MethodGet, "/admin/api/logs/", nil)
		w := httptest.NewRecorder()

		h.GetLog(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusBadRequest, w.Body.String())
		}
	})

	t.Run("store error", func(t *testing.T) {
		h, st, _ := testHandler()
		st.logsErr = errors.New("store exploded")

		req := httptest.NewRequest(http.MethodGet, "/admin/api/logs/1", nil)
		setPathValue(req, "id", "1")
		w := httptest.NewRecorder()

		h.GetLog(w, req)

		if w.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusInternalServerError, w.Body.String())
		}
	})
}

func TestGetLogs_Success(t *testing.T) {
	h, st, _ := testHandler()

	now := time.Now()
	committed := true
	st.logs = []model.RequestLog{
		{
			ID:               1,
			ProviderID:       "provider-1",
			APIType:          "claude",
			SemanticsVersion: model.RequestSemanticsVersionNormalizedV1,
			ServiceOutcome:   lifecycleServiceOutcomePtr(model.ServiceOutcomeCompleted),
			SessionCommitted: &committed,
			ClientVisible:    lifecycleBoolPtr(true),
			CommitSource:     lifecycleCommitSourcePtr(model.CommitSemantic),
			CreatedAt:        now,
		},
		{
			ID:               2,
			ProviderID:       "legacy-provider",
			APIType:          "claude",
			SemanticsVersion: model.RequestSemanticsVersionLegacyPreAssessment,
			CreatedAt:        now.Add(-time.Hour),
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/api/logs", nil)
	w := httptest.NewRecorder()

	h.GetLogs(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	body := w.Body.Bytes()
	var resp LogsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(resp.Logs) != 2 {
		t.Fatalf("expected 2 logs, got %d", len(resp.Logs))
	}
	if resp.Total != 2 {
		t.Fatalf("expected total 2, got %d", resp.Total)
	}

	var raw struct {
		Logs []map[string]any `json:"logs"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("failed to decode raw response: %v", err)
	}
	if _, exists := raw.Logs[0]["sticky_written"]; exists {
		t.Fatal("sticky_written leaked into normalized request-log response")
	}
	if _, exists := raw.Logs[0]["probe_outcome"]; exists {
		t.Fatal("probe_outcome leaked into normalized request-log response")
	}
}

func TestGetLogs_WithQueryFilters(t *testing.T) {
	h, st, _ := testHandler()

	now := time.Now()
	committed := true
	uncommitted := false
	st.logs = []model.RequestLog{
		{
			ID:                        1,
			ProviderID:                "provider-1",
			APIType:                   "claude",
			UserID:                    "user-1",
			SemanticsVersion:          model.RequestSemanticsVersionNormalizedV1,
			ClientTransportStatusCode: lifecycleIntPtr(101),
			CompletionState:           lifecycleCompletionStatePtr(model.CompletionStateCompleted),
			ServiceOutcome:            lifecycleServiceOutcomePtr(model.ServiceOutcomeCompleted),
			ClientAction:              lifecycleClientActionPtr(model.ClientActionNone),
			IsWebSocket:               true,
			SessionCommitted:          &committed,
			ClientVisible:             lifecycleBoolPtr(true),
			CommitSource:              lifecycleCommitSourcePtr(model.CommitSemantic),
			CreatedAt:                 now,
		},
		{
			ID:                        2,
			ProviderID:                "provider-2",
			APIType:                   "codex",
			UserID:                    "user-2",
			SemanticsVersion:          model.RequestSemanticsVersionNormalizedV1,
			ClientTransportStatusCode: lifecycleIntPtr(101),
			CompletionState:           lifecycleCompletionStatePtr(model.CompletionStateIncomplete),
			ServiceOutcome:            lifecycleServiceOutcomePtr(model.ServiceOutcomeInterrupted),
			ClientAction:              lifecycleClientActionPtr(model.ClientActionReconnectRequired),
			TerminationActor:          lifecycleTerminationActorPtr(model.TerminationActorUpstream),
			TerminationReason:         lifecycleTerminationReasonPtr(model.TerminationReasonUsageLimitReached),
			IsWebSocket:               true,
			SessionCommitted:          &uncommitted,
			ClientVisible:             lifecycleBoolPtr(true),
			CommitSource:              lifecycleCommitSourcePtr(model.CommitUpstreamMessage),
			CreatedAt:                 now.Add(-time.Hour),
		},
		{
			ID:                        3,
			ProviderID:                "provider-3",
			APIType:                   "gemini",
			UserID:                    "user-4",
			SemanticsVersion:          model.RequestSemanticsVersionNormalizedV1,
			ClientTransportStatusCode: lifecycleIntPtr(0),
			CompletionState:           lifecycleCompletionStatePtr(model.CompletionStateUnknown),
			ServiceOutcome:            lifecycleServiceOutcomePtr(model.ServiceOutcomeNeverStarted),
			ClientAction:              lifecycleClientActionPtr(model.ClientActionNone),
			IsWebSocket:               true,
			SessionCommitted:          &uncommitted,
			ClientVisible:             lifecycleBoolPtr(false),
			CreatedAt:                 now.Add(-90 * time.Minute),
		},
		{
			ID:               4,
			ProviderID:       "provider-legacy",
			APIType:          "claude",
			UserID:           "user-3",
			SemanticsVersion: model.RequestSemanticsVersionLegacyPreAssessment,
			CreatedAt:        now.Add(-2 * time.Hour),
		},
	}

	tests := []struct {
		name          string
		query         string
		expectedCount int
	}{
		{"filter by provider_id", "?provider_id=provider-1", 1},
		{"filter by api_type", "?api_type=codex", 1},
		{"filter by semantics_version", "?semantics_version=legacy_pre_assessment", 1},
		{"filter by completion_state", "?completion_state=incomplete", 1},
		{"filter by service_outcome", "?service_outcome=completed", 1},
		{"filter by client_action", "?client_action=reconnect_required", 1},
		{"filter by termination_actor", "?termination_actor=upstream", 1},
		{"filter by termination_reason", "?termination_reason=usage_limit_reached", 1},
		{"filter by client_transport_status_code", "?client_transport_status_code=101", 2},
		{"filter by client_transport_status_code zero", "?client_transport_status_code=0", 1},
		{"filter by session_committed", "?session_committed=true", 1},
		{"filter by client_visible", "?client_visible=true", 2},
		{"filter by commit_source", "?commit_source=upstream_message", 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/admin/api/logs"+tt.query, nil)
			w := httptest.NewRecorder()

			h.GetLogs(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
			}

			var resp LogsResponse
			if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
				t.Fatalf("failed to decode response: %v", err)
			}

			if len(resp.Logs) != tt.expectedCount {
				t.Fatalf("expected %d logs, got %d", tt.expectedCount, len(resp.Logs))
			}
		})
	}
}

func TestGetLogs_PaginationAndSort(t *testing.T) {
	h, st, _ := testHandler()

	now := time.Now()
	for i := 1; i <= 5; i++ {
		st.logs = append(st.logs, model.RequestLog{
			ID:               uint(i),
			ProviderID:       "provider-1",
			SemanticsVersion: model.RequestSemanticsVersionNormalizedV1,
			LatencyMs:        int64(i * 10),
			CreatedAt:        now.Add(-time.Duration(i) * time.Hour),
		})
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/api/logs?limit=2&offset=2&sort_by=latency_ms&sort_order=asc", nil)
	w := httptest.NewRecorder()

	h.GetLogs(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp LogsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Limit != 2 || resp.Offset != 2 {
		t.Fatalf("pagination = limit %d offset %d, want 2/2", resp.Limit, resp.Offset)
	}
	if resp.SortBy != "latency_ms" || resp.SortOrder != "asc" {
		t.Fatalf("sort = %s/%s, want latency_ms/asc", resp.SortBy, resp.SortOrder)
	}
}

func TestGetLogs_InvalidParams(t *testing.T) {
	h, _, _ := testHandler()

	tests := []struct {
		name  string
		query string
	}{
		{"invalid limit", "?limit=abc"},
		{"removed success filter", "?success=true"},
		{"removed terminal cause filter", "?terminal_cause=clean_close"},
		{"removed recovery action filter", "?recovery_action=none"},
		{"invalid semantics version", "?semantics_version=bogus"},
		{"invalid completion state", "?completion_state=bogus"},
		{"invalid service outcome", "?service_outcome=bogus"},
		{"invalid client action", "?client_action=bogus"},
		{"invalid termination actor", "?termination_actor=bogus"},
		{"invalid termination reason", "?termination_reason=bogus"},
		{"invalid negative client transport status code", "?client_transport_status_code=-1"},
		{"invalid commit source", "?commit_source=bogus"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/admin/api/logs"+tt.query, nil)
			w := httptest.NewRecorder()

			h.GetLogs(w, req)

			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusBadRequest, w.Body.String())
			}
		})
	}
}

func TestGetLogs_StoreErrors(t *testing.T) {
	h, st, _ := testHandler()
	st.logsErr = errors.New("database error")

	req := httptest.NewRequest(http.MethodGet, "/admin/api/logs", nil)
	w := httptest.NewRecorder()

	h.GetLogs(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestGetLogs_CountError(t *testing.T) {
	h, st, _ := testHandler()
	h.store = &countLogsErrorStore{mockStore: st}
	st.logs = []model.RequestLog{{
		ID:               1,
		ProviderID:       "provider-1",
		SemanticsVersion: model.RequestSemanticsVersionNormalizedV1,
		CreatedAt:        time.Now(),
	}}

	req := httptest.NewRequest(http.MethodGet, "/admin/api/logs", nil)
	w := httptest.NewRecorder()

	h.GetLogs(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusInternalServerError, w.Body.String())
	}
}

func TestParseLogFilter_Defaults(t *testing.T) {
	filter, errMsg := parseLogFilter(map[string][]string{})

	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	if filter.Limit != DefaultLogsLimit {
		t.Fatalf("Limit = %d, want %d", filter.Limit, DefaultLogsLimit)
	}
	if filter.SortBy != "created_at" || filter.SortOrder != "desc" {
		t.Fatalf("sort defaults = %s/%s, want created_at/desc", filter.SortBy, filter.SortOrder)
	}
	if filter.SemanticsVersion != "" || filter.ServiceOutcome != "" || filter.ClientAction != "" {
		t.Fatal("expected normalized filters to default to empty values")
	}
}

func TestParseLogFilter_AllParams(t *testing.T) {
	startTime := "2024-01-01T00:00:00Z"
	endTime := "2024-12-31T23:59:59Z"

	filter, errMsg := parseLogFilter(map[string][]string{
		"limit":                        {"50"},
		"offset":                       {"10"},
		"provider_id":                  {"provider-1"},
		"api_type":                     {"claude"},
		"semantics_version":            {"normalized_v1"},
		"completion_state":             {"completed"},
		"service_outcome":              {"completed"},
		"client_action":                {"none"},
		"termination_actor":            {"upstream"},
		"termination_reason":           {"usage_limit_reached"},
		"client_transport_status_code": {"101"},
		"is_sse":                       {"false"},
		"is_websocket":                 {"true"},
		"session_committed":            {"false"},
		"client_visible":               {"true"},
		"commit_source":                {"upstream_message"},
		"user_id":                      {"user-123"},
		"start_time":                   {startTime},
		"end_time":                     {endTime},
		"min_latency":                  {"100"},
		"min_retry_count":              {"1"},
		"has_retries":                  {"true"},
		"sort_by":                      {"latency_ms"},
		"sort_order":                   {"asc"},
	})

	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	if filter.SemanticsVersion != model.RequestSemanticsVersionNormalizedV1 {
		t.Fatalf("SemanticsVersion = %q, want %q", filter.SemanticsVersion, model.RequestSemanticsVersionNormalizedV1)
	}
	if filter.ServiceOutcome != model.ServiceOutcomeCompleted {
		t.Fatalf("ServiceOutcome = %q, want %q", filter.ServiceOutcome, model.ServiceOutcomeCompleted)
	}
	if filter.ClientTransportStatusCode == nil || *filter.ClientTransportStatusCode != 101 {
		t.Fatalf("ClientTransportStatusCode = %v, want 101", filter.ClientTransportStatusCode)
	}
	if filter.CommitSource != model.CommitUpstreamMessage {
		t.Fatalf("CommitSource = %q, want %q", filter.CommitSource, model.CommitUpstreamMessage)
	}
	if filter.SortBy != "latency_ms" || filter.SortOrder != "asc" {
		t.Fatalf("sort = %s/%s, want latency_ms/asc", filter.SortBy, filter.SortOrder)
	}
}

func TestParseLogFilter_AllowsZeroClientTransportStatusCode(t *testing.T) {
	filter, errMsg := parseLogFilter(map[string][]string{
		"client_transport_status_code": {"0"},
	})

	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	if filter.ClientTransportStatusCode == nil || *filter.ClientTransportStatusCode != 0 {
		t.Fatalf("ClientTransportStatusCode = %v, want 0", filter.ClientTransportStatusCode)
	}
}

func TestParseLogFilter_CapsLimitAtMaxLogsLimit(t *testing.T) {
	filter, errMsg := parseLogFilter(map[string][]string{
		"limit": {"10000"},
	})

	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	if filter.Limit != MaxLogsLimit {
		t.Fatalf("Limit = %d, want %d", filter.Limit, MaxLogsLimit)
	}
}

func TestParseLogFilter_RejectsZeroLimit(t *testing.T) {
	_, errMsg := parseLogFilter(map[string][]string{
		"limit": {"0"},
	})
	if errMsg != "Invalid limit: must be a positive integer" {
		t.Fatalf("errMsg = %q, want invalid limit message", errMsg)
	}
}

func TestParseLogFilter_RejectsInvalidOffset(t *testing.T) {
	_, errMsg := parseLogFilter(map[string][]string{
		"offset": {"oops"},
	})
	if errMsg != "Invalid offset: must be a valid integer" {
		t.Fatalf("errMsg = %q, want invalid offset message", errMsg)
	}
}

func TestParseLogFilter_RejectsInvalidSortBy(t *testing.T) {
	_, errMsg := parseLogFilter(map[string][]string{
		"sort_by": {"duration"},
	})
	if errMsg != "Invalid sort_by: must be 'created_at' or 'latency_ms'" {
		t.Fatalf("errMsg = %q, want invalid sort field message", errMsg)
	}
}

func TestParseLogFilter_RejectsLateValidationErrors(t *testing.T) {
	tests := []struct {
		name    string
		query   map[string][]string
		wantErr string
	}{
		{
			name:    "invalid sse flag",
			query:   map[string][]string{"is_sse": {"maybe"}},
			wantErr: "Invalid is_sse: must be 'true' or 'false'",
		},
		{
			name:    "invalid start time",
			query:   map[string][]string{"start_time": {"2026-04-06"}},
			wantErr: "Invalid start_time: must be RFC3339 format (e.g., 2026-01-11T00:00:00Z)",
		},
		{
			name:    "invalid min latency",
			query:   map[string][]string{"min_latency": {"fast"}},
			wantErr: "Invalid min_latency: must be a valid integer",
		},
		{
			name:    "invalid has retries flag",
			query:   map[string][]string{"has_retries": {"sometimes"}},
			wantErr: "Invalid has_retries: must be 'true' or 'false'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, errMsg := parseLogFilter(tt.query)
			if errMsg != tt.wantErr {
				t.Fatalf("errMsg = %q, want %q", errMsg, tt.wantErr)
			}
		})
	}
}

func TestParseIntegerPointerHelpers(t *testing.T) {
	t.Run("parsePositiveIntPtr", func(t *testing.T) {
		tests := []struct {
			name      string
			input     string
			wantValue int
			wantNil   bool
			wantErr   string
		}{
			{name: "empty", input: "", wantNil: true},
			{name: "invalid", input: "abc", wantNil: true, wantErr: "Invalid retry_count: must be a valid integer"},
			{name: "zero", input: "0", wantNil: true, wantErr: "Invalid retry_count: must be a positive integer"},
			{name: "positive", input: "3", wantValue: 3},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				value, errMsg := parsePositiveIntPtr(tt.input, "retry_count")
				if errMsg != tt.wantErr {
					t.Fatalf("errMsg = %q, want %q", errMsg, tt.wantErr)
				}
				if tt.wantNil {
					if value != nil {
						t.Fatalf("value = %v, want nil", value)
					}
					return
				}
				if value == nil || *value != tt.wantValue {
					t.Fatalf("value = %v, want %d", value, tt.wantValue)
				}
			})
		}
	})

	t.Run("parseNonNegativeInt", func(t *testing.T) {
		tests := []struct {
			name      string
			input     string
			wantValue int
			wantErr   string
		}{
			{name: "default", input: "", wantValue: 7},
			{name: "invalid", input: "oops", wantErr: "Invalid offset: must be a valid integer"},
			{name: "negative", input: "-1", wantErr: "Invalid offset: must be a non-negative integer"},
			{name: "valid", input: "4", wantValue: 4},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				value, errMsg := parseNonNegativeInt(tt.input, "offset", 7)
				if errMsg != tt.wantErr {
					t.Fatalf("errMsg = %q, want %q", errMsg, tt.wantErr)
				}
				if errMsg == "" && value != tt.wantValue {
					t.Fatalf("value = %d, want %d", value, tt.wantValue)
				}
			})
		}
	})

	t.Run("parseNonNegativeIntPtr", func(t *testing.T) {
		tests := []struct {
			name      string
			input     string
			wantValue int
			wantNil   bool
			wantErr   string
		}{
			{name: "empty", input: "", wantNil: true},
			{name: "invalid", input: "oops", wantNil: true, wantErr: "Invalid client_transport_status_code: must be a valid integer"},
			{name: "negative", input: "-1", wantNil: true, wantErr: "Invalid client_transport_status_code: must be a non-negative integer"},
			{name: "valid", input: "101", wantValue: 101},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				value, errMsg := parseNonNegativeIntPtr(tt.input, "client_transport_status_code")
				if errMsg != tt.wantErr {
					t.Fatalf("errMsg = %q, want %q", errMsg, tt.wantErr)
				}
				if tt.wantNil {
					if value != nil {
						t.Fatalf("value = %v, want nil", value)
					}
					return
				}
				if value == nil || *value != tt.wantValue {
					t.Fatalf("value = %v, want %d", value, tt.wantValue)
				}
			})
		}
	})
}

func TestParseBoolPtr_InvalidValue(t *testing.T) {
	value, errMsg := parseBoolPtr("not-bool", "is_websocket")
	if errMsg != "Invalid is_websocket: must be 'true' or 'false'" {
		t.Fatalf("errMsg = %q, want invalid bool message", errMsg)
	}
	if value != nil {
		t.Fatalf("value = %v, want nil", value)
	}
}

func TestParseLogFilter_DeprecatedFiltersFailFast(t *testing.T) {
	tests := []struct {
		name   string
		query  map[string][]string
		errMsg string
	}{
		{
			name:   "success",
			query:  map[string][]string{"success": {"true"}},
			errMsg: "Invalid success: filter was removed; use service_outcome",
		},
		{
			name:   "terminal_cause",
			query:  map[string][]string{"terminal_cause": {"clean_close"}},
			errMsg: "Invalid terminal_cause: filter was removed; use termination_reason",
		},
		{
			name:   "recovery_action",
			query:  map[string][]string{"recovery_action": {"none"}},
			errMsg: "Invalid recovery_action: filter was removed; use client_action",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, errMsg := parseLogFilter(tt.query)
			if errMsg != tt.errMsg {
				t.Fatalf("errMsg = %q, want %q", errMsg, tt.errMsg)
			}
		})
	}
}
