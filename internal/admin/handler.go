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
	"switch-a/internal/providerauth"
	"switch-a/internal/proxy"
	"switch-a/internal/store"

	"go.uber.org/zap"
)

// Store defines the storage interface needed by admin handlers.
// This is a subset of internal.Store, defined at the consumer side per Go idiom
// "Accept Interfaces, Return Structs". Admin handlers only need read/list operations
// and don't require log insertion, health state mutations, or cleanup operations.
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
	GetLogByID(ctx context.Context, id uint) (*model.RequestLog, error)
	GetAttemptsByRequestID(ctx context.Context, requestID string) ([]model.RequestAttempt, error)
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

// ActiveRequestLister provides access to active requests.
type ActiveRequestLister interface {
	List() []proxy.ActiveRequest
}

// ActiveRequest is an alias for proxy.ActiveRequest for convenience.
type ActiveRequest = proxy.ActiveRequest

// ProviderAuthService captures the provider-auth behaviors the admin surface needs
// without binding handlers to the concrete OAuth service implementation.
type ProviderAuthService interface {
	StartChatGPTLogin() (*providerauth.ChatGPTLoginStartResponse, error)
	GetChatGPTLoginStatus(loginID string) (*providerauth.ChatGPTLoginStatusResponse, error)
	ApplyChatGPTLogin(provider *model.Provider, loginID string) error
	FinalizeChatGPTLogin(loginID string) error
	BuildProviderAuthView(provider *model.Provider) *providerauth.ProviderAuthView
	RefreshProviderCredentials(ctx context.Context, provider *model.Provider) (bool, error)
	RefreshProviderUsage(ctx context.Context, provider *model.Provider) (bool, error)
}

// Handler handles admin API requests.
type Handler struct {
	store         Store
	health        internal.HealthManager
	concurrency   ConcurrencyTracker
	cleaner       ConcurrencyCleaner
	activeReqList ActiveRequestLister
	auth          ProviderAuthService
	logger        *zap.Logger
}

// Config holds admin handler configuration.
type Config struct {
	Store         Store
	Health        internal.HealthManager
	Concurrency   ConcurrencyTracker
	Cleaner       ConcurrencyCleaner
	ActiveReqList ActiveRequestLister
	Auth          ProviderAuthService
	Logger        *zap.Logger
}

// NewHandler creates a new admin handler.
func NewHandler(cfg Config) *Handler {
	return &Handler{
		store:         cfg.Store,
		health:        cfg.Health,
		concurrency:   cfg.Concurrency,
		cleaner:       cfg.Cleaner,
		activeReqList: cfg.ActiveReqList,
		auth:          cfg.Auth,
		logger:        cfg.Logger,
	}
}

// deleteConfig holds configuration for the generic delete handler.
type deleteConfig struct {
	resourceType string
	getFunc      func(ctx context.Context, id string) error
	deleteFunc   func(ctx context.Context, id string) error
}

// handleDelete provides consistent error handling and response formatting for
// all resource deletions, ensuring uniform API behavior across providers, groups,
// and other deletable entities.
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
	w.Header().Set("Content-Type", ContentTypeJSON)
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil { // coverage-ignore -- JSON encoding rarely fails
		// Can't write error response since headers are already sent
		return
	}
}

// writeError writes an error response in the standard format.
func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", ContentTypeJSON)
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
