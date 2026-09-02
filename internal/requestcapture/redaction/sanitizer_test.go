package redaction

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/requestcapture/capturevalue"
)

func TestHeadersDetailedRedactsOnlyExplicitCredentialValue(t *testing.T) {
	const (
		authorization = "Bearer auth-secret"
		proxyAuth     = "Basic proxy-secret"
		cookie        = `session="cookie-secret"; theme=blue`
		setCookie     = "sid=set-secret; Path=/"
		custom        = "custom-secret"
	)
	source := http.Header{
		"authorization":       {authorization},
		"Proxy_Authorization": {proxyAuth},
		"Cookie":              {cookie},
		"Set-Cookie":          {setCookie},
		"X_Custom_Credential": {custom},
		"X-Safe": {strings.Join([]string{
			authorization, "auth-secret", proxyAuth, "proxy-secret",
			cookie, "cookie-secret", setCookie, "set-secret", custom,
		}, " | ")},
	}

	result := (Sanitizer{}).HeadersDetailed(
		source,
		nil,
		[]string{custom},
		false,
	)
	if result.Discovered || result.RedactAll || result.Truncated {
		t.Fatalf("header sanitization state = %#v", result)
	}
	for _, name := range []string{"authorization", "Proxy_Authorization", "Cookie", "Set-Cookie"} {
		if got := result.Value[name]; len(got) != 1 || got[0] != source[name][0] {
			t.Fatalf("provider header %q changed: %#v", name, got)
		}
	}
	if result.Value["X_Custom_Credential"][0] != RedactedValue ||
		strings.Contains(result.Value["X-Safe"][0], custom) {
		t.Fatalf("injected value was not replaced on every header surface: %#v", result.Value)
	}

	clone := (Sanitizer{}).Headers(source, []string{"X-Custom-Credential"})
	source["X-Safe"][0] = "mutated"
	if clone["X-Safe"][0] == "mutated" {
		t.Fatal("sanitized headers retained caller-owned storage")
	}
}

func TestHeadersDetailedFailsClosedAndBoundsAttackerInput(t *testing.T) {
	sanitizer := Sanitizer{}
	if got := sanitizer.HeadersDetailed(nil, nil, nil, true); len(got.Value) != 0 || !got.RedactAll {
		t.Fatalf("empty redact-all headers = %#v", got)
	}

	tooManyFields := make(http.Header, MaxRetainedHeaderFields+1)
	for index := 0; index <= MaxRetainedHeaderFields; index++ {
		tooManyFields[string(rune(index+1))] = []string{"value"}
	}
	if got := sanitizer.HeadersDetailed(tooManyFields, nil, nil, false); len(got.Value) != 0 || !got.RedactAll || !got.Truncated {
		t.Fatalf("oversized header map = %#v", got)
	}

	oversizedName := strings.Repeat("n", MaxRetainedHeaderNameBytes+1)
	if got := sanitizer.HeadersDetailed(http.Header{oversizedName: {"value"}, "X-Safe": {"visible"}}, nil, nil, false); !got.RedactAll || !got.Truncated || got.Value["X-Safe"][0] != RedactedValue {
		t.Fatalf("oversized header name = %#v", got)
	}

	tooManyValues := make([]string, MaxRetainedHeaderValuesPerField+1)
	for index := range tooManyValues {
		tooManyValues[index] = "secret"
	}
	if got := sanitizer.HeadersDetailed(http.Header{"Authorization": tooManyValues}, nil, nil, false); got.RedactAll || !got.Truncated || len(got.Value["Authorization"]) != MaxRetainedHeaderValuesPerField {
		t.Fatalf("oversized header values = %#v", got)
	}

	oversizedValue := strings.Repeat("v", MaxRetainedHeaderValueBytes+1)
	if got := sanitizer.HeadersDetailed(http.Header{"X-Safe": {oversizedValue}}, nil, nil, false); !got.Truncated || got.Value["X-Safe"][0] != "[TRUNCATED_HEADER]" {
		t.Fatalf("oversized safe header value = %#v", got)
	}

	byteHeavy := make(http.Header)
	for index := range 10 {
		byteHeavy[string(rune('a'+index))] = []string{strings.Repeat("x", MaxRetainedHeaderValueBytes)}
	}
	if got := sanitizer.HeadersDetailed(byteHeavy, nil, nil, false); !got.Truncated || len(got.Value) >= len(byteHeavy) {
		t.Fatalf("aggregate header budget was not enforced: retained=%d result=%#v", len(got.Value), got)
	}

	unboundedCredentials := make([]string, MaxRetainedCredentialValues+1)
	if got := sanitizer.HeadersDetailed(http.Header{"X-Safe": {"visible"}}, nil, unboundedCredentials, false); !got.RedactAll || got.Value["X-Safe"][0] != RedactedValue {
		t.Fatalf("unbounded credential set = %#v", got)
	}
}

