package providerauth

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"switch-a/internal/model"
)

type chatGPTIdentitySnapshot struct {
	IDToken       string
	OAuthIssuer   string
	OAuthClientID string
	AccountID     string
	Email         string
	PlanType      string
	ExpiresAt     time.Time
}

type storedChatGPTCredential struct {
	model.ChatGPTProviderCredential
	AuthStatus ProviderAuthStatus `json:"auth_status,omitempty"`
	AuthReason string             `json:"auth_reason,omitempty"`
	LastError  string             `json:"last_error,omitempty"`
}

// chatGPTCredentialSecret keeps only refresh-capable material in the secret blob.
// Account identity, plan, and usage belong in provider_auth_states so usage syncs
// never need to rewrite secrets.
type chatGPTCredentialSecret struct {
	AccessToken   string `json:"access_token"`
	RefreshToken  string `json:"refresh_token"`
	IDToken       string `json:"id_token"`
	OAuthIssuer   string `json:"oauth_issuer,omitempty"`
	OAuthClientID string `json:"oauth_client_id,omitempty"`
}

func encodeChatGPTCredential(credential model.ChatGPTProviderCredential) (string, error) {
	payload, err := json.Marshal(credential)
	if err != nil {
		return "", fmt.Errorf("marshal chatgpt credential: %w", err)
	}
	return string(payload), nil
}

func encodeStoredChatGPTCredential(credential storedChatGPTCredential) (string, error) {
	payload, err := json.Marshal(credential)
	if err != nil {
		return "", fmt.Errorf("marshal stored chatgpt credential: %w", err)
	}
	return string(payload), nil
}

func decodeChatGPTCredential(raw string) (*model.ChatGPTProviderCredential, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("missing chatgpt credential payload")
	}
	var credential model.ChatGPTProviderCredential
	if err := json.Unmarshal([]byte(raw), &credential); err != nil {
		return nil, fmt.Errorf("decode chatgpt credential payload: %w", err)
	}
	return &credential, nil
}

func decodeStoredChatGPTCredential(raw string) (*storedChatGPTCredential, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("missing chatgpt credential payload")
	}
	var credential storedChatGPTCredential
	if err := json.Unmarshal([]byte(raw), &credential); err != nil {
		return nil, fmt.Errorf("decode stored chatgpt credential payload: %w", err)
	}
	return &credential, nil
}

func newStoredChatGPTCredential(
	credential *model.ChatGPTProviderCredential,
	status ProviderAuthStatus,
	reason string,
	lastError string,
) *storedChatGPTCredential {
	if credential == nil {
		return nil
	}
	return &storedChatGPTCredential{
		ChatGPTProviderCredential: *cloneChatGPTCredential(credential),
		AuthStatus:                status,
		AuthReason:                strings.TrimSpace(reason),
		LastError:                 strings.TrimSpace(lastError),
	}
}

func encodeChatGPTCredentialSecret(credential *model.ChatGPTProviderCredential) (string, error) {
	if credential == nil {
		return "", nil
	}
	payload, err := json.Marshal(chatGPTCredentialSecret{
		AccessToken:   credential.AccessToken,
		RefreshToken:  credential.RefreshToken,
		IDToken:       credential.IDToken,
		OAuthIssuer:   strings.TrimSpace(credential.OAuthIssuer),
		OAuthClientID: strings.TrimSpace(credential.OAuthClientID),
	})
	if err != nil {
		return "", fmt.Errorf("marshal chatgpt credential secret: %w", err)
	}
	return string(payload), nil
}

func decodeChatGPTCredentialSecret(raw string) (*chatGPTCredentialSecret, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("missing chatgpt credential payload")
	}
	var secret chatGPTCredentialSecret
	if err := json.Unmarshal([]byte(raw), &secret); err != nil {
		return nil, fmt.Errorf("decode chatgpt credential secret: %w", err)
	}
	return &secret, nil
}

