package providerauth

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/model"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

const (
	defaultOAuthIssuer       = "https://auth.openai.com"
	defaultOAuthClientID     = "app_EMoamEEZ73f0CkXaXp7hrann"
	defaultOAuthScope        = "openid profile email offline_access"
	defaultOAuthOriginator   = "codex_vscode"
	chatGPTOAuthUserAgent    = "github.com/doraemonkeys/switch-a/0.1"
	chatGPTCodexOriginator   = "codex_cli_rs"
	chatGPTCodexBaseURL      = "https://chatgpt.com/backend-api/codex"
	chatGPTAPIAudience       = "https://api.openai.com/v1"
	codexAPIType             = "codex"
	loopbackCallbackURLHost  = "localhost"
	loopbackCallbackPort     = 1455
	loopbackCallbackPath     = "/auth/callback"
	loginSessionTTL          = 10 * time.Minute
	completedLoginSessionTTL = 15 * time.Minute
	proactiveRefreshWindow   = 60 * time.Second
	// Keep the newest credential briefly so stale provider copies within the same
	// process do not re-use a refresh token that was just rotated successfully.
	recentRefreshReuseWindow = 2 * proactiveRefreshWindow
	callbackPageTitle        = "Switch-A GPT Login"
)

// CredentialStore persists refresh-capable secrets and the non-sensitive auth
// state snapshot without overwriting unrelated provider configuration.
type CredentialStore interface {
	GetCredentialSession(ctx context.Context, sessionID string) (*credentialsession.Session, error)
	WithCredentialSessionMutations(
		ctx context.Context,
		sessionIDs []string,
	) (ownedCtx context.Context, release func(), err error)
	UpdateCredentialSessionCAS(ctx context.Context, sessionID string, expectedVersion int64, secretData string, subject credentialsession.Subject, authState credentialsession.AuthState) (int64, error)
	UpdateCredentialSessionAuthState(ctx context.Context, sessionID string, authState credentialsession.AuthState) error
}

// OAuthHTTPDoer performs outbound OAuth token requests.
type OAuthHTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// IDGenerator keeps process-local auth and import session identifiers opaque
// while allowing lifecycle behavior to be deterministic in tests.
type IDGenerator interface {
	NewID() string
}

type uuidIDGenerator struct{}

func (uuidIDGenerator) NewID() string {
	return uuid.NewString()
}

// Config configures the provider auth service.
type Config struct {
	CredentialStore any
	HTTPClient      OAuthHTTPDoer
	Clock           internal.Clock
	Logger          *zap.Logger
	IDGenerator     IDGenerator
}

type scheduledTask interface {
	Stop() bool
}

type scheduleAfterFunc func(delay time.Duration, task func()) scheduledTask

// serviceRuntime keeps process-bound infrastructure out of the public service
// configuration while still allowing lifecycle behavior to be tested without
// claiming the machine-wide OAuth port or waiting on wall-clock timers.
type serviceRuntime struct {
	callback      callbackEndpoint
	scheduleAfter scheduleAfterFunc
}

type inFlightChatGPTRefresh struct {
	done       chan struct{}
	credential *model.ChatGPTProviderCredential
	err        error
}

type recentChatGPTRefresh struct {
	credential *model.ChatGPTProviderCredential
	expiresAt  time.Time
}

type inFlightProviderUsageObservation struct {
	latest *model.ProviderUsageSnapshot
}

// Service manages provider-backed authentication flows and credential injection.
type Service struct {
	credentialStore any
	httpClient      OAuthHTTPDoer
	clock           internal.Clock
	logger          *zap.Logger
	idGenerator     IDGenerator
	callback        callbackEndpoint
	scheduleAfter   scheduleAfterFunc

	callbackLifecycleMu sync.Mutex

	mu                  sync.Mutex
	pendingByState      map[string]pendingLogin
	pendingByLoginID    map[string]pendingLogin
	completed           map[string]completedLogin
	providerImports     map[string]stagedChatGPTProviderImport
	providerImportSlots int
	providerImportBytes int64
	callbackActive      bool
	sessionExpiryTask   scheduledTask
	sessionExpiryEpoch  uint64
	shutdown            bool

	refreshMu              sync.Mutex
	inFlightRefreshes      map[string]*inFlightChatGPTRefresh
	recentChatGPTRefreshes map[string]recentChatGPTRefresh

	usageObservationMu        sync.Mutex
	inFlightUsageObservations map[string]*inFlightProviderUsageObservation
}

// NewService creates a provider auth service.
func NewService(cfg Config) *Service {
	return newService(cfg, serviceRuntime{})
}

func newService(cfg Config, runtime serviceRuntime) *Service {
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}

	clock := cfg.Clock
	if clock == nil {
		clock = internal.RealClock{}
	}

	logger := cfg.Logger
	if logger == nil {
		logger = zap.NewNop()
	}

	idGenerator := cfg.IDGenerator
	if idGenerator == nil {
		idGenerator = uuidIDGenerator{}
	}

	scheduleAfter := runtime.scheduleAfter
	if scheduleAfter == nil {
		scheduleAfter = func(delay time.Duration, task func()) scheduledTask {
			return time.AfterFunc(delay, task)
		}
	}

	service := &Service{
		credentialStore:           cfg.CredentialStore,
		httpClient:                httpClient,
		clock:                     clock,
		logger:                    logger,
		idGenerator:               idGenerator,
		callback:                  runtime.callback,
		scheduleAfter:             scheduleAfter,
		pendingByState:            make(map[string]pendingLogin),
		pendingByLoginID:          make(map[string]pendingLogin),
		completed:                 make(map[string]completedLogin),
		providerImports:           make(map[string]stagedChatGPTProviderImport),
		inFlightRefreshes:         make(map[string]*inFlightChatGPTRefresh),
		recentChatGPTRefreshes:    make(map[string]recentChatGPTRefresh),
		inFlightUsageObservations: make(map[string]*inFlightProviderUsageObservation),
	}
	if service.callback == nil {
		service.callback = newLoopbackCallbackServer(http.HandlerFunc(service.handleChatGPTOAuthCallback), logger)
	}
	return service
}

// LoopbackCallbackAddress returns the fixed OAuth callback address mirrored from codex-tools.
func LoopbackCallbackAddress() string {
	return fmt.Sprintf("http://%s:%d%s", loopbackCallbackURLHost, loopbackCallbackPort, loopbackCallbackPath)
}

// LoopbackCallbackPort returns the dedicated loopback listener port.
func LoopbackCallbackPort() int {
	return loopbackCallbackPort
}

// ChatGPTCodexBaseURL returns the upstream base URL used by ChatGPT-backed Codex providers.
func ChatGPTCodexBaseURL() string {
	return chatGPTCodexBaseURL
}
