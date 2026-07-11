package admin

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"switch-a/internal/model"
	"switch-a/internal/store"

	"go.uber.org/zap"
)

// Log API handlers

// LogsResponse represents the response for logs API.
type LogsResponse struct {
	Logs      []model.RequestLog `json:"logs"`
	Total     int64              `json:"total"`
	Limit     int                `json:"limit"`
	Offset    int                `json:"offset"`
	SortBy    string             `json:"sort_by"`
	SortOrder string             `json:"sort_order"`
}

// GetLogs handles GET /admin/api/logs.
// Query parameters:
//   - limit: max results (default: 100, max: 1000)
//   - offset: pagination offset (default: 0)
//   - provider_id: filter by provider ID
//   - api_type: filter by API type (claude/codex/gemini/grok/custom:*)
//   - semantics_version: filter by request semantics version
//   - completion_state: filter by normalized completion state
//   - service_outcome: filter by normalized service outcome
//   - client_action: filter by normalized client action
//   - termination_actor: filter by normalized termination actor
//   - termination_reason: filter by normalized termination reason
//   - client_transport_status_code: filter by normalized client transport status code
//   - is_sse: filter by SSE/regular request (true/false)
//   - is_websocket: filter by WebSocket/regular request (true/false)
//   - session_committed: filter by whether committed service is known to have started (true/false)
//   - client_visible: filter by whether upstream payload became client-visible (true/false)
//   - commit_source: filter by explicit commit source enum
//   - user_id: filter by user ID
//   - start_time: filter by start time (RFC3339)
//   - end_time: filter by end time (RFC3339)
//   - min_latency: filter by minimum latency in ms
//   - min_retry_count: filter by minimum retry count
//   - has_retries: filter by has retries (true = retry_count > 0, false = retry_count = 0)
//   - sort_by: sort field (created_at/latency_ms, default: created_at)
//   - sort_order: sort direction (asc/desc, default: desc)
func (h *Handler) GetLogs(w http.ResponseWriter, r *http.Request) {
	filter, errMsg := parseLogFilter(r.URL.Query())
	if errMsg != "" {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, errMsg)
		return
	}

	logs, err := h.store.ListLogs(r.Context(), filter)
	if err != nil {
		h.logger.Error("failed to list logs", zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to get logs")
		return
	}

	total, err := h.store.CountLogs(r.Context(), filter)
	if err != nil {
		h.logger.Error("failed to count logs", zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to count logs")
		return
	}

	writeJSON(w, http.StatusOK, LogsResponse{
		Logs:      logs,
		Total:     total,
		Limit:     filter.Limit,
		Offset:    filter.Offset,
		SortBy:    filter.SortBy,
		SortOrder: filter.SortOrder,
	})
}

// parseLogFilter parses query parameters into a LogFilter.
// Returns the filter and an error message (empty string if no error).
//
// Design rationale:
//   - limit requires positive values because zero results are never useful
//   - offset allows zero for the first page of results
//   - defaults to descending sort so users see recent logs first (typical use case)
func parseLogFilter(query map[string][]string) (model.LogFilter, string) {
	var filter model.LogFilter
	var errMsg string

	if errMsg = parseLogPagination(query, &filter); errMsg != "" {
		return filter, errMsg
	}

	filter.ProviderID = getQueryParam(query, "provider_id")
	filter.APIType = getQueryParam(query, "api_type")
	filter.UserID = getQueryParam(query, "user_id")

	if errMsg = rejectDeprecatedLogFilters(query); errMsg != "" {
		return filter, errMsg
	}
	if errMsg = parseNormalizedLogFilters(query, &filter); errMsg != "" {
		return filter, errMsg
	}
	if errMsg = parseLogProtocolFilters(query, &filter); errMsg != "" {
		return filter, errMsg
	}
	if errMsg = parseLogRangeFilters(query, &filter); errMsg != "" {
		return filter, errMsg
	}

	filter.SortBy, filter.SortOrder, errMsg = parseSortParams(
		getQueryParam(query, "sort_by"),
		getQueryParam(query, "sort_order"),
	)
	if errMsg != "" {
		return filter, errMsg
	}

	return filter, ""
}

func parseLogPagination(query map[string][]string, filter *model.LogFilter) string {
	var errMsg string

	filter.Limit, errMsg = parsePositiveInt(getQueryParam(query, "limit"), "limit", DefaultLogsLimit)
	if errMsg != "" {
		return errMsg
	}
	if filter.Limit > MaxLogsLimit {
		filter.Limit = MaxLogsLimit
	}

	filter.Offset, errMsg = parseNonNegativeInt(getQueryParam(query, "offset"), "offset", 0)
	if errMsg != "" {
		return errMsg
	}

	return ""
}

func rejectDeprecatedLogFilters(query map[string][]string) string {
	for _, deprecated := range []struct {
		name        string
		replacement string
	}{
		{name: "success", replacement: "service_outcome"},
		{name: "terminal_cause", replacement: "termination_reason"},
		{name: "recovery_action", replacement: "client_action"},
	} {
		if errMsg := rejectDeprecatedLogFilter(query, deprecated.name, deprecated.replacement); errMsg != "" {
			return errMsg
		}
	}

	return ""
}

func parseNormalizedLogFilters(query map[string][]string, filter *model.LogFilter) string {
	var errMsg string

	filter.SemanticsVersion, errMsg = parseRequestSemanticsVersion(getQueryParam(query, "semantics_version"))
	if errMsg != "" {
		return errMsg
	}
	filter.CompletionState, errMsg = parseCompletionState(getQueryParam(query, "completion_state"))
	if errMsg != "" {
		return errMsg
	}
	filter.ServiceOutcome, errMsg = parseServiceOutcome(getQueryParam(query, "service_outcome"))
	if errMsg != "" {
		return errMsg
	}
	filter.ClientAction, errMsg = parseClientAction(getQueryParam(query, "client_action"))
	if errMsg != "" {
		return errMsg
	}
	filter.TerminationActor, errMsg = parseTerminationActor(getQueryParam(query, "termination_actor"))
	if errMsg != "" {
		return errMsg
	}
	filter.TerminationReason, errMsg = parseTerminationReason(getQueryParam(query, "termination_reason"))
	if errMsg != "" {
		return errMsg
	}
	filter.ClientTransportStatusCode, errMsg = parseNonNegativeIntPtr(
		getQueryParam(query, "client_transport_status_code"),
		"client_transport_status_code",
	)
	if errMsg != "" {
		return errMsg
	}

	return ""
}

func parseLogProtocolFilters(query map[string][]string, filter *model.LogFilter) string {
	var errMsg string

	filter.IsSSE, errMsg = parseBoolPtr(getQueryParam(query, "is_sse"), "is_sse")
	if errMsg != "" {
		return errMsg
	}
	filter.IsWebSocket, errMsg = parseBoolPtr(getQueryParam(query, "is_websocket"), "is_websocket")
	if errMsg != "" {
		return errMsg
	}
	filter.SessionCommitted, errMsg = parseBoolPtr(getQueryParam(query, "session_committed"), "session_committed")
	if errMsg != "" {
		return errMsg
	}
	filter.ClientVisible, errMsg = parseBoolPtr(getQueryParam(query, "client_visible"), "client_visible")
	if errMsg != "" {
		return errMsg
	}
	filter.CommitSource, errMsg = parseCommitSource(getQueryParam(query, "commit_source"))
	if errMsg != "" {
		return errMsg
	}

	return ""
}

func parseLogRangeFilters(query map[string][]string, filter *model.LogFilter) string {
	var errMsg string

	filter.StartTime, errMsg = parseTimePtr(getQueryParam(query, "start_time"), "start_time")
	if errMsg != "" {
		return errMsg
	}
	filter.EndTime, errMsg = parseTimePtr(getQueryParam(query, "end_time"), "end_time")
	if errMsg != "" {
		return errMsg
	}
	filter.MinLatency, errMsg = parseNonNegativeInt64Ptr(getQueryParam(query, "min_latency"), "min_latency")
	if errMsg != "" {
		return errMsg
	}
	filter.MinRetryCount, errMsg = parseNonNegativeIntPtr(getQueryParam(query, "min_retry_count"), "min_retry_count")
	if errMsg != "" {
		return errMsg
	}
	filter.HasRetries, errMsg = parseBoolPtr(getQueryParam(query, "has_retries"), "has_retries")
	if errMsg != "" {
		return errMsg
	}

	return ""
}

// getQueryParam returns the first value for a query parameter key.
func getQueryParam(query map[string][]string, key string) string {
	if v, ok := query[key]; ok && len(v) > 0 {
		return v[0]
	}
	return ""
}

// parsePositiveInt parses a positive integer from string.
// Returns (value, error message). Empty string is valid and returns defaultVal.
func parsePositiveInt(s string, name string, defaultVal int) (int, string) {
	if s == "" {
		return defaultVal, ""
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, "Invalid " + name + ": must be a valid integer"
	}
	if v <= 0 {
		return 0, "Invalid " + name + ": must be a positive integer"
	}
	return v, ""
}

// parseNonNegativeInt parses a non-negative integer from string.
func parseNonNegativeInt(s string, name string, defaultVal int) (int, string) {
	if s == "" {
		return defaultVal, ""
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, "Invalid " + name + ": must be a valid integer"
	}
	if v < 0 {
		return 0, "Invalid " + name + ": must be a non-negative integer"
	}
	return v, ""
}

// parseNonNegativeIntPtr parses a non-negative integer pointer from string.
func parseNonNegativeIntPtr(s string, name string) (*int, string) {
	if s == "" {
		return nil, ""
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return nil, "Invalid " + name + ": must be a valid integer"
	}
	if v < 0 {
		return nil, "Invalid " + name + ": must be a non-negative integer"
	}
	return &v, ""
}

func parsePositiveIntPtr(s string, name string) (*int, string) {
	if s == "" {
		return nil, ""
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return nil, "Invalid " + name + ": must be a valid integer"
	}
	if v <= 0 {
		return nil, "Invalid " + name + ": must be a positive integer"
	}
	return &v, ""
}

// parseBoolPtr parses a boolean pointer from string.
func parseBoolPtr(s string, name string) (*bool, string) {
	if s == "" {
		return nil, ""
	}
	switch strings.ToLower(s) {
	case "true", "1":
		v := true
		return &v, ""
	case "false", "0":
		v := false
		return &v, ""
	default:
		return nil, "Invalid " + name + ": must be 'true' or 'false'"
	}
}

func rejectDeprecatedLogFilter(query map[string][]string, name, replacement string) string {
	if getQueryParam(query, name) == "" {
		return ""
	}
	return "Invalid " + name + ": filter was removed; use " + replacement
}

func parseRequestSemanticsVersion(s string) (model.RequestSemanticsVersion, string) {
	switch model.RequestSemanticsVersion(s) {
	case "":
		return "", ""
	case model.RequestSemanticsVersionLegacyPreAssessment, model.RequestSemanticsVersionNormalizedV1:
		return model.RequestSemanticsVersion(s), ""
	default:
		return "", "Invalid semantics_version: must be 'legacy_pre_assessment' or 'normalized_v1'"
	}
}

func parseCompletionState(s string) (model.CompletionState, string) {
	switch model.CompletionState(s) {
	case "":
		return "", ""
	case model.CompletionStateUnknown, model.CompletionStateIncomplete, model.CompletionStateCompleted:
		return model.CompletionState(s), ""
	default:
		return "", "Invalid completion_state: must be 'unknown', 'incomplete', or 'completed'"
	}
}

func parseServiceOutcome(s string) (model.ServiceOutcome, string) {
	switch model.ServiceOutcome(s) {
	case "":
		return "", ""
	case model.ServiceOutcomeCompleted,
		model.ServiceOutcomeInterrupted,
		model.ServiceOutcomeNeverStarted,
		model.ServiceOutcomeAbandonedByClient,
		model.ServiceOutcomeUnknown:
		return model.ServiceOutcome(s), ""
	default:
		return "", "Invalid service_outcome: must be a valid normalized service outcome"
	}
}

func parseClientAction(s string) (model.ClientAction, string) {
	switch model.ClientAction(s) {
	case "":
		return "", ""
	case model.ClientActionNone, model.ClientActionTransparentRetry, model.ClientActionReconnectRequired:
		return model.ClientAction(s), ""
	default:
		return "", "Invalid client_action: must be 'none', 'transparent_retry', or 'reconnect_required'"
	}
}

func parseTerminationActor(s string) (model.TerminationActor, string) {
	switch model.TerminationActor(s) {
	case "":
		return "", ""
	case model.TerminationActorClient,
		model.TerminationActorGateway,
		model.TerminationActorUpstream,
		model.TerminationActorInternal,
		model.TerminationActorUnknown:
		return model.TerminationActor(s), ""
	default:
		return "", "Invalid termination_actor: must be a valid normalized termination actor"
	}
}

func parseTerminationReason(s string) (model.TerminationReason, string) {
	switch model.TerminationReason(s) {
	case "":
		return "", ""
	case model.TerminationReasonProviderUnavailable,
		model.TerminationReasonProviderConfigurationError,
		model.TerminationReasonUsageLimitReached,
		model.TerminationReasonWebSocketConnectionLimitReached,
		model.TerminationReasonClientRequestError,
		model.TerminationReasonClientDisconnect,
		model.TerminationReasonTransportError,
		model.TerminationReasonUpstreamSemanticError,
		model.TerminationReasonUpstreamHandshakeRejected,
		model.TerminationReasonClientUpgradeRejected,
		model.TerminationReasonInternalError,
		model.TerminationReasonCleanClose,
		model.TerminationReasonUnknown:
		return model.TerminationReason(s), ""
	default:
		return "", "Invalid termination_reason: must be a valid normalized termination reason"
	}
}

func parseCommitSource(s string) (model.CommitSource, string) {
	if s == "" {
		return "", ""
	}

	source := model.CommitSource(s)
	if !model.IsValidCommitSource(source) {
		return "", "Invalid commit_source: must be a valid commit source"
	}
	return source, ""
}

// parseTimePtr parses a RFC3339 time pointer from string.
func parseTimePtr(s string, name string) (*time.Time, string) {
	if s == "" {
		return nil, ""
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil, "Invalid " + name + ": must be RFC3339 format (e.g., 2026-01-11T00:00:00Z)"
	}
	return &t, ""
}

// parseNonNegativeInt64Ptr parses a non-negative int64 pointer from string.
func parseNonNegativeInt64Ptr(s string, name string) (*int64, string) {
	if s == "" {
		return nil, ""
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return nil, "Invalid " + name + ": must be a valid integer"
	}
	if v < 0 {
		return nil, "Invalid " + name + ": must be a non-negative integer"
	}
	return &v, ""
}

// parseSortParams validates and returns sort parameters with defaults.
// Defaults to descending "created_at" because most users want recent logs first.
func parseSortParams(sortBy, sortOrder string) (string, string, string) {
	if sortBy != "" && sortBy != "created_at" && sortBy != "latency_ms" {
		return "", "", "Invalid sort_by: must be 'created_at' or 'latency_ms'"
	}
	if sortBy == "" {
		sortBy = "created_at"
	}

	sortOrder = strings.ToLower(sortOrder)
	if sortOrder != "" && sortOrder != "asc" && sortOrder != "desc" {
		return "", "", "Invalid sort_order: must be 'asc' or 'desc'"
	}
	if sortOrder == "" {
		sortOrder = "desc"
	}

	return sortBy, sortOrder, ""
}

// GetLog handles GET /admin/api/logs/{id}.
// Returns a single log entry with its attempts if any.
func (h *Handler) GetLog(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	if idStr == "" {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Log ID is required")
		return
	}

	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Invalid log ID: must be a positive integer")
		return
	}

	log, err := h.store.GetLogByID(r.Context(), uint(id))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, ErrCodeNotFound, "Log not found")
			return
		}
		h.logger.Error("failed to get log", zap.Uint64("id", id), zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to get log")
		return
	}

	// Fetch attempts if request has a request_id
	if log.RequestID != "" {
		attempts, err := h.store.GetAttemptsByRequestID(r.Context(), log.RequestID)
		if err != nil {
			h.logger.Error("failed to get attempts", zap.String("request_id", log.RequestID), zap.Error(err))
			// Don't fail the request, just log the error and return the log without attempts
		} else {
			log.Attempts = attempts
		}
	}

	writeJSON(w, http.StatusOK, log)
}