func newChatGPTCredentialFromTokens(accessToken, refreshToken, idToken string) (*model.ChatGPTProviderCredential, error) {
	return newChatGPTCredentialFromTokensAt(accessToken, refreshToken, idToken, time.Now().UTC())
}

func newChatGPTCredentialFromTokensAt(accessToken, refreshToken, idToken string, now time.Time) (*model.ChatGPTProviderCredential, error) {
	snapshot, err := extractChatGPTIdentitySnapshot(idToken)
	if err != nil {
		return nil, err
	}

	return newChatGPTCredentialFromSnapshot(accessToken, refreshToken, snapshot, nil, now), nil
}

func newChatGPTCredentialFromSnapshot(
	accessToken string,
	refreshToken string,
	snapshot chatGPTIdentitySnapshot,
	usage *model.ProviderUsageSnapshot,
	now time.Time,
) *model.ChatGPTProviderCredential {
	return &model.ChatGPTProviderCredential{
		AccessToken:   accessToken,
		RefreshToken:  refreshToken,
		IDToken:       snapshot.IDToken,
		OAuthIssuer:   snapshot.OAuthIssuer,
		OAuthClientID: snapshot.OAuthClientID,
		AccountID:     snapshot.AccountID,
		Email:         snapshot.Email,
		PlanType:      snapshot.PlanType,
		Usage:         usage,
		LastRefresh:   now,
		ExpiresAt:     snapshot.ExpiresAt,
	}
}

func extractChatGPTIdentitySnapshot(idToken string) (chatGPTIdentitySnapshot, error) {
	claims, err := decodeJWTPayload(idToken)
	if err != nil {
		return chatGPTIdentitySnapshot{}, fmt.Errorf("decode chatgpt id_token: %w", err)
	}

	accountID, err := extractChatGPTAccountID(claims)
	if err != nil {
		return chatGPTIdentitySnapshot{}, err
	}

	issuer := strings.TrimSpace(readStringClaim(claims, "iss"))
	if issuer == "" {
		issuer = defaultOAuthIssuer
	}

	return chatGPTIdentitySnapshot{
		IDToken:       idToken,
		OAuthIssuer:   issuer,
		OAuthClientID: extractClientIDFromClaims(claims),
		AccountID:     accountID,
		Email:         readStringClaim(claims, "email"),
		PlanType:      extractChatGPTPlanType(claims),
		ExpiresAt:     extractJWTExpiry(claims),
	}, nil
}

func snapshotChatGPTCredentialIdentity(
	credential *model.ChatGPTProviderCredential,
	issuer string,
	clientID string,
) chatGPTIdentitySnapshot {
	if credential == nil {
		return chatGPTIdentitySnapshot{
			OAuthIssuer:   firstNonEmpty(strings.TrimSpace(issuer), defaultOAuthIssuer),
			OAuthClientID: strings.TrimSpace(clientID),
		}
	}

	return chatGPTIdentitySnapshot{
		IDToken:       credential.IDToken,
		OAuthIssuer:   firstNonEmpty(strings.TrimSpace(credential.OAuthIssuer), strings.TrimSpace(issuer), defaultOAuthIssuer),
		OAuthClientID: firstNonEmpty(strings.TrimSpace(credential.OAuthClientID), strings.TrimSpace(clientID)),
		AccountID:     credential.AccountID,
		Email:         credential.Email,
		PlanType:      credential.PlanType,
		ExpiresAt:     credential.ExpiresAt,
	}
}

func extractChatGPTAccountID(claims map[string]any) (string, error) {
	authBlock, ok := claims["https://api.openai.com/auth"].(map[string]any)
	if !ok {
		return "", fmt.Errorf("chatgpt id_token missing auth claims")
	}
	accountID, _ := authBlock["chatgpt_account_id"].(string)
	if accountID == "" {
		return "", fmt.Errorf("chatgpt id_token missing chatgpt_account_id")
	}
	return accountID, nil
}