func TestURLSanitizationReplacesOnlyExactCredentialEchoes(t *testing.T) {
	const secret = "exact-secret"
	raw := "https://user:password@example.test/path/" + secret +
		"?refresh_token=query-secret&token%5B%5D=array-secret&safe=" + secret +
		"&X-Amz-Signature=aws-secret#" + secret
	result := (Sanitizer{}).URLDetailed(raw, []string{secret})
	if result.Truncated {
		t.Fatalf("bounded URL unexpectedly truncated: %#v", result)
	}
	if !strings.Contains(result.Value, "user:password") || !strings.Contains(result.Value, "query-secret") ||
		!strings.Contains(result.Value, "array-secret") || !strings.Contains(result.Value, "aws-secret") {
		t.Fatalf("unrelated URL metadata was removed: %q", result.Value)
	}
	parsed, err := url.Parse(result.Value)
	if err != nil {
		t.Fatalf("parse sanitized URL: %v", err)
	}
	if parsed.User == nil || parsed.Query().Get("refresh_token") != "query-secret" ||
		parsed.Query().Get("token[]") != "array-secret" ||
		parsed.Query().Get("safe") != RedactedValue ||
		parsed.Query().Get("X-Amz-Signature") != "aws-secret" {
		t.Fatalf("sanitized URL components = %#v", parsed)
	}

	sanitizer := Sanitizer{}
	if got := sanitizer.URL("", nil); got != "" {
		t.Fatalf("empty URL = %q", got)
	}
	for _, malformed := range []string{"://secret", "user@example.test", "https://example.test/?bad=%zz"} {
		if got := sanitizer.URL(malformed, []string{"secret"}); got != InvalidURLRedaction {
			t.Fatalf("malformed URL %q = %q", malformed, got)
		}
	}
	if got := sanitizer.URL(strings.Repeat("x", MaxRetainedURLBytes+1), nil); got != "[TRUNCATED_URL]" {
		t.Fatalf("oversized URL = %q", got)
	}
}

func TestTargetVariantsAndCredentialBounds(t *testing.T) {
	sanitizer := Sanitizer{}
	invalid := InvalidTarget().Sanitize(sanitizer, nil)
	if invalid.Target.Value != InvalidURLRedaction || invalid.Host.Value != RedactedValue || !invalid.Target.Truncated || !invalid.Host.Truncated {
		t.Fatalf("invalid target = %#v", invalid)
	}
	if got := BorrowedHTTPTarget(nil).Sanitize(sanitizer, nil); got != (TargetSanitization{}) {
		t.Fatalf("nil HTTP target = %#v", got)
	}
	webSocket := BorrowedWebSocketTarget("wss://example.test/socket?api_key=secret").Sanitize(sanitizer, []string{"secret"})
	if parsed, err := url.Parse(webSocket.Target.Value); err != nil || parsed.Query().Get("api_key") != RedactedValue || webSocket.Host.Value != "example.test" {
		t.Fatalf("WebSocket target = %#v, parse error = %v", webSocket, err)
	}

	tooManySecrets := make([]string, MaxRetainedCredentialValues+1)
	structured := &url.URL{Scheme: "https", Host: "example.test", Path: "/safe"}
	if got := BorrowedHTTPTarget(structured).Sanitize(sanitizer, tooManySecrets); got.Target.Value != RedactedValue || got.Host.Value != RedactedValue || !got.Target.Truncated {
		t.Fatalf("unbounded target credentials = %#v", got)
	}
	structured = &url.URL{Scheme: "https", Host: strings.Repeat("h", MaxRetainedHostBytes+1), Path: strings.Repeat("p", MaxRetainedURLBytes)}
	if got := BorrowedHTTPTarget(structured).Sanitize(sanitizer, nil); got.Target.Value != "[TRUNCATED_URL]" || !got.Target.Truncated || !got.Host.Truncated {
		t.Fatalf("oversized structured target = %#v", got)
	}

	overflow := CredentialEvidence{}
	overflow.Add(strings.Repeat("x", MaxRetainedCredentialValueBytes+1))
	overflow.Seal()
	if got := sanitizer.TargetWithEvidence(BorrowedWebSocketTarget("wss://example.test/safe"), overflow); got.Target.Value != RedactedValue || got.Host.Value != RedactedValue || !got.Target.Truncated || !got.Host.Truncated {
		t.Fatalf("overflowed target evidence = %#v", got)
	}
	unsealed := sanitizer.TargetWithEvidence(
		BorrowedWebSocketTarget("wss://secret.example.test/path?safe=secret"),
		CredentialEvidence{},
	)
	if unsealed.Target.Value != RedactedValue || unsealed.Host.Value != RedactedValue ||
		!unsealed.Target.Truncated || !unsealed.Host.Truncated {
		t.Fatalf("unsealed target evidence leaked metadata: %#v", unsealed)
	}
}

