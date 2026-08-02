package requestcapture

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"unsafe"
)

func TestSanitizerHeadersCaseInsensitiveAndCustom(t *testing.T) {
	input := http.Header{
		"aUtHoRiZaTiOn":       {"Bearer auth-secret"},
		"Proxy-Authorization": {"Basic proxy-secret"},
		"Cookie":              {"cookie-secret"},
		"Set-Cookie":          {"set-cookie-secret"},
		"X-API-Key":           {"x-secret"},
		"API-Key":             {"api-secret"},
		"X-Goog-API-Key":      {"google-secret"},
		"ChatGPT-Account-Id":  {"account-secret"},
		"X-Safe":              {"safe"},
	}
	output := (sanitizer{}).headers(input, []string{"chatgpt-account-id"})
	for name, values := range output {
		if strings.EqualFold(name, "X-Safe") {
			if values[0] != "safe" {
				t.Fatalf("safe header = %q", values[0])
			}
			continue
		}
		if values[0] != redactedValue {
			t.Fatalf("%s = %q", name, values[0])
		}
	}
	output["X-Safe"][0] = "changed"
	if input["X-Safe"][0] != "safe" {
		t.Fatal("sanitizer aliased source values")
	}
}

func TestSanitizerURLRemovesUserInfoAndSensitiveQueries(t *testing.T) {
	raw := "https://user:pass@example.test/path?token=one&TOKEN=two&safe=credential-value&encoded=credential%2Dvalue"
	output := (sanitizer{}).url(raw, []string{"credential-value"})
	if strings.Contains(output, "user") || strings.Contains(output, "pass") ||
		strings.Contains(output, "one") || strings.Contains(output, "two") ||
		strings.Contains(output, "credential-value") {
		t.Fatalf("URL leaked secret: %q", output)
	}
	parsed, err := url.Parse(output)
	if err != nil {
		t.Fatalf("sanitized URL parse error = %v", err)
	}
	if parsed.User != nil {
		t.Fatalf("sanitized URL retained userinfo: %q", output)
	}
	query := parsed.Query()
	for _, key := range []string{"token", "TOKEN", "safe", "encoded"} {
		for _, value := range query[key] {
			if value != redactedValue {
				t.Fatalf("query %q value = %q in %q", key, value, output)
			}
		}
	}
}

func TestSanitizedTextScrubsStructuredAndAuthForms(t *testing.T) {
	input := "Bearer bearer-secret API_KEY=key-secret exact-secret"
	output := sanitizedText(input, []string{"exact-secret"}, maxRetainedErrorBytes, "FAILURE_MESSAGE").value
	for _, secret := range []string{"bearer-secret", "key-secret", "exact-secret"} {
		if strings.Contains(output, secret) {
			t.Fatalf("text leaked %q: %q", secret, output)
		}
	}
	if strings.Count(output, redactedValue) < 3 {
		t.Fatalf("text was not redacted: %q", output)
	}

	plain := sanitizedText("password: hunter2", nil, maxRetainedErrorBytes, "FAILURE_MESSAGE").value
	if strings.Contains(plain, "hunter2") {
		t.Fatalf("auth-shaped text leaked: %q", plain)
	}
}

func TestSanitizerSnapshotsWhitelistAndSort(t *testing.T) {
	attempt := AttemptMetadata{
		Provider: ProviderIdentity{ID: "provider", Name: "Provider"},
		APIType:  "claude",
	}
	raw := RawRequest{
		Method:             http.MethodPost,
		Headers:            http.Header{"X-Credential": {"secret"}},
		Trailers:           http.Header{"Authorization": {"secret"}},
		ContentLength:      12,
		SensitiveHeaders:   testSensitiveHeaderEvidence("X-Credential"),
		CredentialEvidence: testCredentialEvidence("secret"),
	}
	s := sanitizer{}
	target := borrowedWebSocketTarget("https://example.test/path?key=secret")
	provider := s.provider(attempt, raw, target)
	if provider.ID != "provider" || provider.Name != "Provider" || provider.APIType != "claude" {
		t.Fatalf("provider snapshot = %#v", provider)
	}
	if strings.Contains(provider.TargetURL, "secret") {
		t.Fatalf("provider target leaked: %q", provider.TargetURL)
	}
	request := s.request(raw, target)
	if request.Headers["X-Credential"][0] != redactedValue ||
		request.Trailers["Authorization"][0] != redactedValue {
		t.Fatalf("request snapshot leaked: %#v", request)
	}
	response := s.httpResponse(HTTPResponseHead{
		StatusCode:       http.StatusOK,
		Protocol:         "HTTP/2.0",
		DeclaredTrailers: http.Header{"z": nil, "a": nil},
		Headers:          http.Header{"Set-Cookie": {"secret"}},
	})
	if response.DeclaredTrailerKeys[0] != "a" || response.Headers["Set-Cookie"][0] != redactedValue {
		t.Fatalf("HTTP response snapshot = %#v", response)
	}
	handshake := s.webSocketHandshake(WebSocketHandshake{
		StatusCode: http.StatusSwitchingProtocols,
		Headers:    http.Header{"Cookie": {"secret"}},
	})
	if handshake.Headers["Cookie"][0] != redactedValue {
		t.Fatalf("handshake = %#v", handshake)
	}
}

