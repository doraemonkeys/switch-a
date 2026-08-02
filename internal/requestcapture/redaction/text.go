package redaction

import (
	"net/http"
	"sort"
	"strings"

	"github.com/doraemonkeys/switch-a/internal/requestcapture/capturevalue"
)

func SanitizedText(value string, secrets []string, limit int, kind string) TextSanitization {
	if value == "" {
		return TextSanitization{}
	}
	if len(value) > limit {
		return TextSanitization{Value: BoundedRedaction(kind, value), Truncated: true}
	}
	return sanitizedTextWithReplacer(value, compileCredentialReplacer(secrets), limit, kind)
}

func SanitizedTextWithEvidence(
	value string,
	evidence CredentialEvidence,
	limit int,
	kind string,
) TextSanitization {
	if !evidence.Sealed() || evidence.Overflowed() {
		return TextSanitization{Value: RedactedValue, Truncated: true}
	}
	return SanitizedText(value, evidence.valuesView(), limit, kind)
}

func sanitizedTextWithReplacer(
	value string,
	replacer credentialReplacer,
	limit int,
	kind string,
) TextSanitization {
	if value == "" {
		return TextSanitization{}
	}
	if len(value) > limit {
		return TextSanitization{Value: BoundedRedaction(kind, value), Truncated: true}
	}
	if !replacer.bounded {
		return TextSanitization{Value: RedactedValue, Truncated: true}
	}
	result := scrubTextWithReplacer(value, replacer)
	if len(result) > limit {
		return TextSanitization{Value: truncateSanitized(result, limit), Truncated: true}
	}
	return TextSanitization{Value: strings.Clone(result)}
}

func boundedPlainText(value string, limit int, kind string) TextSanitization {
	if len(value) <= limit {
		return TextSanitization{Value: strings.Clone(value)}
	}
	return TextSanitization{Value: BoundedRedaction(kind, value), Truncated: true}
}

func BoundedRedaction(kind, _ string) string {
	// Oversized attacker-controlled values are never hashed: hashing would make
	// capture work proportional to data that cannot be retained.
	return "[TRUNCATED_" + kind + "]"
}

func truncateSanitized(value string, limit int) string {
	const marker = "...[TRUNCATED]"
	if limit <= len(marker) {
		return marker[:limit]
	}
	return value[:limit-len(marker)] + marker
}

func normalizeSensitiveHeaderNames(source []string) sensitiveNameSanitization {
	result := sensitiveNameSanitization{
		names: make([]string, 0, min(len(source), MaxRetainedSensitiveHeaderNames)),
	}
	if len(source) > MaxRetainedSensitiveHeaderNames {
		result.redactAll = true
		result.truncated = true
	}
	for index, value := range source {
		if index == MaxRetainedSensitiveHeaderNames {
			break
		}
		if len(value) > MaxRetainedHeaderNameBytes {
			result.redactAll = true
			result.truncated = true
			continue
		}
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		value = strings.Clone(strings.ToLower(value))
		duplicate := false
		for _, existing := range result.names {
			if existing == value {
				duplicate = true
				break
			}
		}
		if !duplicate {
			result.names = append(result.names, value)
		}
	}
	return result
}

