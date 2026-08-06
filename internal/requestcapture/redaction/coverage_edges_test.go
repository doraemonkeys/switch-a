package redaction

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/requestcapture/capturevalue"
)

func TestEvidenceMergeAndHeaderCredentialSetFailClosedAtInternalBounds(t *testing.T) {
	t.Parallel()

	credentialSource := CredentialEvidence{count: 1, bytes: 1}
	credentialSource.values[0] = "x"
	credentialDestination := CredentialEvidence{bytes: MaxRetainedCredentialBytes}
	credentialDestination.Merge(credentialSource)
	if !credentialDestination.Overflowed() {
		t.Fatal("credential merge must stop as soon as the destination byte budget overflows")
	}

	var emptyName SensitiveHeaderEvidence
	emptyName.Add("   ")
	if emptyName.count != 0 || emptyName.Overflowed() {
		t.Fatalf("blank sensitive header changed evidence: %#v", emptyName)
	}
	headerSource := SensitiveHeaderEvidence{count: 1, bytes: 1}
	headerSource.names[0] = "X"
	headerDestination := SensitiveHeaderEvidence{
		bytes: MaxRetainedSensitiveHeaderNames * MaxRetainedHeaderNameBytes,
	}
	headerDestination.Merge(headerSource)
	if !headerDestination.Overflowed() {
		t.Fatal("sensitive-header merge must stop as soon as its arena overflows")
	}

	var nilSet *headerCredentialSet
	nilSet.add("ignored")
	if nilSet.slice() != nil {
		t.Fatal("nil credential set exposed values")
	}
	redactingSet := headerCredentialSet{redactAll: true}
	redactingSet.add("ignored")
	if redactingSet.count != 0 {
		t.Fatal("fail-closed credential set accepted another value")
	}

	set := headerCredentialSet{}
	set.add("   ")
	set.add("secret")
	set.add("secret")
	if got := set.slice(); len(got) != 1 || got[0] != "secret" {
		t.Fatalf("deduplicated credential set = %#v", got)
	}
	set.add(strings.Repeat("x", MaxRetainedCredentialValueBytes+1))
	if !set.redactAll {
		t.Fatal("oversized discovered credential did not fail closed")
	}

	set = headerCredentialSet{bytes: MaxRetainedHeaderBytes}
	set.add("x")
	if !set.redactAll {
		t.Fatal("aggregate discovered credential budget was not enforced")
	}
	set = headerCredentialSet{count: MaxRetainedCredentialValues}
	set.add("x")
	if !set.redactAll {
		t.Fatal("discovered credential count was not enforced")
	}

	discovered := discoverHeaderCredentials(
		http.Header{"Authorization": {strings.Repeat("x", MaxRetainedCredentialValueBytes+1)}},
		[]string{"Authorization"},
		nil,
		false,
	)
	if !discovered.redactAll {
		t.Fatal("credential discovery continued after a sensitive value exceeded bounds")
	}
}

func TestHeaderNormalizationCookieAndReplacerEdges(t *testing.T) {
	t.Parallel()

	if equalNormalizedHeaderName(strings.Repeat("x", MaxRetainedHeaderNameBytes+1), "x") {
		t.Fatal("oversized header name compared equal")
	}
	if equalNormalizedHeaderName("X-A", "X-B") || equalNormalizedHeaderName("X", "XX") {
		t.Fatal("different header names compared equal")
	}
	if !equalNormalizedHeaderName(" X_API_KEY ", "x-api-key") ||
		!equalNormalizedHeaderName("x-api-key", "X_API_KEY") {
		t.Fatal("case and underscore normalization was not symmetric")
	}
	if got := normalizedHeaderName(strings.Repeat("x", MaxRetainedHeaderNameBytes+1)); got != "" {
		t.Fatalf("oversized normalized header = %q", got)
	}

	set := headerCredentialSet{}
	collectSensitiveHeaderValue(&set, "Authorization", "   ")
	if set.count != 0 {
		t.Fatalf("blank authorization value was collected: %#v", set.slice())
	}
	collectCookieComponents(&set, "malformed; second", false)
	if got := set.slice(); len(got) != 2 || got[0] != "malformed" || got[1] != "second" {
		t.Fatalf("malformed cookie components = %#v", got)
	}

	replacer := compileCredentialReplacer([]string{" ", "short", "long-secret", "short"})
	if !replacer.bounded {
		t.Fatal("bounded duplicate credentials produced an unbounded replacer")
	}
	if got := replacer.replace("long-secret short"); got != RedactedValue+" "+RedactedValue {
		t.Fatalf("ordered replacement = %q", got)
	}
}

