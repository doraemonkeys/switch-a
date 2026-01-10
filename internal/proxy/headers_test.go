package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCopyHeaders(t *testing.T) {
	tests := []struct {
		name        string
		srcHeaders  map[string]string
		wantHeaders map[string]string
		skipHeaders map[string]bool
	}{
		{
			name: "copies normal headers",
			srcHeaders: map[string]string{
				"Content-Type": "application/json",
				"Accept":       "text/html",
				"X-Custom":     "value",
			},
			wantHeaders: map[string]string{
				"Content-Type": "application/json",
				"Accept":       "text/html",
				"X-Custom":     "value",
			},
		},
		{
			name: "skips hop-by-hop headers",
			srcHeaders: map[string]string{
				"Content-Type":      "application/json",
				"Connection":        "keep-alive",
				"Keep-Alive":        "timeout=5",
				"Transfer-Encoding": "chunked",
				"Upgrade":           "websocket",
			},
			wantHeaders: map[string]string{
				"Content-Type": "application/json",
			},
			skipHeaders: map[string]bool{
				"Connection":        true,
				"Keep-Alive":        true,
				"Transfer-Encoding": true,
				"Upgrade":           true,
			},
		},
		{
			name: "skips auth headers",
			srcHeaders: map[string]string{
				"Content-Type":  "application/json",
				"Authorization": "Bearer token",
				"X-Api-Key":     "key123",
			},
			wantHeaders: map[string]string{
				"Content-Type": "application/json",
			},
			skipHeaders: map[string]bool{
				"Authorization": true,
				"X-Api-Key":     true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := make(http.Header)
			for k, v := range tt.srcHeaders {
				src.Set(k, v)
			}

			dst := make(http.Header)
			CopyHeaders(dst, src)

			// Check expected headers are present
			for k, v := range tt.wantHeaders {
				if got := dst.Get(k); got != v {
					t.Errorf("header %q = %q, want %q", k, got, v)
				}
			}

			// Check skipped headers are absent
			for k := range tt.skipHeaders {
				if got := dst.Get(k); got != "" {
					t.Errorf("header %q should be skipped, got %q", k, got)
				}
			}
		})
	}
}

func TestIsAuthHeader(t *testing.T) {
	tests := []struct {
		header string
		want   bool
	}{
		{"Authorization", true},
		{"authorization", true},
		{"AUTHORIZATION", true},
		{"X-Api-Key", true},
		{"x-api-key", true},
		{"X-API-KEY", true},
		{"Content-Type", false},
		{"X-Custom", false},
		{"Auth", false},
	}

	for _, tt := range tests {
		t.Run(tt.header, func(t *testing.T) {
			got := isAuthHeader(tt.header)
			if got != tt.want {
				t.Errorf("isAuthHeader(%q) = %v, want %v", tt.header, got, tt.want)
			}
		})
	}
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
