package proxy

import (
	"testing"
)

func TestParseAPIType(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		wantType string
		wantOK   bool
	}{
		// Claude API paths
		{
			name:     "claude messages with leading slash",
			path:     "/v1/messages",
			wantType: APITypeClaude,
			wantOK:   true,
		},
		{
			name:     "claude messages without leading slash",
			path:     "v1/messages",
			wantType: APITypeClaude,
			wantOK:   true,
		},
		{
			name:     "claude messages with trailing path",
			path:     "/v1/messages/stream",
			wantType: APITypeClaude,
			wantOK:   true,
		},
		{
			name:     "claude models",
			path:     "/v1/models",
			wantType: APITypeClaude,
			wantOK:   true,
		},
		{
			name:     "claude models with subpath",
			path:     "/v1/models/claude-3",
			wantType: APITypeClaude,
			wantOK:   true,
		},

		// Codex API paths
		{
			name:     "codex responses",
			path:     "/responses",
			wantType: APITypeCodex,
			wantOK:   true,
		},
		{
			name:     "codex responses with subpath",
			path:     "/responses/submit",
			wantType: APITypeCodex,
			wantOK:   true,
		},

		// Gemini API paths
		{
			name:     "gemini v1beta",
			path:     "/gemini/v1beta/models/gemini-pro:generateContent",
			wantType: APITypeGemini,
			wantOK:   true,
		},
		{
			name:     "gemini v1",
			path:     "/gemini/v1/models/gemini-pro:generateContent",
			wantType: APITypeGemini,
			wantOK:   true,
		},
		{
			name:     "gemini simple",
			path:     "/gemini/",
			wantType: APITypeGemini,
			wantOK:   true,
		},

		// Custom API paths
		{
			name:     "custom tool messages",
			path:     "/custom/mytool/v1/messages",
			wantType: "custom:mytool",
			wantOK:   true,
		},
		{
			name:     "custom tool models",
			path:     "/custom/search/v1/models",
			wantType: "custom:search",
			wantOK:   true,
		},
		{
			name:     "custom tool with hyphens",
			path:     "/custom/my-custom-tool/v1/messages",
			wantType: "custom:my-custom-tool",
			wantOK:   true,
		},

		// Invalid paths
		{
			name:     "unknown path",
			path:     "/unknown/path",
			wantType: "",
			wantOK:   false,
		},
		{
			name:     "empty path",
			path:     "",
			wantType: "",
			wantOK:   false,
		},
		{
			name:     "root path",
			path:     "/",
			wantType: "",
			wantOK:   false,
		},
		{
			name:     "custom without toolId",
			path:     "/custom/",
			wantType: "",
			wantOK:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotType, gotOK := ParseAPIType(tt.path)
			if gotType != tt.wantType || gotOK != tt.wantOK {
				t.Errorf("ParseAPIType(%q) = (%q, %v), want (%q, %v)",
					tt.path, gotType, gotOK, tt.wantType, tt.wantOK)
			}
		})
	}
}

func TestBuildUpstreamPath(t *testing.T) {
	tests := []struct {
		name         string
		originalPath string
		apiType      string
		wantPath     string
	}{
		{
			name:         "claude passthrough",
			originalPath: "/v1/messages",
			apiType:      APITypeClaude,
			wantPath:     "/v1/messages",
		},
		{
			name:         "codex passthrough",
			originalPath: "/responses",
			apiType:      APITypeCodex,
			wantPath:     "/responses",
		},
		{
			name:         "gemini passthrough",
			originalPath: "/gemini/v1beta/models/gemini-pro:generateContent",
			apiType:      APITypeGemini,
			wantPath:     "/gemini/v1beta/models/gemini-pro:generateContent",
		},
		{
			name:         "custom strips prefix",
			originalPath: "/custom/mytool/v1/messages",
			apiType:      "custom:mytool",
			wantPath:     "/v1/messages",
		},
		{
			name:         "custom strips prefix with complex path",
			originalPath: "/custom/search/v1/models/list",
			apiType:      "custom:search",
			wantPath:     "/v1/models/list",
		},
		{
			name:         "custom with minimal path",
			originalPath: "/custom/tool",
			apiType:      "custom:tool",
			wantPath:     "/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildUpstreamPath(tt.originalPath, tt.apiType)
			if got != tt.wantPath {
				t.Errorf("BuildUpstreamPath(%q, %q) = %q, want %q",
					tt.originalPath, tt.apiType, got, tt.wantPath)
			}
		})
	}
}
