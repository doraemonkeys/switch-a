package apicontract

import (
	"net/http"
	"net/url"
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

// RequestRoute is the single routing interpretation of an inbound URL. Keeping
// the upstream path and endpoint capabilities beside API ownership prevents
// later policy checks from reinterpreting an already-decoded URL.Path.
type RequestRoute struct {
	APIType                  string
	UpstreamEscapedPath      string
	RequiresWebSocketUpgrade bool
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
	apiType, contractPath, ok, err := splitNamespace(path, preserveSegment)
	if err != nil {
		return "", "", false
	}
	return apiType, contractPath, ok
}

// ResolveRequest classifies the exact method/path contract exposed by the
// gateway, including custom namespaces that remain opaque to semantic analysis.
func ResolveRequest(method, path string) (string, bool) {
	apiType, ok, err := resolveRequest(method, path, preserveSegment)
	return apiType, ok && err == nil
}

// ResolveRequestURL interprets path separators from the escaped wire form. An
// encoded slash remains data in one segment, while percent-encoded characters
// inside a segment retain the same route identity as their literal spelling.
func ResolveRequestURL(method string, requestURL *url.URL) (RequestRoute, bool) {
	if requestURL == nil {
		return RequestRoute{}, false
	}

	escapedPath := requestURL.EscapedPath()
	apiType, ok, err := resolveRequest(method, escapedPath, url.PathUnescape)
	if err != nil || !ok {
		return RequestRoute{}, false
	}
	upstreamPath, err := RewriteUpstreamEscapedPath(escapedPath, apiType)
	if err != nil {
		return RequestRoute{}, false
	}
	webSocketOnly, err := pathMatches(
		upstreamPath,
		RouteCodexResponses,
		RouteMatchExact,
		url.PathUnescape,
	)
	if err != nil {
		return RequestRoute{}, false
	}

	return RequestRoute{
		APIType:                  apiType,
		UpstreamEscapedPath:      upstreamPath,
		RequiresWebSocketUpgrade: method == MethodGet && apiType == string(APITypeCodex) && webSocketOnly,
	}, true
}

// RewriteUpstreamPath applies the request contract's explicit namespace and
// path policy. Keeping this beside route resolution prevents HTTP and
// WebSocket forwarding from implementing subtly different transformations.
func RewriteUpstreamPath(originalPath, apiType string) string {
	rewrittenPath, err := rewriteUpstreamPath(originalPath, apiType, func(segment string) (string, error) {
		return segment, nil
	})
	if err != nil {
		return originalPath
	}
	return rewrittenPath
}

// RewriteUpstreamEscapedPath applies the same contract to an escaped path while
// treating only literal slashes as separators. Encoded slashes therefore remain
// data inside a segment instead of accidentally activating a path policy.
func RewriteUpstreamEscapedPath(originalPath, apiType string) (string, error) {
	return rewriteUpstreamPath(originalPath, apiType, url.PathUnescape)
}

type pathSegmentDecoder func(string) (string, error)

func preserveSegment(segment string) (string, error) {
	return segment, nil
}

func resolveRequest(method, path string, decodeSegment pathSegmentDecoder) (string, bool, error) {
	if apiType, _, ok, err := splitNamespace(path, decodeSegment); err != nil {
		return "", false, err
	} else if ok && supportsNamespaceMethod(method) {
		return string(apiType), true, nil
	}
	for _, definition := range definitions {
		for _, route := range definition.UnnamespacedRoutes {
			matched, err := routeMatches(route, method, path, decodeSegment)
			if err != nil {
				return "", false, err
			}
			if matched {
				return string(definition.APIType), true, nil
			}
		}
	}
	if supportsNamespaceMethod(method) {
		apiType, ok, err := resolveCustomNamespace(path, decodeSegment)
		if err != nil {
			return "", false, err
		}
		if ok {
			return apiType, true, nil
		}
	}
	return "", false, nil
}

func rewriteUpstreamPath(originalPath, apiType string, decodeSegment pathSegmentDecoder) (string, error) {
	if definition, ok := Lookup(apiType); ok {
		namespace := strings.Trim(definition.NamespacePattern, "/")
		rewrittenPath, stripped, err := stripNamespace(originalPath, namespace, decodeSegment)
		if err != nil {
			return "", err
		}
		if stripped {
			originalPath = rewrittenPath
		}
		if definition.UpstreamPathPolicy == UpstreamPathStripOptionalV1 {
			return trimOptionalV1Segment(originalPath, decodeSegment)
		}
		return originalPath, nil
	}

	toolID, ok := ParseCustomAPIType(apiType)
	if !ok {
		return originalPath, nil
	}
	rewrittenPath, stripped, err := stripCustomNamespace(originalPath, toolID, decodeSegment)
	if err != nil || !stripped {
		return originalPath, err
	}
	return rewrittenPath, nil
}

func routeMatches(route Route, method, path string, decodeSegment pathSegmentDecoder) (bool, error) {
	if !methodMatches(route.Method, method) {
		return false, nil
	}
	return pathMatches(path, route.Pattern, route.Match, decodeSegment)
}

func pathMatches(path, pattern string, match RouteMatch, decodeSegment pathSegmentDecoder) (bool, error) {
	pathSegments, pathOK, err := decodePathSegments(path, decodeSegment)
	if err != nil || !pathOK {
		return false, err
	}
	patternSegments, patternOK, err := decodePathSegments(pattern, preserveSegment)
	if err != nil || !patternOK {
		return false, err
	}

	comparedSegments := len(patternSegments)
	if match == RouteMatchPrefix {
		if !strings.HasSuffix(pattern, "/") {
			return false, nil
		}
		comparedSegments--
		if len(pathSegments) < len(patternSegments) {
			return false, nil
		}
	} else if len(pathSegments) != len(patternSegments) {
		return false, nil
	}
	for index := 0; index < comparedSegments; index++ {
		if pathSegments[index] != patternSegments[index] {
			return false, nil
		}
	}
	return true, nil
}

func decodePathSegments(path string, decodeSegment pathSegmentDecoder) ([]string, bool, error) {
	if !strings.HasPrefix(path, "/") {
		return nil, false, nil
	}
	rawSegments := strings.Split(strings.TrimPrefix(path, "/"), "/")
	segments := make([]string, len(rawSegments))
	for index, segment := range rawSegments {
		decoded, err := decodeSegment(segment)
		if err != nil {
			return nil, false, err
		}
		segments[index] = decoded
	}
	return segments, true, nil
}

func methodMatches(routeMethod, requestMethod string) bool {
	return routeMethod == requestMethod || routeMethod == MethodGet && requestMethod == MethodHead
}

func supportsNamespaceMethod(method string) bool {
	return method == MethodPost || methodMatches(MethodGet, method)
}

func splitNamespace(path string, decodeSegment pathSegmentDecoder) (APIType, string, bool, error) {
	if !strings.HasPrefix(path, "/") {
		return "", "", false, nil
	}
	segment, remainder, hasContractPath := strings.Cut(strings.TrimPrefix(path, "/"), "/")
	if !hasContractPath {
		return "", "", false, nil
	}
	decodedSegment, err := decodeSegment(segment)
	if err != nil {
		return "", "", false, err
	}
	for _, definition := range definitions {
		if strings.Trim(definition.NamespacePattern, "/") == decodedSegment {
			return definition.APIType, "/" + remainder, true, nil
		}
	}
	return "", "", false, nil
}

func resolveCustomNamespace(path string, decodeSegment pathSegmentDecoder) (string, bool, error) {
	if !strings.HasPrefix(path, "/") {
		return "", false, nil
	}
	namespaceSegment, remainder, hasTool := strings.Cut(strings.TrimPrefix(path, "/"), "/")
	if !hasTool {
		return "", false, nil
	}
	decodedNamespace, err := decodeSegment(namespaceSegment)
	if err != nil {
		return "", false, err
	}
	if decodedNamespace != strings.Trim(RouteCustomPrefix, "/") {
		return "", false, nil
	}
	toolSegment, _, _ := strings.Cut(remainder, "/")
	toolID, err := decodeSegment(toolSegment)
	if err != nil {
		return "", false, err
	}
	apiType := CustomAPITypePrefix + toolID
	if _, ok := ParseCustomAPIType(apiType); !ok {
		return "", false, nil
	}
	return apiType, true, nil
}

func stripNamespace(path, expectedNamespace string, decodeSegment pathSegmentDecoder) (string, bool, error) {
	if !strings.HasPrefix(path, "/") {
		return path, false, nil
	}
	segment, remainder, hasContractPath := strings.Cut(strings.TrimPrefix(path, "/"), "/")
	if !hasContractPath {
		return path, false, nil
	}
	decodedSegment, err := decodeSegment(segment)
	if err != nil {
		return "", false, err
	}
	if decodedSegment != expectedNamespace {
		return path, false, nil
	}
	return "/" + remainder, true, nil
}

func stripCustomNamespace(path, expectedToolID string, decodeSegment pathSegmentDecoder) (string, bool, error) {
	if !strings.HasPrefix(path, "/") {
		return path, false, nil
	}
	namespaceSegment, remainder, hasTool := strings.Cut(strings.TrimPrefix(path, "/"), "/")
	if !hasTool {
		return path, false, nil
	}
	toolSegment, contractPath, hasContractPath := strings.Cut(remainder, "/")
	decodedNamespace, err := decodeSegment(namespaceSegment)
	if err != nil {
		return "", false, err
	}
	decodedToolID, err := decodeSegment(toolSegment)
	if err != nil {
		return "", false, err
	}
	if decodedNamespace != strings.Trim(RouteCustomPrefix, "/") || decodedToolID != expectedToolID {
		return path, false, nil
	}
	if !hasContractPath {
		return "/", true, nil
	}
	return "/" + contractPath, true, nil
}

func trimOptionalV1Segment(path string, decodeSegment pathSegmentDecoder) (string, error) {
	if !strings.HasPrefix(path, "/") {
		return path, nil
	}
	segment, remainder, hasRemainder := strings.Cut(strings.TrimPrefix(path, "/"), "/")
	decodedSegment, err := decodeSegment(segment)
	if err != nil {
		return "", err
	}
	if decodedSegment != "v1" {
		return path, nil
	}
	if !hasRemainder {
		return "/", nil
	}
	return "/" + remainder, nil
}