func extractChatGPTPlanType(claims map[string]any) string {
	authBlock, ok := claims["https://api.openai.com/auth"].(map[string]any)
	if !ok {
		return ""
	}
	planType, _ := authBlock["chatgpt_plan_type"].(string)
	return strings.TrimSpace(planType)
}

func decodeJWTPayload(token string) (map[string]any, error) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid jwt format")
	}

	decoded, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode jwt payload: %w", err)
	}

	var claims map[string]any
	if err := json.Unmarshal(decoded, &claims); err != nil {
		return nil, fmt.Errorf("parse jwt payload: %w", err)
	}
	return claims, nil
}

func readStringClaim(claims map[string]any, key string) string {
	value, _ := claims[key].(string)
	return value
}

func extractJWTExpiry(claims map[string]any) time.Time {
	raw := claims["exp"]
	switch value := raw.(type) {
	case float64:
		return time.Unix(int64(value), 0).UTC()
	case json.Number:
		if unixSeconds, err := value.Int64(); err == nil {
			return time.Unix(unixSeconds, 0).UTC()
		}
	case string:
		if unixSeconds, err := strconv.ParseInt(value, 10, 64); err == nil {
			return time.Unix(unixSeconds, 0).UTC()
		}
	}
	return time.Time{}
}

func extractClientIDFromClaims(claims map[string]any) string {
	switch aud := claims["aud"].(type) {
	case string:
		return aud
	case []any:
		for _, item := range aud {
			if value, ok := item.(string); ok && value != "" {
				return value
			}
		}
	}
	return ""
}

func readHTTPError(response *http.Response, operation string) error {
	body, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
	message := strings.TrimSpace(string(body))
	if message == "" {
		return fmt.Errorf("%s failed with status %s", operation, response.Status)
	}
	return fmt.Errorf("%s failed with status %s: %s", operation, response.Status, message)
}

type callbackPage struct {
	Status  string
	Message string
	LoginID string
}

var callbackPageTemplate = template.Must(template.New("callback").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width,initial-scale=1">
  <title>{{ .Title }}</title>
  <style>
    :root { color-scheme: light; }
    body {
      margin: 0;
      min-height: 100vh;
      display: grid;
      place-items: center;
      font-family: "Segoe UI", system-ui, sans-serif;
      background: linear-gradient(135deg, #f4f6fb 0%, #e8edf7 100%);
      color: #0f172a;
    }
    main {
      width: min(28rem, calc(100vw - 2rem));
      padding: 1.5rem;
      border-radius: 1rem;
      background: rgba(255, 255, 255, 0.96);
      box-shadow: 0 18px 45px rgba(15, 23, 42, 0.12);
    }
    h1 {
      margin: 0 0 0.75rem;
      font-size: 1.25rem;
    }
    p {
      margin: 0;
      line-height: 1.5;
      color: #334155;
    }
    .status-success { color: #047857; }
    .status-error { color: #b91c1c; }
  </style>
</head>
<body>
  <main>
    <h1 class="status-{{ .Page.Status }}">{{ .Heading }}</h1>
    <p>{{ .Page.Message }}</p>
  </main>
  <script>
    (() => {
      const payload = {{ .Payload }};
      if (window.opener) {
        window.opener.postMessage(payload, "*");
      }
      if (payload.status === "success") {
        window.setTimeout(() => window.close(), 200);
      }
    })();
  </script>
</body>
</html>`))

func renderCallbackPage(w http.ResponseWriter, page callbackPage) {
	payloadBytes, _ := json.Marshal(map[string]string{
		"type":    "switch-a:chatgpt-login",
		"status":  page.Status,
		"message": page.Message,
		"loginId": page.LoginID,
	})
	heading := callbackPageTitle
	if page.Status == "error" {
		heading = callbackPageTitle + " Failed"
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = callbackPageTemplate.Execute(w, struct {
		Title   string
		Heading string
		Page    callbackPage
		Payload template.JS
	}{
		Title:   callbackPageTitle,
		Heading: heading,
		Page:    page,
		Payload: template.JS(payloadBytes),
	})
}
