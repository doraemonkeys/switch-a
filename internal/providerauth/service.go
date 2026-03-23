package providerauth

import (
	"context"
	"fmt"
	"net/http"
	"strings"
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
	callbackPageTitle        = "Switch-A GPT Login"
)

const (
	providerCredentialTypeAPIKey  = model.ProviderCredentialTypeAPIKey
	providerCredentialTypeChatGPT = model.ProviderCredentialTypeChatGPT
)

// CredentialStore persists refreshed provider credentials without overwriting the
// rest of the provider configuration.
type CredentialStore interface {
	UpdateProviderCredential(ctx context.Context, id string, credentialType model.ProviderCredentialType, credentialData string) error
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
		credentialStore:  cfg.CredentialStore,
		httpClient:       httpClient,
		clock:            clock,
		logger:           logger,
		pendingByState:   make(map[string]pendingLogin),
		pendingByLoginID: make(map[string]pendingLogin),
		completed:        make(map[string]completedLogin),
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

// BuildAuthProfile derives a non-sensitive credential summary for API responses.
func BuildAuthProfile(provider *model.Provider) *model.ProviderAuthProfile {
	if provider == nil {
		return nil
	}

	credentialType := model.NormalizeProviderCredentialType(provider.CredentialType)
	profile := &model.ProviderAuthProfile{
		Type: credentialType,
	}

	switch credentialType {
	case providerCredentialTypeChatGPT:
		credential, err := decodeChatGPTCredential(provider.CredentialData)
		if err != nil {
			return profile
		}
		return buildChatGPTAuthProfile(credential)
	default:
		profile.Ready = staticProviderCredentialReady(provider)
		return profile
	}
}

func buildChatGPTAuthProfile(credential *model.ChatGPTProviderCredential) *model.ProviderAuthProfile {
	profile := &model.ProviderAuthProfile{
		Type: providerCredentialTypeChatGPT,
	}
	if credential == nil {
		return profile
	}

	profile.Ready = credential.Ready()
	profile.Email = credential.Email
	profile.AccountID = credential.AccountID
	if credential.Usage != nil {
		profile.Usage = cloneProviderUsageSnapshot(credential.Usage)
	}
	profile.PlanType = strings.TrimSpace(credential.PlanType)
	if profile.PlanType == "" && profile.Usage != nil {
		profile.PlanType = strings.TrimSpace(profile.Usage.PlanType)
	}
	if !credential.ExpiresAt.IsZero() {
		expiresAt := credential.ExpiresAt
		profile.ExpiresAt = &expiresAt
	}
	if !credential.LastRefresh.IsZero() {
		lastRefresh := credential.LastRefresh
		profile.LastRefresh = &lastRefresh
	}
	return profile
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
		provider.CredentialData = ""
	}
}
