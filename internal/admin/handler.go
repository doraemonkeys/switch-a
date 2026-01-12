package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"switch-a/internal"
	"switch-a/internal/model"
	"switch-a/internal/store"

	"go.uber.org/zap"
)

// Store defines the storage interface needed by admin handlers.
type Store interface {
	// Provider operations
	ListProviders(ctx context.Context) ([]model.Provider, error)
	GetProvider(ctx context.Context, id string) (*model.Provider, error)
	CreateProvider(ctx context.Context, p *model.Provider) error
	UpdateProvider(ctx context.Context, p *model.Provider) error
	DeleteProvider(ctx context.Context, id string) error

	// Group operations
	ListGroups(ctx context.Context) ([]model.Group, error)
	GetGroup(ctx context.Context, id string) (*model.Group, error)
	CreateGroup(ctx context.Context, g *model.Group) error
	UpdateGroup(ctx context.Context, g *model.Group) error
	DeleteGroup(ctx context.Context, id string) error

	// Health state operations
	GetHealthState(ctx context.Context, providerID string) (*model.HealthState, error)
	GetHealthStatesByProviderIDs(ctx context.Context, providerIDs []string) (map[string]*model.HealthState, error)
	ListHealthStates(ctx context.Context) ([]model.HealthState, error)

	// Config operations
	GetAllConfig(ctx context.Context) (map[string]string, error)
	SetConfig(ctx context.Context, key, value string) error
	SetConfigs(ctx context.Context, configs map[string]string) error

	// Log operations
	ListLogs(ctx context.Context, filter model.LogFilter) ([]model.RequestLog, error)
	CountLogs(ctx context.Context, filter model.LogFilter) (int64, error)
	GetLogStats(ctx context.Context, startTime, endTime time.Time) (*model.LogStats, error)
}

// ConcurrencyTracker provides concurrency information for status API.
type ConcurrencyTracker interface {
	Current(providerID string) int64
}

// ConcurrencyCleaner provides cleanup for provider concurrency counters.
// This should be called when a provider is deleted to prevent memory leaks.
type ConcurrencyCleaner interface {
	ClearConcurrency(providerID string)
}

// Handler handles admin API requests.
type Handler struct {
	store       Store
	health      internal.HealthManager
	concurrency ConcurrencyTracker
	cleaner     ConcurrencyCleaner
	logger      *zap.Logger
}

// Config holds admin handler configuration.
type Config struct {
	Store       Store
	Health      internal.HealthManager
	Concurrency ConcurrencyTracker
	Cleaner     ConcurrencyCleaner
	Logger      *zap.Logger
}

// NewHandler creates a new admin handler.
func NewHandler(cfg Config) *Handler {
	return &Handler{
		store:       cfg.Store,
		health:      cfg.Health,
		concurrency: cfg.Concurrency,
		cleaner:     cfg.Cleaner,
		logger:      cfg.Logger,
	}
}

// Group API handlers

// ListGroups handles GET /admin/api/groups.
func (h *Handler) ListGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := h.store.ListGroups(r.Context())
	if err != nil {
		h.logger.Error("failed to list groups", zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to list groups")
		return
	}

	writeJSON(w, http.StatusOK, groups)
}

// GetGroup handles GET /admin/api/groups/{id}.
func (h *Handler) GetGroup(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Group ID is required")
		return
	}

	group, err := h.store.GetGroup(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, ErrCodeNotFound, "Group not found: "+id)
			return
		}
		h.logger.Error("failed to get group", zap.String("id", id), zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to get group")
		return
	}

	writeJSON(w, http.StatusOK, group)
}

// CreateGroupRequest represents the request to create a group.
type CreateGroupRequest struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Strategy string `json:"strategy"`
	Priority int    `json:"priority"`
	Weight   int    `json:"weight"`
	Enabled  *bool  `json:"enabled"`
}

