package apicontract

import (
	"net/http"
	"sort"
	"strings"
)

const (
	MethodGet  = http.MethodGet
	MethodHead = http.MethodHead
	MethodPost = http.MethodPost
)

// Route path constants are owned with their API definitions so mux
// registration and request classification cannot evolve independently.
const (
	RouteClaudeMessages          = "/v1/messages"
	RouteClaudeCountTokens       = "/v1/messages/count_tokens"
	RouteClaudeModels            = "/v1/models"
	RouteCodexResponses          = "/responses"
	RouteCodexResponsesV1        = "/v1/responses"
	RouteCodexResponsesSubtree   = "/responses/"
	RouteCodexResponsesSubtreeV1 = "/v1/responses/"
	RouteCodexWebSearch          = "/alpha/search"
	RouteCodexWebSearchV1        = "/v1/alpha/search"
	RouteChatCompletions         = "/chat/completions"
	RouteChatCompletionsV1       = "/v1/chat/completions"
	RouteGeminiV1Beta            = "/v1beta/"
	RouteCustomPrefix            = "/custom/"
)

type RouteMatch string

const (
	RouteMatchExact  RouteMatch = "exact"
	RouteMatchPrefix RouteMatch = "prefix"
)

// Route is an unnamespaced gateway route owned by a built-in API contract.
type Route struct {
	Method  string     `json:"method"`
	Pattern string     `json:"pattern"`
	Match   RouteMatch `json:"match"`
}

// BareRoute is the server-facing route projection. API ownership stays inside
// this package so the mux cannot create a second request-classification table.
type BareRoute struct {
	Method  string
	Pattern string
}

// BareRoutes returns all root-level routes in catalog order.
func BareRoutes() []BareRoute {
	var routes []BareRoute
	for _, definition := range definitions {
		for _, route := range definition.UnnamespacedRoutes {
			routes = append(routes, BareRoute{Method: route.Method, Pattern: route.Pattern})
		}
	}
	return routes
}

// NamespaceRoutePatterns returns deterministic mux prefixes for every built-in
// namespace. Sorting preserves the repository's existing registration order.
func NamespaceRoutePatterns() []string {
	patterns := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		patterns = append(patterns, definition.NamespacePattern)
	}
	sort.Strings(patterns)
	return patterns
}

// SplitNamespace removes one explicit built-in namespace from path.
func SplitNamespace(path string) (APIType, string, bool) {
	if !strings.HasPrefix(path, "/") {
		return "", "", false
	}
	trimmed := strings.TrimPrefix(path, "/")
	segment, remainder, hasContractPath := strings.Cut(trimmed, "/")
	if !hasContractPath {
		return "", "", false
	}
	for _, definition := range definitions {
		if strings.Trim(definition.NamespacePattern, "/") == segment {
			return definition.APIType, "/" + remainder, true
		}
	}
	return "", "", false
}

// ResolveRequest classifies the exact method/path contract exposed by the
// gateway, including custom namespaces that remain opaque to semantic analysis.
func ResolveRequest(method, path string) (string, bool) {
	if apiType, _, ok := SplitNamespace(path); ok && supportsNamespaceMethod(method) {
		return string(apiType), true
	}
	for _, definition := range definitions {
		for _, route := range definition.UnnamespacedRoutes {
			if routeMatches(route, method, path) {
				return string(definition.APIType), true
			}
		}
	}
	if supportsNamespaceMethod(method) && strings.HasPrefix(path, RouteCustomPrefix) {
		remainder := strings.TrimPrefix(path, RouteCustomPrefix)
		toolID, _, _ := strings.Cut(remainder, "/")
		apiType := CustomAPITypePrefix + toolID
		if _, ok := ParseCustomAPIType(apiType); ok {
			return apiType, true
		}
	}
	return "", false
}

// RewriteUpstreamPath applies the request contract's explicit namespace and
// path policy. Keeping this beside route resolution prevents HTTP and
// WebSocket forwarding from implementing subtly different transformations.
func RewriteUpstreamPath(originalPath, apiType string) string {
	if namespaceType, contractPath, ok := SplitNamespace(originalPath); ok && string(namespaceType) == apiType {
		originalPath = contractPath
	}

	if definition, ok := Lookup(apiType); ok {
		if definition.UpstreamPathPolicy == UpstreamPathStripOptionalV1 {
			return trimOptionalV1Segment(originalPath)
		}
		return originalPath
	}

	toolID, ok := ParseCustomAPIType(apiType)
	if !ok {
		return originalPath
	}
	customNamespace := RouteCustomPrefix + toolID
	if originalPath == customNamespace {
		return "/"
	}
	if strings.HasPrefix(originalPath, customNamespace+"/") {
		return strings.TrimPrefix(originalPath, customNamespace)
	}
	return originalPath
}

func routeMatches(route Route, method, path string) bool {
	if !methodMatches(route.Method, method) {
		return false
	}
	if route.Match == RouteMatchPrefix {
		return strings.HasPrefix(path, route.Pattern)
	}
	return path == route.Pattern
}

func methodMatches(routeMethod, requestMethod string) bool {
	return routeMethod == requestMethod || routeMethod == MethodGet && requestMethod == MethodHead
}

func supportsNamespaceMethod(method string) bool {
	return method == MethodPost || methodMatches(MethodGet, method)
}

func trimOptionalV1Segment(path string) string {
	if path == "/v1" {
		return "/"
	}
	if strings.HasPrefix(path, "/v1/") {
		return strings.TrimPrefix(path, "/v1")
	}
	return path
}
