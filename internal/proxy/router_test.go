package proxy

import (
	"testing"
)

func TestResolveAPIType(t *testing.T) {
	tests := []struct {
		name     string
		method   string
		path     string
		wantType string
		wantOK   bool
	}{
		{
			name:     "claude messages",
			method:   "POST",
			path:     "/v1/messages",
			wantType: APITypeClaude,
			wantOK:   true,
		},
		{
			name:     "claude count tokens",
			method:   "POST",
			path:     "/v1/messages/count_tokens",
			wantType: APITypeClaude,
			wantOK:   true,
		},
		{
			name:     "claude models",
			method:   "GET",
			path:     "/v1/models",
			wantType: APITypeClaude,
			wantOK:   true,
		},
		{
			name:     "head follows get route semantics",
			method:   "HEAD",
			path:     "/v1/models",
			wantType: APITypeClaude,
			wantOK:   true,
		},
		{
			name:     "wrong method for claude models",
			method:   "POST",
			path:     "/v1/models",
			wantType: "",
			wantOK:   false,
		},
		{
			name:     "bare routes are exact",
			method:   "POST",
			path:     "/v1/messages/stream",
			wantType: "",
			wantOK:   false,
		},
		{
			name:     "codex responses",
			method:   "POST",
			path:     "/responses",
			wantType: APITypeCodex,
			wantOK:   true,
		},
		{
			name:     "codex responses websocket route",
			method:   "GET",
			path:     "/responses",
			wantType: APITypeCodex,
			wantOK:   true,
		},
		{
			name:     "codex v1 responses",
			method:   "POST",
			path:     "/v1/responses",
			wantType: APITypeCodex,
			wantOK:   true,
		},
		{
			name:     "codex web search",
			method:   "POST",
			path:     "/alpha/search",
			wantType: APITypeCodex,
			wantOK:   true,
		},
		{
			name:     "codex v1 web search",
			method:   "POST",
			path:     "/v1/alpha/search",
			wantType: APITypeCodex,
			wantOK:   true,
		},
		{
			name:     "codex web search is post only",
			method:   "GET",
			path:     "/alpha/search",
			wantType: "",
			wantOK:   false,
		},
		{
			name:     "codex web search sibling is not accepted",
			method:   "POST",
			path:     "/alpha/searching",
			wantType: "",
			wantOK:   false,
		},
		{
			name:     "grok chat completions",
			method:   "POST",
			path:     "/chat/completions",
			wantType: APITypeGrok,
			wantOK:   true,
		},
		{
			name:     "grok v1 chat completions",
			method:   "POST",
			path:     "/v1/chat/completions",
			wantType: APITypeGrok,
			wantOK:   true,
		},
		{
			name:     "gemini native v1beta path",
			method:   "POST",
			path:     "/v1beta/models/gemini-2.5-flash-lite:generateContent",
			wantType: APITypeGemini,
			wantOK:   true,
		},
		{
			name:     "gemini native route is post only",
			method:   "GET",
			path:     "/v1beta/models/gemini-2.5-flash-lite",
			wantType: "",
			wantOK:   false,
		},
		{
			name:     "claude namespace",
			method:   "POST",
			path:     "/claude/v1/messages",
			wantType: APITypeClaude,
			wantOK:   true,
		},
		{
			name:     "namespace head follows get route semantics",
			method:   "HEAD",
			path:     "/claude/v1/models",
			wantType: APITypeClaude,
			wantOK:   true,
		},
		{
			name:     "codex web search namespace",
			method:   "POST",
			path:     "/codex/alpha/search",
			wantType: APITypeCodex,
			wantOK:   true,
		},
		{
			name:     "grok namespace",
			method:   "POST",
			path:     "/grok/chat/completions",
			wantType: APITypeGrok,
			wantOK:   true,
		},
		{
			name:     "gemini namespace",
			method:   "POST",
			path:     "/gemini/v1/models/gemini-pro:generateContent",
			wantType: APITypeGemini,
			wantOK:   true,
		},
		{
			name:     "namespace unsupported method",
			method:   "DELETE",
			path:     "/claude/v1/messages",
			wantType: "",
			wantOK:   false,
		},
		{
			name:     "bare namespace is handled by mux redirect",
			method:   "POST",
			path:     "/claude",
			wantType: "",
			wantOK:   false,
		},
		{
			name:     "namespace-like segment is not a namespace",
			method:   "POST",
			path:     "/claudex/v1/messages",
			wantType: "",
			wantOK:   false,
		},
		{
			name:     "custom tool messages",
			method:   "POST",
			path:     "/custom/mytool/v1/messages",
			wantType: "custom:mytool",
			wantOK:   true,
		},
		{
			name:     "custom get route",
			method:   "GET",
			path:     "/custom/search/v1/models",
			wantType: "custom:search",
			wantOK:   true,
		},
		{
			name:     "custom unsupported method",
			method:   "PATCH",
			path:     "/custom/mytool/v1/messages",
			wantType: "",
			wantOK:   false,
		},
		{
			name:     "unknown path",
			method:   "POST",
			path:     "/unknown/path",
			wantType: "",
			wantOK:   false,
		},
		{
			name:     "path without leading slash",
			method:   "POST",
			path:     "v1/messages",
			wantType: "",
			wantOK:   false,
		},
		{
			name:     "root path",
			method:   "POST",
			path:     "/",
			wantType: "",
			wantOK:   false,
		},
		{
			name:     "custom without toolId",
			method:   "POST",
			path:     "/custom/",
			wantType: "",
			wantOK:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotType, gotOK := ResolveAPIType(tt.method, tt.path)
			if gotType != tt.wantType || gotOK != tt.wantOK {
				t.Errorf("ResolveAPIType(%q, %q) = (%q, %v), want (%q, %v)",
					tt.method, tt.path, gotType, gotOK, tt.wantType, tt.wantOK)
			}
		})
	}
}

