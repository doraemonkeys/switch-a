// Package proxy provides the HTTP proxy implementation for forwarding requests to upstream providers.
package proxy

import (
	"net/http"
	"sort"
	"strings"
)

// API type constants.
const (
	APITypeClaude = "claude"
	APITypeCodex  = "codex"
	APITypeGemini = "gemini"
	APITypeGrok   = "grok"
)

// CustomAPITypePrefix is the prefix for custom API types.
const CustomAPITypePrefix = "custom:"

// Route path constants used for HTTP routing.
// These ensure consistency between server route registration and path parsing.
const (
	// Claude API routes
	RouteClaudeMessages    = "/v1/messages"
	RouteClaudeCountTokens = "/v1/messages/count_tokens"
	RouteClaudeModels      = "/v1/models"
	// Codex API routes
	RouteCodexResponses   = "/responses"
	RouteCodexResponsesV1 = "/v1/responses"
	RouteCodexWebSearch   = "/alpha/search"
	RouteCodexWebSearchV1 = "/v1/alpha/search"
	// Grok API routes (xAI OpenAI-compatible Chat Completions)
	RouteGrokChatCompletions   = "/chat/completions"
	RouteGrokChatCompletionsV1 = "/v1/chat/completions"
	// Gemini API routes (native contract prefix)
	RouteGeminiV1Beta = "/v1beta/"
	// Custom API routes (prefix)
	RouteCustomPrefix = "/custom/"
)

// BareProxyRoute is an HTTP route exposed directly at the gateway root.
// API-type ownership remains internal so callers can register the transport
// contract without duplicating routing policy.
type BareProxyRoute struct {
	Method  string
	Pattern string
}

type bareProxyRouteDefinition struct {
	method  string
	pattern string
	apiType string
}

// bareProxyRouteDefinitions is the single source of truth for root-level API
// contracts. Keeping mux registration and request classification on the same
// catalog prevents a route from being recognized by Handler but unreachable
// through the real server (or vice versa).
var bareProxyRouteDefinitions = []bareProxyRouteDefinition{
	{method: http.MethodPost, pattern: RouteClaudeCountTokens, apiType: APITypeClaude},
	{method: http.MethodPost, pattern: RouteClaudeMessages, apiType: APITypeClaude},
	{method: http.MethodGet, pattern: RouteClaudeModels, apiType: APITypeClaude},
	{method: http.MethodPost, pattern: RouteCodexResponses, apiType: APITypeCodex},
	{method: http.MethodGet, pattern: RouteCodexResponses, apiType: APITypeCodex},
	{method: http.MethodPost, pattern: RouteCodexResponsesV1, apiType: APITypeCodex},
	{method: http.MethodGet, pattern: RouteCodexResponsesV1, apiType: APITypeCodex},
	{method: http.MethodPost, pattern: RouteCodexWebSearch, apiType: APITypeCodex},
	{method: http.MethodPost, pattern: RouteCodexWebSearchV1, apiType: APITypeCodex},
	{method: http.MethodPost, pattern: RouteGrokChatCompletions, apiType: APITypeGrok},
	{method: http.MethodPost, pattern: RouteGrokChatCompletionsV1, apiType: APITypeGrok},
	{method: http.MethodPost, pattern: RouteGeminiV1Beta, apiType: APITypeGemini},
}

// BareProxyRoutes returns a copy of the root-level HTTP contract for server
// registration. Returning value objects prevents consumers from mutating the
// catalog used to resolve API types.
func BareProxyRoutes() []BareProxyRoute {
	routes := make([]BareProxyRoute, 0, len(bareProxyRouteDefinitions))
	for _, route := range bareProxyRouteDefinitions {
		routes = append(routes, BareProxyRoute{
			Method:  route.method,
			Pattern: route.pattern,
		})
	}
	return routes
}

// builtinAPINamespaces maps explicit URL namespaces to built-in API types.
// Bare contract paths cannot distinguish two upstream vendors that share a
// wire contract (every OpenAI-compatible vendor claims /chat/completions), so
// clients may pin the API type in their base URL instead
// (http://gateway/grok → /grok/chat/completions). The namespace is gateway
// routing metadata and is stripped before forwarding; upstreams only ever see
// the native contract path.
var builtinAPINamespaces = map[string]string{
	"claude": APITypeClaude,
	"codex":  APITypeCodex,
	"gemini": APITypeGemini,
	"grok":   APITypeGrok,
}

