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
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/model"

	"go.uber.org/zap"
)

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
	LoginID string             `json:"login_id"`
	Status  ChatGPTLoginStatus `json:"status"`
	Auth    *ProviderAuthView  `json:"auth,omitempty"`
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
	loginID := s.idGenerator.NewID()

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

	if err := s.beginChatGPTLogin(pendingLogin{
		loginID:      loginID,
		state:        state,
		codeVerifier: codeVerifier,
	}); err != nil {
		return nil, err
	}

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
	s.syncSessionExpiryTaskLocked(now)
	shouldReconcile := s.callbackActive && len(s.pendingByLoginID) == 0
	var response *ChatGPTLoginStatusResponse
	if completed, ok := s.completed[loginID]; ok {
		response = &ChatGPTLoginStatusResponse{
			LoginID: loginID,
			Status:  ChatGPTLoginStatusCompleted,
			Auth:    chatGPTCredentialAuthView(&completed.credential),
		}
	} else if _, ok := s.pendingByLoginID[loginID]; ok {
		response = &ChatGPTLoginStatusResponse{
			LoginID: loginID,
			Status:  ChatGPTLoginStatusPending,
		}
	} else {
		response = &ChatGPTLoginStatusResponse{
			LoginID: loginID,
			Status:  ChatGPTLoginStatusExpired,
		}
	}
	s.mu.Unlock()
	if shouldReconcile {
		s.requestCallbackEndpointReconcile()
	}
	return response, nil
}

// lookupCompletedChatGPTLogin retrieves a finished login session without consuming it.
func (s *Service) lookupCompletedChatGPTLogin(loginID string) (*model.ChatGPTProviderCredential, error) {
	now := s.clock.Now()

	s.mu.Lock()
	s.pruneExpiredSessionsLocked(now)
	s.syncSessionExpiryTaskLocked(now)
	shouldReconcile := s.callbackActive && len(s.pendingByLoginID) == 0
	completed, ok := s.completed[loginID]
	s.mu.Unlock()
	if shouldReconcile {
		s.requestCallbackEndpointReconcile()
	}
	if !ok {
		return nil, fmt.Errorf("chatgpt login session %q not found or expired", loginID)
	}
	credential := completed.credential
	return &credential, nil
}

// BuildCredentialSessionFromChatGPTLogin snapshots a completed login without
// consuming it; the admin layer finalizes only after durable session creation.
func (s *Service) BuildCredentialSessionFromChatGPTLogin(loginID, sessionID string) (*credentialsession.Session, error) {
	credential, err := s.lookupCompletedChatGPTLogin(loginID)
	if err != nil {
		return nil, err
	}
	snapshot, err := chatGPTCredentialSessionSnapshot(credential, sessionID)
	if err != nil {
		return nil, err
	}
	session := &credentialsession.Session{
		ID: snapshot.SessionID, Kind: snapshot.Kind,
		SecretData: snapshot.SecretData, Version: snapshot.Version, AuthState: snapshot.AuthState,
	}
	if err := session.SetSubject(snapshot.Subject); err != nil {
		return nil, err
	}
	return session, session.Validate()
}

func chatGPTCredentialSessionSnapshot(
	credential *model.ChatGPTProviderCredential,
	sessionID string,
) (credentialsession.Snapshot, error) {
	if credential == nil || !credential.Ready() {
		return credentialsession.Snapshot{}, fmt.Errorf("complete chatgpt credential is required")
	}
	secretData, err := model.EncodeChatGPTProviderSecret(&model.ChatGPTProviderSecret{
		AccessToken: credential.AccessToken, RefreshToken: credential.RefreshToken,
		IDToken: credential.IDToken, OAuthIssuer: credential.OAuthIssuer, OAuthClientID: credential.OAuthClientID,
	})
	if err != nil {
		return credentialsession.Snapshot{}, err
	}
	subject, err := credentialsession.AccountSubject(credential.AccountID)
	if err != nil {
		return credentialsession.Snapshot{}, err
	}
	return credentialsession.Snapshot{
		SessionID: sessionID, Kind: credentialsession.KindChatGPT,
		SecretData: secretData, Version: 1,
		Subject: subject,
		AuthState: credentialsession.AuthState{
			Status: credentialsession.AuthStatusActive, Email: credential.Email,
			AccountID: credential.AccountID, PlanType: credential.PlanType,
			ExpiresAt: timePointer(credential.ExpiresAt), LastRefreshAt: timePointer(credential.LastRefresh),
			UsageSnapshot: credentialSessionUsageSnapshot(credential.Usage),
		},
	}, nil
}