func TestHeadersWithEvidenceFailsClosedWithoutCompleteInventory(t *testing.T) {
	source := http.Header{
		"X-Debug": {"ordinary-looking credential echo"},
	}
	unsealed := (Sanitizer{}).HeadersWithEvidence(source, nil, CredentialEvidence{}, false)
	if !unsealed.RedactAll || unsealed.Value["X-Debug"][0] != RedactedValue {
		t.Fatalf("unsealed header evidence leaked metadata: %#v", unsealed)
	}

	sealed := (Sanitizer{}).HeadersWithEvidence(source, nil, sealedCredentialsForTest(), false)
	if sealed.RedactAll || sealed.Value["X-Debug"][0] != source["X-Debug"][0] {
		t.Fatalf("sealed empty evidence over-redacted safe metadata: %#v", sealed)
	}

	overflow := CredentialEvidence{}
	overflow.Add(strings.Repeat("x", MaxRetainedCredentialValueBytes+1))
	overflow.Seal()
	failedClosed := (Sanitizer{}).HeadersWithEvidence(source, nil, overflow, false)
	if !failedClosed.RedactAll || failedClosed.Value["X-Debug"][0] != RedactedValue {
		t.Fatalf("overflowed header evidence leaked metadata: %#v", failedClosed)
	}
}

func TestRequestDetailedUsesSealedEvidenceAcrossHeadersURLAndTrailers(t *testing.T) {
	const secret = "request-secret"
	credentials := sealedCredentialsForTest("Bearer "+secret, secret, "session="+secret, "session")
	sensitiveNames := sealedSensitiveHeadersForTest("X-Credential")
	target, err := url.Parse("https://user:" + secret + "@example.test/path?safe=" + secret)
	if err != nil {
		t.Fatal(err)
	}
	raw := RequestMetadata{
		Method:        "POST",
		ContentLength: 42,
		Headers: http.Header{
			"Authorization": {"Bearer " + secret},
			"X-Credential":  {secret},
			"X-Debug":       {"echo " + secret},
		},
		Trailers: http.Header{
			"Cookie":  {"session=" + secret},
			"X-Debug": {"echo " + secret},
		},
		SensitiveHeaders:   sensitiveNames,
		CredentialEvidence: credentials,
	}
	result := (Sanitizer{}).RequestDetailed(raw, BorrowedHTTPTarget(target))
	if result.RedactAll || result.Truncated || result.Snapshot.Method != "POST" || result.Snapshot.ContentLength != 42 {
		t.Fatalf("request sanitization state = %#v", result)
	}
	encoded := result.Snapshot.URL + result.Snapshot.Host +
		strings.Join(result.Snapshot.Headers["X-Debug"], "") +
		strings.Join(result.Snapshot.Trailers["X-Debug"], "")
	if strings.Contains(encoded, secret) || result.Snapshot.Headers["Authorization"][0] != RedactedValue ||
		result.Snapshot.Headers["X-Credential"][0] != RedactedValue ||
		result.Snapshot.Trailers["Cookie"][0] != RedactedValue {
		t.Fatalf("request snapshot leaked credentials: %#v", result.Snapshot)
	}

	plain := (Sanitizer{}).Request(raw, BorrowedHTTPTarget(target))
	if plain.URL != result.Snapshot.URL {
		t.Fatalf("Request wrapper URL = %q, want %q", plain.URL, result.Snapshot.URL)
	}
}

