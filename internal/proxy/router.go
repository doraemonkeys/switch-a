// Package proxy provides the HTTP proxy implementation for forwarding requests to upstream providers.
package proxy

import (
	"net/url"

	"github.com/doraemonkeys/switch-a/internal/apicontract"
)

// API type constants.
const (
	APITypeClaude         = string(apicontract.APITypeClaude)
	APITypeDeepSeekClaude = string(apicontract.APITypeDeepSeekClaude)
	APITypeCodex          = string(apicontract.APITypeCodex)
	APITypeGemini         = string(apicontract.APITypeGemini)
	APITypeGrok           = string(apicontract.APITypeGrok)
	APITypeDeepSeekOpenAI = string(apicontract.APITypeDeepSeekOpenAI)
)

// CustomAPITypePrefix is the prefix for custom API types.
const CustomAPITypePrefix = apicontract.CustomAPITypePrefix

// Route path constants used for HTTP routing.
// These ensure consistency between server route registration and path parsing.
const (
	// Claude API routes
	RouteClaudeMessages    = apicontract.RouteClaudeMessages
	RouteClaudeCountTokens = apicontract.RouteClaudeCountTokens
	RouteClaudeModels      = apicontract.RouteClaudeModels
	// Codex API routes
	RouteCodexResponses          = apicontract.RouteCodexResponses
	RouteCodexResponsesV1        = apicontract.RouteCodexResponsesV1
	RouteCodexResponsesSubtree   = apicontract.RouteCodexResponsesSubtree
	RouteCodexResponsesSubtreeV1 = apicontract.RouteCodexResponsesSubtreeV1
	RouteCodexWebSearch          = apicontract.RouteCodexWebSearch
	RouteCodexWebSearchV1        = apicontract.RouteCodexWebSearchV1
	// OpenAI-compatible Chat Completions API routes (Grok, DeepSeek OpenAI)
	RouteGrokChatCompletions   = apicontract.RouteChatCompletions
	RouteGrokChatCompletionsV1 = apicontract.RouteChatCompletionsV1
	// Gemini API routes (native contract prefix)
	RouteGeminiV1Beta = apicontract.RouteGeminiV1Beta
	// Custom API routes (prefix)
	RouteCustomPrefix = apicontract.RouteCustomPrefix
)

// BareProxyRoute is an HTTP route exposed directly at the gateway root.
// API-type ownership remains internal so callers can register the transport
// contract without duplicating routing policy.
type BareProxyRoute = apicontract.BareRoute

// RequestRoute is the canonical interpretation shared by the server boundary
// and the proxy handler.
type RequestRoute = apicontract.RequestRoute

// BareProxyRoutes returns a copy of the root-level HTTP contract for server
// registration. Returning value objects prevents consumers from mutating the
// catalog used to resolve API types.
func BareProxyRoutes() []BareProxyRoute {
	return apicontract.BareRoutes()
}

// APINamespaceRoutePatterns returns the mux route prefixes for the explicit
// built-in API namespaces (e.g. "/claude/"), sorted for deterministic
// registration.
func APINamespaceRoutePatterns() []string {
	return apicontract.NamespaceRoutePatterns()
}

// SplitAPINamespace splits an explicit built-in API namespace from a request
// path: "/grok/v1/chat/completions" → ("grok", "/v1/chat/completions", true).
// Returns ok=false when the first path segment is not a built-in namespace.
func SplitAPINamespace(path string) (apiType, contractPath string, ok bool) {
	typeID, contractPath, ok := apicontract.SplitNamespace(path)
	return string(typeID), contractPath, ok
}

// ResolveAPIType determines the API type from a decoded method/path pair.
//
// Explicit namespaces pin the type without contract-path sniffing:
//   - /claude/*, /deepseek-claude/*, /codex/*, /deepseek-openai/*, /grok/*, /gemini/* → the corresponding built-in type
//   - /custom/:toolId/* → custom:{toolId}
//
// Namespaced methods and paths remain opaque. Bare contract paths are resolved
// from the canonical method/path catalog because they do not identify an owner.
func ResolveAPIType(method, path string) (apiType string, ok bool) {
	return apicontract.ResolveRequest(method, path)
}

// ResolveRequestURL preserves literal path-segment boundaries while resolving
// API ownership, upstream rewriting, and endpoint capabilities.
func ResolveRequestURL(method string, requestURL *url.URL) (RequestRoute, bool) {
	return apicontract.ResolveRequestURL(method, requestURL)
}

// BuildUpstreamPath constructs the upstream request path.
// Explicit namespaces (/claude, /deepseek-claude, /codex, /deepseek-openai,
// /grok, /gemini, /custom/:toolId) are stripped: they exist to route inside
// the gateway, not upstream.
// Codex and OpenAI-chat-compatible types additionally strip an optional client-
// side /v1 segment so the provider base_url owns the API version.
// Everything else passes through unchanged.
func BuildUpstreamPath(originalPath, apiType string) string {
	return apicontract.RewriteUpstreamPath(originalPath, apiType)
}

func isClaudeCompatibleAPIType(apiType string) bool {
	return apicontract.UsesRequestDialect(apiType, apicontract.RequestDialectAnthropicMessages)
}

func isOpenAIChatCompletionsAPIType(apiType string) bool {
	return apicontract.UsesRequestDialect(apiType, apicontract.RequestDialectOpenAIChatCompletions)
}
