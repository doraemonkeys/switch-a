package redaction

import (
	"strings"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/requestcapture/capturevalue"
)

func TestCredentialEvidenceSealAndFailClosedLifecycle(t *testing.T) {
	var evidence CredentialEvidence
	if evidence.Sealed() || evidence.Overflowed() || evidence.valuesView() != nil {
		t.Fatal("zero evidence must not authorize retaining opaque diagnostics")
	}
	evidence.Add("")
	evidence.Add("secret")
	evidence.Add("secret")
	evidence.Seal()
	if got := evidence.valuesView(); len(got) != 1 || got[0] != "secret" {
		t.Fatalf("sealed evidence = %#v, want one deduplicated value", got)
	}

	evidence.Add("rotated-secret")
	if evidence.Sealed() || evidence.valuesView() != nil {
		t.Fatal("mutation must invalidate the producer's completeness seal")
	}
	other := sealedCredentialsForTest("third-secret")
	evidence.Merge(other)
	evidence.Seal()
	if got := evidence.valuesView(); len(got) != 3 {
		t.Fatalf("merged evidence count = %d, want 3", len(got))
	}

	var oversized CredentialEvidence
	oversized.Add(strings.Repeat("x", MaxRetainedCredentialValueBytes+1))
	oversized.Seal()
	if !oversized.Sealed() || !oversized.Overflowed() || oversized.valuesView() != nil {
		t.Fatal("oversized evidence must remain sealed but fail closed")
	}
	var mergedOverflow CredentialEvidence
	mergedOverflow.Merge(oversized)
	if !mergedOverflow.Overflowed() {
		t.Fatal("merging incomplete evidence must poison the destination")
	}

	var nilEvidence *CredentialEvidence
	nilEvidence.Add("ignored")
	nilEvidence.Merge(evidence)
	nilEvidence.Seal()
	nilEvidence.failClosed()
}

func TestCredentialEvidenceCapacityFailsClosed(t *testing.T) {
	var evidence CredentialEvidence
	for index := 0; index <= MaxRetainedCredentialValues; index++ {
		evidence.Add(string(rune('a' + index)))
	}
	if !evidence.Overflowed() {
		t.Fatal("credential count beyond the fixed evidence arena must fail closed")
	}

	var byteLimited CredentialEvidence
	value := strings.Repeat("x", MaxRetainedCredentialValueBytes)
	for index := 0; index <= MaxRetainedCredentialBytes/len(value); index++ {
		byteLimited.Add(value + string(rune(index)))
		if byteLimited.Overflowed() {
			break
		}
	}
	if !byteLimited.Overflowed() {
		t.Fatal("credential bytes beyond the fixed budget must fail closed")
	}
}

func TestSensitiveHeaderEvidenceSealMergeAndBounds(t *testing.T) {
	var evidence SensitiveHeaderEvidence
	evidence.Add("  X-Private-Token  ")
	evidence.Add("x-private-token")
	evidence.Seal()
	if got := evidence.namesView(); len(got) != 1 || got[0] != "X-Private-Token" {
		t.Fatalf("sealed sensitive names = %#v", got)
	}

	addition := sealedSensitiveHeadersForTest("X-Other-Secret")
	evidence.Merge(addition)
	if evidence.Sealed() || evidence.namesView() != nil {
		t.Fatal("merging names must invalidate the completeness seal")
	}
	evidence.Seal()
	if got := evidence.namesView(); len(got) != 2 {
		t.Fatalf("merged sensitive-name count = %d, want 2", len(got))
	}

	var oversized SensitiveHeaderEvidence
	oversized.Add(strings.Repeat("x", MaxRetainedHeaderNameBytes+1))
	if !oversized.Overflowed() {
		t.Fatal("oversized sensitive header name must fail closed")
	}
	var mergedOverflow SensitiveHeaderEvidence
	mergedOverflow.Merge(oversized)
	if !mergedOverflow.Overflowed() {
		t.Fatal("overflowed sensitive-name evidence must poison merges")
	}

	var full SensitiveHeaderEvidence
	for index := 0; index <= MaxRetainedSensitiveHeaderNames; index++ {
		full.Add(strings.Repeat("x", index+1))
	}
	if !full.Overflowed() {
		t.Fatal("sensitive-name count beyond capacity must fail closed")
	}

	var nilEvidence *SensitiveHeaderEvidence
	nilEvidence.Add("ignored")
	nilEvidence.Merge(evidence)
	nilEvidence.Seal()
	nilEvidence.failClosed()
}

