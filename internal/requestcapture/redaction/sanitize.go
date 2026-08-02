package redaction

import (
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/doraemonkeys/switch-a/internal/requestcapture/capturevalue"
)

var (
	defaultSensitiveHeaderNames = [...]string{
		"authorization",
		"proxy-authorization",
		"cookie",
		"set-cookie",
		"x-api-key",
		"api-key",
		"x-goog-api-key",
		"x-access-token",
		"x-amz-credential",
		"x-amz-security-token",
		"x-auth-token",
		"x-goog-credential",
	}
	defaultSensitiveQueryKeys = map[string]struct{}{
		"access_token":         {},
		"api_key":              {},
		"apikey":               {},
		"authorization":        {},
		"auth":                 {},
		"client_secret":        {},
		"credential":           {},
		"id_token":             {},
		"key":                  {},
		"password":             {},
		"refresh_token":        {},
		"secret":               {},
		"session_token":        {},
		"sig":                  {},
		"signature":            {},
		"token":                {},
		"x_amz_credential":     {},
		"x_amz_security_token": {},
		"x_amz_signature":      {},
		"x_goog_credential":    {},
		"x_goog_signature":     {},
	}
	authValuePattern = regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]+`)
	keyValuePattern  = regexp.MustCompile(
		`(?i)(["']?(?:api[_-]?key|access[_-]?token|refresh[_-]?token|id[_-]?token|session[_-]?token|client[_-]?(?:secret|assertion)|x[_-]amz[_-]?(?:signature|credential|security[_-]?token)|x[_-]goog[_-]?(?:signature|credential)|authorization|password|secret|token)["']?(?:\[\])?\s*[:=]\s*["']?)[^"',\s;&}\]]+`,
	)
)

type Sanitizer struct{}

type TextSanitization struct {
	Value     string
	Truncated bool
}

type HeaderSanitization struct {
	Value      map[string][]string
	Discovered bool
	RedactAll  bool
	Truncated  bool
}

type sensitiveNameSanitization struct {
	names     []string
	redactAll bool
	truncated bool
}

type RequestSanitization struct {
	Snapshot       capturevalue.RequestSnapshot
	SensitiveNames []string
	RedactAll      bool
	Truncated      bool
}

type HTTPResponseSanitization struct {
	Snapshot       capturevalue.HTTPResponseSnapshot
	SensitiveNames []string
	RedactAll      bool
	Truncated      bool
}

type WebSocketHandshakeSanitization struct {
	Snapshot       capturevalue.WebSocketHandshakeSnapshot
	SensitiveNames []string
	RedactAll      bool
	Truncated      bool
}

func (s Sanitizer) Headers(source http.Header, extraSensitive []string) map[string][]string {
	names := normalizeSensitiveHeaderNames(extraSensitive)
	return s.HeadersDetailed(source, names.names, nil, names.redactAll).Value
}

