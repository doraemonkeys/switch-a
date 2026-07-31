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

	"github.com/doraemonkeys/switch-a/internal/model"
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

const (
	chatGPTImportedAccessTokenKind = "access_token"
	chatGPTImportedIDTokenKind     = "id_token"
)

type storedChatGPTCredential struct {
	model.ChatGPTProviderCredential
	AuthStatus ProviderAuthStatus `json:"auth_status,omitempty"`
	AuthReason string             `json:"auth_reason,omitempty"`
	LastError  string             `json:"last_error,omitempty"`
}

// The model package owns the persisted wire contract; this alias keeps verifier
// terminology local without maintaining a second, drift-prone schema.
type chatGPTCredentialSecret = model.ChatGPTProviderSecret

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
	payload, err := model.EncodeChatGPTProviderSecret(&model.ChatGPTProviderSecret{
		AccessToken:   credential.AccessToken,
		RefreshToken:  credential.RefreshToken,
		IDToken:       credential.IDToken,
		OAuthIssuer:   strings.TrimSpace(credential.OAuthIssuer),
		OAuthClientID: strings.TrimSpace(credential.OAuthClientID),
	})
	if err != nil {
		return "", fmt.Errorf("marshal chatgpt credential secret: %w", err)
	}
	return payload, nil
}