func TestSanitizerScrubsCredentialValuesFromEveryHeaderSurface(t *testing.T) {
	const secret = "credential-secret"
	s := sanitizer{}

	request := s.request(RawRequest{
		Headers:            http.Header{"X-Debug": {"prefix " + secret}},
		Trailers:           http.Header{"X-Trailer": {"token=" + secret}},
		CredentialEvidence: testCredentialEvidence(secret),
	})
	for name, values := range map[string][]string{
		"request header":  request.Headers["X-Debug"],
		"request trailer": request.Trailers["X-Trailer"],
	} {
		if len(values) != 1 || strings.Contains(values[0], secret) {
			t.Fatalf("%s leaked credential: %#v", name, values)
		}
	}

	response := s.httpResponse(HTTPResponseHead{
		Headers:            http.Header{"X-Debug": {"prefix " + secret}},
		CredentialEvidence: testCredentialEvidence(secret),
	})
	if got := response.Headers["X-Debug"][0]; strings.Contains(got, secret) {
		t.Fatalf("response header leaked credential: %q", got)
	}

	handshake := s.webSocketHandshake(WebSocketHandshake{
		Headers:            http.Header{"X-Debug": {"refresh_token=" + secret}},
		CredentialEvidence: testCredentialEvidence(secret),
	})
	if got := handshake.Headers["X-Debug"][0]; strings.Contains(got, secret) {
		t.Fatalf("websocket handshake header leaked credential: %q", got)
	}

	authShaped := s.headersDetailed(
		http.Header{"X-Debug": {"Bearer opaque-token"}},
		nil,
		nil,
		false,
	)
	if got := authShaped.value["X-Debug"][0]; strings.Contains(got, "opaque-token") {
		t.Fatalf("authentication-shaped value leaked without credential metadata: %q", got)
	}
}

func TestSanitizerDiscoversCredentialsAcrossEachHeaderMap(t *testing.T) {
	s := sanitizer{}
	request := s.request(RawRequest{
		Headers: http.Header{
			"Authorization": {"Bearer auth-secret"},
			"Cookie":        {"session=cookie-secret"},
			"X-Debug":       {"auth-secret cookie-secret"},
		},
		Trailers: http.Header{"X-Trailer": {"auth-secret cookie-secret"}},
	}, borrowedWebSocketTarget("https://auth-secret.example.test/path?debug=auth-secret&cookie=cookie-secret"))
	for surface, value := range map[string]string{
		"request URL":     request.URL,
		"request host":    request.Host,
		"request header":  request.Headers["X-Debug"][0],
		"request trailer": request.Trailers["X-Trailer"][0],
	} {
		if strings.Contains(value, "auth-secret") || strings.Contains(value, "cookie-secret") {
			t.Fatalf("%s leaked discovered credential: %q", surface, value)
		}
	}

	response := s.httpResponse(HTTPResponseHead{Headers: http.Header{
		"X-Auth-Token": {"response-secret"},
		"X-Debug":      {"response-secret"},
	}})
	if got := response.Headers["X-Debug"][0]; strings.Contains(got, "response-secret") {
		t.Fatalf("response header leaked discovered credential: %q", got)
	}
	response = s.httpResponse(HTTPResponseHead{Headers: http.Header{
		"Set-Cookie": {"session=cookie-secret; Path=/"},
		"X-Debug":    {"cookie-secret"},
	}})
	if got := response.Headers["X-Debug"][0]; strings.Contains(got, "cookie-secret") {
		t.Fatalf("response cookie component leaked: %q", got)
	}

	handshake := s.webSocketHandshake(WebSocketHandshake{Headers: http.Header{
		"X-Amz-Security-Token": {"websocket-secret"},
		"X-Debug":              {"websocket-secret"},
	}})
	if got := handshake.Headers["X-Debug"][0]; strings.Contains(got, "websocket-secret") {
		t.Fatalf("websocket handshake leaked discovered credential: %q", got)
	}

	trailers := s.headersDetailed(
		http.Header{
			"X-Access-Token": {"trailer-secret"},
			"X-Debug":        {"trailer-secret"},
		},
		nil,
		nil,
		false,
	)
	if got := trailers.value["X-Debug"][0]; strings.Contains(got, "trailer-secret") {
		t.Fatalf("response trailer leaked discovered credential: %q", got)
	}
}

