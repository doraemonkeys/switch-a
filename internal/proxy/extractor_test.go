package proxy

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestExtractClientIP(t *testing.T) {
	tests := []struct {
		name             string
		remoteAddr       string
		xForwardedFor    string
		xRealIP          string
		trustProxyHeader bool
		wantIP           string
	}{
		{
			name:             "remote addr only",
			remoteAddr:       "192.168.1.100:12345",
			trustProxyHeader: false,
			wantIP:           "192.168.1.100",
		},
		{
			name:             "X-Forwarded-For trusted",
			remoteAddr:       "192.168.1.100:12345",
			xForwardedFor:    "203.0.113.50, 70.41.3.18, 150.172.238.178",
			trustProxyHeader: true,
			wantIP:           "203.0.113.50",
		},
		{
			name:             "X-Forwarded-For not trusted",
			remoteAddr:       "192.168.1.100:12345",
			xForwardedFor:    "203.0.113.50",
			trustProxyHeader: false,
			wantIP:           "192.168.1.100",
		},
		{
			name:             "X-Real-IP trusted",
			remoteAddr:       "192.168.1.100:12345",
			xRealIP:          "203.0.113.75",
			trustProxyHeader: true,
			wantIP:           "203.0.113.75",
		},
		{
			name:             "X-Forwarded-For takes precedence over X-Real-IP",
			remoteAddr:       "192.168.1.100:12345",
			xForwardedFor:    "203.0.113.50",
			xRealIP:          "203.0.113.75",
			trustProxyHeader: true,
			wantIP:           "203.0.113.50",
		},
		{
			name:             "IPv6 remote addr",
			remoteAddr:       "[::1]:12345",
			trustProxyHeader: false,
			wantIP:           "::1",
		},
		{
			name:             "remote addr without port",
			remoteAddr:       "192.168.1.100",
			trustProxyHeader: false,
			wantIP:           "192.168.1.100",
		},
		{
			name:             "empty X-Forwarded-For falls back to X-Real-IP",
			remoteAddr:       "192.168.1.100:12345",
			xForwardedFor:    "",
			xRealIP:          "203.0.113.75",
			trustProxyHeader: true,
			wantIP:           "203.0.113.75",
		},
		{
			name:             "whitespace in X-Forwarded-For",
			remoteAddr:       "192.168.1.100:12345",
			xForwardedFor:    "  203.0.113.50  ",
			trustProxyHeader: true,
			wantIP:           "203.0.113.50",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tt.remoteAddr
			if tt.xForwardedFor != "" {
				req.Header.Set("X-Forwarded-For", tt.xForwardedFor)
			}
			if tt.xRealIP != "" {
				req.Header.Set("X-Real-IP", tt.xRealIP)
			}

			got := ExtractClientIP(req, tt.trustProxyHeader)
			if got != tt.wantIP {
				t.Errorf("ExtractClientIP() = %q, want %q", got, tt.wantIP)
			}
		})
	}
}

func TestExtractUserID(t *testing.T) {
	tests := []struct {
		name       string
		header     string
		headerVal  string
		userHeader string
		wantUserID string
	}{
		{
			name:       "X-User-ID present",
			header:     "X-User-ID",
			headerVal:  "user123",
			userHeader: "X-User-ID",
			wantUserID: "user123",
		},
		{
			name:       "custom header",
			header:     "X-Custom-User",
			headerVal:  "customuser",
			userHeader: "X-Custom-User",
			wantUserID: "customuser",
		},
		{
			name:       "header not present",
			header:     "X-Other",
			headerVal:  "value",
			userHeader: "X-User-ID",
			wantUserID: "",
		},
		{
			name:       "empty header value",
			header:     "X-User-ID",
			headerVal:  "",
			userHeader: "X-User-ID",
			wantUserID: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set(tt.header, tt.headerVal)

			got := ExtractUserID(req, tt.userHeader)
			if got != tt.wantUserID {
				t.Errorf("ExtractUserID() = %q, want %q", got, tt.wantUserID)
			}
		})
	}
}