// FinalizeChatGPTLogin consumes a finished login session after the provider write succeeds.
func (s *Service) FinalizeChatGPTLogin(loginID string) error {
	now := s.clock.Now()

	s.mu.Lock()
	s.pruneExpiredSessionsLocked(now)
	s.syncSessionExpiryTaskLocked(now)
	shouldReconcile := s.callbackActive && len(s.pendingByLoginID) == 0
	if _, ok := s.completed[loginID]; !ok {
		s.mu.Unlock()
		if shouldReconcile {
			s.requestCallbackEndpointReconcile()
		}
		return fmt.Errorf("chatgpt login session %q not found or expired", loginID)
	}
	delete(s.completed, loginID)
	s.mu.Unlock()
	if shouldReconcile {
		s.requestCallbackEndpointReconcile()
	}
	return nil
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
		s.cancelPendingLoginFromCallback(state, now)
		renderCallbackPage(w, callbackPage{
			Status:  "error",
			Message: firstNonEmpty(errorDescription, oauthError),
		})
		return
	}
	if state == "" || code == "" {
		s.cancelPendingLoginFromCallback(state, now)
		renderCallbackPage(w, callbackPage{
			Status:  "error",
			Message: "OAuth callback missing state or code.",
		})
		return
	}

	s.mu.Lock()
	s.pruneExpiredSessionsLocked(now)
	pending, ok := s.claimPendingLoginLocked(state, now)
	s.syncSessionExpiryTaskLocked(now)
	shouldReconcile := s.callbackActive && len(s.pendingByLoginID) == 0
	s.mu.Unlock()
	if !ok {
		if shouldReconcile {
			s.requestCallbackEndpointReconcile()
		}
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
		s.syncSessionExpiryTaskLocked(s.clock.Now())
		shouldReconcile = s.callbackActive && len(s.pendingByLoginID) == 0
		s.mu.Unlock()
		if shouldReconcile {
			s.requestCallbackEndpointReconcile()
		}
		s.logger.Warn("chatgpt oauth exchange failed", zap.Error(err))
		renderCallbackPage(w, callbackPage{
			Status:  "error",
			Message: err.Error(),
		})
		return
	}

	completionNow := s.clock.Now()
	s.mu.Lock()
	s.deletePendingLoginLocked(pending)
	storeErr := s.storeCompletedLoginLocked(pending.loginID, credential, completionNow)
	shouldReconcile = s.callbackActive && len(s.pendingByLoginID) == 0
	s.mu.Unlock()
	if shouldReconcile {
		s.requestCallbackEndpointReconcile()
	}
	if storeErr != nil {
		renderCallbackPage(w, callbackPage{
			Status:  "error",
			Message: "Login service stopped before credentials could be staged.",
		})
		return
	}

	renderCallbackPage(w, callbackPage{
		Status:  "success",
		Message: "GPT account connected. You can close this window.",
		LoginID: pending.loginID,
	})
}

// storeCompletedLoginLocked is the single transition that admits credential
// material into the staged-login map. Network work happens outside s.mu, so the
// shutdown check must be repeated at this exact ownership boundary.
func (s *Service) storeCompletedLoginLocked(
	loginID string,
	credential *model.ChatGPTProviderCredential,
	now time.Time,
) error {
	if s.shutdown {
		return errProviderAuthServiceShutdown
	}
	if strings.TrimSpace(loginID) == "" || credential == nil {
		return fmt.Errorf("completed login is incomplete")
	}
	s.pruneExpiredSessionsLocked(now)
	s.completed[loginID] = completedLogin{
		loginID:    loginID,
		credential: *credential,
		expiresAt:  now.Add(completedLoginSessionTTL),
	}
	s.syncSessionExpiryTaskLocked(now)
	return nil
}

func (s *Service) cancelPendingLoginFromCallback(state string, now time.Time) {
	if state == "" {
		return
	}

	s.mu.Lock()
	s.pruneExpiredSessionsLocked(now)
	if pending, ok := s.pendingByState[state]; ok {
		s.deletePendingLoginLocked(pending)
	}
	s.syncSessionExpiryTaskLocked(now)
	shouldReconcile := s.callbackActive && len(s.pendingByLoginID) == 0
	s.mu.Unlock()
	if shouldReconcile {
		s.requestCallbackEndpointReconcile()
	}
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
	s.pruneExpiredProviderImportsLocked(now)
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