func TestSanitizerStructuredCredentialValuesAndReplacementAreBounded(t *testing.T) {
	input := `prepare failed: {"access_token":["array-secret"],"nested":{"client_assertion":{"value":"assertion-secret"}},"vendor_signature":["signature-secret"]}`
	output := scrubText(input, []string{"E", "E", "array-secret"})
	for _, secret := range []string{"array-secret", "assertion-secret", "signature-secret"} {
		if strings.Contains(output, secret) {
			t.Fatalf("structured metadata leaked %q: %q", secret, output)
		}
	}
	if len(output) > len(input)+8*len(redactedValue) {
		t.Fatalf("single-pass replacement expanded unexpectedly: input=%d output=%d", len(input), len(output))
	}

	ambiguous := scrubText(`{"access_token":[unterminated`, nil)
	if ambiguous != redactedValue {
		t.Fatalf("ambiguous credential JSON = %q, want fail-closed marker", ambiguous)
	}
}

func TestCredentialReplacerUsesLeftmostLongestTrieScan(t *testing.T) {
	got := replaceCredentialValues("aabc/shorter", []string{"aa", "abc", "bc", "short", "shorter"})
	want := redactedValue + redactedValue + "/" + redactedValue
	if got != want {
		t.Fatalf("replaceCredentialValues() = %q, want %q", got, want)
	}

	oversized := strings.Repeat("s", maxRetainedCredentialValueBytes+1)
	if got := replaceCredentialValues("safe", []string{oversized}); got != redactedValue {
		t.Fatalf("oversized credential set = %q, want fail-closed marker", got)
	}
}

func TestSanitizerTightClonesRetainedSubstrings(t *testing.T) {
	backing := strings.Repeat("x", 1<<20)
	small := backing[:16]
	request := (sanitizer{}).request(RawRequest{
		Method:  small,
		Headers: http.Header{small: {small}},
	})
	if unsafe.StringData(request.Method) == unsafe.StringData(small) {
		t.Fatal("method retained the caller's oversized backing allocation")
	}
	for name, values := range request.Headers {
		if unsafe.StringData(name) == unsafe.StringData(small) ||
			unsafe.StringData(values[0]) == unsafe.StringData(small) {
			t.Fatal("header retained the caller's oversized backing allocation")
		}
	}
	if got := boundedRedaction("ERROR", backing); got != "[TRUNCATED_ERROR]" {
		t.Fatalf("oversized marker = %q", got)
	}
}

func TestSanitizerNormalizesSensitiveQueryKeys(t *testing.T) {
	raw := "https://example.test/path?refresh_token=one&id-token=two&client_secret=three&session-token=four&token%5B%5D=five&api-key=six&X-Amz-Signature=aws-signature&X-Amz-Security-Token=aws-token&X-Amz-Credential=aws-credential&X-Goog-Signature=goog-signature&X-Goog-Credential=goog-credential&sig=azure-signature&safe=visible"
	output := (sanitizer{}).url(raw, nil)
	parsed, err := url.Parse(output)
	if err != nil {
		t.Fatalf("parse sanitized URL: %v", err)
	}
	query := parsed.Query()
	for _, key := range []string{
		"refresh_token", "id-token", "client_secret", "session-token", "token[]", "api-key",
		"X-Amz-Signature", "X-Amz-Security-Token", "X-Amz-Credential",
		"X-Goog-Signature", "X-Goog-Credential", "sig",
	} {
		if got := query.Get(key); got != redactedValue {
			t.Fatalf("query %q = %q, want redacted", key, got)
		}
	}
	if got := query.Get("safe"); got != "visible" {
		t.Fatalf("safe query = %q", got)
	}
}

