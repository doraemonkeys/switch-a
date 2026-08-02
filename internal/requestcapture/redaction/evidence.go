package redaction

import (
	"net/http"
	"strconv"
	"strings"
)

func (evidence *CredentialEvidence) Add(value string) {
	if evidence == nil || evidence.overflow || value == "" {
		return
	}
	evidence.sealed = false
	if len(value) > MaxRetainedCredentialValueBytes ||
		len(value) > MaxRetainedCredentialBytes-int(evidence.bytes) ||
		int(evidence.count) == len(evidence.values) {
		evidence.failClosed()
		return
	}
	for index := 0; index < int(evidence.count); index++ {
		if evidence.values[index] == value {
			return
		}
	}
	evidence.values[evidence.count] = value
	evidence.count++
	evidence.bytes += uint32(len(value))
}

func (evidence *CredentialEvidence) Merge(other CredentialEvidence) {
	if evidence == nil || evidence.overflow {
		return
	}
	evidence.sealed = false
	if other.overflow {
		evidence.failClosed()
		return
	}
	for index := 0; index < int(other.count); index++ {
		evidence.Add(other.values[index])
		if evidence.overflow {
			return
		}
	}
}

// Seal certifies that the producer inspected every credential source for this
// capture phase. Mutation invalidates the seal so partially rebuilt evidence
// cannot accidentally authorize retaining opaque diagnostic text.
func (evidence *CredentialEvidence) Seal() {
	if evidence != nil {
		evidence.sealed = true
	}
}

func (evidence CredentialEvidence) Sealed() bool {
	return evidence.sealed
}

func (evidence CredentialEvidence) Overflowed() bool {
	return evidence.overflow
}

func (evidence *CredentialEvidence) failClosed() {
	if evidence == nil {
		return
	}
	*evidence = CredentialEvidence{overflow: true}
}

func (evidence *CredentialEvidence) valuesView() []string {
	if evidence == nil || evidence.overflow || !evidence.sealed {
		return nil
	}
	return evidence.values[:evidence.count]
}

