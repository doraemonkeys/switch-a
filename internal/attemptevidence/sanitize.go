package attemptevidence

import (
	"strings"
	"unicode/utf8"
)

const (
	SnippetLimitBytes   = 512
	RedactedPlaceholder = "[REDACTED]"
)

// SanitizeSnippet bounds an evidence snippet and, when supplied, removes only
// the exact API key that switch-a injected for the current provider attempt.
// Evidence is intentionally transparent by default: provider-owned tokens,
// cookies, URLs, and diagnostics are useful debugging facts and are not
// classified by appearance alone.
func SanitizeSnippet(value, injectedAPIKey string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	sanitized := trimmed
	key := strings.TrimSpace(injectedAPIKey)
	if key != "" {
		sanitized = strings.ReplaceAll(sanitized, key, RedactedPlaceholder)
	}
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