func TestSanitizerScrubsJSONCredentialShapesWithoutCredentialMetadata(t *testing.T) {
	input := `prepare failed: {"access_token":"access-secret","refresh_token":"refresh-secret","client_secret":"client-secret","id_token":"id-secret","session_token":"session-secret"}`
	output := sanitizedText(input, nil, maxRetainedErrorBytes, "FAILURE_MESSAGE").value
	for _, secret := range []string{
		"access-secret", "refresh-secret", "client-secret", "id-secret", "session-secret",
	} {
		if strings.Contains(output, secret) {
			t.Fatalf("preparation error leaked %q: %q", secret, output)
		}
	}
}

func TestSanitizedMetadataNeverLeaksThroughStoreQueryOrExport(t *testing.T) {
	manager := newTestManager(t, nil)
	session := startTestSession(t, manager, 4, 1<<20, "selected")
	attempt := AttemptMetadata{Provider: ProviderIdentity{ID: "selected"}}

	httpGateway := manager.BeginGateway(GatewayStart{GatewayRequestID: "sanitizer-http"})
	httpGateway.Transition(TransitionStart{
		Attempt: attempt,
		Target: HTTPTransitionTarget(
			testParsedURL("https://example.test?X-Amz-Signature=transition-secret"),
		),
		Failure: testFailure(`prepare: {"access_token":["transition-secret"]}`),
	})
	httpRecorder := httpGateway.BeginHTTP(RawHTTPStart{
		Attempt: attempt,
		URL:     testParsedURL("https://request-secret.example.test/path?debug=request-secret"),
		Request: RawRequest{
			Headers: http.Header{
				"X-Auth-Token": {"request-secret"},
				"X-Debug":      {"request-secret"},
			},
		},
	})
	httpRecorder.ObserveResponse(HTTPResponseHead{Headers: http.Header{
		"Set-Cookie": {"session=response-secret; Path=/"},
		"X-Debug":    {"response-secret"},
	}})
	httpRecorder.Finish(Outcome{
		SourceCompletion: SourceCompletionPartial,
		ResponseTrailers: http.Header{
			"X-Access-Token": {"trailer-secret"},
			"X-Debug":        {"response-secret trailer-secret"},
		},
		Failure: testFailure(`upstream: {"client_assertion":["response-secret"]}`),
	})
	httpGateway.Finish(GatewayOutcome{})

	wsGateway := manager.BeginGateway(GatewayStart{GatewayRequestID: "sanitizer-ws"})
	wsRecorder := wsGateway.BeginWebSocket(RawWebSocketStart{Attempt: attempt})
	wsRecorder.ObserveWebSocketHandshake(WebSocketHandshake{Headers: http.Header{
		"X-Amz-Security-Token": {"websocket-secret"},
		"X-Debug":              {"websocket-secret"},
	}})
	wsRecorder.Finish(Outcome{
		SourceCompletion:  SourceCompletionComplete,
		TerminationReason: TerminationReasonWebSocketClose,
		WebSocketClose: &WebSocketCloseObservation{
			Direction: MessageDirectionUpstreamToClient,
			Code:      1000,
			Reason:    `{"access_token":["websocket-secret"]}`,
			Clean:     true,
		},
	})
	wsGateway.Finish(GatewayOutcome{})

	secrets := []string{
		"transition-secret", "request-secret", "response-secret", "trailer-secret", "websocket-secret",
	}
	httpRecord := testRecordState(t, httpRecorder)
	wsRecord := testRecordState(t, wsRecorder)
	if httpRecord == nil || wsRecord == nil {
		t.Fatal("value recorder lookup failed")
	}
	state := manager.active.Load()
	state.mu.Lock()
	stored := []string{
		httpRecord.request.URL,
		httpRecord.request.Host,
		httpRecord.request.Headers["X-Debug"][0],
		httpRecord.httpResponse.Headers["X-Debug"][0],
		httpRecord.httpResponse.Trailers["X-Debug"][0],
		httpRecord.summary.Failure.Primary.Message,
		wsRecord.wsHandshake.Headers["X-Debug"][0],
		wsRecord.wsClose.Reason,
	}
	for entry := httpRecord.gateway.entryFirst; entry != nil; entry = entry.after {
		stored = append(stored, entry.snapshot.Failure.Primary.Message, entry.snapshot.Provider.TargetURL)
	}
	state.mu.Unlock()
	for _, value := range stored {
		for _, secret := range secrets {
			if strings.Contains(value, secret) {
				t.Fatalf("stored metadata leaked %q in %q", secret, value)
			}
		}
	}

	var queryPayload bytes.Buffer
	for _, recordID := range []string{httpRecorder.ID(), wsRecorder.ID()} {
		detail, err := readRecordDetailForTest(t, manager, session.SessionID, recordID, 64)
		if err != nil {
			t.Fatalf("query record %s: %v", recordID, err)
		}
		encoded, err := json.Marshal(detail)
		if err != nil {
			t.Fatalf("encode query detail: %v", err)
		}
		queryPayload.Write(encoded)
	}
	for _, secret := range secrets {
		if strings.Contains(queryPayload.String(), secret) {
			t.Fatalf("query payload leaked %q", secret)
		}
	}

	ticket, err := manager.CreateExport(context.Background(), session.SessionID, ExportRequest{Scope: ExportScopeAll})
	if err != nil {
		t.Fatalf("CreateExport() error = %v", err)
	}
	download, err := manager.AcceptDownload(ticket.ExportID, ticket.DownloadToken)
	if err != nil {
		t.Fatalf("AcceptDownload() error = %v", err)
	}
	var exportPayload bytes.Buffer
	if err := download.WriteTo(context.Background(), &exportPayload); err != nil {
		t.Fatalf("WriteTo() error = %v", err)
	}
	for _, secret := range secrets {
		if strings.Contains(exportPayload.String(), secret) {
			t.Fatalf("export payload leaked %q", secret)
		}
	}
}

