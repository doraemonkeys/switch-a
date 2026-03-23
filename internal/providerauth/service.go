package providerauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"switch-a/internal"
	"switch-a/internal/model"

	"github.com/google/uuid"
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

type pendingLogin struct {
	loginID      string
	state        string
	codeVerifier string
	expiresAt    time.Time
}

type completedLogin struct {
	loginID    string
	credential model.ChatGPTProviderCredential
	expiresAt  time.Time
}

// ChatGPTLoginStartResponse is returned to the admin UI before the popup opens.
type ChatGPTLoginStartResponse struct {
	LoginID string `json:"login_id"`
	AuthURL string `json:"auth_url"`
}

// ChatGPTLoginStatus identifies the current state of a pending or completed GPT login flow.
type ChatGPTLoginStatus string

const (
	ChatGPTLoginStatusPending   ChatGPTLoginStatus = "pending"
	ChatGPTLoginStatusCompleted ChatGPTLoginStatus = "completed"
	ChatGPTLoginStatusExpired   ChatGPTLoginStatus = "expired"
)

// ChatGPTLoginStatusResponse describes whether a login session is still pending,
// ready to assign, or has already expired from the local session cache.
type ChatGPTLoginStatusResponse struct {
	LoginID     string                     `json:"login_id"`
	Status      ChatGPTLoginStatus         `json:"status"`
	AuthProfile *model.ProviderAuthProfile `json:"auth_profile,omitempty"`
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

// PopulateProviderAuthProfile keeps admin-facing provider metadata close to the
// real upstream account state without making the admin API depend on raw tokens.
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

// StartChatGPTLogin prepares a ChatGPT OAuth login flow for the admin UI popup.
func (s *Service) StartChatGPTLogin() (*ChatGPTLoginStartResponse, error) {
	state, err := randomURLSafeString(32)
	if err != nil {
		return nil, err
	}
	codeVerifier, err := randomURLSafeString(48)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256([]byte(codeVerifier))
	codeChallenge := base64.RawURLEncoding.EncodeToString(hash[:])
	loginID := uuid.NewString()
	now := s.clock.Now()

	authURL, err := url.Parse(defaultOAuthIssuer + "/oauth/authorize")
	if err != nil {
		return nil, fmt.Errorf("build oauth authorize url: %w", err)
	}
	query := authURL.Query()
	query.Set("response_type", "code")
	query.Set("client_id", defaultOAuthClientID)
	query.Set("redirect_uri", LoopbackCallbackAddress())
	query.Set("scope", defaultOAuthScope)
	query.Set("state", state)
	query.Set("code_challenge", codeChallenge)
	query.Set("code_challenge_method", "S256")
	query.Set("id_token_add_organizations", "true")
	query.Set("codex_cli_simplified_flow", "true")
	query.Set("originator", defaultOAuthOriginator)
	authURL.RawQuery = query.Encode()

	expiresAt := now.Add(loginSessionTTL)
	s.mu.Lock()
	s.pruneExpiredSessionsLocked(now)
	s.storePendingLoginLocked(pendingLogin{
		loginID:      loginID,
		state:        state,
		codeVerifier: codeVerifier,
		expiresAt:    expiresAt,
	})
	s.mu.Unlock()

	return &ChatGPTLoginStartResponse{
		LoginID: loginID,
		AuthURL: authURL.String(),
	}, nil
}

// GetChatGPTLoginStatus reports whether a prepared login session is still waiting
// for OAuth completion, already has refresh-capable credentials, or has expired.
func (s *Service) GetChatGPTLoginStatus(loginID string) (*ChatGPTLoginStatusResponse, error) {
	now := s.clock.Now()

	s.mu.Lock()
	s.pruneExpiredSessionsLocked(now)

	if completed, ok := s.completed[loginID]; ok {
		s.mu.Unlock()
		return &ChatGPTLoginStatusResponse{
			LoginID:     loginID,
			Status:      ChatGPTLoginStatusCompleted,
			AuthProfile: buildChatGPTAuthProfile(&completed.credential),
		}, nil
	}

	if _, ok := s.pendingByLoginID[loginID]; ok {
		s.mu.Unlock()
		return &ChatGPTLoginStatusResponse{
			LoginID: loginID,
			Status:  ChatGPTLoginStatusPending,
		}, nil
	}
	s.mu.Unlock()

	return &ChatGPTLoginStatusResponse{
		LoginID: loginID,
		Status:  ChatGPTLoginStatusExpired,
	}, nil
}

// lookupCompletedChatGPTLogin retrieves a finished login session without consuming it.
func (s *Service) lookupCompletedChatGPTLogin(loginID string) (*model.ChatGPTProviderCredential, error) {
	now := s.clock.Now()

	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneExpiredSessionsLocked(now)
	completed, ok := s.completed[loginID]
	if !ok {
		return nil, fmt.Errorf("chatgpt login session %q not found or expired", loginID)
	}
	credential := completed.credential
	return &credential, nil
}

// ApplyChatGPTLogin stages a finished login session onto the provider without
// consuming the session so the admin layer can finalize only after persistence succeeds.
func (s *Service) ApplyChatGPTLogin(provider *model.Provider, loginID string) error {
	credential, err := s.lookupCompletedChatGPTLogin(loginID)
	if err != nil {
		return err
	}
	return applyChatGPTCredential(provider, credential)
}

// FinalizeChatGPTLogin consumes a finished login session after the provider write succeeds.
func (s *Service) FinalizeChatGPTLogin(loginID string) error {
	now := s.clock.Now()

	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneExpiredSessionsLocked(now)
	if _, ok := s.completed[loginID]; !ok {
		return fmt.Errorf("chatgpt login session %q not found or expired", loginID)
	}
	delete(s.completed, loginID)
	return nil
}

// ApplyProviderCredentials injects the upstream auth material for the selected provider.
func (s *Service) ApplyProviderCredentials(ctx context.Context, headers http.Header, provider *model.Provider, apiType, globalAuthMode string, originalReq *http.Request) error {
	switch model.NormalizeProviderCredentialType(provider.CredentialType) {
	case providerCredentialTypeChatGPT:
		if apiType != codexAPIType {
			return fmt.Errorf("chatgpt provider %q only supports api_type %q", provider.ID, codexAPIType)
		}

		credential, err := s.ensureFreshChatGPTCredential(ctx, provider, false)
		if err != nil {
			return err
		}

		headers.Set("Authorization", "Bearer "+credential.AccessToken)
		headers.Set("ChatGPT-Account-Id", credential.AccountID)
		// Preserve a caller-supplied Originator so Codex variants can retain their
		// own identity; only default when the proxy is the first component to add it.
		if headers.Get("Originator") == "" {
			headers.Set("Originator", chatGPTCodexOriginator)
		}
		return nil
	default:
		apiKey := provider.APIKeyForAPIType(apiType)
		applyStaticAuthHeader(headers, apiKey, provider.AuthMode, globalAuthMode, originalReq)
		return nil
	}
}

// RefreshProviderCredentials forces a credential refresh for providers that support it.
// Returns true when a refresh was attempted.
func (s *Service) RefreshProviderCredentials(ctx context.Context, provider *model.Provider) (bool, error) {
	switch model.NormalizeProviderCredentialType(provider.CredentialType) {
	case providerCredentialTypeChatGPT:
		_, err := s.ensureFreshChatGPTCredential(ctx, provider, true)
		return true, err
	default:
		return false, nil
	}
}

func (s *Service) ensureFreshChatGPTCredential(ctx context.Context, provider *model.Provider, force bool) (*model.ChatGPTProviderCredential, error) {
	credential, err := decodeChatGPTCredential(provider.CredentialData)
	if err != nil {
		return nil, err
	}
	if !credential.Ready() {
		return nil, fmt.Errorf("provider %q has an incomplete chatgpt credential", provider.ID)
	}

	now := s.clock.Now()
	if !force && credential.ExpiresAt.After(now.Add(proactiveRefreshWindow)) {
		return credential, nil
	}

	refreshed, err := s.refreshChatGPTCredential(ctx, credential)
	if err != nil {
		return nil, err
	}
	if err := applyChatGPTCredential(provider, refreshed); err != nil {
		return nil, err
	}
	if err := s.persistChatGPTCredential(ctx, provider, refreshed); err != nil {
		return nil, err
	}

	return refreshed, nil
}

func applyChatGPTCredential(provider *model.Provider, credential *model.ChatGPTProviderCredential) error {
	if provider == nil {
		return fmt.Errorf("provider is required")
	}
	if credential == nil {
		return fmt.Errorf("chatgpt credential is required")
	}

	raw, err := encodeChatGPTCredential(*credential)
	if err != nil {
		return err
	}

	provider.CredentialType = providerCredentialTypeChatGPT
	provider.CredentialData = raw
	provider.AuthProfile = buildChatGPTAuthProfile(credential)
	return nil
}

func (s *Service) persistChatGPTCredential(ctx context.Context, provider *model.Provider, credential *model.ChatGPTProviderCredential) error {
	if s.credentialStore != nil && provider.ID != "" {
		if err := s.credentialStore.UpdateProviderCredential(ctx, provider.ID, providerCredentialTypeChatGPT, provider.CredentialData); err != nil {
			return fmt.Errorf("persist refreshed chatgpt credential for provider %q: %w", provider.ID, err)
		}
	}
	return nil
}

func (s *Service) refreshChatGPTCredential(ctx context.Context, credential *model.ChatGPTProviderCredential) (*model.ChatGPTProviderCredential, error) {
	issuer, clientID, err := resolveChatGPTRefreshContext(credential)
	if err != nil {
		return nil, err
	}
	tokenURL := strings.TrimRight(issuer, "/") + "/oauth/token"

	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", credential.RefreshToken)
	if clientID != "" {
		form.Set("client_id", clientID)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build chatgpt refresh request: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("User-Agent", chatGPTOAuthUserAgent)

	response, err := s.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("refresh chatgpt token: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, readHTTPError(response, "refresh chatgpt token")
	}

	var payload refreshedTokenPayload
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode refreshed chatgpt token response: %w", err)
	}

	return buildRefreshedChatGPTCredential(
		credential,
		payload.AccessToken,
		firstNonEmpty(payload.RefreshToken, credential.RefreshToken),
		payload.IDToken,
		issuer,
		clientID,
		s.clock.Now(),
	)
}

func (s *Service) handleChatGPTOAuthCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	now := s.clock.Now()
	state := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")
	oauthError := r.URL.Query().Get("error")
	errorDescription := r.URL.Query().Get("error_description")
	if oauthError != "" {
		renderCallbackPage(w, callbackPage{
			Status:  "error",
			Message: firstNonEmpty(errorDescription, oauthError),
		})
		return
	}
	if state == "" || code == "" {
		renderCallbackPage(w, callbackPage{
			Status:  "error",
			Message: "OAuth callback missing state or code.",
		})
		return
	}

	s.mu.Lock()
	s.pruneExpiredSessionsLocked(now)
	pending, ok := s.claimPendingLoginLocked(state, now)
	s.mu.Unlock()
	if !ok {
		renderCallbackPage(w, callbackPage{
			Status:  "error",
			Message: "Login session expired or was not recognized.",
		})
		return
	}

	credential, err := s.exchangeAuthorizationCode(r.Context(), code, pending.codeVerifier)
	if err != nil {
		s.mu.Lock()
		s.deletePendingLoginLocked(pending)
		s.mu.Unlock()
		s.logger.Warn("chatgpt oauth exchange failed", zap.Error(err))
		renderCallbackPage(w, callbackPage{
			Status:  "error",
			Message: err.Error(),
		})
		return
	}

	s.mu.Lock()
	s.deletePendingLoginLocked(pending)
	s.completed[pending.loginID] = completedLogin{
		loginID:    pending.loginID,
		credential: *credential,
		expiresAt:  now.Add(completedLoginSessionTTL),
	}
	s.mu.Unlock()

	renderCallbackPage(w, callbackPage{
		Status:  "success",
		Message: "GPT account connected. You can close this window.",
		LoginID: pending.loginID,
	})
}