// CreateGroup handles POST /admin/api/groups.
func (h *Handler) CreateGroup(w http.ResponseWriter, r *http.Request) {
	limitRequestBody(w, r)
	var req CreateGroupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Invalid request body")
		return
	}

	if req.ID == "" {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Group ID is required")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Group name is required")
		return
	}

	// Check if group already exists
	_, err := h.store.GetGroup(r.Context(), req.ID)
	if err == nil {
		writeError(w, http.StatusConflict, ErrCodeConflict, "Group already exists: "+req.ID)
		return
	}
	if !errors.Is(err, store.ErrNotFound) {
		h.logger.Error("failed to check group existence", zap.String("id", req.ID), zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to create group")
		return
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	group := &model.Group{
		ID:       req.ID,
		Name:     req.Name,
		Strategy: req.Strategy,
		Priority: req.Priority,
		Weight:   req.Weight,
		Enabled:  enabled,
	}

	// Set defaults and validate
	if group.Strategy == "" {
		group.Strategy = DefaultStrategy
	} else if !IsValidStrategy(group.Strategy) {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Invalid strategy: must be 'priority', 'random', or 'weight'")
		return
	}
	if group.Weight <= 0 {
		group.Weight = DefaultWeight
	}
	// Validate priority is not reserved for ungrouped providers
	if group.Priority == ReservedGroupPriority {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Priority value 2147483647 is reserved for ungrouped providers")
		return
	}

	if err := h.store.CreateGroup(r.Context(), group); err != nil {
		h.logger.Error("failed to create group", zap.String("id", req.ID), zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to create group")
		return
	}

	writeJSON(w, http.StatusCreated, group)
}

// UpdateGroupRequest represents the request to update a group.
type UpdateGroupRequest struct {
	Name     *string `json:"name"`
	Strategy *string `json:"strategy"`
	Priority *int    `json:"priority"`
	Weight   *int    `json:"weight"`
	Enabled  *bool   `json:"enabled"`
}

// UpdateGroup handles PUT /admin/api/groups/{id}.
func (h *Handler) UpdateGroup(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Group ID is required")
		return
	}

	limitRequestBody(w, r)
	var req UpdateGroupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Invalid request body")
		return
	}

	// Validate fields before fetching group
	if req.Name != nil && *req.Name == "" {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Name cannot be empty")
		return
	}
	if req.Weight != nil && *req.Weight <= 0 {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Weight must be positive")
		return
	}

	group, err := h.store.GetGroup(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, ErrCodeNotFound, "Group not found: "+id)
			return
		}
		h.logger.Error("failed to get group", zap.String("id", id), zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to update group")
		return
	}

	// Update fields if provided
	if req.Name != nil {
		group.Name = *req.Name
	}
	if req.Strategy != nil {
		if !IsValidStrategy(*req.Strategy) {
			writeError(w, http.StatusBadRequest, ErrCodeValidation, "Invalid strategy: must be 'priority', 'random', or 'weight'")
			return
		}
		group.Strategy = *req.Strategy
	}
	if req.Priority != nil {
		// Validate priority is not reserved for ungrouped providers
		if *req.Priority == ReservedGroupPriority {
			writeError(w, http.StatusBadRequest, ErrCodeValidation, "Priority value 2147483647 is reserved for ungrouped providers")
			return
		}
		group.Priority = *req.Priority
	}
	if req.Weight != nil {
		group.Weight = *req.Weight
	}
	if req.Enabled != nil {
		group.Enabled = *req.Enabled
	}

	if err := h.store.UpdateGroup(r.Context(), group); err != nil {
		h.logger.Error("failed to update group", zap.String("id", id), zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to update group")
		return
	}

	writeJSON(w, http.StatusOK, group)
}

// DeleteGroup handles DELETE /admin/api/groups/{id}.
func (h *Handler) DeleteGroup(w http.ResponseWriter, r *http.Request) {
	h.handleDelete(w, r, deleteConfig{
		resourceType: "Group",
		getFunc: func(ctx context.Context, id string) error {
			_, err := h.store.GetGroup(ctx, id)
			return err
		},
		deleteFunc: h.store.DeleteGroup,
	})
}

// Config API handlers

