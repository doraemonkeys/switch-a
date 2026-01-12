package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
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
	GetLogTimeSeries(ctx context.Context, startTime, endTime time.Time, granularity time.Duration) ([]model.TimeSeriesPoint, error)
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