func (s *Service) exchangeAuthorizationCode(ctx context.Context, code, codeVerifier string) (*model.ChatGPTProviderCredential, error) {
	tokenURL := defaultOAuthIssuer + "/oauth/token"
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", LoopbackCallbackAddress())
	form.Set("client_id", defaultOAuthClientID)
	form.Set("code_verifier", codeVerifier)

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build oauth token exchange request: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("User-Agent", chatGPTOAuthUserAgent)

	response, err := s.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("exchange authorization code: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, readHTTPError(response, "exchange authorization code")
	}

	var payload oauthTokenResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode oauth token response: %w", err)
	}

	credential, err := newChatGPTCredentialFromTokensAt(payload.AccessToken, payload.RefreshToken, payload.IDToken, s.clock.Now())
	if err != nil {
		return nil, err
	}

	snapshot, usageErr := s.fetchChatGPTUsageSnapshot(ctx, credential)
	if usageErr != nil {
		s.logger.Debug("chatgpt usage snapshot unavailable after oauth exchange",
			zap.String("account_id", credential.AccountID),
			zap.Error(usageErr),
		)
		return credential, nil
	}
	return applyChatGPTUsageSnapshot(credential, snapshot), nil
}

func (s *Service) pruneExpiredSessionsLocked(now time.Time) {
	for _, pending := range s.pendingByLoginID {
		if !pending.expiresAt.After(now) {
			s.deletePendingLoginLocked(pending)
		}
	}
	for loginID, completed := range s.completed {
		if !completed.expiresAt.After(now) {
			delete(s.completed, loginID)
		}
	}
}

