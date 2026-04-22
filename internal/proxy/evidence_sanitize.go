package proxy

import (
	"regexp"
	"strings"
)

// Evidence-layer redaction is protocol-neutral on purpose: SSE and WebSocket
// snippets (raw error text, close reason, upstream payload, gateway message)
// all travel through the same secret-exposure pipeline into
// `session_evidence_json` / `attempt_evidence_json`. Hosting the regexes and
// the sanitize helper on the SSE side or the WS side would invite drift; a
// shared file keeps the token classes in exactly one place.

// evidenceSnippetLimitBytes bounds every sanitized snippet written to evidence
// JSON. The value is chosen so that even a redaction that expands input (e.g.
// a short bearer prefix replaced with `[REDACTED]`) still fits comfortably
// inside the 4 KiB overall evidence budget enforced by the WS builder.
const evidenceSnippetLimitBytes = 512

// evidenceRedactedPlaceholder is the fixed replacement emitted for any matched
// secret-bearing token. Downstream tests key off this exact string, so it is a
// wire contract — renames are schema-visible to log consumers.
const evidenceRedactedPlaceholder = "[REDACTED]"

// Secret-class regexes. Defined once, at package scope, so the regex engine
// only compiles them once and every sanitize call is allocation-free.
//
//   - evidenceSecretHeaderPattern: header-shaped key/value pairs whose name is
//     in the allowlist of credential-bearing headers. `[^\s,;]+` is greedy
//     enough to consume token-style values and the `(?i)` flag makes the
//     header name match case-insensitively (real traffic uses mixed case).
//   - evidenceBearerTokenPattern / evidenceBasicTokenPattern: the two RFC 7617
//     / 6750 auth scheme prefixes. Covered separately from the header pattern
//     because error text often quotes the credential inline (e.g.
//     `dial: bearer sk-abc`) without the full header prefix.
var (
	evidenceSecretHeaderPattern = regexp.MustCompile(`(?i)\b(authorization|proxy-authorization|x-api-key|api[_-]?key|cookie|set-cookie)\b\s*[:=]\s*[^\s,;]+`)
	evidenceBearerTokenPattern  = regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]+\b`)
	evidenceBasicTokenPattern   = regexp.MustCompile(`(?i)\bbasic\s+[A-Za-z0-9._~+/=-]+\b`)
)

// sanitizeEvidenceSnippet trims whitespace, redacts credential-bearing tokens,
// and enforces the evidence-layer byte cap. The three regexes run in sequence
// rather than a single alternation so the replacement text can be tailored to
// each token class (headers keep their name; schemes keep their prefix) —
// this keeps post-redaction snippets diagnostically useful.
func sanitizeEvidenceSnippet(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	sanitized := evidenceSecretHeaderPattern.ReplaceAllString(trimmed, `$1: `+evidenceRedactedPlaceholder)
	sanitized = evidenceBearerTokenPattern.ReplaceAllString(sanitized, "Bearer "+evidenceRedactedPlaceholder)
	sanitized = evidenceBasicTokenPattern.ReplaceAllString(sanitized, "Basic "+evidenceRedactedPlaceholder)
	return truncateUTF8(sanitized, evidenceSnippetLimitBytes)
}
