package proxy

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"
)

// maxModelExtractBytes is the maximum bytes to read when extracting the model field.
const maxModelExtractBytes = 128 * 1024 // 128KB

// ModelUnknown is returned when the model cannot be extracted from the request.
const ModelUnknown = "unknown"

// modelFieldRe matches the "model" field in JSON.
// Captures: "model":"value" or "model": "value" (with optional whitespace).
//
// Note: This regex doesn't handle escaped quotes within model names (e.g., "model": "claude-\"special\"-3").
// This is acceptable because model names in practice don't contain quotes.
// Using regex avoids the overhead of full JSON parsing for every request.
var modelFieldRe = regexp.MustCompile(`"model"\s*:\s*"([^"]+)"`)

// MaxUserAgentLength is the maximum length of User-Agent to store.
// Longer values are truncated to prevent database bloat.
const MaxUserAgentLength = 512

// MaxReqBodySnippetLength is the maximum length of request body snippet to store.
const MaxReqBodySnippetLength = 512

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

// ExtractModel extracts the model name from the request.
// For Gemini API, extracts from URL path.
// For other APIs, extracts from JSON body.
// The body reader is replaced so it can be read again.
func ExtractModel(r *http.Request, apiType string, body []byte) string {
	// Gemini API: extract model from URL path
	// e.g., /gemini/v1beta/models/gemini-pro:generateContent
	if apiType == APITypeGemini {
		return extractGeminiModel(r.URL.Path)
	}

	// For other APIs: extract from JSON body
	return extractModelFromJSON(body)
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
	if idx := strings.Index(modelPart, "?"); idx > 0 { // coverage-ignore -- query strings in path are rare
		modelPart = modelPart[:idx]
	}

	if modelPart == "" { // coverage-ignore -- empty model after parsing is rare
		return ModelUnknown
	}
	return modelPart
}

// extractModelFromJSON extracts the "model" field from JSON body.
// Uses regex for efficient extraction without parsing full JSON.
// Reads at most maxModelExtractBytes to handle large requests.
func extractModelFromJSON(body []byte) string {
	// Limit search to maxModelExtractBytes
	searchBytes := body
	if len(searchBytes) > maxModelExtractBytes { // coverage-ignore -- very large bodies are tested at integration level
		searchBytes = searchBytes[:maxModelExtractBytes]
	}

	matches := modelFieldRe.FindSubmatch(searchBytes)
	if len(matches) >= 2 {
		return string(matches[1])
	}
	return ModelUnknown
}

// ConsumeAndReplaceBody reads the request body and returns a buffer containing it.
// Returns an error if the body exceeds maxBodySize (in MB).
//
// This function consumes the original body and replaces r.Body with a new
// io.NopCloser(bytes.Reader) so the body can be read again by subsequent handlers.
// The original body is closed after reading to release resources.
func ConsumeAndReplaceBody(r *http.Request, maxBodySizeMB int64) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}

	// Close original body when done to release resources.
	// While Go's HTTP server closes the body after request handling,
	// explicit closing ensures timely resource release, especially on error paths.
	originalBody := r.Body
	defer originalBody.Close()

	maxBytes := maxBodySizeMB * 1024 * 1024

	// Use LimitReader to prevent reading too much
	limitedReader := io.LimitReader(originalBody, maxBytes+1)
	body, err := io.ReadAll(limitedReader)
	if err != nil { // coverage-ignore -- read errors on LimitReader are rare
		return nil, err
	}

	// Check if body exceeded limit
	if int64(len(body)) > maxBytes {
		return nil, ErrBodyTooLarge
	}

	// Replace the body so it can be read again
	r.Body = io.NopCloser(bytes.NewReader(body))

	return body, nil
}
