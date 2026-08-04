package attemptevidence

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	SnippetLimitBytes   = 512
	RedactedPlaceholder = "[REDACTED]"
)

var (
	secretHeaderPattern = regexp.MustCompile(`(?i)\b(authorization|proxy-authorization|x-api-key|api[_-]?key|cookie|set-cookie)\b\s*[:=]\s*[^\r\n,;]+`)
	bearerTokenPattern  = regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]+\b`)
	basicTokenPattern   = regexp.MustCompile(`(?i)\bbasic\s+[A-Za-z0-9._~+/=-]+\b`)
)

// SanitizeSnippet centralizes the evidence redaction boundary so transport and
// semantic siblings cannot drift into different credential taxonomies.
func SanitizeSnippet(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	sanitized := secretHeaderPattern.ReplaceAllString(trimmed, `$1: `+RedactedPlaceholder)
	sanitized = bearerTokenPattern.ReplaceAllString(sanitized, "Bearer "+RedactedPlaceholder)
	sanitized = basicTokenPattern.ReplaceAllString(sanitized, "Basic "+RedactedPlaceholder)
	return truncateUTF8(sanitized, SnippetLimitBytes)
}

func truncateUTF8(value string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(value) <= maxBytes {
		return value
	}
	for maxBytes > 0 && !utf8.RuneStart(value[maxBytes]) {
		maxBytes--
	}
	return value[:maxBytes]
}