func TestSanitizerMalformedURLStillScrubsExactSecret(t *testing.T) {
	output := (sanitizer{}).url("://exact-secret", []string{"exact-secret"})
	if output != invalidURLRedaction {
		t.Fatalf("malformed URL = %q, want fail-closed redaction", output)
	}
}

func TestFailureEvidenceMustBeSealed(t *testing.T) {
	failure := testFailure("opaque diagnostic")
	s := sanitizer{}

	unsealed, hasFailure := s.failureDetailed(failure, CredentialEvidence{}, false)
	if !hasFailure || unsealed.Primary.Message != "" || !unsealed.Truncated {
		t.Fatalf("unsealed failure = %#v, want blank truncated message", unsealed)
	}

	var inspectedEmpty CredentialEvidence
	inspectedEmpty.Seal()
	sealed, hasFailure := s.failureDetailed(failure, inspectedEmpty, false)
	if !hasFailure || sealed.Primary.Message != "opaque diagnostic" || sealed.Truncated {
		t.Fatalf("sealed empty evidence failure = %#v", sealed)
	}

	inspectedEmpty.Add("secret")
	if inspectedEmpty.Sealed() {
		t.Fatal("Add retained an obsolete evidence seal")
	}
	mutated, _ := s.failureDetailed(failure, inspectedEmpty, false)
	if mutated.Primary.Message != "" || !mutated.Truncated {
		t.Fatalf("mutated evidence failure = %#v, want fail-closed result", mutated)
	}

	inspectedEmpty.Seal()
	redacted, _ := s.failureDetailed(testFailure("upstream secret"), inspectedEmpty, false)
	if strings.Contains(redacted.Primary.Message, "secret") || !redacted.Truncated {
		t.Fatalf("sealed evidence did not redact exact credential: %#v", redacted)
	}
}

func TestHostileEnumsCollapseToStaticSentinels(t *testing.T) {
	hostile := strings.Repeat("x", 1<<20)
	attempt, truncated := boundedAttemptMetadata(AttemptMetadata{
		APIType:         hostile,
		SelectionMode:   SelectionMode(hostile),
		SelectionSource: SelectionSource(hostile),
		CredentialPhase: CredentialPhase(hostile),
	})
	if !truncated ||
		attempt.SelectionMode != SelectionModeUnknown ||
		attempt.SelectionSource != SelectionSourceUnknown ||
		attempt.CredentialPhase != CredentialPhaseUnknown {
		t.Fatalf("hostile attempt was not canonicalized: %#v", attempt)
	}
	if len(attempt.APIType) > maxRetainedAPITypeBytes || strings.Contains(attempt.APIType, hostile[:1024]) {
		t.Fatalf("hostile API type was retained: length=%d", len(attempt.APIType))
	}
	termination := retainedTerminationReason(TerminationReason(hostile))
	completion := retainedSourceCompletion(SourceCompletion(hostile))
	if termination.Value != string(TerminationReasonUnknown) || !termination.Truncated ||
		completion.Value != string(SourceCompletionUnknown) || !completion.Truncated {
		t.Fatalf("hostile completion enums survived: termination=%#v completion=%#v", termination, completion)
	}
}

