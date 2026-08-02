package requestcapture

import (
	"net/http"
	"net/url"

	"github.com/doraemonkeys/switch-a/internal/requestcapture/redaction"
)

const (
	redactedValue       = redaction.RedactedValue
	invalidURLRedaction = redaction.InvalidURLRedaction
)

type textSanitization struct {
	value     string
	truncated bool
}

type headerSanitization struct {
	value      map[string][]string
	discovered bool
	redactAll  bool
	truncated  bool
}

type requestSanitization struct {
	snapshot       RequestSnapshot
	sensitiveNames []string
	redactAll      bool
	truncated      bool
}

type httpResponseSanitization struct {
	snapshot       HTTPResponseSnapshot
	sensitiveNames []string
	redactAll      bool
	truncated      bool
}

type sanitizer struct {
	inner redaction.Sanitizer
}

type requestTarget = redaction.Target

func (s sanitizer) headers(source http.Header, extraSensitive []string) map[string][]string {
	return s.inner.Headers(source, extraSensitive)
}

func (s sanitizer) headersDetailed(
	source http.Header,
	extraSensitive, credentialValues []string,
	redactAll bool,
) headerSanitization {
	result := s.inner.HeadersDetailed(source, extraSensitive, credentialValues, redactAll)
	return headerSanitization{
		value:      result.Value,
		discovered: result.Discovered,
		redactAll:  result.RedactAll,
		truncated:  result.Truncated,
	}
}

func (s sanitizer) url(raw string, secrets []string) string {
	return s.inner.URL(raw, secrets)
}

func (s sanitizer) request(raw RawRequest, targets ...redaction.Target) RequestSnapshot {
	return s.inner.Request(requestMetadata(raw), targets...)
}

func (s sanitizer) requestDetailed(raw RawRequest, target redaction.Target) requestSanitization {
	result := s.inner.RequestDetailed(requestMetadata(raw), target)
	return requestSanitization{
		snapshot:       result.Snapshot,
		sensitiveNames: result.SensitiveNames,
		redactAll:      result.RedactAll,
		truncated:      result.Truncated,
	}
}

func (s sanitizer) provider(
	attempt AttemptMetadata,
	raw RawRequest,
	targets ...redaction.Target,
) ProviderSnapshot {
	return s.inner.Provider(attempt, requestMetadata(raw), targets...)
}

func (s sanitizer) httpResponse(raw HTTPResponseHead) HTTPResponseSnapshot {
	return s.inner.HTTPResponse(responseMetadata(raw))
}

func (s sanitizer) httpResponseDetailed(
	raw HTTPResponseHead,
	inheritedNames []string,
	inheritedRedactAll bool,
) httpResponseSanitization {
	result := s.inner.HTTPResponseDetailed(
		responseMetadata(raw),
		inheritedNames,
		inheritedRedactAll,
	)
	return httpResponseSanitization{
		snapshot:       result.Snapshot,
		sensitiveNames: result.SensitiveNames,
		redactAll:      result.RedactAll,
		truncated:      result.Truncated,
	}
}

func (s sanitizer) webSocketHandshake(raw WebSocketHandshake) WebSocketHandshakeSnapshot {
	return s.inner.WebSocketHandshake(handshakeMetadata(raw))
}

func (s sanitizer) failureDetailed(
	input FailureObservation,
	evidence CredentialEvidence,
	redactAll bool,
) (FailureObservation, bool) {
	return s.inner.FailureDetailed(input, evidence, redactAll)
}

func borrowedHTTPTarget(raw *url.URL) redaction.Target {
	return redaction.BorrowedHTTPTarget(raw)
}

func borrowedWebSocketTarget(raw string) redaction.Target {
	return redaction.BorrowedWebSocketTarget(raw)
}

func sanitizedText(value string, secrets []string, limit int, kind string) textSanitization {
	result := redaction.SanitizedText(value, secrets, limit, kind)
	return textSanitization{value: result.Value, truncated: result.Truncated}
}

func scrubText(value string, secrets []string) string {
	return redaction.ScrubText(value, secrets)
}

func replaceCredentialValues(value string, secrets []string) string {
	return redaction.ReplaceCredentialValues(value, secrets)
}

func boundedRedaction(kind, value string) string {
	return redaction.BoundedRedaction(kind, value)
}

func boundedAttemptMetadata(attempt AttemptMetadata) (AttemptMetadata, bool) {
	return redaction.BoundedAttemptMetadata(attempt)
}