func (s *Service) storePendingLoginLocked(login pendingLogin) {
	s.pendingByState[login.state] = login
	s.pendingByLoginID[login.loginID] = login
}

func (s *Service) claimPendingLoginLocked(state string, now time.Time) (pendingLogin, bool) {
	login, ok := s.pendingByState[state]
	if !ok {
		return pendingLogin{}, false
	}

	// Keep the login visible to status polling while the callback exchanges the
	// authorization code. Removing it here creates a false "expired" gap that the
	// admin modal interprets as a failed sign-in.
	delete(s.pendingByState, state)
	login.expiresAt = now.Add(loginSessionTTL)
	s.pendingByLoginID[login.loginID] = login
	return login, true
}

func (s *Service) deletePendingLoginLocked(login pendingLogin) {
	delete(s.pendingByState, login.state)
	delete(s.pendingByLoginID, login.loginID)
}

func randomURLSafeString(byteCount int) (string, error) {
	buf := make([]byte, byteCount)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate secure random bytes: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

type oauthTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
}

type refreshedTokenPayload struct {
	AccessToken  string `json:"access_token"`
	IDToken      string `json:"id_token"`
	RefreshToken string `json:"refresh_token"`
}

func resolveChatGPTRefreshContext(credential *model.ChatGPTProviderCredential) (string, string, error) {
	if credential == nil {
		return "", "", fmt.Errorf("chatgpt credential is required")
	}

	issuer := strings.TrimSpace(credential.OAuthIssuer)
	clientID := strings.TrimSpace(credential.OAuthClientID)
	if issuer != "" {
		return issuer, clientID, nil
	}
	if credential.IDToken == "" {
		return "", "", fmt.Errorf("chatgpt credential missing oauth refresh context")
	}

	claims, err := decodeJWTPayload(credential.IDToken)
	if err != nil {
		return "", "", fmt.Errorf("decode chatgpt id_token for refresh: %w", err)
	}

	issuer = strings.TrimSpace(readStringClaim(claims, "iss"))
	if issuer == "" {
		issuer = defaultOAuthIssuer
	}
	return issuer, extractClientIDFromClaims(claims), nil
}