func mergeSensitiveHeaderNames(existing, additions []string, redactAll bool) sensitiveNameSanitization {
	normalized := normalizeSensitiveHeaderNames(additions)
	result := normalizeSensitiveHeaderNames(existing)
	result.redactAll = result.redactAll || normalized.redactAll || redactAll
	result.truncated = result.truncated || normalized.truncated
	if result.redactAll {
		return result
	}
	for _, name := range normalized.names {
		duplicate := false
		for _, existing := range result.names {
			if existing == name {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		if len(result.names) == MaxRetainedSensitiveHeaderNames {
			result.redactAll = true
			result.truncated = true
			return result
		}
		result.names = append(result.names, name)
	}
	return result
}

func boundedTrailerKeys(source http.Header) ([]string, bool) {
	if len(source) > MaxRetainedHeaderFields {
		return nil, true
	}
	result := make([]string, 0, len(source))
	truncated := false
	for value := range source {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if len(value) > MaxRetainedHeaderNameBytes {
			truncated = true
			continue
		}
		result = append(result, strings.Clone(value))
	}
	sort.Strings(result)
	return result, truncated
}

func normalizedCredentialKey(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	for strings.HasSuffix(value, "[]") {
		value = strings.TrimSuffix(value, "[]")
	}
	return strings.ReplaceAll(value, "-", "_")
}

func scrubText(value string, secrets []string) string {
	if value == "" || len(secrets) == 0 {
		return scrubTextWithReplacer(value, credentialReplacer{bounded: true})
	}
	return scrubTextWithReplacer(value, compileCredentialReplacer(secrets))
}

// ScrubText exposes the complete structured-text redaction path to callers that
// already hold explicit borrowed credential evidence.
func ScrubText(value string, secrets []string) string {
	return scrubText(value, secrets)
}

func scrubTextWithReplacer(value string, replacer credentialReplacer) string {
	if !replacer.bounded {
		return RedactedValue
	}
	result := replacer.replace(value)
	var ok bool
	result, ok = redactStructuredCredentialValues(result)
	if !ok {
		return RedactedValue
	}
	result = authValuePattern.ReplaceAllString(result, "$1 "+RedactedValue)
	return keyValuePattern.ReplaceAllString(result, "$1"+RedactedValue)
}

func failureFactPresent(fact capturevalue.FailureFact) bool {
	return fact.Site != "" || fact.Peer != "" || fact.Class != "" || fact.Code != "" ||
		fact.HTTPStatusCode != 0 || fact.WebSocketCloseCode != 0 ||
		fact.SystemErrorCode != 0 || fact.ProviderErrorType != "" ||
		fact.ProviderErrorCode != "" || fact.Message != ""
}

func (s Sanitizer) FailureDetailed(
	input capturevalue.FailureObservation,
	evidence CredentialEvidence,
	redactAll bool,
) (capturevalue.FailureObservation, bool) {
	if !failureFactPresent(input.Primary) && !input.HasSecondary && !input.Truncated {
		return capturevalue.FailureObservation{}, false
	}
	result := capturevalue.FailureObservation{
		HasSecondary: input.HasSecondary,
		Truncated:    input.Truncated,
	}
	result.Primary, result.Truncated = s.failureFactDetailed(
		input.Primary, &evidence, redactAll, result.Truncated,
	)
	if input.HasSecondary {
		result.Secondary, result.Truncated = s.failureFactDetailed(
			input.Secondary, &evidence, redactAll, result.Truncated,
		)
	}
	return result, true
}

func (s Sanitizer) failureFactDetailed(
	input capturevalue.FailureFact,
	evidence *CredentialEvidence,
	redactAll, truncated bool,
) (capturevalue.FailureFact, bool) {
	result := input
	var known bool
	result.Site, known = capturevalue.CanonicalFailureSite(input.Site)
	truncated = truncated || !known
	result.Peer, known = capturevalue.CanonicalFailurePeer(input.Peer)
	truncated = truncated || !known
	result.Class, known = capturevalue.CanonicalFailureClass(input.Class)
	truncated = truncated || !known
	result.Code, known = capturevalue.CanonicalFailureCode(input.Code)
	truncated = truncated || !known
	if result.HTTPStatusCode < 0 || result.HTTPStatusCode > 999 {
		result.HTTPStatusCode = 0
		truncated = true
	}
	if result.WebSocketCloseCode < 0 || result.WebSocketCloseCode > 4999 {
		result.WebSocketCloseCode = 0
		truncated = true
	}
	hasDiagnosticText := input.ProviderErrorType != "" ||
		input.ProviderErrorCode != "" || input.Message != ""
	if !hasDiagnosticText {
		return result, truncated
	}
	if redactAll || evidence == nil || !evidence.Sealed() || evidence.Overflowed() {
		result.ProviderErrorType = ""
		result.ProviderErrorCode = ""
		result.Message = ""
		return result, true
	}

	secrets := evidence.valuesView()
	if result.Code != capturevalue.FailureCodeProviderSemantic {
		if input.ProviderErrorType != "" || input.ProviderErrorCode != "" {
			truncated = true
		}
		result.ProviderErrorType = ""
		result.ProviderErrorCode = ""
	} else {
		providerType := SanitizedText(
			input.ProviderErrorType,
			secrets,
			MaxRetainedProviderErrorFieldBytes,
			"PROVIDER_ERROR_TYPE",
		)
		providerCode := SanitizedText(
			input.ProviderErrorCode,
			secrets,
			MaxRetainedProviderErrorFieldBytes,
			"PROVIDER_ERROR_CODE",
		)
		result.ProviderErrorType = providerType.Value
		result.ProviderErrorCode = providerCode.Value
		truncated = truncated || providerType.Truncated || providerCode.Truncated ||
			providerType.Value != input.ProviderErrorType ||
			providerCode.Value != input.ProviderErrorCode
	}
	message := SanitizedText(input.Message, secrets, MaxRetainedErrorBytes, "FAILURE_MESSAGE")
	result.Message = message.Value
	return result, truncated || message.Truncated || message.Value != input.Message
}