func TestExtractModel(t *testing.T) {
	tests := []struct {
		name      string
		apiType   string
		urlPath   string
		body      []byte
		wantModel string
	}{
		// Gemini API - model from URL
		{
			name:      "gemini model from path",
			apiType:   APITypeGemini,
			urlPath:   "/gemini/v1beta/models/gemini-pro:generateContent",
			body:      nil,
			wantModel: "gemini-pro",
		},
		{
			name:      "gemini v1 model from path",
			apiType:   APITypeGemini,
			urlPath:   "/gemini/v1/models/gemini-1.5-flash:streamGenerateContent",
			body:      nil,
			wantModel: "gemini-1.5-flash",
		},
		{
			name:      "gemini path without model",
			apiType:   APITypeGemini,
			urlPath:   "/gemini/v1/chat",
			body:      nil,
			wantModel: "unknown",
		},
		{
			name:      "gemini path with query string",
			apiType:   APITypeGemini,
			urlPath:   "/gemini/v1beta/models/gemini-pro?key=abc",
			body:      nil,
			wantModel: "gemini-pro",
		},

		// Claude/Codex API - model from JSON body
		{
			name:      "claude model from body",
			apiType:   APITypeClaude,
			urlPath:   "/v1/messages",
			body:      []byte(`{"model":"claude-3-opus-20240229","max_tokens":1024}`),
			wantModel: "claude-3-opus-20240229",
		},
		{
			name:      "codex model from body",
			apiType:   APITypeCodex,
			urlPath:   "/responses",
			body:      []byte(`{"model":"gpt-4o","prompt":"Hello"}`),
			wantModel: "gpt-4o",
		},
		{
			name:      "model not in body",
			apiType:   APITypeClaude,
			urlPath:   "/v1/messages",
			body:      []byte(`{"max_tokens":1024}`),
			wantModel: "unknown",
		},
		{
			name:      "empty body",
			apiType:   APITypeClaude,
			urlPath:   "/v1/messages",
			body:      nil,
			wantModel: "unknown",
		},
		{
			name:      "model with whitespace",
			apiType:   APITypeClaude,
			urlPath:   "/v1/messages",
			body:      []byte(`{"model"  :  "claude-3-sonnet"}`),
			wantModel: "claude-3-sonnet",
		},
		{
			name:      "custom api with model",
			apiType:   "custom:mytool",
			urlPath:   "/custom/mytool/v1/messages",
			body:      []byte(`{"model":"custom-model"}`),
			wantModel: "custom-model",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tt.urlPath, nil)

			got := ExtractModel(req, tt.apiType, tt.body)
			if got != tt.wantModel {
				t.Errorf("ExtractModel() = %q, want %q", got, tt.wantModel)
			}
		})
	}
}

func TestExtractGeminiModel(t *testing.T) {
	tests := []struct {
		path      string
		wantModel string
	}{
		{"/gemini/v1beta/models/gemini-pro:generateContent", "gemini-pro"},
		{"/gemini/v1/models/gemini-1.5-flash:streamGenerateContent", "gemini-1.5-flash"},
		{"/gemini/v1beta/models/gemini-pro", "gemini-pro"},
		{"/gemini/v1beta/chat", "unknown"},
		{"/other/path", "unknown"},
		{"/gemini/v1beta/models/", "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := extractGeminiModel(tt.path)
			if got != tt.wantModel {
				t.Errorf("extractGeminiModel(%q) = %q, want %q", tt.path, got, tt.wantModel)
			}
		})
	}
}

func TestExtractModelFromJSON(t *testing.T) {
	tests := []struct {
		name      string
		body      []byte
		wantModel string
	}{
		{
			name:      "simple model field",
			body:      []byte(`{"model":"test-model"}`),
			wantModel: "test-model",
		},
		{
			name:      "model with spaces",
			body:      []byte(`{"model" : "spaced-model"}`),
			wantModel: "spaced-model",
		},
		{
			name:      "model in complex json",
			body:      []byte(`{"messages":[{"role":"user"}],"model":"nested-model","max_tokens":100}`),
			wantModel: "nested-model",
		},
		{
			name:      "no model field",
			body:      []byte(`{"other":"value"}`),
			wantModel: "unknown",
		},
		{
			name:      "empty body",
			body:      nil,
			wantModel: "unknown",
		},
		{
			name:      "invalid json",
			body:      []byte(`not json`),
			wantModel: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractModelFromJSON(tt.body)
			if got != tt.wantModel {
				t.Errorf("extractModelFromJSON() = %q, want %q", got, tt.wantModel)
			}
		})
	}
}

func TestConsumeAndReplaceBody(t *testing.T) {
	t.Run("normal body", func(t *testing.T) {
		body := "test body content"
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))

		got, err := ConsumeAndReplaceBody(req, 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if string(got) != body {
			t.Errorf("got body %q, want %q", string(got), body)
		}

		// Verify body can be read again
		readAgain, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatalf("failed to read body again: %v", err)
		}
		if string(readAgain) != body {
			t.Errorf("second read got %q, want %q", string(readAgain), body)
		}
	})

	t.Run("nil body", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Body = nil

		got, err := ConsumeAndReplaceBody(req, 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != nil {
			t.Errorf("expected nil body, got %v", got)
		}
	})

	t.Run("body exceeds limit", func(t *testing.T) {
		// Create a body larger than 1MB
		largeBody := bytes.Repeat([]byte("x"), 2*1024*1024)
		req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(largeBody))

		_, err := ConsumeAndReplaceBody(req, 1) // 1MB limit
		if err != ErrBodyTooLarge {
			t.Errorf("expected ErrBodyTooLarge, got %v", err)
		}
	})

	t.Run("body at limit", func(t *testing.T) {
		// Create a body exactly at limit
		body := bytes.Repeat([]byte("x"), 1024*1024) // 1MB
		req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))

		got, err := ConsumeAndReplaceBody(req, 1) // 1MB limit
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != len(body) {
			t.Errorf("got body length %d, want %d", len(got), len(body))
		}
	})
}

