// Package upstreamheaders owns the request-header boundary between an untrusted
// client request and one physical upstream attempt.
package upstreamheaders

import (
	"net/http"
	"sort"
	"strings"
)

const webSocketHandshakePrefix = "sec-websocket-"

var providerOwnedHeaders = map[string]struct{}{
	"authorization":      {},
	"chatgpt-account-id": {},
	"x-api-key":          {},
}

var hopByHopHeaders = map[string]struct{}{
	"connection":          {},
	"keep-alive":          {},
	"proxy-authenticate":  {},
	"proxy-authorization": {},
	"proxy-connection":    {},
	"te":                  {},
	"trailer":             {},
	"transfer-encoding":   {},
	"upgrade":             {},
}

// ForHTTPAttempt returns an independent header snapshot suitable for one HTTP
// attempt. Provider-owned fields are absent so credential injection has exactly
// one authority: the provider selected for that attempt.
func ForHTTPAttempt(source http.Header) http.Header {
	return forAttempt(source, false, true)
}

// ForHTTPTransportAttempt preserves legacy provider identity fields while
// still removing hop-by-hop fields that belong to the gateway connection.
// Feature rollout must never transfer Connection semantics across exchanges.
func ForHTTPTransportAttempt(source http.Header) http.Header {
	return forAttempt(source, false, false)
}

// ForWebSocketAttempt applies the same ownership boundary while also excluding
// fields that the WebSocket implementation must negotiate through its dedicated
// handshake API.
func ForWebSocketAttempt(source http.Header) http.Header {
	return forAttempt(source, true, true)
}

// ForWebSocketTransportAttempt excludes only fields owned by the WebSocket
// transport. It preserves provider identity fields for the disabled rollout so
// the feature cannot silently change legacy or non-Codex authentication.
func ForWebSocketTransportAttempt(source http.Header) http.Header {
	return forAttempt(source, true, false)
}

func forAttempt(source http.Header, webSocket, sanitizeProviderIdentity bool) http.Header {
	nominated := connectionNominations(source)
	clean := make(http.Header, len(source))
	keys := make([]string, 0, len(source))
	for name := range source {
		keys = append(keys, name)
	}
	sort.Strings(keys)

	for _, name := range keys {
		normalized := strings.ToLower(name)
		if shouldExclude(normalized, nominated, webSocket, sanitizeProviderIdentity) {
			continue
		}

		canonical := http.CanonicalHeaderKey(name)
		values := source[name]
		if len(values) == 0 {
			if _, exists := clean[canonical]; !exists {
				clean[canonical] = nil
			}
			continue
		}
		clean[canonical] = append(clean[canonical], values...)
	}
	return clean
}

func shouldExclude(name string, nominated map[string]struct{}, webSocket, sanitizeProviderIdentity bool) bool {
	if _, excluded := providerOwnedHeaders[name]; sanitizeProviderIdentity && excluded {
		return true
	}
	if _, excluded := hopByHopHeaders[name]; excluded {
		return true
	}
	if _, excluded := nominated[name]; excluded {
		return true
	}
	return webSocket && strings.HasPrefix(name, webSocketHandshakePrefix)
}

func connectionNominations(source http.Header) map[string]struct{} {
	nominated := make(map[string]struct{})
	for name, values := range source {
		if !strings.EqualFold(name, "Connection") {
			continue
		}
		for _, value := range values {
			for token := range strings.SplitSeq(value, ",") {
				if token = strings.TrimSpace(token); token != "" {
					nominated[strings.ToLower(token)] = struct{}{}
				}
			}
		}
	}
	return nominated
}