func (Sanitizer) HeadersDetailed(
	source http.Header,
	extraSensitive, credentialValues []string,
	redactAll bool,
) HeaderSanitization {
	credentials := discoverHeaderCredentials(
		source,
		extraSensitive,
		credentialValues,
		redactAll,
	)
	redactAll = credentials.redactAll
	if len(source) == 0 {
		return HeaderSanitization{
			Value: map[string][]string{}, Discovered: credentials.discovered, RedactAll: redactAll,
		}
	}
	if len(source) > MaxRetainedHeaderFields {
		// Choosing an arbitrary map subset could omit the credential-bearing field.
		return HeaderSanitization{Value: map[string][]string{}, RedactAll: true, Truncated: true}
	}
	secrets := credentials.slice()
	replacer := compileCredentialReplacer(secrets)
	if !replacer.bounded {
		return HeaderSanitization{Value: map[string][]string{}, RedactAll: true, Truncated: true}
	}
	keys := make([]string, 0, len(source))
	for name := range source {
		keys = append(keys, name)
	}
	sort.Strings(keys)

	result := make(map[string][]string, len(keys))
	retainedBytes := 0
	truncated := false
	for _, name := range keys {
		if len(name) == 0 || len(name) > MaxRetainedHeaderNameBytes {
			truncated = true
			continue
		}
		values := source[name]
		valueLimit := len(values)
		if valueLimit > MaxRetainedHeaderValuesPerField {
			valueLimit = MaxRetainedHeaderValuesPerField
			truncated = true
		}
		copied := make([]string, 0, valueLimit)
		sensitive := redactAll || isSensitiveHeaderName(name, extraSensitive)
		fieldBytes := len(name)
		for index := 0; index < valueLimit; index++ {
			value := values[index]
			if sensitive {
				value = RedactedValue
			} else {
				bounded := sanitizedTextWithReplacer(value, replacer, MaxRetainedHeaderValueBytes, "HEADER")
				value = bounded.Value
				truncated = truncated || bounded.Truncated
			}
			if retainedBytes+fieldBytes+len(value) > MaxRetainedHeaderBytes {
				truncated = true
				copied = nil
				break
			}
			copied = append(copied, value)
			fieldBytes += len(value)
		}
		if copied == nil {
			break
		}
		if retainedBytes+fieldBytes > MaxRetainedHeaderBytes {
			truncated = true
			break
		}
		result[strings.Clone(name)] = copied
		retainedBytes += fieldBytes
	}
	return HeaderSanitization{
		Value: result, Discovered: credentials.discovered, RedactAll: redactAll, Truncated: truncated,
	}
}

func (s Sanitizer) HeadersWithEvidence(
	source http.Header,
	extraSensitive []string,
	evidence CredentialEvidence,
	redactAll bool,
) HeaderSanitization {
	// An incomplete producer inventory cannot prove that an ordinary-looking
	// field does not echo a credential, so every retained value must fail closed.
	return s.HeadersDetailed(
		source,
		extraSensitive,
		evidence.valuesView(),
		redactAll || !evidence.Sealed() || evidence.Overflowed(),
	)
}

type Target struct {
	httpURL      *url.URL
	webSocketURL string
	invalid      bool
}

type TargetSanitization struct {
	Target TextSanitization
	Host   TextSanitization
}

func BorrowedHTTPTarget(raw *url.URL) Target {
	return Target{httpURL: raw}
}

func BorrowedWebSocketTarget(raw string) Target {
	return Target{webSocketURL: raw}
}

func InvalidTarget() Target {
	return Target{invalid: true}
}

func (target Target) present() bool {
	return target.httpURL != nil || target.webSocketURL != "" || target.invalid
}

func (target Target) Sanitize(s Sanitizer, secrets []string) TargetSanitization {
	if target.invalid {
		return TargetSanitization{
			Target: TextSanitization{Value: InvalidURLRedaction, Truncated: true},
			Host:   TextSanitization{Value: RedactedValue, Truncated: true},
		}
	}
	if target.httpURL != nil {
		return s.structuredURLDetailed(target.httpURL, secrets)
	}
	return s.parsedURLDetailed(target.webSocketURL, secrets)
}

func (s Sanitizer) TargetWithEvidence(target Target, evidence CredentialEvidence) TargetSanitization {
	result := target.Sanitize(s, evidence.valuesView())
	if !evidence.Sealed() || evidence.Overflowed() {
		// URL userinfo and known query keys are insufficient when the producer
		// could not certify every credential value; path, host, and safe-looking
		// query components may still echo an unknown secret.
		result.Target = TextSanitization{Value: RedactedValue, Truncated: true}
		result.Host = TextSanitization{Value: RedactedValue, Truncated: true}
	}
	return result
}

func (s Sanitizer) URL(raw string, secrets []string) string {
	return s.URLDetailed(raw, secrets).Value
}

func (s Sanitizer) URLDetailed(raw string, secrets []string) TextSanitization {
	return s.parsedURLDetailed(raw, secrets).Target
}