func TestProviderFailureDiagnosticsRequireSemanticCodeAndSealedEvidence(t *testing.T) {
	input := FailureObservation{Primary: FailureFact{
		Site:              FailureSiteWebSocketMessage,
		Peer:              FailurePeerProvider,
		Class:             FailureClassUpstreamSemantic,
		Code:              FailureCodeProviderSemantic,
		ProviderErrorType: "quota_secret",
		ProviderErrorCode: "secret_code",
		Message:           "provider rejected secret",
	}}
	sealed := testCredentialEvidence("secret")
	result, hasFailure := (sanitizer{}).failureDetailed(input, sealed, false)
	if !hasFailure {
		t.Fatal("provider failure was not retained")
	}
	for _, value := range []string{
		result.Primary.ProviderErrorType,
		result.Primary.ProviderErrorCode,
		result.Primary.Message,
	} {
		if strings.Contains(value, "secret") {
			t.Fatalf("provider diagnostic leaked credential: %#v", result)
		}
	}
	if !result.Truncated {
		t.Fatal("credential replacement was not reflected as truncation")
	}

	input.Primary.Code = FailureCodeRoundTrip
	result, _ = (sanitizer{}).failureDetailed(input, sealed, false)
	if result.Primary.ProviderErrorType != "" || result.Primary.ProviderErrorCode != "" ||
		!result.Truncated {
		t.Fatalf("non-semantic provider fields survived: %#v", result)
	}

	result, _ = (sanitizer{}).failureDetailed(input, CredentialEvidence{}, false)
	if result.Primary.ProviderErrorType != "" || result.Primary.ProviderErrorCode != "" ||
		result.Primary.Message != "" || !result.Truncated {
		t.Fatalf("unsealed provider diagnostics survived: %#v", result)
	}
}

func TestCredentialEvidenceOverflowFailsClosed(t *testing.T) {
	var evidence CredentialEvidence
	evidence.Add(strings.Repeat("x", maxRetainedCredentialValueBytes+1))
	evidence.Seal()
	if !evidence.Sealed() || !evidence.Overflowed() {
		t.Fatalf("overflow evidence state = sealed:%t overflow:%t", evidence.Sealed(), evidence.Overflowed())
	}
	failure, _ := (sanitizer{}).failureDetailed(testFailure("opaque diagnostic"), evidence, false)
	if failure.Primary.Message != "" || !failure.Truncated {
		t.Fatalf("overflow failure = %#v, want blank truncated message", failure)
	}
}

func TestInitialCredentialDiscoveryDoesNotPoisonSealedLaterFailure(t *testing.T) {
	request := (sanitizer{}).requestDetailed(
		RawRequest{
			Headers:            http.Header{"Authorization": {"Bearer request-secret"}},
			SensitiveHeaders:   testSensitiveHeaderEvidence(),
			CredentialEvidence: testCredentialEvidence("Bearer request-secret", "request-secret"),
		},
		requestTarget{},
	)
	if request.redactAll {
		t.Fatal("bounded initial credential discovery poisoned unrelated later metadata")
	}

	const responseCredential = "session=response-secret; Path=/"
	responseEvidence := testCredentialEvidence(responseCredential, "response-secret")
	response := (sanitizer{}).httpResponseDetailed(
		HTTPResponseHead{
			Headers:            http.Header{"Set-Cookie": {responseCredential}},
			SensitiveHeaders:   testSensitiveHeaderEvidence(),
			CredentialEvidence: responseEvidence,
		},
		nil,
		false,
	)
	if response.redactAll {
		t.Fatal("sealed complete response evidence unnecessarily poisoned later metadata")
	}

	response = (sanitizer{}).httpResponseDetailed(
		HTTPResponseHead{
			Headers:            http.Header{"Set-Cookie": {responseCredential}},
			SensitiveHeaders:   testSensitiveHeaderEvidence(),
			CredentialEvidence: testCredentialEvidence(),
		},
		nil,
		false,
	)
	if !response.redactAll {
		t.Fatal("unavailable response evidence did not poison later opaque metadata")
	}
}
