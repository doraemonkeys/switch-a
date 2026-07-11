// Package proxy provides the HTTP proxy implementation for forwarding requests to upstream providers.
package proxy

import (
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
	// Grok API routes (xAI OpenAI-compatible Chat Completions)
	RouteGrokChatCompletions   = "/chat/completions"
	RouteGrokChatCompletionsV1 = "/v1/chat/completions"
	// Gemini API routes (native contract prefix)
	RouteGeminiV1Beta = "/v1beta/"
	// Custom API routes (prefix)
	RouteCustomPrefix = "/custom/"
)

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
	trimmed := strings.TrimPrefix(path, "/")
	segment, remainder, _ := strings.Cut(trimmed, "/")
	apiType, ok = builtinAPINamespaces[segment]
	if !ok {
		return "", "", false
	}
	return apiType, "/" + remainder, true
}

// ParseAPIType determines the API type from the request path.
// Returns the API type and a boolean indicating if the type was recognized.
//
// Explicit namespaces pin the type without contract-path sniffing:
//   - /claude/*, /codex/*, /grok/*, /gemini/* → the corresponding built-in type
//   - /custom/:toolId/* → custom:{toolId}
//
// Bare contract paths are matched by the shape native tools emit:
//   - POST /v1/messages, GET /v1/models → claude
//   - POST /responses, POST /v1/responses → codex
//   - POST /chat/completions, POST /v1/chat/completions → grok
//   - POST /v1beta/* → gemini
func ParseAPIType(path string) (apiType string, ok bool) {
	if namespaceType, _, isNamespaced := SplitAPINamespace(path); isNamespaced {
		return namespaceType, true
	}

	// Normalize path
	path = strings.TrimPrefix(path, "/")

	// Claude API
	if strings.HasPrefix(path, "v1/messages") || strings.HasPrefix(path, "v1/models") {
		return APITypeClaude, true
	}

	// Codex API
	if strings.HasPrefix(path, "responses") || strings.HasPrefix(path, "v1/responses") {
		return APITypeCodex, true
	}

	// Grok API
	if strings.HasPrefix(path, "chat/completions") || strings.HasPrefix(path, "v1/chat/completions") {
		return APITypeGrok, true
	}

	// Gemini native contract path
	if strings.HasPrefix(path, "v1beta/") {
		return APITypeGemini, true
	}

	// Custom API: /custom/:toolId/...
	if strings.HasPrefix(path, "custom/") {
		parts := strings.SplitN(path, "/", 3)
		if len(parts) >= 2 && parts[1] != "" {
			return CustomAPITypePrefix + parts[1], true
		}
	}

	return "", false
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
