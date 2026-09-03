package proxy

import (
	"reflect"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/apicontract"
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
			name:     "codex responses compact",
			method:   "POST",
			path:     RouteCodexResponsesSubtree + "compact",
			wantType: APITypeCodex,
			wantOK:   true,
		},
		{
			name:     "codex v1 responses compact",
			method:   "POST",
			path:     RouteCodexResponsesSubtreeV1 + "compact",
			wantType: APITypeCodex,
			wantOK:   true,
		},
		{
			name:   "codex responses compact is post only",
			method: "GET",
			path:   RouteCodexResponsesSubtree + "compact",
		},
		{
			name:     "codex responses post subtree accepts another endpoint",
			method:   "POST",
			path:     RouteCodexResponsesSubtree + "resp_123/cancel",
			wantType: APITypeCodex,
			wantOK:   true,
		},
		{
			name:   "codex responses subtree requires segment boundary",
			method: "POST",
			path:   "/responses-evil/compact",
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
			name:     "deepseek claude namespace",
			method:   "POST",
			path:     "/deepseek-claude/v1/messages",
			wantType: APITypeDeepSeekClaude,
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
			name:     "codex responses compact namespace",
			method:   "POST",
			path:     "/codex/responses/compact",
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
			name:     "deepseek openai namespace",
			method:   "POST",
			path:     "/deepseek-openai/v1/chat/completions",
			wantType: APITypeDeepSeekOpenAI,
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
			name:     "namespace preserves delete method",
			method:   "DELETE",
			path:     "/codex/v1/responses/resp_123",
			wantType: APITypeCodex,
			wantOK:   true,
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
			name:     "custom namespace preserves patch method",
			method:   "PATCH",
			path:     "/custom/mytool/v1/resources/123",
			wantType: "custom:mytool",
			wantOK:   true,
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
	wantCodexPOSTRoutes := map[string]bool{
		RouteCodexResponsesSubtree:   false,
		RouteCodexResponsesSubtreeV1: false,
		RouteCodexWebSearch:          false,
		RouteCodexWebSearchV1:        false,
	}

	for _, route := range routes {
		key := route.Method + " " + route.Pattern
		if seen[key] {
			t.Fatalf("duplicate bare proxy route %q", key)
		}
		seen[key] = true

		if _, ok := ResolveAPIType(route.Method, route.Pattern); !ok {
			t.Fatalf("registered route %q cannot be resolved", key)
		}
		if _, tracked := wantCodexPOSTRoutes[route.Pattern]; tracked && route.Method == "POST" {
			wantCodexPOSTRoutes[route.Pattern] = true
		}
	}

	for route, found := range wantCodexPOSTRoutes {
		if !found {
			t.Errorf("Codex POST route %q is missing", route)
		}
	}

	routes[0] = BareProxyRoute{Method: "DELETE", Pattern: "/mutated"}
	if fresh := BareProxyRoutes()[0]; fresh.Method == "DELETE" || fresh.Pattern == "/mutated" {
		t.Fatal("BareProxyRoutes exposed mutable catalog state")
	}
}

func TestRouterConsumesCanonicalAPIContract(t *testing.T) {
	t.Parallel()

	for _, definition := range apicontract.All() {
		apiType := string(definition.APIType)
		if got, _, ok := SplitAPINamespace(definition.NamespacePattern + "probe"); !ok || got != apiType {
			t.Errorf("namespace for %q resolved as (%q, %v)", apiType, got, ok)
		}
		for _, route := range definition.UnnamespacedRoutes {
			if got, ok := ResolveAPIType(route.Method, route.Pattern); !ok || got != apiType {
				t.Errorf("canonical route %s %s resolved as (%q, %v), want %q", route.Method, route.Pattern, got, ok, apiType)
			}
		}
	}

	if CustomAPITypePrefix != apicontract.CustomAPITypePrefix || RouteCustomPrefix != apicontract.RouteCustomPrefix {
		t.Fatal("proxy compatibility aliases drifted from the canonical contract")
	}
}

func TestAPINamespaceRoutePatterns(t *testing.T) {
	want := []string{
		"/claude/",
		"/codex/",
		"/deepseek-claude/",
		"/deepseek-openai/",
		"/gemini/",
		"/grok/",
	}

	got := APINamespaceRoutePatterns()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("APINamespaceRoutePatterns() = %v, want %v", got, want)
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
			name:         "codex compact passthrough",
			originalPath: RouteCodexResponsesSubtree + "compact",
			apiType:      APITypeCodex,
			wantPath:     RouteCodexResponsesSubtree + "compact",
		},
		{
			name:         "codex v1 compact normalizes",
			originalPath: RouteCodexResponsesSubtreeV1 + "compact",
			apiType:      APITypeCodex,
			wantPath:     RouteCodexResponsesSubtree + "compact",
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
			name:         "deepseek openai v1 normalizes to chat completions",
			originalPath: "/v1/chat/completions",
			apiType:      APITypeDeepSeekOpenAI,
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
			name:         "deepseek claude namespace strips to native contract path",
			originalPath: "/deepseek-claude/v1/messages",
			apiType:      APITypeDeepSeekClaude,
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
			name:         "codex namespace normalizes compact v1",
			originalPath: "/codex/v1/responses/compact",
			apiType:      APITypeCodex,
			wantPath:     RouteCodexResponsesSubtree + "compact",
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
			name:         "deepseek openai namespace normalizes v1",
			originalPath: "/deepseek-openai/v1/chat/completions",
			apiType:      APITypeDeepSeekOpenAI,
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
		{
			name:         "custom preserves a different tool namespace",
			originalPath: "/custom/other/v1/messages",
			apiType:      "custom:tool",
			wantPath:     "/custom/other/v1/messages",
		},
		{
			name:         "unroutable custom API type preserves path",
			originalPath: "/custom/tool/v1/messages",
			apiType:      "custom:tool/nested",
			wantPath:     "/custom/tool/v1/messages",
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