func (s Sanitizer) parsedURLDetailed(raw string, secrets []string) TargetSanitization {
	if raw == "" {
		return TargetSanitization{}
	}
	if len(raw) > MaxRetainedURLBytes {
		return TargetSanitization{
			Target: TextSanitization{Value: BoundedRedaction("URL", raw), Truncated: true},
		}
	}
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme == "" && (strings.Contains(raw, "://") || strings.Contains(raw, "@"))) {
		return TargetSanitization{
			Target: TextSanitization{Value: InvalidURLRedaction, Truncated: true},
		}
	}
	return s.sanitizeParsedURL(*parsed, secrets)
}

func (s Sanitizer) structuredURLDetailed(raw *url.URL, secrets []string) TargetSanitization {
	if raw == nil {
		return TargetSanitization{}
	}
	if !boundedURLShape(raw) {
		return TargetSanitization{
			Target: TextSanitization{Value: BoundedRedaction("URL", ""), Truncated: true},
			Host:   SanitizedText(raw.Host, secrets, MaxRetainedHostBytes, "HOST"),
		}
	}
	return s.sanitizeParsedURL(*raw, secrets)
}

func boundedURLShape(raw *url.URL) bool {
	remaining := MaxRetainedURLBytes
	for _, field := range [...]string{
		raw.Scheme,
		raw.Opaque,
		raw.Host,
		raw.Path,
		raw.RawPath,
		raw.RawQuery,
		raw.Fragment,
		raw.RawFragment,
	} {
		if len(field) > remaining {
			return false
		}
		remaining -= len(field)
	}
	return true
}

func (Sanitizer) sanitizeParsedURL(parsed url.URL, secrets []string) TargetSanitization {
	replacer := compileCredentialReplacer(secrets)
	if !replacer.bounded {
		host := TextSanitization{}
		if parsed.Host != "" {
			host = TextSanitization{Value: RedactedValue, Truncated: true}
		}
		return TargetSanitization{
			Target: TextSanitization{Value: RedactedValue, Truncated: true},
			Host:   host,
		}
	}
	parsed.User = nil
	host := sanitizedTextWithReplacer(parsed.Host, replacer, MaxRetainedHostBytes, "HOST")
	parsed.Host = host.Value
	parsed.Scheme = scrubTextWithReplacer(parsed.Scheme, replacer)
	parsed.Opaque = scrubTextWithReplacer(parsed.Opaque, replacer)
	parsed.Path = scrubTextWithReplacer(parsed.Path, replacer)
	parsed.RawPath = ""
	parsed.Fragment = scrubTextWithReplacer(parsed.Fragment, replacer)
	parsed.RawFragment = ""

	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return TargetSanitization{
			Target: TextSanitization{Value: InvalidURLRedaction, Truncated: true},
			Host:   host,
		}
	}
	for key, values := range query {
		if _, sensitive := defaultSensitiveQueryKeys[normalizedCredentialKey(key)]; sensitive {
			for index := range values {
				values[index] = RedactedValue
			}
			query[key] = values
			continue
		}
		for index, value := range values {
			values[index] = scrubTextWithReplacer(value, replacer)
		}
		query[key] = values
	}
	parsed.RawQuery = query.Encode()
	// Components are scrubbed before re-encoding because scrubbing the assembled
	// URL would reinterpret query delimiters and corrupt unrelated parameters.
	result := parsed.String()
	if len(result) > MaxRetainedURLBytes {
		return TargetSanitization{
			Target: TextSanitization{Value: BoundedRedaction("URL", ""), Truncated: true},
			Host:   host,
		}
	}
	return TargetSanitization{
		Target: TextSanitization{Value: strings.Clone(result)},
		Host:   host,
	}
}

func (s Sanitizer) Request(raw RequestMetadata, targets ...Target) capturevalue.RequestSnapshot {
	return s.RequestDetailed(raw, firstRequestTarget(targets)).Snapshot
}