func TestRequestDetailedFailsClosedWithoutCompleteProducerEvidence(t *testing.T) {
	target, err := url.Parse("https://example.test/path?safe=secret")
	if err != nil {
		t.Fatal(err)
	}
	raw := RequestMetadata{
		Method:  strings.Repeat("M", MaxRetainedMethodBytes+1),
		Headers: http.Header{"X-Debug": {"secret"}},
	}
	result := (Sanitizer{}).RequestDetailed(raw, BorrowedHTTPTarget(target))
	if !result.RedactAll || !result.Truncated || result.Snapshot.URL != RedactedValue ||
		result.Snapshot.Host != RedactedValue || result.Snapshot.Headers["X-Debug"][0] != RedactedValue ||
		result.Snapshot.Method != "[TRUNCATED_METHOD]" {
		t.Fatalf("unsealed request evidence did not fail closed: %#v", result)
	}

	sealedEmpty := raw
	sealedEmpty.SensitiveHeaders = sealedSensitiveHeadersForTest()
	sealedEmpty.CredentialEvidence = sealedCredentialsForTest()
	withoutTarget := (Sanitizer{}).RequestDetailed(sealedEmpty, Target{})
	if withoutTarget.Snapshot.URL != "" || withoutTarget.Snapshot.Host != "" {
		t.Fatalf("absent request target = %#v", withoutTarget.Snapshot)
	}
}

func TestProviderAndAttemptMetadataAreCanonicalAndBounded(t *testing.T) {
	attempt := capturevalue.AttemptMetadata{
		Provider:        capturevalue.ProviderIdentity{ID: "provider", Name: "Provider"},
		APIType:         "chat",
		SelectionMode:   capturevalue.SelectionModeInitial,
		SelectionSource: capturevalue.SelectionSourceStrategy,
		CredentialPhase: capturevalue.CredentialPhaseInitial,
	}
	bounded, truncated := BoundedAttemptMetadata(attempt)
	if truncated || bounded != attempt {
		t.Fatalf("valid attempt = (%#v, %t)", bounded, truncated)
	}

	hostile := attempt
	hostile.APIType = strings.Repeat("x", MaxRetainedAPITypeBytes+1)
	hostile.SelectionMode = capturevalue.SelectionMode("hostile")
	hostile.SelectionSource = capturevalue.SelectionSource("hostile")
	hostile.CredentialPhase = capturevalue.CredentialPhase("hostile")
	bounded, truncated = BoundedAttemptMetadata(hostile)
	if !truncated || bounded.APIType != "[TRUNCATED_API_TYPE]" ||
		bounded.SelectionMode != capturevalue.SelectionModeUnknown ||
		bounded.SelectionSource != capturevalue.SelectionSourceUnknown ||
		bounded.CredentialPhase != capturevalue.CredentialPhaseUnknown {
		t.Fatalf("hostile attempt = (%#v, %t)", bounded, truncated)
	}

	provider, truncated := SanitizedProvider(capturevalue.AttemptMetadata{
		Provider: capturevalue.ProviderIdentity{
			ID:   strings.Repeat("i", MaxRetainedProviderIDBytes+1),
			Name: strings.Repeat("n", MaxRetainedProviderNameBytes+1),
		},
		APIType: strings.Repeat("a", MaxRetainedAPITypeBytes+1),
	}, "https://example.test")
	if !truncated || provider.ID != "[TRUNCATED_PROVIDER_ID]" ||
		provider.Name != "[TRUNCATED_PROVIDER_NAME]" || provider.APIType != "[TRUNCATED_API_TYPE]" {
		t.Fatalf("bounded provider = (%#v, %t)", provider, truncated)
	}

	request := RequestMetadata{CredentialEvidence: sealedCredentialsForTest("secret")}
	provider = (Sanitizer{}).Provider(attempt, request, BorrowedWebSocketTarget("wss://example.test/path?safe=secret"))
	if strings.Contains(provider.TargetURL, "secret") {
		t.Fatalf("provider target leaked credential: %#v", provider)
	}
	request.CredentialEvidence = CredentialEvidence{}
	provider = (Sanitizer{}).Provider(attempt, request, BorrowedWebSocketTarget("wss://example.test/safe"))
	if provider.TargetURL != RedactedValue {
		t.Fatalf("provider with unsealed evidence = %#v", provider)
	}
}