func TestTextNormalizationAndSanitizerInternalEdges(t *testing.T) {
	t.Parallel()

	boundedReplacer := credentialReplacer{bounded: true}
	if got := sanitizedTextWithReplacer("", boundedReplacer, 10, "TEXT"); got != (TextSanitization{}) {
		t.Fatalf("empty internal sanitization = %#v", got)
	}
	if got := truncateSanitized("abcdefghijklmnopqrstuvwxyz", len("...[TRUNCATED]")+3); !strings.HasSuffix(got, "...[TRUNCATED]") {
		t.Fatalf("long truncation marker = %q", got)
	}

	names := []string{"", strings.Repeat("x", MaxRetainedHeaderNameBytes+1), "X-Test", "x-test"}
	normalized := normalizeSensitiveHeaderNames(names)
	if !normalized.redactAll || !normalized.truncated || len(normalized.names) != 1 || normalized.names[0] != "x-test" {
		t.Fatalf("normalized sensitive names = %#v", normalized)
	}
	tooMany := make([]string, MaxRetainedSensitiveHeaderNames+1)
	for index := range tooMany {
		tooMany[index] = "x-" + strconv.Itoa(index)
	}
	normalized = normalizeSensitiveHeaderNames(tooMany)
	if !normalized.redactAll || !normalized.truncated || len(normalized.names) != MaxRetainedSensitiveHeaderNames {
		t.Fatalf("over-capacity normalized names = %#v", normalized)
	}

	merged := mergeSensitiveHeaderNames([]string{"X-Duplicate"}, []string{"x-duplicate"}, false)
	if merged.redactAll || len(merged.names) != 1 {
		t.Fatalf("duplicate merged names = %#v", merged)
	}
	existing := make([]string, MaxRetainedSensitiveHeaderNames)
	for index := range existing {
		existing[index] = "existing-" + strconv.Itoa(index)
	}
	merged = mergeSensitiveHeaderNames(existing, []string{"new-name"}, false)
	if !merged.redactAll || !merged.truncated {
		t.Fatalf("over-capacity merged names = %#v", merged)
	}

	trailers, truncated := boundedTrailerKeys(http.Header{" ": nil, "X-Valid": nil})
	if truncated || len(trailers) != 1 || trailers[0] != "X-Valid" {
		t.Fatalf("bounded trailer keys = (%#v, %t)", trailers, truncated)
	}
	if got := ScrubText("secret", []string{"secret"}); got != RedactedValue {
		t.Fatalf("explicit scrub = %q", got)
	}
	if got := scrubTextWithReplacer("diagnostic", credentialReplacer{}); got != RedactedValue {
		t.Fatalf("unbounded internal scrub = %q", got)
	}

	fact := capturevalue.FailureFact{
		Site:  capturevalue.FailureSiteGateway,
		Peer:  capturevalue.FailurePeerGateway,
		Class: capturevalue.FailureClassTransport,
		Code:  capturevalue.FailureCodeRoundTrip,
	}
	result, present := (Sanitizer{}).FailureDetailed(
		capturevalue.FailureObservation{Primary: fact},
		sealedCredentialsForTest(),
		false,
	)
	if !present || result.Primary != fact || result.Truncated {
		t.Fatalf("failure without diagnostic text = (%#v, %t)", result, present)
	}

	if got := (Sanitizer{}).structuredURLDetailed(nil, nil); got != (TargetSanitization{}) {
		t.Fatalf("nil structured URL = %#v", got)
	}
	rawURL := &url.URL{
		Scheme:   "https",
		Host:     "example.test",
		RawQuery: "value=" + strings.Repeat("[", 3000),
	}
	if got := (Sanitizer{}).structuredURLDetailed(rawURL, nil); !got.Target.Truncated || got.Target.Value != "[TRUNCATED_URL]" {
		t.Fatalf("URL enlarged by canonical escaping = %#v", got)
	}
	metadata := RequestMetadata{
		SensitiveHeaders:   sealedSensitiveHeadersForTest(),
		CredentialEvidence: sealedCredentialsForTest(),
	}
	if got := (Sanitizer{}).Request(metadata); got.URL != "" || got.Host != "" {
		t.Fatalf("request without target = %#v", got)
	}
}
