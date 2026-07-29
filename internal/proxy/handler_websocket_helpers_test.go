package proxy

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"
)

type mockOAuthHTTPClient struct {
	do func(req *http.Request) (*http.Response, error)
}

func (m mockOAuthHTTPClient) Do(req *http.Request) (*http.Response, error) {
	if m.do != nil {
		return m.do(req)
	}
	return nil, nil
}

func testUnsignedJWT(t *testing.T, claims map[string]any) string {
	t.Helper()

	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal jwt payload: %v", err)
	}

	return "eyJhbGciOiJub25lIn0." + base64.RawURLEncoding.EncodeToString(payload) + "."
}

func testChatGPTCredentialData(t *testing.T, accessToken, refreshToken, accountID string) string {
	t.Helper()

	expiresAt := time.Now().UTC().Add(1 * time.Hour)
	idToken := testUnsignedJWT(t, map[string]any{
		"iss":   "https://auth.openai.com",
		"aud":   "app_EMoamEEZ73f0CkXaXp7hrann",
		"email": "codex@example.com",
		"exp":   expiresAt.Unix(),
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": accountID,
		},
	})

	raw, err := json.Marshal(model.ChatGPTProviderCredential{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		IDToken:      idToken,
		AccountID:    accountID,
		Email:        "codex@example.com",
		LastRefresh:  time.Now().UTC(),
		ExpiresAt:    expiresAt,
	})
	if err != nil {
		t.Fatalf("marshal chatgpt credential: %v", err)
	}

	return string(raw)
}

func TestIsWebSocketUpgrade(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		headers  http.Header
		expected bool
	}{
		{
			name:     "valid upgrade",
			headers:  http.Header{"Upgrade": {"websocket"}, "Connection": {"Upgrade"}},
			expected: true,
		},
		{
			name:     "case insensitive",
			headers:  http.Header{"Upgrade": {"WebSocket"}, "Connection": {"upgrade"}},
			expected: true,
		},
		{
			name:     "connection with multiple values",
			headers:  http.Header{"Upgrade": {"websocket"}, "Connection": {"keep-alive, Upgrade"}},
			expected: true,
		},
		{
			name:     "missing upgrade header",
			headers:  http.Header{"Connection": {"Upgrade"}},
			expected: false,
		},
		{
			name:     "missing connection header",
			headers:  http.Header{"Upgrade": {"websocket"}},
			expected: false,
		},
		{
			name:     "wrong upgrade value",
			headers:  http.Header{"Upgrade": {"h2c"}, "Connection": {"Upgrade"}},
			expected: false,
		},
		{
			name:     "empty headers",
			headers:  http.Header{},
			expected: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := &http.Request{Header: tt.headers}
			got := isWebSocketUpgrade(r)
			if got != tt.expected {
				t.Errorf("isWebSocketUpgrade() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestExtractWebSocketModel(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		url      string
		expected string
	}{
		{name: "model in query", url: "/responses?model=gpt-4o-realtime", expected: "gpt-4o-realtime"},
		{name: "no model param", url: "/responses", expected: ModelUnknown},
		{name: "empty model param", url: "/responses?model=", expected: ModelUnknown},
		{name: "model with other params", url: "/responses?foo=bar&model=claude-4", expected: "claude-4"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := httptest.NewRequest(http.MethodGet, tt.url, nil)
			got := extractWebSocketModel(r)
			if got != tt.expected {
				t.Errorf("extractWebSocketModel() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestBuildWebSocketDialHeaders(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequest(http.MethodGet, "/responses", nil)
	r.Header.Set("OpenAI-Beta", "realtime=v1")
	r.Header.Set("X-Custom", "value")
	r.Header.Set("Authorization", "Bearer client-key")
	r.Header.Set("Connection", "Upgrade")
	r.Header.Set("Upgrade", "websocket")

	provider := &model.Provider{
		APIKey:   "sk-provider-key",
		AuthMode: "bearer",
	}

	headers := buildWebSocketDialHeaders(r, provider, "codex", "auto")

	if got := headers.Get("Authorization"); got != "Bearer sk-provider-key" {
		t.Errorf("Authorization = %q, want 'Bearer sk-provider-key'", got)
	}
	if got := headers.Get("OpenAI-Beta"); got != "realtime=v1" {
		t.Errorf("OpenAI-Beta = %q, want 'realtime=v1'", got)
	}
	if got := headers.Get("X-Custom"); got != "value" {
		t.Errorf("X-Custom = %q, want 'value'", got)
	}
	if got := headers.Get("Connection"); got != "" {
		t.Errorf("Connection should be empty, got %q", got)
	}
	if got := headers.Get("Upgrade"); got != "" {
		t.Errorf("Upgrade should be empty, got %q", got)
	}
}

func TestBuildWebSocketDialHeaders_UsesAPITypeKeyOverride(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequest(http.MethodGet, "/responses", nil)
	provider := &model.Provider{
		APIKey:   "default-key",
		AuthMode: "bearer",
		APITypes: []model.ProviderAPIType{{
			ProviderID: "p1",
			APIType:    "codex",
			BaseURL:    "https://example.com",
			APIKey:     "codex-key",
		}},
	}

	headers := buildWebSocketDialHeaders(r, provider, "codex", "auto")

	if got := headers.Get("Authorization"); got != "Bearer codex-key" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer codex-key")
	}
}