func buildRefreshedChatGPTCredential(
	current *model.ChatGPTProviderCredential,
	accessToken string,
	refreshToken string,
	idToken string,
	issuer string,
	clientID string,
	now time.Time,
) (*model.ChatGPTProviderCredential, error) {
	snapshot := snapshotChatGPTCredentialIdentity(current, issuer, clientID)
	if idToken != "" {
		var err error
		snapshot, err = extractChatGPTIdentitySnapshot(idToken)
		if err != nil {
			return nil, err
		}
	}

	var usage *model.ProviderUsageSnapshot
	if current != nil {
		usage = current.Usage
	}
	return newChatGPTCredentialFromSnapshot(accessToken, refreshToken, snapshot, usage, now), nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func applyStaticAuthHeader(headers http.Header, apiKey, providerAuthMode, globalAuthMode string, originalReq *http.Request) {
	mode := providerAuthMode
	if mode == "" {
		mode = globalAuthMode
	}
	if mode == authModeAuto {
		mode = detectAuthMode(originalReq)
	}

	switch mode {
	case authModeXAPIKey:
		headers.Set("x-api-key", apiKey)
	default:
		headers.Set("Authorization", "Bearer "+apiKey)
	}
}

func detectAuthMode(r *http.Request) string {
	if r != nil && r.Header.Get("Authorization") != "" {
		return authModeBearer
	}
	if r != nil && r.Header.Get("X-Api-Key") != "" {
		return authModeXAPIKey
	}
	return authModeBearer
}
