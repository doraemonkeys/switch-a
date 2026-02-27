package proxy

import (
	"net/http"
	"strings"
)

// AuthMode constants.
const (
	AuthModeAuto   = "auto"
	AuthModeBearer = "bearer"
	AuthModeXAPI   = "x-api-key"
)

// hopByHopHeaders are HTTP headers that should not be forwarded.
// Note: Host is not included here because it's handled separately in
// BuildUpstreamRequest (transport.go) where req.Host is set to the upstream host.
var hopByHopHeaders = map[string]bool{
	"Connection":          true,
	"Keep-Alive":          true,
	"Proxy-Authenticate":  true,
	"Proxy-Authorization": true,
	"Te":                  true,
	"Trailer":             true,
	"Transfer-Encoding":   true,
	"Upgrade":             true,
}

// CopyHeaders copies headers from src to dst, filtering out hop-by-hop and auth headers.
func CopyHeaders(dst, src http.Header) {
	for key, values := range src {
		// Skip hop-by-hop headers
		if hopByHopHeaders[key] {
			continue
		}

		// Skip auth headers (they will be set by SetAuthHeader)
		if isAuthHeader(key) {
			continue
		}

		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

// isAuthHeader checks if the header is an authentication header.
func isAuthHeader(key string) bool {
	// Case-insensitive check
	lower := strings.ToLower(key)
	return lower == "authorization" || lower == "x-api-key"
}

// isWebSocketHandshakeHeader returns true for Sec-WebSocket-* headers.
// These are client–proxy negotiation headers that the WebSocket library regenerates
// when dialing upstream; forwarding them would conflict with the new handshake.
func isWebSocketHandshakeHeader(key string) bool {
	return len(key) > 14 && strings.EqualFold(key[:14], "Sec-Websocket-")
}

// DetectAuthMode determines the authentication mode from the request headers.
// Returns the detected mode based on which auth header is present.
func DetectAuthMode(r *http.Request) string {
	if r.Header.Get("Authorization") != "" {
		return AuthModeBearer
	}
	// http.Header.Get is case-insensitive, so only one check is needed
	if r.Header.Get("X-Api-Key") != "" {
		return AuthModeXAPI
	}
	// Default to bearer if no auth header present
	return AuthModeBearer
}

// SetAuthHeader sets the appropriate authentication header based on the mode.
// If providerAuthMode is empty, falls back to globalAuthMode.
// If globalAuthMode is "auto", detects from the original request.
func SetAuthHeader(dst http.Header, apiKey, providerAuthMode, globalAuthMode string, originalReq *http.Request) {
	mode := providerAuthMode
	if mode == "" {
		mode = globalAuthMode
	}
	if mode == AuthModeAuto {
		mode = DetectAuthMode(originalReq)
	}

	switch mode {
	case AuthModeXAPI:
		dst.Set("x-api-key", apiKey)
	default: // AuthModeBearer or fallback
		dst.Set("Authorization", "Bearer "+apiKey)
	}
}
