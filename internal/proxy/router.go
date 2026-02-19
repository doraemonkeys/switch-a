// Package proxy provides the HTTP proxy implementation for forwarding requests to upstream providers.
package proxy

import (
	"strings"
)

// API type constants.
const (
	APITypeClaude = "claude"
	APITypeCodex  = "codex"
	APITypeGemini = "gemini"
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
	RouteCodexResponses = "/responses"
	// Gemini API routes (prefix)
	RouteGeminiPrefix = "/gemini/"
	RouteGeminiV1Beta = "/v1beta/"
	// Custom API routes (prefix)
	RouteCustomPrefix = "/custom/"
)

// ParseAPIType determines the API type from the request path.
// Returns the API type and a boolean indicating if the type was recognized.
//
// Path mappings:
//   - POST /v1/messages, GET /v1/models → claude
//   - POST /responses → codex
//   - POST /gemini/*, POST /v1beta/* → gemini
//   - POST /custom/:toolId/v1/messages, GET /custom/:toolId/v1/models → custom:{toolId}
func ParseAPIType(path string) (apiType string, ok bool) {
	// Normalize path
	path = strings.TrimPrefix(path, "/")

	// Claude API
	if strings.HasPrefix(path, "v1/messages") || strings.HasPrefix(path, "v1/models") {
		return APITypeClaude, true
	}

	// Codex API
	if strings.HasPrefix(path, "responses") {
		return APITypeCodex, true
	}

	// Gemini API
	if strings.HasPrefix(path, "gemini/") || strings.HasPrefix(path, "v1beta/") {
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
// For most API types, the path is passed through unchanged.
// For custom APIs, the /custom/:toolId prefix is stripped.
func BuildUpstreamPath(originalPath, apiType string) string {
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