func TestSanitizedTextScrubsOnlyExactCredential(t *testing.T) {
	input := `round trip failed: Bearer exact-secret token=pattern-secret {"client_secret":{"nested":["json-secret"]},"safe":"visible"}`
	result := SanitizedText(input, []string{"exact-secret"}, MaxRetainedErrorBytes, "ERROR")
	if strings.Contains(result.Value, "exact-secret") || !strings.Contains(result.Value, "pattern-secret") ||
		!strings.Contains(result.Value, "json-secret") {
		t.Fatalf("provider diagnostics were changed unexpectedly: %q", result.Value)
	}
	if result.Truncated || !strings.Contains(result.Value, `"safe":"visible"`) {
		t.Fatalf("sanitized text lost safe context: %#v", result)
	}

	structured := ScrubText(`{"access_token":["first",{"deep":true}],"safe":7}`, nil)
	if !strings.Contains(structured, "first") || !strings.Contains(structured, `"safe":7`) {
		t.Fatalf("structured scrub = %q", structured)
	}
	if got := ScrubText(`{"access_token":[`, nil); got == RedactedValue {
		t.Fatalf("malformed provider JSON was unnecessarily suppressed: %q", got)
	}
	if got := ScrubText(`{"safe":[`, nil); got == RedactedValue {
		t.Fatalf("malformed non-credential text was unnecessarily suppressed: %q", got)
	}
}

func TestSanitizedTextBoundsAndEvidence(t *testing.T) {
	if got := SanitizedText("", nil, 8, "ERROR"); got != (TextSanitization{}) {
		t.Fatalf("empty sanitization = %#v", got)
	}
	if got := SanitizedText("oversized", nil, 3, "ERROR"); got.Value != "[TRUNCATED_ERROR]" || !got.Truncated {
		t.Fatalf("oversized sanitization = %#v", got)
	}
	if got := SanitizedText("x", []string{"x"}, 5, "ERROR"); len(got.Value) != 5 || !got.Truncated {
		t.Fatalf("replacement expansion was not bounded: %#v", got)
	}
	if got := SanitizedText("diagnostic", []string{strings.Repeat("x", MaxRetainedCredentialValueBytes+1)}, 32, "ERROR"); got.Value != RedactedValue || !got.Truncated {
		t.Fatalf("unbounded credentials did not fail closed: %#v", got)
	}

	if got := SanitizedTextWithEvidence("diagnostic", CredentialEvidence{}, 32, "ERROR"); got.Value != RedactedValue || !got.Truncated {
		t.Fatalf("unsealed evidence result = %#v", got)
	}
	overflow := CredentialEvidence{}
	overflow.Add(strings.Repeat("x", MaxRetainedCredentialValueBytes+1))
	overflow.Seal()
	if got := SanitizedTextWithEvidence("diagnostic", overflow, 32, "ERROR"); got.Value != RedactedValue || !got.Truncated {
		t.Fatalf("overflowed evidence result = %#v", got)
	}
	sealed := sealedCredentialsForTest("secret")
	if got := SanitizedTextWithEvidence("diagnostic secret", sealed, 64, "ERROR"); strings.Contains(got.Value, "secret") || got.Truncated {
		t.Fatalf("sealed evidence result = %#v", got)
	}
}

func TestCredentialReplacementIsBoundedAndPrefersLongestMatch(t *testing.T) {
	if got := ReplaceCredentialValues("prefix-long-secret", []string{"secret", "long-secret"}); got != "prefix-"+RedactedValue {
		t.Fatalf("overlapping replacement = %q", got)
	}
	if got := ReplaceCredentialValues("", []string{"secret"}); got != "" {
		t.Fatalf("empty replacement = %q", got)
	}
	if got := ReplaceCredentialValues("unchanged", nil); got != "unchanged" {
		t.Fatalf("nil-secret replacement = %q", got)
	}
	tooMany := make([]string, MaxRetainedCredentialValues+1)
	for index := range tooMany {
		tooMany[index] = string(rune('a' + index))
	}
	if got := ReplaceCredentialValues("diagnostic", tooMany); got != RedactedValue {
		t.Fatalf("unbounded replacement = %q, want fail-closed marker", got)
	}
	if got := BoundedRedaction("ERROR", "ignored"); got != "[TRUNCATED_ERROR]" {
		t.Fatalf("bounded marker = %q", got)
	}
}