func firstRequestTarget(targets []Target) Target {
	if len(targets) == 0 {
		return Target{}
	}
	return targets[0]
}

func (s Sanitizer) RequestDetailed(raw RequestMetadata, targetInput Target) RequestSanitization {
	headerEvidence := &raw.SensitiveHeaders
	names := normalizeSensitiveHeaderNames(headerEvidence.namesView())
	if !headerEvidence.Sealed() || headerEvidence.Overflowed() {
		names.redactAll = true
		names.truncated = true
	}
	credentialEvidence := &raw.CredentialEvidence
	headerCredentials, headerRedactAll, headerDiscovered := discoverRequestHeaderCredentials(
		raw.Headers,
		raw.Trailers,
		names.names,
		credentialEvidence.valuesView(),
		names.redactAll || !credentialEvidence.Sealed() || credentialEvidence.Overflowed(),
	)
	method := boundedPlainText(raw.Method, MaxRetainedMethodBytes, "METHOD")
	targetResult := targetInput.Sanitize(s, headerCredentials)
	if headerRedactAll && targetInput.present() {
		targetResult = TargetSanitization{
			Target: TextSanitization{Value: RedactedValue, Truncated: true},
			Host:   TextSanitization{Value: RedactedValue, Truncated: true},
		}
	}
	headers := s.HeadersDetailed(raw.Headers, names.names, headerCredentials, headerRedactAll)
	trailers := s.HeadersDetailed(raw.Trailers, names.names, headerCredentials, headerRedactAll)
	return RequestSanitization{
		Snapshot: capturevalue.RequestSnapshot{
			Method:        method.Value,
			URL:           targetResult.Target.Value,
			Host:          targetResult.Host.Value,
			Headers:       headers.Value,
			ContentLength: raw.ContentLength,
			Trailers:      trailers.Value,
		},
		SensitiveNames: names.names,
		// A value absent from sealed producer evidence cannot be scrubbed from
		// later opaque metadata, so discovery of such a value poisons future text.
		RedactAll: headerRedactAll || headerDiscovered,
		Truncated: method.Truncated || targetResult.Target.Truncated || targetResult.Host.Truncated ||
			headers.Truncated || trailers.Truncated || names.truncated,
	}
}

func (s Sanitizer) Provider(attempt capturevalue.AttemptMetadata, raw RequestMetadata, targets ...Target) capturevalue.ProviderSnapshot {
	evidence := &raw.CredentialEvidence
	target := firstRequestTarget(targets).Sanitize(s, evidence.valuesView()).Target
	if !evidence.Sealed() || evidence.Overflowed() {
		target = TextSanitization{Value: RedactedValue, Truncated: true}
	}
	provider, _ := SanitizedProvider(attempt, target.Value)
	return provider
}

func BoundedAttemptMetadata(attempt capturevalue.AttemptMetadata) (capturevalue.AttemptMetadata, bool) {
	apiType := boundedPlainText(attempt.APIType, MaxRetainedAPITypeBytes, "API_TYPE")
	selectionMode, modeKnown := capturevalue.CanonicalSelectionMode(attempt.SelectionMode)
	selectionSource, sourceKnown := capturevalue.CanonicalSelectionSource(attempt.SelectionSource)
	credentialPhase, phaseKnown := capturevalue.CanonicalCredentialPhase(attempt.CredentialPhase)
	truncated := apiType.Truncated || !modeKnown || !sourceKnown || !phaseKnown
	if !modeKnown {
		selectionMode = capturevalue.SelectionModeUnknown
	}
	if !sourceKnown {
		selectionSource = capturevalue.SelectionSourceUnknown
	}
	if !phaseKnown {
		credentialPhase = capturevalue.CredentialPhaseUnknown
	}
	attempt.APIType = apiType.Value
	attempt.SelectionMode = selectionMode
	attempt.SelectionSource = selectionSource
	attempt.CredentialPhase = credentialPhase
	return attempt, truncated
}

