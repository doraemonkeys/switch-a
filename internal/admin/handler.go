package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	adminerrorruleapi "github.com/doraemonkeys/switch-a/internal/admin/errorruleapi"
	adminproviderimport "github.com/doraemonkeys/switch-a/internal/admin/providerimport"
	"github.com/doraemonkeys/switch-a/internal/analyticswindow"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/providerauth"
	"github.com/doraemonkeys/switch-a/internal/proxy"
	"github.com/doraemonkeys/switch-a/internal/store"

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

	// Routing policy operations
	ListRoutingPolicies(ctx context.Context) ([]model.RoutingPolicy, error)
	GetRoutingPolicy(ctx context.Context, id uint) (*model.RoutingPolicy, error)
	CreateRoutingPolicy(ctx context.Context, policy *model.RoutingPolicy) error
	UpdateRoutingPolicy(ctx context.Context, policy *model.RoutingPolicy) error
	DeleteRoutingPolicy(ctx context.Context, id uint) error

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
	ApplyConfigImport(ctx context.Context, bundle *store.ConfigImportBundle) error

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

// ProviderLifecycleCoordinator makes durable eligibility mutations atomic with
// selector generation retirement. Group and routing mutations use the global
// boundary because their effects are not confined to one provider ID.
type ProviderLifecycleCoordinator interface {
	RetireProviderGeneration(providerID string, mutation func() error) error
	RetireAllProviderGenerations(mutation func() error) error
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
	ImportChatGPTLogin(ctx context.Context, rawAuthData string) (*providerauth.ChatGPTLoginStatusResponse, error)
}

type ProviderImportService = adminproviderimport.DraftService
type ProviderImportStore = adminproviderimport.Store

// Handler handles admin API requests.
type Handler struct {
	store                 Store
	health                internal.HealthManager
	concurrency           ConcurrencyTracker
	providerLifecycles    ProviderLifecycleCoordinator
	activeReqList         ActiveRequestLister
	auth                  ProviderAuthService
	providerImportHandler *adminproviderimport.Handler
	internalErrorRules    *adminerrorruleapi.Handler
	statsWindowResolver   *analyticswindow.Resolver
	configMutationMu      sync.Mutex
	logger                *zap.Logger
}

// Config holds admin handler configuration.
type Config struct {
	Store               Store
	Health              internal.HealthManager
	Concurrency         ConcurrencyTracker
	ProviderLifecycles  ProviderLifecycleCoordinator
	ActiveReqList       ActiveRequestLister
	Auth                ProviderAuthService
	ProviderImports     ProviderImportService
	ProviderImportStore ProviderImportStore
	InternalErrorRules  *adminerrorruleapi.Handler
	StatsWindowResolver *analyticswindow.Resolver
	Logger              *zap.Logger
}

// NewHandler creates a new admin handler.
func NewHandler(cfg Config) *Handler {
	if cfg.StatsWindowResolver == nil {
		resolver := analyticswindow.NewResolver(internal.RealClock{})
		cfg.StatsWindowResolver = &resolver
	}
	handler := &Handler{
		store:               cfg.Store,
		health:              cfg.Health,
		concurrency:         cfg.Concurrency,
		providerLifecycles:  cfg.ProviderLifecycles,
		activeReqList:       cfg.ActiveReqList,
		auth:                cfg.Auth,
		internalErrorRules:  cfg.InternalErrorRules,
		statsWindowResolver: cfg.StatsWindowResolver,
		logger:              cfg.Logger,
	}
	handler.providerImportHandler = adminproviderimport.NewHandler(adminproviderimport.Config{
		ProviderCatalog: cfg.Store,
		Drafts:          cfg.ProviderImports,
		Store:           cfg.ProviderImportStore,
		Lifecycles:      cfg.ProviderLifecycles,
		Logger:          cfg.Logger,
	})
	return handler
}

func (h *Handler) mutateProviderGeneration(providerID string, mutation func() error) error {
	if h.providerLifecycles == nil {
		return mutation()
	}
	return h.providerLifecycles.RetireProviderGeneration(providerID, mutation)
}

func (h *Handler) mutateAllProviderGenerations(mutation func() error) error {
	if h.providerLifecycles == nil {
		return mutation()
	}
	return h.providerLifecycles.RetireAllProviderGenerations(mutation)
}

// GetAPICatalog serves the canonical projection without consulting storage so
// every authenticated client observes the same routing/analysis contract for
// the running binary.
func (h *Handler) GetAPICatalog(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, APICatalogResponse())
}

func (h *Handler) ListInternalErrorRules(w http.ResponseWriter, r *http.Request) {
	h.internalErrorRules.ListRules(w, r)
}

func (h *Handler) GetInternalErrorRule(w http.ResponseWriter, r *http.Request) {
	h.internalErrorRules.GetRule(w, r)
}

func (h *Handler) CreateInternalErrorRule(w http.ResponseWriter, r *http.Request) {
	h.internalErrorRules.CreateRule(w, r)
}

func (h *Handler) UpdateInternalErrorRule(w http.ResponseWriter, r *http.Request) {
	h.internalErrorRules.UpdateRule(w, r)
}

func (h *Handler) DeleteInternalErrorRule(w http.ResponseWriter, r *http.Request) {
	h.internalErrorRules.DeleteRule(w, r)
}

func (h *Handler) ReorderInternalErrorRules(w http.ResponseWriter, r *http.Request) {
	h.internalErrorRules.ReorderRules(w, r)
}

func (h *Handler) GetInternalErrorRuleStats(w http.ResponseWriter, r *http.Request) {
	h.internalErrorRules.GetStats(w, r)
}

func (h *Handler) TestInternalErrorMessage(w http.ResponseWriter, r *http.Request) {
	h.internalErrorRules.TestMessage(w, r)
}

func (h *Handler) PreviewProviderImport(w http.ResponseWriter, r *http.Request) {
	h.providerImportHandler.PreviewProviderImport(w, r)
}

func (h *Handler) CommitProviderImport(w http.ResponseWriter, r *http.Request) {
	h.providerImportHandler.CommitProviderImport(w, r)
}

func (h *Handler) CancelProviderImport(w http.ResponseWriter, r *http.Request) {
	h.providerImportHandler.CancelProviderImport(w, r)
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
		if errors.Is(err, store.ErrRoutingPolicyReferenceConflict) {
			writeError(w, http.StatusConflict, ErrCodeConflict, err.Error())
			return
		}
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
	writeErrorWithDetails(w, status, code, message, nil)
}

// writeErrorWithDetails keeps the standard error envelope while allowing
// caller-actionable conflicts to expose structured resolution data.
func writeErrorWithDetails(w http.ResponseWriter, status int, code, message string, details map[string]string) {
	w.Header().Set("Content-Type", ContentTypeJSON)
	w.WriteHeader(status)
	resp := model.ErrorResponse{
		Code:    code,
		Message: message,
		Details: details,
	}
	if err := json.NewEncoder(w).Encode(resp); err != nil { // coverage-ignore -- JSON encoding rarely fails
		return
	}
}

// limitRequestBody wraps the request body with a size limit to prevent abuse.
func limitRequestBody(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, MaxRequestBodySize)
}