func (evidence *SensitiveHeaderEvidence) Add(name string) {
	if evidence == nil || evidence.overflow {
		return
	}
	if len(name) > MaxRetainedHeaderNameBytes {
		evidence.sealed = false
		evidence.failClosed()
		return
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	evidence.sealed = false
	if len(name) > MaxRetainedSensitiveHeaderNames*MaxRetainedHeaderNameBytes-int(evidence.bytes) ||
		int(evidence.count) == len(evidence.names) {
		evidence.failClosed()
		return
	}
	for index := 0; index < int(evidence.count); index++ {
		if strings.EqualFold(evidence.names[index], name) {
			return
		}
	}
	evidence.names[evidence.count] = name
	evidence.count++
	evidence.bytes += uint32(len(name))
}

func (evidence *SensitiveHeaderEvidence) Merge(other SensitiveHeaderEvidence) {
	if evidence == nil || evidence.overflow {
		return
	}
	evidence.sealed = false
	if other.overflow {
		evidence.failClosed()
		return
	}
	for index := 0; index < int(other.count); index++ {
		evidence.Add(other.names[index])
		if evidence.overflow {
			return
		}
	}
}

func (evidence *SensitiveHeaderEvidence) Seal() {
	if evidence != nil {
		evidence.sealed = true
	}
}

func (evidence SensitiveHeaderEvidence) Sealed() bool {
	return evidence.sealed
}

func (evidence SensitiveHeaderEvidence) Overflowed() bool {
	return evidence.overflow
}

func (evidence *SensitiveHeaderEvidence) failClosed() {
	if evidence == nil {
		return
	}
	*evidence = SensitiveHeaderEvidence{overflow: true}
}

func (evidence *SensitiveHeaderEvidence) namesView() []string {
	if evidence == nil || evidence.overflow || !evidence.sealed {
		return nil
	}
	return evidence.names[:evidence.count]
}

type headerCredentialSet struct {
	values     [MaxRetainedCredentialValues]string
	count      int
	bytes      int
	discovered bool
	redactAll  bool
}

func discoverRequestHeaderCredentials(
	headers, trailers http.Header,
	extraSensitive, explicit []string,
	redactAll bool,
) ([]string, bool, bool) {
	credentials := discoverHeaderCredentials(headers, extraSensitive, explicit, redactAll)
	discovered := credentials.discovered
	credentials = discoverHeaderCredentials(
		trailers,
		extraSensitive,
		credentials.slice(),
		credentials.redactAll,
	)
	return credentials.slice(), credentials.redactAll, discovered || credentials.discovered
}

func discoverHeaderCredentials(
	source http.Header,
	extraSensitive []string,
	explicit []string,
	redactAll bool,
) headerCredentialSet {
	set := headerCredentialSet{redactAll: redactAll}
	if len(explicit) > MaxRetainedCredentialValues {
		set.redactAll = true
		return set
	}
	for _, value := range explicit {
		set.add(value)
	}
	if len(source) > MaxRetainedHeaderFields {
		set.redactAll = true
		return set
	}
	for name, values := range source {
		boundedName := strings.TrimSpace(name)
		if boundedName == "" || len(boundedName) > MaxRetainedHeaderNameBytes {
			set.redactAll = true
			return set
		}
		if !isSensitiveHeaderName(boundedName, extraSensitive) {
			continue
		}
		if len(values) > MaxRetainedHeaderValuesPerField {
			set.redactAll = true
			return set
		}
		for _, value := range values {
			before := set.count
			collectSensitiveHeaderValue(&set, name, value)
			set.discovered = set.discovered || set.count > before
			if set.redactAll {
				return set
			}
		}
	}
	return set
}

func (set *headerCredentialSet) add(value string) {
	if set == nil || set.redactAll {
		return
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	if len(value) > MaxRetainedCredentialValueBytes ||
		set.count == len(set.values) ||
		len(value) > MaxRetainedHeaderBytes-set.bytes {
		set.redactAll = true
		return
	}
	for index := 0; index < set.count; index++ {
		if set.values[index] == value {
			return
		}
	}
	set.values[set.count] = value
	set.count++
	set.bytes += len(value)
}

func (set *headerCredentialSet) slice() []string {
	if set == nil {
		return nil
	}
	return set.values[:set.count]
}

func isSensitiveHeaderName(name string, extra []string) bool {
	for _, sensitive := range defaultSensitiveHeaderNames {
		if equalNormalizedHeaderName(name, sensitive) {
			return true
		}
	}
	for _, sensitive := range extra {
		if equalNormalizedHeaderName(name, sensitive) {
			return true
		}
	}
	return false
}

func equalNormalizedHeaderName(left, right string) bool {
	if len(left) > MaxRetainedHeaderNameBytes || len(right) > MaxRetainedHeaderNameBytes {
		return false
	}
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if len(left) != len(right) {
		return false
	}
	for index := 0; index < len(left); index++ {
		leftByte := left[index]
		rightByte := right[index]
		if leftByte == '_' {
			leftByte = '-'
		} else if leftByte >= 'A' && leftByte <= 'Z' {
			leftByte += 'a' - 'A'
		}
		if rightByte == '_' {
			rightByte = '-'
		} else if rightByte >= 'A' && rightByte <= 'Z' {
			rightByte += 'a' - 'A'
		}
		if leftByte != rightByte {
			return false
		}
	}
	return true
}

func normalizedHeaderName(name string) string {
	name = strings.TrimSpace(name)
	if len(name) > MaxRetainedHeaderNameBytes {
		return ""
	}
	return strings.ReplaceAll(strings.ToLower(name), "_", "-")
}

func collectSensitiveHeaderValue(set *headerCredentialSet, name, value string) {
	name = normalizedHeaderName(name)
	trimmed := strings.TrimSpace(value)
	set.add(trimmed)
	if set.redactAll || trimmed == "" {
		return
	}
	switch name {
	case "authorization", "proxy-authorization":
		if separator := strings.IndexAny(trimmed, " \t"); separator >= 0 {
			set.add(trimmed[separator+1:])
		}
	case "cookie":
		collectCookieComponents(set, trimmed, false)
	case "set-cookie":
		collectCookieComponents(set, trimmed, true)
	}
}

func collectCookieComponents(set *headerCredentialSet, value string, firstOnly bool) {
	for offset := 0; offset <= len(value); {
		end := strings.IndexByte(value[offset:], ';')
		if end < 0 {
			end = len(value)
		} else {
			end += offset
		}
		part := strings.TrimSpace(value[offset:end])
		separator := strings.IndexByte(part, '=')
		if separator <= 0 {
			// The complete sensitive value was already collected. Retaining the
			// malformed component as an additional exact secret avoids turning an
			// unrelated safe field into collateral redaction.
			set.add(part)
			if set.redactAll || firstOnly || end == len(value) {
				return
			}
			offset = end + 1
			continue
		}
		component := strings.TrimSpace(part[separator+1:])
		if len(component) >= 2 && component[0] == '"' && component[len(component)-1] == '"' {
			component = component[1 : len(component)-1]
		}
		set.add(component)
		if set.redactAll || firstOnly || end == len(value) {
			return
		}
		offset = end + 1
	}
}

type credentialReplacer struct {
	replacer *strings.Replacer
	bounded  bool
}

func compileCredentialReplacer(secrets []string) credentialReplacer {
	if len(secrets) > MaxRetainedCredentialValues {
		return credentialReplacer{}
	}

	var unique [MaxRetainedCredentialValues]string
	uniqueCount := 0
	totalBytes := 0
	for _, rawSecret := range secrets {
		if len(rawSecret) > MaxRetainedCredentialValueBytes ||
			len(rawSecret) > MaxRetainedCredentialBytes-totalBytes {
			return credentialReplacer{}
		}
		totalBytes += len(rawSecret)

		secret := strings.TrimSpace(rawSecret)
		if secret == "" {
			continue
		}
		duplicate := false
		for index := 0; index < uniqueCount; index++ {
			if unique[index] == secret {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		unique[uniqueCount] = secret
		uniqueCount++
	}

	// Replacer resolves equal-position matches in argument order. Ordering by
	// length prevents a short credential prefix from exposing the remainder of
	// a longer credential that starts at the same byte.
	for index := 1; index < uniqueCount; index++ {
		secret := unique[index]
		position := index
		for position > 0 && len(secret) > len(unique[position-1]) {
			unique[position] = unique[position-1]
			position--
		}
		unique[position] = secret
	}

	compiled := credentialReplacer{bounded: true}
	if uniqueCount == 0 {
		return compiled
	}
	pairs := make([]string, 0, uniqueCount*2)
	for index := 0; index < uniqueCount; index++ {
		pairs = append(pairs, unique[index], RedactedValue)
	}
	// strings.Replacer compiles the bounded set into a trie and scans source
	// text once. Replacement output is not rescanned, so a credential equal to
	// the marker cannot recursively rewrite newly emitted markers.
	compiled.replacer = strings.NewReplacer(pairs...)
	return compiled
}

func (replacer credentialReplacer) replace(value string) string {
	if !replacer.bounded {
		return RedactedValue
	}
	if value == "" || replacer.replacer == nil {
		return value
	}
	return replacer.replacer.Replace(value)
}

func ReplaceCredentialValues(value string, secrets []string) string {
	if value == "" || len(secrets) == 0 {
		return value
	}
	return compileCredentialReplacer(secrets).replace(value)
}

func redactStructuredCredentialValues(value string) (string, bool) {
	var builder strings.Builder
	cursor := 0
	replaced := false
	for offset := 0; offset < len(value); {
		if value[offset] != '"' {
			offset++
			continue
		}
		keyEnd, ok := scanJSONString(value, offset)
		if !ok {
			break
		}
		next := skipJSONSpace(value, keyEnd)
		if next >= len(value) || value[next] != ':' {
			offset = keyEnd
			continue
		}
		key, err := strconv.Unquote(value[offset:keyEnd])
		if err != nil {
			if credentialKeyHint(value[offset:keyEnd]) {
				return "", false
			}
			offset = keyEnd
			continue
		}
		if !isStructuredCredentialKey(key) {
			offset = keyEnd
			continue
		}
		valueStart := skipJSONSpace(value, next+1)
		valueEnd, ok := scanJSONValue(value, valueStart)
		if !ok {
			return "", false
		}
		if !replaced {
			builder.Grow(len(value))
		}
		builder.WriteString(value[cursor:valueStart])
		builder.WriteString(`"[REDACTED]"`)
		cursor = valueEnd
		offset = valueEnd
		replaced = true
	}
	if !replaced {
		return value, true
	}
	builder.WriteString(value[cursor:])
	return builder.String(), true
}

func isStructuredCredentialKey(key string) bool {
	key = normalizedCredentialKey(key)
	if _, sensitive := defaultSensitiveQueryKeys[key]; sensitive {
		return true
	}
	if key == "client_assertion" {
		return true
	}
	for _, suffix := range []string{"_credential", "_signature", "_token", "_secret", "_assertion"} {
		if strings.HasSuffix(key, suffix) {
			return true
		}
	}
	return false
}

func credentialKeyHint(raw string) bool {
	raw = strings.ToLower(raw)
	for _, hint := range []string{"token", "secret", "credential", "signature", "assertion"} {
		if strings.Contains(raw, hint) {
			return true
		}
	}
	return false
}

func scanJSONString(value string, start int) (int, bool) {
	if start >= len(value) || value[start] != '"' {
		return start, false
	}
	escaped := false
	for index := start + 1; index < len(value); index++ {
		switch {
		case escaped:
			escaped = false
		case value[index] == '\\':
			escaped = true
		case value[index] == '"':
			return index + 1, true
		}
	}
	return len(value), false
}

func scanJSONValue(value string, start int) (int, bool) {
	if start >= len(value) {
		return start, false
	}
	if value[start] == '"' {
		return scanJSONString(value, start)
	}
	if value[start] != '[' && value[start] != '{' {
		end := start
		for end < len(value) && !isJSONValueDelimiter(value[end]) {
			end++
		}
		return end, end > start
	}
	stack := [maximumCredentialJSONDepth]byte{}
	depth := 1
	stack[0] = value[start]
	for index := start + 1; index < len(value); index++ {
		switch value[index] {
		case '"':
			end, ok := scanJSONString(value, index)
			if !ok {
				return len(value), false
			}
			index = end - 1
		case '[', '{':
			if depth == len(stack) {
				return len(value), false
			}
			stack[depth] = value[index]
			depth++
		case ']', '}':
			opening := stack[depth-1]
			if value[index] == ']' && opening != '[' || value[index] == '}' && opening != '{' {
				return len(value), false
			}
			depth--
			if depth == 0 {
				return index + 1, true
			}
		}
	}
	return len(value), false
}

func skipJSONSpace(value string, start int) int {
	for start < len(value) {
		switch value[start] {
		case ' ', '\t', '\r', '\n':
			start++
		default:
			return start
		}
	}
	return start
}

func isJSONValueDelimiter(value byte) bool {
	switch value {
	case ' ', '\t', '\r', '\n', ',', '}', ']':
		return true
	default:
		return false
	}
}