func SanitizedProvider(attempt capturevalue.AttemptMetadata, targetURL string) (capturevalue.ProviderSnapshot, bool) {
	id := boundedPlainText(attempt.Provider.ID, MaxRetainedProviderIDBytes, "PROVIDER_ID")
	name := boundedPlainText(attempt.Provider.Name, MaxRetainedProviderNameBytes, "PROVIDER_NAME")
	apiType := boundedPlainText(attempt.APIType, MaxRetainedAPITypeBytes, "API_TYPE")
	return capturevalue.ProviderSnapshot{
		ID:        id.Value,
		Name:      name.Value,
		APIType:   apiType.Value,
		TargetURL: targetURL,
	}, id.Truncated || name.Truncated || apiType.Truncated
}

func (s Sanitizer) HTTPResponse(raw HTTPResponseMetadata) capturevalue.HTTPResponseSnapshot {
	return s.HTTPResponseDetailed(raw, nil, false).Snapshot
}

func (s Sanitizer) HTTPResponseDetailed(raw HTTPResponseMetadata, inheritedNames []string, inheritedRedactAll bool) HTTPResponseSanitization {
	headerEvidence := &raw.SensitiveHeaders
	names := mergeSensitiveHeaderNames(
		inheritedNames,
		headerEvidence.namesView(),
		inheritedRedactAll || !headerEvidence.Sealed() || headerEvidence.Overflowed(),
	)
	credentialEvidence := &raw.CredentialEvidence
	headers := s.HeadersDetailed(
		raw.Headers,
		names.names,
		credentialEvidence.valuesView(),
		names.redactAll || !credentialEvidence.Sealed() || credentialEvidence.Overflowed(),
	)
	protocol := boundedPlainText(raw.Protocol, MaxRetainedIdentifierBytes, "PROTOCOL")
	trailerKeys, trailerKeysTruncated := boundedTrailerKeys(raw.DeclaredTrailers)
	return HTTPResponseSanitization{
		Snapshot: capturevalue.HTTPResponseSnapshot{
			StatusCode:          raw.StatusCode,
			Protocol:            protocol.Value,
			Headers:             headers.Value,
			ContentLength:       raw.ContentLength,
			DeclaredTrailerKeys: trailerKeys,
		},
		SensitiveNames: names.names,
		RedactAll:      headers.RedactAll || headers.Discovered,
		Truncated:      names.truncated || headers.Truncated || protocol.Truncated || trailerKeysTruncated,
	}
}

func (s Sanitizer) WebSocketHandshake(raw WebSocketHandshakeMetadata) capturevalue.WebSocketHandshakeSnapshot {
	return s.WebSocketHandshakeDetailed(raw, nil, false).Snapshot
}

func (s Sanitizer) WebSocketHandshakeDetailed(raw WebSocketHandshakeMetadata, inheritedNames []string, inheritedRedactAll bool) WebSocketHandshakeSanitization {
	headerEvidence := &raw.SensitiveHeaders
	names := mergeSensitiveHeaderNames(
		inheritedNames,
		headerEvidence.namesView(),
		inheritedRedactAll || !headerEvidence.Sealed() || headerEvidence.Overflowed(),
	)
	credentialEvidence := &raw.CredentialEvidence
	headers := s.HeadersDetailed(
		raw.Headers,
		names.names,
		credentialEvidence.valuesView(),
		names.redactAll || !credentialEvidence.Sealed() || credentialEvidence.Overflowed(),
	)
	protocol := boundedPlainText(raw.Protocol, MaxRetainedIdentifierBytes, "PROTOCOL")
	return WebSocketHandshakeSanitization{
		Snapshot: capturevalue.WebSocketHandshakeSnapshot{
			StatusCode: raw.StatusCode,
			Protocol:   protocol.Value,
			Headers:    headers.Value,
		},
		SensitiveNames: names.names,
		RedactAll:      headers.RedactAll || headers.Discovered,
		Truncated:      names.truncated || headers.Truncated || protocol.Truncated,
	}
}
