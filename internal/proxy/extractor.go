package proxy

import (
	"math"
	"net"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/doraemonkeys/switch-a/internal/model"
)

// ModelUnknown is returned when the model cannot be extracted from the request.
const ModelUnknown = "unknown"

// MaxUserAgentLength is the maximum length of User-Agent to store.
// Longer values are truncated to prevent database bloat.
const MaxUserAgentLength = 512

// MaxReqBodySnippetLength is the maximum length of request body snippet to store.
const MaxReqBodySnippetLength = 512

const (
	bytesPerMiB           int64 = 1 << 20
	maxBoundedBodyBytes         = math.MaxInt64 - 1
	contentEncodingHeader       = "Content-Encoding"
)

// RequestInfo contains information extracted from a proxy request.
type RequestInfo struct {
	ClientIP    string
	UserID      string
	Model       string
	APIType     string
	Path        string // Request path (relative, e.g., /v1/messages)
	Method      string // HTTP method (GET/POST/PUT/DELETE)
	UserAgent   string // Client User-Agent (truncated to MaxUserAgentLength)
	RequestID   string // Client's X-Request-ID header for tracing
	ContentType string // Request Content-Type header
	Reasoning   model.RequestedReasoningObservation
}

// ExtractClientIP extracts the client IP address from the request.
// It checks X-Forwarded-For and X-Real-IP headers if trustProxyHeaders is true,
// otherwise uses the remote address directly.
func ExtractClientIP(r *http.Request, trustProxyHeaders bool) string {
	if trustProxyHeaders {
		if ip := extractFromProxyHeaders(r); ip != "" {
			return ip
		}
	}
	return extractFromRemoteAddr(r.RemoteAddr)
}

// extractFromProxyHeaders extracts client IP from proxy headers.
func extractFromProxyHeaders(r *http.Request) string {
	// Check X-Forwarded-For first (may contain multiple IPs)
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		ips := strings.Split(xff, ",")
		if ip := strings.TrimSpace(ips[0]); ip != "" {
			return ip
		}
	}
	// Check X-Real-IP
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}
	return ""
}

// extractFromRemoteAddr extracts client IP from RemoteAddr.
func extractFromRemoteAddr(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		// RemoteAddr might not have port (unlikely but handle it)
		return remoteAddr
	}
	return host
}

// ExtractUserID extracts the user ID from the specified header.
func ExtractUserID(r *http.Request, userHeader string) string {
	return r.Header.Get(userHeader)
}

// ExtractUserAgent extracts and truncates the User-Agent header.
func ExtractUserAgent(r *http.Request) string {
	ua := r.Header.Get("User-Agent")
	if len(ua) > MaxUserAgentLength {
		return ua[:MaxUserAgentLength]
	}
	return ua
}

// ExtractRequestIDHeader extracts the X-Request-ID header from the request.
// This is the client-provided request ID for distributed tracing.
func ExtractRequestIDHeader(r *http.Request) string {
	return r.Header.Get("X-Request-ID")
}

// ExtractContentType extracts the Content-Type header from the request.
func ExtractContentType(r *http.Request) string {
	return r.Header.Get("Content-Type")
}

// GetReqBodySnippet returns a truncated snippet of the request body for debugging.
// Returns empty string if body is nil or empty.
//
// When truncation occurs, "..." is appended to indicate the body was truncated.
// Truncation respects UTF-8 boundaries to avoid splitting multi-byte characters.
//
// SECURITY NOTE: Request bodies may contain sensitive data (API keys, auth tokens, PII).
// This function is used only for error diagnostics and stored in request_attempts table.
// Administrators should be aware that error logs may expose partial request content.
func GetReqBodySnippet(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	if len(body) <= MaxReqBodySnippetLength {
		return string(body)
	}

	// Truncate at MaxReqBodySnippetLength, but ensure we don't split a UTF-8 character.
	// Walk backwards from the cut point to find a valid UTF-8 boundary.
	snippet := body[:MaxReqBodySnippetLength]
	for len(snippet) > 0 && !utf8.Valid(snippet) {
		snippet = snippet[:len(snippet)-1]
	}

	return string(snippet) + "..."
}

// requestHeadModel resolves model evidence available before body acquisition.
func requestHeadModel(r *http.Request, apiType string) string {
	if apiType == APITypeGemini {
		return extractGeminiModel(r.URL.Path)
	}
	return ModelUnknown
}

// extractGeminiModel extracts the model name from a Gemini API URL path.
// e.g., /gemini/v1beta/models/gemini-pro:generateContent → gemini-pro
func extractGeminiModel(path string) string {
	// Path format: /gemini/v1beta/models/{model}:{action}
	// or: /gemini/v1/{model}:{action}
	parts := strings.Split(path, "/models/")
	if len(parts) < 2 {
		return ModelUnknown
	}

	modelPart := parts[1]
	// Remove action suffix (e.g., :generateContent)
	if idx := strings.Index(modelPart, ":"); idx > 0 {
		modelPart = modelPart[:idx]
	}
	// Remove query string if present
	if idx := strings.Index(modelPart, "?"); idx > 0 {
		modelPart = modelPart[:idx]
	}

	if modelPart == "" {
		return ModelUnknown
	}
	return modelPart
}

func requestBodyLimitBytes(maxBodySizeMB int64) int64 {
	if maxBodySizeMB <= 0 {
		return 0
	}
	if maxBodySizeMB > maxBoundedBodyBytes/bytesPerMiB {
		return maxBoundedBodyBytes
	}
	return maxBodySizeMB * bytesPerMiB
}
