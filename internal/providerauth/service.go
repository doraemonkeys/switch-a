package providerauth

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"switch-a/internal"
	"switch-a/internal/model"

	"go.uber.org/zap"
)

const (
	defaultOAuthIssuer       = "https://auth.openai.com"
	defaultOAuthClientID     = "app_EMoamEEZ73f0CkXaXp7hrann"
	defaultOAuthScope        = "openid profile email offline_access"
	defaultOAuthOriginator   = "codex_vscode"
	chatGPTOAuthUserAgent    = "switch-a/0.1"
	chatGPTCodexOriginator   = "codex_cli_rs"
	chatGPTCodexBaseURL      = "https://chatgpt.com/backend-api/codex"
	codexAPIType             = "codex"
	authModeAuto             = "auto"
	authModeBearer           = "bearer"
	authModeXAPIKey          = "x-api-key"
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

const (
	providerCredentialTypeAPIKey  = model.ProviderCredentialTypeAPIKey
	providerCredentialTypeChatGPT = model.ProviderCredentialTypeChatGPT
)

// CredentialStore persists refresh-capable secrets and the non-sensitive auth
// state snapshot without overwriting unrelated provider configuration.
type CredentialStore interface {
	UpdateProviderCredential(ctx context.Context, id string, credentialType model.ProviderCredentialType, credentialData string) error
	UpdateProviderAuthState(ctx context.Context, providerID string, authState *model.ProviderAuthState) error
}

// OAuthHTTPDoer performs outbound OAuth token requests.
type OAuthHTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Config configures the provider auth service.
type Config struct {
	CredentialStore CredentialStore
	HTTPClient      OAuthHTTPDoer
	Clock           internal.Clock
	Logger          *zap.Logger
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

// Service manages provider-backed authentication flows and credential injection.
type Service struct {
	credentialStore CredentialStore
	httpClient      OAuthHTTPDoer
	clock           internal.Clock
	logger          *zap.Logger

	mu               sync.Mutex
	pendingByState   map[string]pendingLogin
	pendingByLoginID map[string]pendingLogin
	completed        map[string]completedLogin

	refreshMu              sync.Mutex
	inFlightRefreshes      map[string]*inFlightChatGPTRefresh
	recentChatGPTRefreshes map[string]recentChatGPTRefresh
}

// NewService creates a provider auth service.
func NewService(cfg Config) *Service {
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

	return &Service{
		credentialStore:        cfg.CredentialStore,
		httpClient:             httpClient,
		clock:                  clock,
		logger:                 logger,
		pendingByState:         make(map[string]pendingLogin),
		pendingByLoginID:       make(map[string]pendingLogin),
		completed:              make(map[string]completedLogin),
		inFlightRefreshes:      make(map[string]*inFlightChatGPTRefresh),
		recentChatGPTRefreshes: make(map[string]recentChatGPTRefresh),
	}
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

// staticProviderCredentialReady reports whether a non-ChatGPT provider already
// carries usable API-key material in its persisted configuration.
func staticProviderCredentialReady(provider *model.Provider) bool {
	if model.HasAPIKey(provider.APIKey) {
		return true
	}
	for _, apiType := range provider.APITypes {
		if model.HasAPIKey(apiType.APIKey) {
			return true
		}
	}
	return false
}

// NormalizeProviderForPersistence applies provider-type-specific invariants before the
// provider is validated and persisted.
func NormalizeProviderForPersistence(provider *model.Provider) {
	provider.CredentialType = model.NormalizeProviderCredentialType(provider.CredentialType)
	switch provider.CredentialType {
	case providerCredentialTypeChatGPT:
		provider.APIKey = ""
		provider.AuthMode = authModeBearer
		provider.APITypes = []model.ProviderAPIType{{
			ProviderID: provider.ID,
			APIType:    codexAPIType,
			BaseURL:    chatGPTCodexBaseURL,
			APIKey:     "",
		}}
	case providerCredentialTypeAPIKey:
		provider.Credential = nil
	}
}