func TestGetReqBodySnippet(t *testing.T) {
	tests := []struct {
		name       string
		body       []byte
		wantSuffix string // expected suffix (empty for non-truncated, "..." for truncated)
		wantLen    int    // expected length (0 means check equals original)
	}{
		{
			name:       "empty body",
			body:       nil,
			wantSuffix: "",
			wantLen:    0,
		},
		{
			name:       "empty slice",
			body:       []byte{},
			wantSuffix: "",
			wantLen:    0,
		},
		{
			name:       "short body",
			body:       []byte("short body"),
			wantSuffix: "",
			wantLen:    10,
		},
		{
			name:       "body at exact limit",
			body:       bytes.Repeat([]byte("x"), MaxReqBodySnippetLength),
			wantSuffix: "",
			wantLen:    MaxReqBodySnippetLength,
		},
		{
			name:       "body exceeds limit",
			body:       bytes.Repeat([]byte("x"), MaxReqBodySnippetLength+100),
			wantSuffix: "...",
			wantLen:    MaxReqBodySnippetLength + 3, // truncated + "..."
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetReqBodySnippet(tt.body)

			if tt.wantLen == 0 && len(tt.body) > 0 {
				if got != string(tt.body) {
					t.Errorf("GetReqBodySnippet() = %q, want %q", got, string(tt.body))
				}
			} else if tt.wantLen > 0 && len(got) != tt.wantLen {
				t.Errorf("GetReqBodySnippet() length = %d, want %d", len(got), tt.wantLen)
			}

			if tt.wantSuffix != "" && !strings.HasSuffix(got, tt.wantSuffix) {
				t.Errorf("GetReqBodySnippet() should end with %q, got %q", tt.wantSuffix, got)
			}
		})
	}
}

func TestGetReqBodySnippet_UTF8Safety(t *testing.T) {
	// Test that UTF-8 multi-byte characters are not split during truncation

	t.Run("truncation at UTF-8 boundary", func(t *testing.T) {
		// Create a body with UTF-8 characters that would be split at MaxReqBodySnippetLength
		// Chinese character "中" is 3 bytes: 0xE4 0xB8 0xAD
		// Fill up to near the limit with ASCII, then add multi-byte chars
		padding := bytes.Repeat([]byte("a"), MaxReqBodySnippetLength-2)
		// Add a 3-byte UTF-8 character that would be split if we just truncate at limit
		body := append(padding, []byte("中文")...) // 2 Chinese chars = 6 bytes

		got := GetReqBodySnippet(body)

		// The result should be valid UTF-8
		if !utf8.ValidString(got) {
			t.Errorf("GetReqBodySnippet() produced invalid UTF-8: %q", got)
		}

		// Should end with "..."
		if !strings.HasSuffix(got, "...") {
			t.Errorf("GetReqBodySnippet() should end with '...', got %q", got)
		}
	})

	t.Run("body with only multi-byte characters", func(t *testing.T) {
		// Create body with only 4-byte emoji characters that exceeds limit
		// Each emoji is 4 bytes
		emoji := "😀"
		numEmojis := MaxReqBodySnippetLength/4 + 10 // Exceed limit
		body := []byte(strings.Repeat(emoji, numEmojis))

		got := GetReqBodySnippet(body)

		// The result should be valid UTF-8
		if !utf8.ValidString(got) {
			t.Errorf("GetReqBodySnippet() produced invalid UTF-8: %q", got)
		}

		// Should end with "..."
		if !strings.HasSuffix(got, "...") {
			t.Errorf("GetReqBodySnippet() should end with '...', got %q", got)
		}
	})

	t.Run("mixed ASCII and multi-byte at boundary", func(t *testing.T) {
		// Position a multi-byte character exactly at the truncation point
		// MaxReqBodySnippetLength-1 ASCII chars + 3-byte UTF-8 char
		padding := bytes.Repeat([]byte("x"), MaxReqBodySnippetLength-1)
		body := append(padding, []byte("日")...) // 3-byte character

		got := GetReqBodySnippet(body)

		if !utf8.ValidString(got) {
			t.Errorf("GetReqBodySnippet() produced invalid UTF-8: %q", got)
		}
	})
}
