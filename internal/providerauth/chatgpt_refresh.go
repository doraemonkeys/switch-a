package providerauth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"
)

// The OAuth exchange is deliberately separate from session coordination and
// persistence so the applied-identity boundary can reject a mismatched final
// authority before any refresh token is sent over the network.
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
		if issuer != defaultOAuthIssuer {
			return "", "", fmt.Errorf("chatgpt credential has unsupported oauth issuer")
		}
		if clientID == "" {
			clientID = defaultOAuthClientID
		}
		if clientID != defaultOAuthClientID {
			return "", "", fmt.Errorf("chatgpt credential has unsupported oauth client id")
		}
		return defaultOAuthIssuer, defaultOAuthClientID, nil
	}
	if credential.IDToken == "" {
		return "", "", fmt.Errorf("chatgpt credential missing oauth refresh context")
	}

	claims, err := decodeJWTPayload(credential.IDToken)
	if err != nil {
		return "", "", fmt.Errorf("decode chatgpt id_token for refresh: %w", err)
	}

	issuer = strings.TrimSpace(readStringClaim(claims, "iss"))
	if issuer != defaultOAuthIssuer {
		return "", "", fmt.Errorf("chatgpt credential has unsupported oauth issuer")
	}
	clientID, ok := allowedImportedChatGPTOAuthClientID(claims)
	if !ok {
		return "", "", fmt.Errorf("chatgpt credential has unsupported oauth audience")
	}
	return defaultOAuthIssuer, clientID, nil
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
	var accessSnapshot *chatGPTIdentitySnapshot
	if chatGPTTokenLooksLikeJWT(accessToken) {
		parsed, err := snapshotFromImportedChatGPTToken(accessToken, chatGPTImportedAccessTokenKind)
		if err != nil {
			return nil, fmt.Errorf("validate refreshed chatgpt access_token: %w", err)
		}
		if current != nil && current.AccountID != "" && parsed.AccountID != current.AccountID {
			return nil, fmt.Errorf("refreshed chatgpt access_token identifies a different account")
		}
		accessSnapshot = &parsed
		if snapshot.AccountID == "" {
			snapshot = parsed
		} else if !parsed.ExpiresAt.IsZero() {
			snapshot.ExpiresAt = parsed.ExpiresAt
		}
	}
	if idToken != "" {
		parsed, err := snapshotFromImportedChatGPTToken(idToken, chatGPTImportedIDTokenKind)
		if err != nil {
			return nil, fmt.Errorf("validate refreshed chatgpt id_token: %w", err)
		}
		if current != nil && current.AccountID != "" && parsed.AccountID != current.AccountID {
			return nil, fmt.Errorf("refreshed chatgpt id_token identifies a different account")
		}
		if accessSnapshot != nil && parsed.AccountID != accessSnapshot.AccountID {
			return nil, fmt.Errorf("refreshed chatgpt access_token and id_token identify different accounts")
		}

		parsed.IDToken = idToken
		parsed.Email = firstNonEmpty(parsed.Email, snapshot.Email)
		parsed.PlanType = firstNonEmpty(parsed.PlanType, snapshot.PlanType)
		if accessSnapshot != nil && !accessSnapshot.ExpiresAt.IsZero() {
			parsed.ExpiresAt = accessSnapshot.ExpiresAt
		}
		snapshot = parsed
	}

	var usage *model.ProviderUsageSnapshot
	if current != nil {
		usage = current.Usage
	}
	return newChatGPTCredentialFromSnapshot(accessToken, refreshToken, snapshot, usage, now), nil
}

func chatGPTTokenLooksLikeJWT(token string) bool {
	_, _, _, err := splitCompactJWS(token)
	return err == nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
