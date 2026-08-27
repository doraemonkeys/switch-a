package providerauth

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"go.uber.org/zap"
)

// importedChatGPTTokens holds the token material lifted from a pasted credential
// blob before it is validated and converted into a stored credential.
type importedChatGPTTokens struct {
	AccessToken  string
	RefreshToken string
	IDToken      string
}

// parseImportedChatGPTTokens accepts the common ChatGPT/Codex credential shapes and
// extracts the token material:
//   - Codex CLI ~/.codex/auth.json, which nests the tokens under "tokens"
//   - a flat object using snake_case or camelCase keys
//   - the chatgpt.com /api/auth/session payload (access token at the top level)
//
// Lookups prefer the nested "tokens" object and fall back to the root so a single
// helper covers every shape without per-format branching.
func parseImportedChatGPTTokens(raw string) (importedChatGPTTokens, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return importedChatGPTTokens{}, fmt.Errorf("auth data is empty")
	}

	var root map[string]any
	if err := json.Unmarshal([]byte(trimmed), &root); err != nil {
		return importedChatGPTTokens{}, fmt.Errorf("parse auth data as JSON: %w", err)
	}

	scopes := []map[string]any{root}
	if nested, ok := root["tokens"].(map[string]any); ok {
		// Codex auth.json keeps the real tokens here; prefer it over the root.
		scopes = []map[string]any{nested, root}
	}

	return importedChatGPTTokens{
		AccessToken:  firstStringField(scopes, "access_token", "accessToken"),
		RefreshToken: firstStringField(scopes, "refresh_token", "refreshToken"),
		IDToken:      firstStringField(scopes, "id_token", "idToken"),
	}, nil
}

// firstStringField returns the first non-empty string value found for any of the
// keys, scanning the scopes in order.
func firstStringField(scopes []map[string]any, keys ...string) string {
	for _, scope := range scopes {
		for _, key := range keys {
			if value, ok := scope[key].(string); ok {
				if trimmed := strings.TrimSpace(value); trimmed != "" {
					return trimmed
				}
			}
		}
	}
	return ""
}

// ImportChatGPTLogin stages a pasted ChatGPT credential as a completed login
// session so the existing provider-creation pipeline can persist it via
// credential_login_id, exactly like the OAuth popup flow. Returning the same shape
// as a completed GetChatGPTLoginStatus lets the admin UI skip status polling.
func (s *Service) ImportChatGPTLogin(ctx context.Context, rawAuthData string) (*ChatGPTLoginStatusResponse, error) {
	tokens, err := parseImportedChatGPTTokens(rawAuthData)
	if err != nil {
		return nil, err
	}
	if tokens.AccessToken == "" {
		return nil, fmt.Errorf("auth data is missing an access token")
	}
	// switch-a proxies long-lived traffic and refreshes tokens on its own, so a
	// refresh token is mandatory; an access-only paste would stop working at expiry.
	if tokens.RefreshToken == "" {
		return nil, fmt.Errorf("auth data is missing a refresh token, which switch-a needs to keep the account signed in")
	}

	credential, err := newChatGPTCredentialFromImportedTokens(
		tokens.AccessToken,
		tokens.RefreshToken,
		tokens.IDToken,
		s.clock.Now(),
	)
	if err != nil {
		return nil, err
	}
	if !credential.Ready() {
		return nil, fmt.Errorf("imported chatgpt credential is incomplete")
	}

	// Mirror the OAuth flow: the usage snapshot is best-effort so a flaky upstream
	// never blocks the import.
	if snapshot, usageErr := s.fetchChatGPTUsageSnapshot(ctx, credential); usageErr != nil {
		s.logger.Debug("chatgpt usage snapshot unavailable after token import",
			zap.String("account_id", credential.AccountID),
			zap.Error(usageErr),
		)
	} else {
		credential = applyChatGPTUsageSnapshot(credential, snapshot)
	}

	loginID := s.idGenerator.NewID()
	now := s.clock.Now()
	s.mu.Lock()
	err = s.storeCompletedLoginLocked(loginID, credential, now)
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}

	return &ChatGPTLoginStatusResponse{
		LoginID: loginID,
		Status:  ChatGPTLoginStatusCompleted,
		Auth:    chatGPTCredentialAuthView(credential),
	}, nil
}