// GetConfig handles GET /admin/api/config.
func (h *Handler) GetConfig(w http.ResponseWriter, r *http.Request) {
	config, err := h.store.GetAllConfig(r.Context())
	if err != nil {
		h.logger.Error("failed to get config", zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to get config")
		return
	}

	writeJSON(w, http.StatusOK, config)
}

// UpdateConfig handles PUT /admin/api/config.
func (h *Handler) UpdateConfig(w http.ResponseWriter, r *http.Request) {
	limitRequestBody(w, r)
	var updates map[string]string
	if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Invalid request body")
		return
	}

	if len(updates) > MaxConfigUpdates {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Too many config updates: maximum "+strconv.Itoa(MaxConfigUpdates)+" allowed")
		return
	}

	// Validate all keys and values before updating
	for key, value := range updates {
		if !IsValidConfigKey(key) {
			writeError(w, http.StatusBadRequest, ErrCodeValidation, "Invalid config key: "+key)
			return
		}
		if err := ValidateConfigValue(key, value); err != nil {
			writeError(w, http.StatusBadRequest, ErrCodeValidation, "Invalid value for "+key+": "+err.Error())
			return
		}
	}

	// Update all configs atomically in a single transaction
	if err := h.store.SetConfigs(r.Context(), updates); err != nil {
		h.logger.Error("failed to update configs", zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to update config")
		return
	}

	// Return updated config
	config, err := h.store.GetAllConfig(r.Context())
	if err != nil {
		h.logger.Error("failed to get config after update", zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to get config")
		return
	}

	writeJSON(w, http.StatusOK, config)
}

// Health API handlers

// GetHealth handles GET /admin/api/health.
func (h *Handler) GetHealth(w http.ResponseWriter, r *http.Request) {
	states, err := h.store.ListHealthStates(r.Context())
	if err != nil {
		h.logger.Error("failed to list health states", zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to get health states")
		return
	}

	writeJSON(w, http.StatusOK, states)
}

// Status API handlers

// ProviderStatus represents the status of a single provider.
type ProviderStatus struct {
	ID              string             `json:"id"`
	Name            string             `json:"name"`
	Enabled         bool               `json:"enabled"`
	CurrentRequests int64              `json:"current_requests"`
	Health          *model.HealthState `json:"health"`
}

// SystemStatus represents the overall system status.
type SystemStatus struct {
	Providers []ProviderStatus `json:"providers"`
}

// GetStatus handles GET /admin/api/status.
func (h *Handler) GetStatus(w http.ResponseWriter, r *http.Request) {
	providers, err := h.store.ListProviders(r.Context())
	if err != nil {
		h.logger.Error("failed to list providers", zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to get status")
		return
	}

	status := SystemStatus{
		Providers: make([]ProviderStatus, len(providers)),
	}

	for i, p := range providers {
		ps := ProviderStatus{
			ID:      p.ID,
			Name:    p.Name,
			Enabled: p.Enabled,
		}

		// Get current concurrency
		if h.concurrency != nil {
			ps.CurrentRequests = h.concurrency.Current(p.ID)
		}

		// Get health state.
		// Partial failure design: if health state retrieval fails for a provider,
		// we log and continue rather than failing the entire request. This allows
		// clients to receive status for all available providers even when some
		// health data is temporarily unavailable.
		state, err := h.store.GetHealthState(r.Context(), p.ID)
		if err != nil {
			h.logger.Warn("failed to get health state", zap.String("id", p.ID), zap.Error(err))
		} else {
			ps.Health = state
		}

		status.Providers[i] = ps
	}

	writeJSON(w, http.StatusOK, status)
}

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
//   - api_type: filter by API type (claude/codex/gemini/custom:*)
//   - success: filter by success/failure (true/false)
//   - user_id: filter by user ID
//   - start_time: filter by start time (RFC3339)
//   - end_time: filter by end time (RFC3339)
//   - min_latency: filter by minimum latency in ms
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

// Helper functions

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

// parseLogFilter parses query parameters into a LogFilter.
// Returns the filter and an error message (empty string if no error).
func parseLogFilter(query map[string][]string) (model.LogFilter, string) {
	var filter model.LogFilter
	var errMsg string

	filter.Limit, errMsg = parsePositiveInt(getQueryParam(query, "limit"), "limit", DefaultLogsLimit)
	if errMsg != "" {
		return filter, errMsg
	}
	if filter.Limit > MaxLogsLimit {
		filter.Limit = MaxLogsLimit
	}

	filter.Offset, errMsg = parseNonNegativeInt(getQueryParam(query, "offset"), "offset", 0)
	if errMsg != "" {
		return filter, errMsg
	}

	filter.ProviderID = getQueryParam(query, "provider_id")
	filter.APIType = getQueryParam(query, "api_type")
	filter.UserID = getQueryParam(query, "user_id")

	filter.Success, errMsg = parseBoolPtr(getQueryParam(query, "success"), "success")
	if errMsg != "" {
		return filter, errMsg
	}

	filter.StartTime, errMsg = parseTimePtr(getQueryParam(query, "start_time"), "start_time")
	if errMsg != "" {
		return filter, errMsg
	}

	filter.EndTime, errMsg = parseTimePtr(getQueryParam(query, "end_time"), "end_time")
	if errMsg != "" {
		return filter, errMsg
	}

	filter.MinLatency, errMsg = parseNonNegativeInt64Ptr(getQueryParam(query, "min_latency"), "min_latency")
	if errMsg != "" {
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

// deleteConfig holds configuration for the generic delete handler.
type deleteConfig struct {
	resourceType string
	getFunc      func(ctx context.Context, id string) error
	deleteFunc   func(ctx context.Context, id string) error
}

// handleDelete is a generic delete handler to reduce code duplication.
func (h *Handler) handleDelete(w http.ResponseWriter, r *http.Request, cfg deleteConfig) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, cfg.resourceType+" ID is required")
		return
	}

	// Check if resource exists
	if err := cfg.getFunc(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, ErrCodeNotFound, cfg.resourceType+" not found: "+id)
			return
		}
		h.logger.Error("failed to get "+strings.ToLower(cfg.resourceType), zap.String("id", id), zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to delete "+strings.ToLower(cfg.resourceType))
		return
	}

	if err := cfg.deleteFunc(r.Context(), id); err != nil {
		h.logger.Error("failed to delete "+strings.ToLower(cfg.resourceType), zap.String("id", id), zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to delete "+strings.ToLower(cfg.resourceType))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil { // coverage-ignore -- JSON encoding rarely fails
		// Can't write error response since headers are already sent
		return
	}
}

// writeError writes an error response in the standard format.
func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	resp := model.ErrorResponse{
		Code:    code,
		Message: message,
	}
	if err := json.NewEncoder(w).Encode(resp); err != nil { // coverage-ignore -- JSON encoding rarely fails
		return
	}
}

// limitRequestBody wraps the request body with a size limit to prevent abuse.
func limitRequestBody(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, MaxRequestBodySize)
}