func decodeChatGPTCredentialSecret(raw string) (*chatGPTCredentialSecret, error) {
	secret, err := model.DecodeChatGPTProviderSecret(raw)
	if err != nil {
		return nil, fmt.Errorf("decode chatgpt credential secret: %w", err)
	}
	if secret == nil {
		return nil, fmt.Errorf("missing chatgpt credential payload")
	}
	return secret, nil
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

// newChatGPTCredentialFromImportedTokens checks every supplied JWT's decoded
// claims before staging it. Access and ID tokens intentionally have different
// audiences, but both must claim the expected OpenAI issuer and ChatGPT account.
func newChatGPTCredentialFromImportedTokens(accessToken, refreshToken, idToken string, now time.Time) (*model.ChatGPTProviderCredential, error) {
	accessSnapshot, err := snapshotFromImportedChatGPTToken(accessToken, chatGPTImportedAccessTokenKind)
	if err != nil {
		return nil, err
	}

	snapshot := accessSnapshot
	if strings.TrimSpace(idToken) != "" {
		idSnapshot, idErr := snapshotFromImportedChatGPTToken(idToken, chatGPTImportedIDTokenKind)
		if idErr != nil {
			return nil, idErr
		}
		if idSnapshot.AccountID != accessSnapshot.AccountID {
			return nil, fmt.Errorf("chatgpt access_token and id_token identify different accounts")
		}

		snapshot = idSnapshot
		snapshot.IDToken = idToken
		snapshot.Email = firstNonEmpty(idSnapshot.Email, accessSnapshot.Email)
		snapshot.PlanType = firstNonEmpty(idSnapshot.PlanType, accessSnapshot.PlanType)
		snapshot.ExpiresAt = accessSnapshot.ExpiresAt
		if snapshot.ExpiresAt.IsZero() {
			snapshot.ExpiresAt = idSnapshot.ExpiresAt
		}
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
	snapshot, err := snapshotFromChatGPTToken(idToken, "id_token")
	if err != nil {
		return chatGPTIdentitySnapshot{}, err
	}
	snapshot.IDToken = idToken
	return snapshot, nil
}

// snapshotFromChatGPTToken reads account identity from any ChatGPT-issued JWT.
// Both id_token and access_token carry the "https://api.openai.com/auth" block
// plus iss/aud, so token import can resolve identity from the access_token when no
// id_token is supplied. The kind label only shapes the decode error so callers
// report the token they actually passed; IDToken is set by the caller because only
// it knows whether the source token is genuinely an id_token.
func snapshotFromChatGPTToken(token, kind string) (chatGPTIdentitySnapshot, error) {
	claims, err := decodeJWTPayload(token)
	if err != nil {
		return chatGPTIdentitySnapshot{}, fmt.Errorf("decode chatgpt %s: %w", kind, err)
	}
	return snapshotFromChatGPTClaims(claims)
}

// snapshotFromImportedChatGPTToken validates the claims that control refresh
// routing before any imported secret is staged. Imported JWTs are untrusted
// configuration, so neither their issuer nor audience may expand the set of
// OAuth endpoints and clients switch-a is willing to use.
func snapshotFromImportedChatGPTToken(token, kind string) (chatGPTIdentitySnapshot, error) {
	_, payload, _, err := splitCompactJWS(token)
	if err != nil {
		return chatGPTIdentitySnapshot{}, fmt.Errorf("decode chatgpt %s: %w", kind, err)
	}
	payloadJSON, err := decodeChatGPTProviderImportJWTSegment(payload, maxChatGPTProviderImportJWTPayloadBytes)
	if err != nil {
		return chatGPTIdentitySnapshot{}, fmt.Errorf("decode chatgpt %s: %w", kind, err)
	}
	claims, err := decodeVerifiedChatGPTProviderImportClaims(payloadJSON)
	if err != nil || claims.Issuer != defaultOAuthIssuer {
		return chatGPTIdentitySnapshot{}, fmt.Errorf("chatgpt %s has an unsupported issuer", kind)
	}

	expectedAudience := ""
	switch kind {
	case chatGPTImportedAccessTokenKind:
		expectedAudience = chatGPTAPIAudience
	case chatGPTImportedIDTokenKind:
		expectedAudience = defaultOAuthClientID
	default:
		return chatGPTIdentitySnapshot{}, fmt.Errorf("unsupported chatgpt imported token kind")
	}
	if !hasExactChatGPTProviderImportAudience(claims.Audience, expectedAudience) {
		return chatGPTIdentitySnapshot{}, fmt.Errorf("chatgpt %s has an unsupported audience", kind)
	}
	return chatGPTIdentitySnapshot{
		OAuthIssuer:   defaultOAuthIssuer,
		OAuthClientID: defaultOAuthClientID,
		AccountID:     claims.AccountID,
		Email:         strings.TrimSpace(claims.Email),
		PlanType:      claims.PlanType,
		ExpiresAt:     claims.ExpiresAt,
	}, nil
}

func snapshotFromChatGPTClaims(claims map[string]any) (chatGPTIdentitySnapshot, error) {
	accountID, err := extractChatGPTAccountID(claims)
	if err != nil {
		return chatGPTIdentitySnapshot{}, err
	}
	expiresAt, err := extractJWTExpiryChecked(claims)
	if err != nil {
		return chatGPTIdentitySnapshot{}, fmt.Errorf("chatgpt token has an invalid expiration")
	}

	issuer := strings.TrimSpace(readStringClaim(claims, "iss"))
	if issuer == "" {
		issuer = defaultOAuthIssuer
	}

	return chatGPTIdentitySnapshot{
		OAuthIssuer:   issuer,
		OAuthClientID: extractClientIDFromClaims(claims),
		AccountID:     accountID,
		Email:         readStringClaim(claims, "email"),
		PlanType:      extractChatGPTPlanType(claims),
		ExpiresAt:     expiresAt,
	}, nil
}

// allowedImportedChatGPTOAuthClientID is deliberately an allowlist rather than
// accepting any aud claim. The default Codex client is the only imported client
// whose refresh contract switch-a currently implements.
func allowedImportedChatGPTOAuthClientID(claims map[string]any) (string, bool) {
	if hasOnlyImportedChatGPTAudience(claims, defaultOAuthClientID) {
		return defaultOAuthClientID, true
	}
	return "", false
}

func hasOnlyImportedChatGPTAudience(claims map[string]any, allowed string) bool {
	switch audience := claims["aud"].(type) {
	case string:
		return strings.TrimSpace(audience) == allowed
	case []any:
		if len(audience) == 0 {
			return false
		}
		matched := false
		for _, raw := range audience {
			value, ok := raw.(string)
			if !ok {
				return false
			}
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			if value != allowed {
				return false
			}
			matched = true
		}
		return matched
	default:
		return false
	}
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
	_, payload, _, err := splitCompactJWS(token)
	if err != nil {
		return nil, err
	}
	decoded, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return nil, fmt.Errorf("decode jwt payload: %w", err)
	}

	var claims map[string]any
	if err := json.Unmarshal(decoded, &claims); err != nil {
		return nil, fmt.Errorf("parse jwt payload: %w", err)
	}
	return claims, nil
}

// splitCompactJWS uses bounded cuts so a dot-heavy attacker-controlled token
// cannot amplify a 5 MiB import into millions of slice headers.
func splitCompactJWS(token string) (header, payload, signature string, err error) {
	token = strings.TrimSpace(token)
	header, remainder, ok := strings.Cut(token, ".")
	if !ok {
		return "", "", "", fmt.Errorf("invalid jwt format")
	}
	payload, signature, ok = strings.Cut(remainder, ".")
	if !ok || strings.Contains(signature, ".") || header == "" || payload == "" || signature == "" {
		return "", "", "", fmt.Errorf("invalid jwt format")
	}
	return header, payload, signature, nil
}

func readStringClaim(claims map[string]any, key string) string {
	value, _ := claims[key].(string)
	return value
}

func extractJWTExpiry(claims map[string]any) time.Time {
	expiresAt, _ := extractJWTExpiryChecked(claims)
	return expiresAt
}

func extractJWTExpiryChecked(claims map[string]any) (time.Time, error) {
	raw, present := claims["exp"]
	if !present || raw == nil {
		return time.Time{}, nil
	}
	switch value := raw.(type) {
	case float64:
		return jsonRepresentableUnixFloat(value)
	case json.Number:
		unixSeconds, err := value.Int64()
		if err != nil {
			return time.Time{}, fmt.Errorf("jwt expiration must be an integer")
		}
		return jsonRepresentableUnixTime(unixSeconds)
	case string:
		unixSeconds, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return time.Time{}, fmt.Errorf("jwt expiration must be an integer")
		}
		return jsonRepresentableUnixTime(unixSeconds)
	default:
		return time.Time{}, fmt.Errorf("jwt expiration has an unsupported type")
	}
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
