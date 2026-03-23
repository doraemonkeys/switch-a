package providerauth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"switch-a/internal/model"
)

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