func TestResponseAndWebSocketMetadataRedactAndPropagateState(t *testing.T) {
	const secret = "response-secret"
	credentials := sealedCredentialsForTest("session="+secret+"; Path=/", "session="+secret, secret)
	sensitiveNames := sealedSensitiveHeadersForTest("X-Response-Credential")
	response := HTTPResponseMetadata{
		StatusCode:    200,
		Protocol:      "HTTP/2",
		ContentLength: 99,
		Headers: http.Header{
			"Set-Cookie":            {"session=" + secret + "; Path=/"},
			"X-Response-Credential": {secret},
			"X-Debug":               {"echo " + secret},
		},
		DeclaredTrailers:   http.Header{"Z-Trailer": nil, "A-Trailer": nil},
		SensitiveHeaders:   sensitiveNames,
		CredentialEvidence: credentials,
	}
	result := (Sanitizer{}).HTTPResponseDetailed(response, []string{"X-Inherited-Credential"}, false)
	if result.RedactAll || result.Truncated || result.Snapshot.StatusCode != 200 ||
		result.Snapshot.Protocol != "HTTP/2" || result.Snapshot.ContentLength != 99 ||
		strings.Join(result.Snapshot.DeclaredTrailerKeys, ",") != "A-Trailer,Z-Trailer" {
		t.Fatalf("HTTP response sanitization = %#v", result)
	}
	if result.Snapshot.Headers["Set-Cookie"][0] != RedactedValue ||
		result.Snapshot.Headers["X-Response-Credential"][0] != RedactedValue ||
		strings.Contains(result.Snapshot.Headers["X-Debug"][0], secret) {
		t.Fatalf("HTTP response leaked credential: %#v", result.Snapshot)
	}
	if got := (Sanitizer{}).HTTPResponse(response); got.Protocol != "HTTP/2" {
		t.Fatalf("HTTPResponse wrapper = %#v", got)
	}

	webSocket := WebSocketHandshakeMetadata{
		StatusCode: 101,
		Protocol:   "HTTP/1.1",
		Headers: http.Header{
			"X-Response-Credential": {secret},
			"X-Debug":               {"echo " + secret},
		},
		SensitiveHeaders:   sensitiveNames,
		CredentialEvidence: credentials,
	}
	wsResult := (Sanitizer{}).WebSocketHandshakeDetailed(webSocket, nil, false)
	if wsResult.RedactAll || wsResult.Truncated || wsResult.Snapshot.StatusCode != 101 ||
		wsResult.Snapshot.Headers["X-Response-Credential"][0] != RedactedValue ||
		strings.Contains(wsResult.Snapshot.Headers["X-Debug"][0], secret) {
		t.Fatalf("WebSocket handshake sanitization = %#v", wsResult)
	}
	if got := (Sanitizer{}).WebSocketHandshake(webSocket); got.StatusCode != 101 {
		t.Fatalf("WebSocketHandshake wrapper = %#v", got)
	}
}

func TestResponseMetadataBoundsAndFailsClosed(t *testing.T) {
	longProtocol := strings.Repeat("p", MaxRetainedIdentifierBytes+1)
	longTrailer := strings.Repeat("t", MaxRetainedHeaderNameBytes+1)
	response := HTTPResponseMetadata{
		Protocol:         longProtocol,
		Headers:          http.Header{"X-Debug": {"secret"}},
		DeclaredTrailers: http.Header{longTrailer: nil},
	}
	result := (Sanitizer{}).HTTPResponseDetailed(response, nil, false)
	if !result.RedactAll || !result.Truncated || result.Snapshot.Protocol != "[TRUNCATED_PROTOCOL]" ||
		result.Snapshot.Headers["X-Debug"][0] != RedactedValue || len(result.Snapshot.DeclaredTrailerKeys) != 0 {
		t.Fatalf("unsealed response metadata = %#v", result)
	}

	tooManyTrailers := make(http.Header, MaxRetainedHeaderFields+1)
	for index := 0; index <= MaxRetainedHeaderFields; index++ {
		tooManyTrailers[string(rune(index+1))] = nil
	}
	response = HTTPResponseMetadata{
		DeclaredTrailers:   tooManyTrailers,
		SensitiveHeaders:   sealedSensitiveHeadersForTest(),
		CredentialEvidence: sealedCredentialsForTest(),
	}
	result = (Sanitizer{}).HTTPResponseDetailed(response, nil, false)
	if !result.Truncated || result.Snapshot.DeclaredTrailerKeys != nil {
		t.Fatalf("oversized declared trailers = %#v", result)
	}

	webSocket := WebSocketHandshakeMetadata{
		Protocol:           longProtocol,
		Headers:            http.Header{"X-Debug": {"secret"}},
		SensitiveHeaders:   sealedSensitiveHeadersForTest(),
		CredentialEvidence: sealedCredentialsForTest(),
	}
	wsResult := (Sanitizer{}).WebSocketHandshakeDetailed(webSocket, nil, true)
	if !wsResult.RedactAll || !wsResult.Truncated || wsResult.Snapshot.Protocol != "[TRUNCATED_PROTOCOL]" ||
		wsResult.Snapshot.Headers["X-Debug"][0] != RedactedValue {
		t.Fatalf("bounded WebSocket metadata = %#v", wsResult)
	}
}