func TestBareProxyRoutes(t *testing.T) {
	routes := BareProxyRoutes()
	seen := make(map[string]bool, len(routes))
	foundSearch := false
	foundVersionedSearch := false

	for _, route := range routes {
		key := route.Method + " " + route.Pattern
		if seen[key] {
			t.Fatalf("duplicate bare proxy route %q", key)
		}
		seen[key] = true

		if _, ok := ResolveAPIType(route.Method, route.Pattern); !ok {
			t.Fatalf("registered route %q cannot be resolved", key)
		}
		foundSearch = foundSearch || key == "POST "+RouteCodexWebSearch
		foundVersionedSearch = foundVersionedSearch || key == "POST "+RouteCodexWebSearchV1
	}

	if !foundSearch || !foundVersionedSearch {
		t.Fatalf("Codex web-search routes missing: bare=%v v1=%v", foundSearch, foundVersionedSearch)
	}

	routes[0] = BareProxyRoute{Method: "DELETE", Pattern: "/mutated"}
	if fresh := BareProxyRoutes()[0]; fresh.Method == "DELETE" || fresh.Pattern == "/mutated" {
		t.Fatal("BareProxyRoutes exposed mutable catalog state")
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
			name:         "claude count_tokens passthrough",
			originalPath: "/v1/messages/count_tokens",
			apiType:      APITypeClaude,
			wantPath:     "/v1/messages/count_tokens",
		},
		{
			name:         "codex passthrough",
			originalPath: "/responses",
			apiType:      APITypeCodex,
			wantPath:     "/responses",
		},
		{
			name:         "codex v1 normalizes to responses",
			originalPath: "/v1/responses",
			apiType:      APITypeCodex,
			wantPath:     "/responses",
		},
		{
			name:         "codex web search passthrough",
			originalPath: RouteCodexWebSearch,
			apiType:      APITypeCodex,
			wantPath:     RouteCodexWebSearch,
		},
		{
			name:         "codex v1 web search normalizes",
			originalPath: RouteCodexWebSearchV1,
			apiType:      APITypeCodex,
			wantPath:     RouteCodexWebSearch,
		},
		{
			name:         "grok passthrough",
			originalPath: "/chat/completions",
			apiType:      APITypeGrok,
			wantPath:     "/chat/completions",
		},
		{
			name:         "grok v1 normalizes to chat completions",
			originalPath: "/v1/chat/completions",
			apiType:      APITypeGrok,
			wantPath:     "/chat/completions",
		},
		{
			name:         "gemini namespace strips to native contract path",
			originalPath: "/gemini/v1beta/models/gemini-pro:generateContent",
			apiType:      APITypeGemini,
			wantPath:     "/v1beta/models/gemini-pro:generateContent",
		},
		{
			name:         "gemini native v1beta passthrough",
			originalPath: "/v1beta/models/gemini-pro:generateContent",
			apiType:      APITypeGemini,
			wantPath:     "/v1beta/models/gemini-pro:generateContent",
		},
		{
			name:         "claude namespace strips to native contract path",
			originalPath: "/claude/v1/messages",
			apiType:      APITypeClaude,
			wantPath:     "/v1/messages",
		},
		{
			name:         "claude namespace count_tokens",
			originalPath: "/claude/v1/messages/count_tokens",
			apiType:      APITypeClaude,
			wantPath:     "/v1/messages/count_tokens",
		},
		{
			name:         "codex namespace normalizes v1",
			originalPath: "/codex/v1/responses",
			apiType:      APITypeCodex,
			wantPath:     "/responses",
		},
		{
			name:         "codex namespace strips from web search",
			originalPath: "/codex/alpha/search",
			apiType:      APITypeCodex,
			wantPath:     RouteCodexWebSearch,
		},
		{
			name:         "grok namespace normalizes v1",
			originalPath: "/grok/v1/chat/completions",
			apiType:      APITypeGrok,
			wantPath:     "/chat/completions",
		},
		{
			name:         "grok namespace enables model discovery",
			originalPath: "/grok/v1/models",
			apiType:      APITypeGrok,
			wantPath:     "/models",
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