func TestFailureDetailedCanonicalizesAndRedactsDiagnostics(t *testing.T) {
	sanitizer := Sanitizer{}
	if got, ok := sanitizer.FailureDetailed(capturevalue.FailureObservation{}, sealedCredentialsForTest(), false); ok || got != (capturevalue.FailureObservation{}) {
		t.Fatalf("empty failure = (%#v, %t)", got, ok)
	}

	input := capturevalue.FailureObservation{
		Primary: capturevalue.FailureFact{
			Site:               capturevalue.FailureSiteWebSocketMessage,
			Peer:               capturevalue.FailurePeerProvider,
			Class:              capturevalue.FailureClassUpstreamSemantic,
			Code:               capturevalue.FailureCodeProviderSemantic,
			HTTPStatusCode:     502,
			WebSocketCloseCode: 1011,
			ProviderErrorType:  "secret_type",
			ProviderErrorCode:  "secret_code",
			Message:            "provider rejected secret",
		},
		Secondary: capturevalue.FailureFact{
			Site:    capturevalue.FailureSiteResponseRead,
			Peer:    capturevalue.FailurePeerUpstream,
			Class:   capturevalue.FailureClassRead,
			Code:    capturevalue.FailureCodeUpstreamRead,
			Message: "secondary secret",
		},
		HasSecondary: true,
	}
	result, ok := sanitizer.FailureDetailed(input, sealedCredentialsForTest("secret"), false)
	if !ok || !result.HasSecondary || !result.Truncated {
		t.Fatalf("sanitized failure = (%#v, %t)", result, ok)
	}
	for _, value := range []string{
		result.Primary.ProviderErrorType,
		result.Primary.ProviderErrorCode,
		result.Primary.Message,
		result.Secondary.Message,
	} {
		if strings.Contains(value, "secret") {
			t.Fatalf("failure diagnostic leaked injected credential: %#v", result)
		}
	}

	nonSemantic := input
	nonSemantic.Primary.Code = capturevalue.FailureCodeRoundTrip
	result, _ = sanitizer.FailureDetailed(nonSemantic, sealedCredentialsForTest("secret"), false)
	if !strings.Contains(result.Primary.ProviderErrorType, RedactedValue) ||
		!strings.Contains(result.Primary.ProviderErrorCode, RedactedValue) ||
		!strings.Contains(result.Primary.Message, RedactedValue) || !result.Truncated {
		t.Fatalf("non-semantic provider fields were not preserved: %#v", result)
	}

	invalid := input
	invalid.Primary.Site = capturevalue.FailureSite("hostile")
	invalid.Primary.Peer = capturevalue.FailurePeer("hostile")
	invalid.Primary.Class = capturevalue.FailureClass("hostile")
	invalid.Primary.Code = capturevalue.FailureCode("hostile")
	invalid.Primary.HTTPStatusCode = 1000
	invalid.Primary.WebSocketCloseCode = -1
	result, _ = sanitizer.FailureDetailed(invalid, sealedCredentialsForTest("secret"), false)
	if result.Primary.Site != capturevalue.FailureSiteUnknown ||
		result.Primary.Peer != capturevalue.FailurePeerUnknown ||
		result.Primary.Class != capturevalue.FailureClassUnknown ||
		result.Primary.Code != capturevalue.FailureCodeUnknown ||
		result.Primary.HTTPStatusCode != 0 || result.Primary.WebSocketCloseCode != 0 || !result.Truncated {
		t.Fatalf("invalid failure fields were not canonicalized: %#v", result)
	}

	result, _ = sanitizer.FailureDetailed(input, CredentialEvidence{}, false)
	if result.Primary.Message != "" || result.Secondary.Message != "" || !result.Truncated {
		t.Fatalf("unsealed evidence failure = %#v", result)
	}
	result, _ = sanitizer.FailureDetailed(input, sealedCredentialsForTest(), true)
	if result.Primary.Message != "" || !result.Truncated {
		t.Fatalf("redact-all failure = %#v", result)
	}
}

func sealedCredentialsForTest(values ...string) CredentialEvidence {
	var evidence CredentialEvidence
	for _, value := range values {
		evidence.Add(value)
	}
	evidence.Seal()
	return evidence
}

func sealedSensitiveHeadersForTest(names ...string) SensitiveHeaderEvidence {
	var evidence SensitiveHeaderEvidence
	for _, name := range names {
		evidence.Add(name)
	}
	evidence.Seal()
	return evidence
}
