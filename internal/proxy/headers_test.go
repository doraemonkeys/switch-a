package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEnsureExplicitUserAgentHeader(t *testing.T) {
	t.Run("suppresses default user agent when absent", func(t *testing.T) {
		headers := make(http.Header)
		EnsureExplicitUserAgentHeader(headers)

		values := headers.Values(headerUserAgent)
		if len(values) != 1 || values[0] != "" {
			t.Fatalf("User-Agent values = %#v, want explicit empty value", values)
		}
	})

	t.Run("preserves explicit user agent", func(t *testing.T) {
		headers := make(http.Header)
		headers.Set(headerUserAgent, "switch-a-test/1.0")
		EnsureExplicitUserAgentHeader(headers)

		if got := headers.Get(headerUserAgent); got != "switch-a-test/1.0" {
			t.Fatalf("User-Agent = %q, want %q", got, "switch-a-test/1.0")
		}
	})
}

func TestDetectAuthMode(t *testing.T) {
	tests := []struct {
		name      string
		authHdr   string
		apiKeyHdr string
		wantMode  string
	}{
		{
			name:     "bearer auth present",
			authHdr:  "Bearer token123",
			wantMode: AuthModeBearer,
		},
		{
			name:      "x-api-key present",
			apiKeyHdr: "key123",
			wantMode:  AuthModeXAPI,
		},
		{
			name:      "both present, bearer takes precedence",
			authHdr:   "Bearer token123",
			apiKeyHdr: "key123",
			wantMode:  AuthModeBearer,
		},
		{
			name:     "no auth headers, defaults to bearer",
			wantMode: AuthModeBearer,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", nil)
			if tt.authHdr != "" {
				req.Header.Set("Authorization", tt.authHdr)
			}
			if tt.apiKeyHdr != "" {
				req.Header.Set("X-Api-Key", tt.apiKeyHdr)
			}

			got := DetectAuthMode(req)
			if got != tt.wantMode {
				t.Errorf("DetectAuthMode() = %q, want %q", got, tt.wantMode)
			}
		})
	}
}

func TestSetAuthHeader(t *testing.T) {
	tests := []struct {
		name             string
		apiKey           string
		providerAuthMode string
		globalAuthMode   string
		origAuthHdr      string
		origAPIKeyHdr    string
		wantAuthHdr      string
		wantAPIKeyHdr    string
	}{
		{
			name:             "bearer mode",
			apiKey:           "sk-test-key",
			providerAuthMode: AuthModeBearer,
			globalAuthMode:   AuthModeAuto,
			wantAuthHdr:      "Bearer sk-test-key",
			wantAPIKeyHdr:    "",
		},
		{
			name:             "x-api-key mode",
			apiKey:           "sk-test-key",
			providerAuthMode: AuthModeXAPI,
			globalAuthMode:   AuthModeAuto,
			wantAuthHdr:      "",
			wantAPIKeyHdr:    "sk-test-key",
		},
		{
			name:             "auto mode with bearer in request",
			apiKey:           "sk-test-key",
			providerAuthMode: "",
			globalAuthMode:   AuthModeAuto,
			origAuthHdr:      "Bearer original",
			wantAuthHdr:      "Bearer sk-test-key",
			wantAPIKeyHdr:    "",
		},
		{
			name:             "auto mode with x-api-key in request",
			apiKey:           "sk-test-key",
			providerAuthMode: "",
			globalAuthMode:   AuthModeAuto,
			origAPIKeyHdr:    "original-key",
			wantAuthHdr:      "",
			wantAPIKeyHdr:    "sk-test-key",
		},
		{
			name:             "auto mode with no auth in request defaults to bearer",
			apiKey:           "sk-test-key",
			providerAuthMode: "",
			globalAuthMode:   AuthModeAuto,
			wantAuthHdr:      "Bearer sk-test-key",
			wantAPIKeyHdr:    "",
		},
		{
			name:             "provider mode overrides global",
			apiKey:           "sk-test-key",
			providerAuthMode: AuthModeXAPI,
			globalAuthMode:   AuthModeBearer,
			wantAuthHdr:      "",
			wantAPIKeyHdr:    "sk-test-key",
		},
		{
			name:             "empty provider mode uses global",
			apiKey:           "sk-test-key",
			providerAuthMode: "",
			globalAuthMode:   AuthModeXAPI,
			wantAuthHdr:      "",
			wantAPIKeyHdr:    "sk-test-key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create original request
			origReq := httptest.NewRequest(http.MethodPost, "/", nil)
			if tt.origAuthHdr != "" {
				origReq.Header.Set("Authorization", tt.origAuthHdr)
			}
			if tt.origAPIKeyHdr != "" {
				origReq.Header.Set("X-Api-Key", tt.origAPIKeyHdr)
			}

			// Create destination headers
			dst := make(http.Header)

			SetAuthHeader(dst, tt.apiKey, tt.providerAuthMode, tt.globalAuthMode, origReq)

			if got := dst.Get("Authorization"); got != tt.wantAuthHdr {
				t.Errorf("Authorization = %q, want %q", got, tt.wantAuthHdr)
			}
			if got := dst.Get("x-api-key"); got != tt.wantAPIKeyHdr {
				t.Errorf("x-api-key = %q, want %q", got, tt.wantAPIKeyHdr)
			}
		})
	}
}