// APINamespaceRoutePatterns returns the mux route prefixes for the explicit
// built-in API namespaces (e.g. "/claude/"), sorted for deterministic
// registration.
func APINamespaceRoutePatterns() []string {
	patterns := make([]string, 0, len(builtinAPINamespaces))
	for namespace := range builtinAPINamespaces {
		patterns = append(patterns, "/"+namespace+"/")
	}
	sort.Strings(patterns)
	return patterns
}

// SplitAPINamespace splits an explicit built-in API namespace from a request
// path: "/grok/v1/chat/completions" → ("grok", "/v1/chat/completions", true).
// Returns ok=false when the first path segment is not a built-in namespace.
func SplitAPINamespace(path string) (apiType, contractPath string, ok bool) {
	if !strings.HasPrefix(path, "/") {
		return "", "", false
	}
	trimmed := strings.TrimPrefix(path, "/")
	segment, remainder, hasContractPath := strings.Cut(trimmed, "/")
	if !hasContractPath {
		return "", "", false
	}
	apiType, ok = builtinAPINamespaces[segment]
	if !ok {
		return "", "", false
	}
	return apiType, "/" + remainder, true
}

// ResolveAPIType determines the API type from the request method and path.
// It mirrors the methods and exact/subtree matching semantics registered with
// http.ServeMux so direct Handler use cannot accept requests that the server
// itself would reject.
//
// Explicit namespaces pin the type without contract-path sniffing:
//   - /claude/*, /codex/*, /grok/*, /gemini/* → the corresponding built-in type
//   - /custom/:toolId/* → custom:{toolId}
//
// Bare contract paths are resolved from bareProxyRouteDefinitions.
func ResolveAPIType(method, path string) (apiType string, ok bool) {
	if namespaceType, _, isNamespaced := SplitAPINamespace(path); isNamespaced && supportsSharedProxyMethod(method) {
		return namespaceType, true
	}

	for _, route := range bareProxyRouteDefinitions {
		if routeMatchesRequest(route, method, path) {
			return route.apiType, true
		}
	}

	// Custom API: /custom/:toolId/...
	if supportsSharedProxyMethod(method) && strings.HasPrefix(path, RouteCustomPrefix) {
		remainder := strings.TrimPrefix(path, RouteCustomPrefix)
		toolID, _, _ := strings.Cut(remainder, "/")
		if toolID != "" {
			return CustomAPITypePrefix + toolID, true
		}
	}

	return "", false
}

func routeMatchesRequest(route bareProxyRouteDefinition, method, path string) bool {
	if !routeMethodMatches(route.method, method) {
		return false
	}
	if strings.HasSuffix(route.pattern, "/") {
		return strings.HasPrefix(path, route.pattern)
	}
	return path == route.pattern
}

func routeMethodMatches(routeMethod, requestMethod string) bool {
	return routeMethod == requestMethod || routeMethod == http.MethodGet && requestMethod == http.MethodHead
}

func supportsSharedProxyMethod(method string) bool {
	return method == http.MethodPost || routeMethodMatches(http.MethodGet, method)
}

// BuildUpstreamPath constructs the upstream request path.
// Explicit namespaces (/claude, /codex, /grok, /gemini, /custom/:toolId) are
// stripped: they exist to route inside the gateway, not upstream.
// Codex and Grok additionally strip an optional client-side /v1 segment so the
// provider base_url owns the API version (e.g. https://api.x.ai/v1).
// Everything else passes through unchanged.
func BuildUpstreamPath(originalPath, apiType string) string {
	if namespaceType, contractPath, ok := SplitAPINamespace(originalPath); ok && namespaceType == apiType {
		originalPath = contractPath
	}
	if apiType == APITypeCodex || apiType == APITypeGrok {
		return trimVersionSegment(originalPath)
	}
	if !strings.HasPrefix(apiType, CustomAPITypePrefix) {
		return originalPath
	}

	// Strip /custom/:toolId prefix from path
	// /custom/mytool/v1/messages → /v1/messages
	path := strings.TrimPrefix(originalPath, "/")
	parts := strings.SplitN(path, "/", 3)
	if len(parts) >= 3 {
		return "/" + parts[2]
	}
	return "/"
}

// trimVersionSegment strips a leading /v1 path segment. Segment-aware so
// version-bearing contract prefixes like /v1beta stay intact.
func trimVersionSegment(path string) string {
	if path == "/v1" {
		return "/"
	}
	if strings.HasPrefix(path, "/v1/") {
		return strings.TrimPrefix(path, "/v1")
	}
	return path
}
