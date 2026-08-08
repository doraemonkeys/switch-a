package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/requestcapture"
	"github.com/doraemonkeys/switch-a/internal/requestcapture/capturefailure"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis"
	"github.com/doraemonkeys/switch-a/internal/upstreamtransport"

	"go.uber.org/zap"
)

type disabledCaptureProbe struct {
	enabledCalls int
	beginCalls   int
}

func (p *disabledCaptureProbe) Enabled() bool {
	p.enabledCalls++
	return false
}

func (p *disabledCaptureProbe) BeginGateway(requestcapture.GatewayStart) requestcapture.GatewayRecorder {
	p.beginCalls++
	return requestcapture.GatewayRecorder{}
}

var gatewayRecorderSink requestcapture.GatewayRecorder

func TestCaptureReasonForClientTermination(t *testing.T) {
	tests := []struct {
		name        string
		termination clientTermination
		want        requestcapture.TerminationReason
	}{
		{name: "no client termination", want: requestcapture.TerminationReasonCanceled},
		{name: "disconnect", termination: clientTerminationDisconnect, want: requestcapture.TerminationReasonClientDisconnect},
		{name: "timeout", termination: clientTerminationTimeout, want: requestcapture.TerminationReasonTimeout},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := captureReasonForClientTermination(test.termination); got != test.want {
				t.Fatalf("captureReasonForClientTermination() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestBeginGatewayCaptureDisabledDoesNotBuildRecorderOrAllocate(t *testing.T) {
	probe := &disabledCaptureProbe{}
	handler := &Handler{capture: probe}

	allocations := testing.AllocsPerRun(1000, func() {
		gatewayRecorderSink = handler.beginGatewayCapture("gateway-request", time.Time{})
	})

	if allocations != 0 {
		t.Fatalf("disabled capture allocations = %v, want 0", allocations)
	}
	if probe.beginCalls != 0 {
		t.Fatalf("BeginGateway calls = %d, want 0", probe.beginCalls)
	}
	if probe.enabledCalls == 0 {
		t.Fatal("Enabled was not checked")
	}
	if gatewayRecorderSink.Valid() {
		t.Fatal("disabled capture returned a valid recorder")
	}
}

func TestHTTPCaptureRedactsOnlyInjectedAPIKey(t *testing.T) {
	t.Parallel()

	credentialComponents := []string{
		"authorization-secret",
		"proxy-secret",
		"cookie-secret",
		"quoted-cookie-secret",
		"set-cookie-secret",
		"x-api-value",
		"api-secret",
		"goog-api-value",
		"access-token-secret",
		"amz-credential-secret",
		"amz-security-secret",
		"auth-token-secret",
		"goog-credential-secret",
		"account-id-secret",
	}
	headers := http.Header{
		"Authorization":        {"Custom\t" + credentialComponents[0]},
		"Proxy-Authorization":  {"Basic " + credentialComponents[1]},
		"Cookie":               {`session=` + credentialComponents[2] + `; quoted="` + credentialComponents[3] + `"`},
		"Set-Cookie":           {"session=" + credentialComponents[4] + "; Path=/"},
		"X-API-Key":            {credentialComponents[5]},
		"API-Key":              {credentialComponents[6]},
		"X-Goog-API-Key":       {credentialComponents[7]},
		"X-Access-Token":       {credentialComponents[8]},
		"X-Amz-Credential":     {credentialComponents[9]},
		"X-Amz-Security-Token": {credentialComponents[10]},
		"X-Auth-Token":         {credentialComponents[11]},
		"X-Goog-Credential":    {credentialComponents[12]},
		"ChatGPT-Account-Id":   {credentialComponents[13]},
	}
	sensitiveHeaders, credentialEvidence := captureCredentialMaterial(credentialComponents[6])
	if !sensitiveHeaders.Sealed() || sensitiveHeaders.Overflowed() ||
		!credentialEvidence.Sealed() || credentialEvidence.Overflowed() {
		t.Fatalf(
			"collector state = sensitive(sealed:%t overflow:%t) credentials(sealed:%t overflow:%t)",
			sensitiveHeaders.Sealed(),
			sensitiveHeaders.Overflowed(),
			credentialEvidence.Sealed(),
			credentialEvidence.Overflowed(),
		)
	}

	provider := captureTestProvider("https://provider.invalid")
	manager, session := startCaptureTestManager(t, []requestcapture.ProviderIdentity{{
		ID:   provider.ID,
		Name: provider.Name,
	}})
	defer manager.Close()
	gateway := manager.BeginGateway(requestcapture.GatewayStart{GatewayRequestID: "credential-components"})
	pctx := &proxyContext{
		apiType:             APITypeClaude,
		body:                []byte(`{"model":"claude-3"}`),
		capture:             gateway,
		captureParticipates: true,
	}
	request := httptest.NewRequest(http.MethodPost, "https://provider.invalid/v1/messages", nil)
	request.Header = headers
	handler := &Handler{logger: zap.NewNop()}
	exchange := handler.beginHTTPExchange(
		pctx,
		httpAttemptContext{
			provider:        &provider,
			selectionMode:   requestcapture.SelectionModeInitial,
			selectionSource: requestcapture.SelectionSourceStrategy,
		},
		requestcapture.CredentialPhaseInitial,
		request,
		credentialComponents[6],
	)
	fact := capturefailure.Fact(
		requestcapture.FailureSiteTransport,
		requestcapture.FailurePeerUpstream,
		requestcapture.FailureClassTransport,
		requestcapture.FailureCodeRoundTrip,
	)
	fact.Message = strings.Join(credentialComponents, "|")
	exchange.finish(
		nil,
		requestcapture.SourceCompletionPartial,
		requestcapture.TerminationReasonTransportError,
		capturefailure.Observation(fact, requestcapture.FailureFact{}),
	)
	gateway.Finish(requestcapture.GatewayOutcome{})

	page, err := readCaptureTestPage(manager, session, requestcapture.ListQuery{Limit: 1})
	if err != nil {
		t.Fatalf("ListRecords() error = %v", err)
	}
	if len(page.Records) != 1 || !page.Records[0].HasFailure {
		t.Fatalf("records = %#v, want one structured failure", page.Records)
	}
	message := page.Records[0].Failure.Primary.Message
	if message == "" || !strings.Contains(message, "[REDACTED]") {
		t.Fatalf("redacted diagnostic = %q, want retained redaction markers", message)
	}
	if strings.Contains(message, credentialComponents[6]) {
		t.Fatalf("diagnostic retained injected API key: %q", message)
	}
	for index, value := range credentialComponents {
		if index != 6 && !strings.Contains(message, value) {
			t.Fatalf("diagnostic removed non-injected value %q: %q", value, message)
		}
	}
}

func TestHTTPCapturePreservesResponseCredentialsInTerminalDiagnostic(t *testing.T) {
	t.Parallel()

	const responseCredential = "response-cookie-secret"
	provider := captureTestProvider("https://provider.invalid")
	manager, session := startCaptureTestManager(t, []requestcapture.ProviderIdentity{{
		ID:   provider.ID,
		Name: provider.Name,
	}})
	defer manager.Close()
	gateway := manager.BeginGateway(requestcapture.GatewayStart{GatewayRequestID: "response-evidence"})
	pctx := &proxyContext{
		apiType:             APITypeClaude,
		body:                []byte(`{"model":"claude-3"}`),
		capture:             gateway,
		captureParticipates: true,
	}
	request := httptest.NewRequest(http.MethodPost, "https://provider.invalid/v1/messages", nil)
	handler := &Handler{logger: zap.NewNop()}
	exchange := handler.beginHTTPExchange(
		pctx,
		httpAttemptContext{
			provider:        &provider,
			selectionMode:   requestcapture.SelectionModeInitial,
			selectionSource: requestcapture.SelectionSourceStrategy,
		},
		requestcapture.CredentialPhaseInitial,
		request,
		"",
	)
	head := upstreamtransport.ResponseHead{
		StatusCode:    http.StatusOK,
		Protocol:      "HTTP/1.1",
		SourceHeader:  http.Header{"Content-Type": {"application/json"}},
		Header:        http.Header{"Content-Type": {"application/json"}},
		Trailer:       make(http.Header),
		ContentLength: -1,
	}
	head.SourceHeader.Set("Set-Cookie", "session="+responseCredential+"; Path=/")
	body := exchange.observeResponse(head, io.NopCloser(strings.NewReader("ignored")))
	_, _ = io.Copy(io.Discard, body)
	_ = body.Close()
	fact := capturefailure.Fact(
		requestcapture.FailureSiteResponseRead,
		requestcapture.FailurePeerUpstream,
		requestcapture.FailureClassRead,
		requestcapture.FailureCodeUpstreamRead,
	)
	fact.Message = "later diagnostic contains " + responseCredential
	exchange.finish(
		head.Trailer,
		requestcapture.SourceCompletionPartial,
		requestcapture.TerminationReasonReadError,
		capturefailure.Observation(fact, requestcapture.FailureFact{}),
	)
	gateway.Finish(requestcapture.GatewayOutcome{})

	page, err := readCaptureTestPage(manager, session, requestcapture.ListQuery{Limit: 1})
	if err != nil {
		t.Fatalf("ListRecords() error = %v", err)
	}
	if len(page.Records) != 1 || !page.Records[0].HasFailure {
		t.Fatalf("records = %#v, want one structured failure", page.Records)
	}
	message := page.Records[0].Failure.Primary.Message
	if !strings.Contains(message, responseCredential) || strings.Contains(message, "[REDACTED]") {
		t.Fatalf("response diagnostic = %q, want provider credential unchanged", message)
	}
}
func TestHTTPCaptureClientCancellationRecordsClientFacingFailure(t *testing.T) {
	t.Parallel()

	provider := captureTestProvider("https://provider.invalid")
	manager, session := startCaptureTestManager(t, []requestcapture.ProviderIdentity{{
		ID:   provider.ID,
		Name: provider.Name,
	}})
	defer manager.Close()
	gateway := manager.BeginGateway(requestcapture.GatewayStart{GatewayRequestID: "client-cancel"})

	// Cancel the client request context before the exchange finishes: a client
	// disconnect must be recorded as a client-facing cancellation, never as an
	// upstream read failure, so downstream diagnostics are not misled.
	request := httptest.NewRequest(http.MethodPost, "https://provider.invalid/v1/messages", nil)
	canceledCtx, cancel := context.WithCancel(request.Context())
	cancel()
	request = request.WithContext(canceledCtx)

	pctx := &proxyContext{
		apiType:             APITypeClaude,
		r:                   request,
		body:                []byte(`{"model":"claude-3"}`),
		capture:             gateway,
		captureParticipates: true,
	}
	handler := &Handler{logger: zap.NewNop()}
	exchange := handler.beginHTTPExchange(
		pctx,
		httpAttemptContext{
			provider:        &provider,
			selectionMode:   requestcapture.SelectionModeInitial,
			selectionSource: requestcapture.SelectionSourceStrategy,
		},
		requestcapture.CredentialPhaseInitial,
		request,
		"",
	)
	head := upstreamtransport.ResponseHead{
		StatusCode:    http.StatusOK,
		Protocol:      "HTTP/1.1",
		SourceHeader:  http.Header{"Content-Type": {"application/json"}},
		Header:        http.Header{"Content-Type": {"application/json"}},
		Trailer:       make(http.Header),
		ContentLength: -1,
	}
	pending := &pendingHTTPResponse{head: head, exchange: exchange, pctx: pctx}
	pending.finishCapture(responseanalysis.Completion{Termination: responseanalysis.TerminationClientCancelled})
	gateway.Finish(requestcapture.GatewayOutcome{})

	page, err := readCaptureTestPage(manager, session, requestcapture.ListQuery{Limit: 1})
	if err != nil {
		t.Fatalf("ListRecords() error = %v", err)
	}
	if len(page.Records) != 1 || !page.Records[0].HasFailure {
		t.Fatalf("records = %#v, want one structured failure", page.Records)
	}
	record := page.Records[0]
	if record.TerminationReason != requestcapture.TerminationReasonClientDisconnect ||
		record.SourceCompletion != requestcapture.SourceCompletionPartial {
		t.Fatalf("termination/source = %q/%q", record.TerminationReason, record.SourceCompletion)
	}
	primary := record.Failure.Primary
	if primary.Site != requestcapture.FailureSiteResponseRead ||
		primary.Peer != requestcapture.FailurePeerClient ||
		primary.Class != requestcapture.FailureClassCanceled ||
		primary.Code != requestcapture.FailureCodeClientCancel {
		t.Fatalf("failure fact = %#v, want response_read/client/canceled/client_cancel", primary)
	}
}

func TestHandlerCaptureEnabledPreservesHTTPAndSSEBehavior(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
	}{
		{name: "regular", contentType: "application/json", body: `{"result":"ok"}`},
		{name: "SSE", contentType: "text/event-stream", body: "data: first\n\ndata: second\n\n"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", test.contentType)
				w.Header().Set("Trailer", "X-Capture-Trailer")
				w.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(w, test.body)
				w.Header().Set("X-Capture-Trailer", "finished")
			}))
			defer upstream.Close()

			withoutCapture := serveCaptureTestRequest(t, upstream.URL, test.body, nil)

			manager, session := startCaptureTestManager(t, []requestcapture.ProviderIdentity{{
				ID:   "provider-1",
				Name: "Provider One",
			}})
			defer manager.Close()
			withCapture := serveCaptureTestRequest(t, upstream.URL, test.body, manager)

			if withCapture.Code != withoutCapture.Code {
				t.Fatalf("status with capture = %d, without = %d", withCapture.Code, withoutCapture.Code)
			}
			if withCapture.Header().Get("Content-Type") != withoutCapture.Header().Get("Content-Type") {
				t.Fatalf("Content-Type with capture = %q, without = %q", withCapture.Header().Get("Content-Type"), withoutCapture.Header().Get("Content-Type"))
			}
			if withCapture.Body.String() != withoutCapture.Body.String() {
				t.Fatalf("body with capture = %q, without = %q", withCapture.Body.String(), withoutCapture.Body.String())
			}

			page, err := readCaptureTestPage(manager, session, requestcapture.ListQuery{Limit: 10})
			if err != nil {
				t.Fatalf("ListRecords() error = %v", err)
			}
			if len(page.Records) != 1 {
				t.Fatalf("record count = %d, want 1", len(page.Records))
			}
			record := page.Records[0]
			if record.CredentialPhase != requestcapture.CredentialPhaseInitial {
				t.Fatalf("credential phase = %q, want initial", record.CredentialPhase)
			}
			if record.TerminationReason != requestcapture.TerminationReasonEOF {
				t.Fatalf("termination reason = %q, want EOF", record.TerminationReason)
			}
			if record.SourceCompletion != requestcapture.SourceCompletionComplete {
				t.Fatalf("source completion = %q, want complete", record.SourceCompletion)
			}
			if record.UpstreamObservedBytes != int64(len(test.body)) {
				t.Fatalf("upstream bytes = %d, want %d", record.UpstreamObservedBytes, len(test.body))
			}
			if record.ApplicationWriteConfirmedBytes != int64(len(test.body)) {
				t.Fatalf("confirmed client bytes = %d, want %d", record.ApplicationWriteConfirmedBytes, len(test.body))
			}

			detail, err := readCaptureTestDetail(manager, session, record.RecordID, 1024)
			if err != nil {
				t.Fatalf("GetRecord() error = %v", err)
			}
			if detail.HTTP == nil || detail.HTTP.Response == nil {
				t.Fatal("HTTP response detail is missing")
			}
			if detail.HTTP.Response.Protocol == "" {
				t.Fatal("upstream protocol was not captured")
			}
			if got := detail.HTTP.Response.Trailers["X-Capture-Trailer"]; len(got) != 1 || got[0] != "finished" {
				t.Fatalf("response trailer = %#v, want finished", got)
			}
			if got := detail.HTTP.Request.Headers["Authorization"]; len(got) != 1 || got[0] != "Bearer [REDACTED]" {
				t.Fatalf("Authorization snapshot = %#v, want redacted", got)
			}
		})
	}
}

func TestHandlerCaptureRecordsCredentialRefreshAsTwoPhysicalExchanges(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer initial-token" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, "stale credential")
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "refreshed response")
	}))
	defer upstream.Close()

	manager, session := startCaptureTestManager(t, []requestcapture.ProviderIdentity{{
		ID:   "provider-1",
		Name: "Provider One",
	}})
	defer manager.Close()

	store := newMockStore()
	store.providers = []model.Provider{captureTestProvider(upstream.URL)}
	auth := &refreshingCaptureAuthenticator{}
	handler := NewHandler(Config{
		Store:   store,
		Auth:    auth,
		Capture: manager,
		Logger:  zap.NewNop(),
	})

	request := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-3"}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK || response.Body.String() != "refreshed response" {
		t.Fatalf("gateway response = (%d, %q), want (200, refreshed response)", response.Code, response.Body.String())
	}
	if auth.applyCalls != 2 || auth.refreshCalls != 1 {
		t.Fatalf("auth calls = apply:%d refresh:%d, want apply:2 refresh:1", auth.applyCalls, auth.refreshCalls)
	}

	page, err := readCaptureTestPage(manager, session, requestcapture.ListQuery{Limit: 10})
	if err != nil {
		t.Fatalf("ListRecords() error = %v", err)
	}
	if len(page.Records) != 2 {
		t.Fatalf("record count = %d, want 2", len(page.Records))
	}
	sort.Slice(page.Records, func(i, j int) bool {
		return page.Records[i].ExchangeIndex < page.Records[j].ExchangeIndex
	})
	initial, refreshed := page.Records[0], page.Records[1]
	if initial.CredentialPhase != requestcapture.CredentialPhaseInitial ||
		initial.TerminationReason != requestcapture.TerminationReasonCredentialRefreshDrain {
		t.Fatalf("initial exchange = phase:%q termination:%q", initial.CredentialPhase, initial.TerminationReason)
	}
	if refreshed.CredentialPhase != requestcapture.CredentialPhaseRefreshed ||
		refreshed.TerminationReason != requestcapture.TerminationReasonEOF {
		t.Fatalf("refreshed exchange = phase:%q termination:%q", refreshed.CredentialPhase, refreshed.TerminationReason)
	}
	if initial.ProviderAttemptIndex != 0 || refreshed.ProviderAttemptIndex != 0 {
		t.Fatalf("provider attempt indices = %d, %d, want 0, 0", initial.ProviderAttemptIndex, refreshed.ProviderAttemptIndex)
	}
}

func TestHandlerCaptureIndexesSameProviderRetries(t *testing.T) {
	const failedBody = "retry this provider"
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = io.WriteString(w, failedBody)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "retry succeeded")
	}))
	defer upstream.Close()

	provider := captureTestProvider(upstream.URL)
	provider.MaxRetries = 1
	manager, session := startCaptureTestManager(t, []requestcapture.ProviderIdentity{{
		ID:   provider.ID,
		Name: provider.Name,
	}})
	defer manager.Close()
	store := newMockStore()
	store.providers = []model.Provider{provider}
	handler := NewHandler(Config{
		Store:   store,
		Capture: manager,
		Logger:  zap.NewNop(),
	})

	request := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-3"}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK || response.Body.String() != "retry succeeded" {
		t.Fatalf("gateway response = (%d, %q), want (200, retry succeeded)", response.Code, response.Body.String())
	}
	page, err := readCaptureTestPage(manager, session, requestcapture.ListQuery{Limit: 10})
	if err != nil {
		t.Fatalf("ListRecords() error = %v", err)
	}
	if len(page.Records) != 2 {
		t.Fatalf("record count = %d, want 2", len(page.Records))
	}
	sort.Slice(page.Records, func(i, j int) bool {
		return page.Records[i].ExchangeIndex < page.Records[j].ExchangeIndex
	})
	first, retry := page.Records[0], page.Records[1]
	if first.ProviderAttemptIndex != 0 || retry.ProviderAttemptIndex != 1 {
		t.Fatalf("provider attempt indices = %d, %d, want 0, 1", first.ProviderAttemptIndex, retry.ProviderAttemptIndex)
	}
	if first.TerminationReason != requestcapture.TerminationReasonStatusFailoverDrain ||
		first.SourceCompletion != requestcapture.SourceCompletionPartial ||
		first.UpstreamObservedBytes != 0 {
		t.Fatalf("first attempt = termination:%q completion:%q bytes:%d", first.TerminationReason, first.SourceCompletion, first.UpstreamObservedBytes)
	}
	if !first.HasFailure ||
		first.Failure.Primary.Code != requestcapture.FailureCodeUnexpectedStatus ||
		first.Failure.Primary.HTTPStatusCode != http.StatusBadGateway ||
		first.Failure.HasSecondary {
		t.Fatalf("first failure = present:%t observation:%#v", first.HasFailure, first.Failure)
	}
	if retry.TerminationReason != requestcapture.TerminationReasonEOF ||
		retry.SourceCompletion != requestcapture.SourceCompletionComplete {
		t.Fatalf("retry = termination:%q completion:%q", retry.TerminationReason, retry.SourceCompletion)
	}
	if first.SelectionMode != requestcapture.SelectionModeInitial || retry.SelectionMode != requestcapture.SelectionModeInitial {
		t.Fatalf("selection modes = %q, %q, want initial, initial", first.SelectionMode, retry.SelectionMode)
	}
}

func TestHandlerCapturePreservesUnselectedStatusFailoverAsTransition(t *testing.T) {
	primaryBody := `{"error":"primary unavailable"}`
	primaryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, primaryBody)
	}))
	defer primaryServer.Close()

	fallbackBody := `{"provider":"fallback"}`
	fallbackServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, fallbackBody)
	}))
	defer fallbackServer.Close()

	primary := captureTestProviderWithIdentity("provider-primary", "Primary", primaryServer.URL)
	fallback := captureTestProviderWithIdentity("provider-fallback", "Fallback", fallbackServer.URL)
	// Only the selected fallback may retain payload; the failed primary must stay
	// visible as a transition so the provider-switch chain remains explainable.
	manager, session := startCaptureTestManager(t, []requestcapture.ProviderIdentity{{
		ID:   fallback.ID,
		Name: fallback.Name,
	}})
	defer manager.Close()

	store := newMockStore()
	store.configs[ConfigKeyGlobalMaxAttempts] = "2"
	store.providers = []model.Provider{primary, fallback}
	selector := &mockSelector{
		selectWithMetadataFunc: func(_ context.Context, _ *model.SelectRequest) (*selectResult, error) {
			return &selectResult{Provider: &primary}, nil
		},
		selectExcludingFunc: func(_ context.Context, _ *model.SelectRequest, excludeIDs map[string]bool) (*model.Provider, error) {
			if !excludeIDs[primary.ID] {
				t.Fatalf("excluded providers = %v, want %q", excludeIDs, primary.ID)
			}
			return &fallback, nil
		},
	}
	handler := NewHandler(Config{
		Store:    store,
		Selector: selector,
		Capture:  manager,
		Logger:   zap.NewNop(),
	})

	request := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-3"}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK || response.Body.String() != fallbackBody {
		t.Fatalf("gateway response = (%d, %q), want (200, %q)", response.Code, response.Body.String(), fallbackBody)
	}

	page, err := readCaptureTestPage(manager, session, requestcapture.ListQuery{Limit: 10})
	if err != nil {
		t.Fatalf("ListRecords() error = %v", err)
	}
	if len(page.Records) != 1 {
		t.Fatalf("retained record count = %d, want 1", len(page.Records))
	}
	record := page.Records[0]
	if record.Provider.ID != fallback.ID || record.SelectionMode != requestcapture.SelectionModeReplacement {
		t.Fatalf("retained exchange = provider:%q mode:%q, want provider:%q mode:replacement", record.Provider.ID, record.SelectionMode, fallback.ID)
	}
	if record.ProviderAttemptIndex != 0 || record.TerminationReason != requestcapture.TerminationReasonEOF {
		t.Fatalf("retained exchange = attempt:%d termination:%q, want attempt:0 termination:EOF", record.ProviderAttemptIndex, record.TerminationReason)
	}
	if len(page.GatewayTraces) != 1 || len(page.GatewayTraces[0].Entries) != 2 {
		t.Fatalf("gateway traces = %#v, want one two-entry trace", page.GatewayTraces)
	}
	transition, retained := page.GatewayTraces[0].Entries[0], page.GatewayTraces[0].Entries[1]
	if transition.Kind != requestcapture.TraceEntryTransition || transition.Provider.ID != primary.ID {
		t.Fatalf("first trace entry = kind:%q provider:%q, want transition for %q", transition.Kind, transition.Provider.ID, primary.ID)
	}
	if transition.TerminationReason != requestcapture.TerminationReasonStatusFailoverDrain ||
		transition.SelectionMode != requestcapture.SelectionModeInitial ||
		transition.ProviderAttemptIndex != 0 {
		t.Fatalf("transition = termination:%q mode:%q attempt:%d", transition.TerminationReason, transition.SelectionMode, transition.ProviderAttemptIndex)
	}
	if !transition.HasFailure ||
		transition.Failure.Primary.Site != requestcapture.FailureSiteResponseStatus ||
		transition.Failure.Primary.Code != requestcapture.FailureCodeUnexpectedStatus ||
		transition.Failure.Primary.HTTPStatusCode != http.StatusBadGateway {
		t.Fatalf("transition failure = present:%t observation:%#v", transition.HasFailure, transition.Failure)
	}
	if retained.Kind != requestcapture.TraceEntryRecord || retained.RecordID != record.RecordID {
		t.Fatalf("second trace entry = kind:%q record:%q, want retained record %q", retained.Kind, retained.RecordID, record.RecordID)
	}
}

func TestHandlerCaptureSanitizesAuthPreparationFailureTransition(t *testing.T) {
	const credential = "primary-auth-secret"
	primaryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("primary request reached transport despite authentication preparation failure")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer primaryServer.Close()

	fallbackServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "fallback response")
	}))
	defer fallbackServer.Close()

	primary := captureTestProviderWithIdentity(
		"provider-primary",
		"Primary",
		primaryServer.URL+"?opaque="+credential,
	)
	fallback := captureTestProviderWithIdentity("provider-fallback", "Fallback", fallbackServer.URL)
	manager, session := startCaptureTestManager(t, []requestcapture.ProviderIdentity{{
		ID:   fallback.ID,
		Name: fallback.Name,
	}})
	defer manager.Close()

	store := newMockStore()
	store.configs[ConfigKeyGlobalMaxAttempts] = "2"
	store.providers = []model.Provider{primary, fallback}
	selector := &mockSelector{
		selectWithMetadataFunc: func(_ context.Context, _ *model.SelectRequest) (*selectResult, error) {
			return &selectResult{Provider: &primary}, nil
		},
		selectExcludingFunc: func(_ context.Context, _ *model.SelectRequest, _ map[string]bool) (*model.Provider, error) {
			return &fallback, nil
		},
	}
	handler := NewHandler(Config{
		Store:    store,
		Selector: selector,
		Auth: &failingPreparationCaptureAuthenticator{
			providerID: primary.ID,
			credential: credential,
		},
		Capture: manager,
		Logger:  zap.NewNop(),
	})

	request := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-3"}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK || response.Body.String() != "fallback response" {
		t.Fatalf("gateway response = (%d, %q), want (200, fallback response)", response.Code, response.Body.String())
	}
	page, err := readCaptureTestPage(manager, session, requestcapture.ListQuery{Limit: 10})
	if err != nil {
		t.Fatalf("ListRecords() error = %v", err)
	}
	if len(page.Records) != 1 || len(page.GatewayTraces) != 1 || len(page.GatewayTraces[0].Entries) != 2 {
		t.Fatalf("capture page = records:%d traces:%#v, want one record and a two-entry trace", len(page.Records), page.GatewayTraces)
	}
	transition := page.GatewayTraces[0].Entries[0]
	if transition.Kind != requestcapture.TraceEntryTransition || transition.Provider.ID != primary.ID {
		t.Fatalf("preparation entry = kind:%q provider:%q, want transition for %q", transition.Kind, transition.Provider.ID, primary.ID)
	}
	if transition.TerminationReason != requestcapture.TerminationReasonPreparationError {
		t.Fatalf("preparation termination = %q, want preparation_error", transition.TerminationReason)
	}
	if transition.Provider.TargetURL == "" || !strings.Contains(transition.Provider.TargetURL, credential) {
		t.Fatalf("target URL = %q, want provider credential visible", transition.Provider.TargetURL)
	}
	if !strings.Contains(transition.Provider.TargetURL, "opaque=") {
		t.Fatalf("target URL = %q, want opaque query retained", transition.Provider.TargetURL)
	}
	if !transition.HasFailure ||
		transition.Failure.Primary.Code != requestcapture.FailureCodeCredentialApply ||
		strings.Contains(transition.Failure.Primary.Message, "[REDACTED]") {
		t.Fatalf("structured preparation failure = present:%t observation:%#v", transition.HasFailure, transition.Failure)
	}
}

func serveCaptureTestRequest(
	t *testing.T,
	upstreamURL string,
	requestBody string,
	capture RequestCapture,
) *httptest.ResponseRecorder {
	t.Helper()
	store := newMockStore()
	store.providers = []model.Provider{captureTestProvider(upstreamURL)}
	handler := NewHandler(Config{
		Store:   store,
		Capture: capture,
		Logger:  zap.NewNop(),
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(requestBody))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func captureTestProvider(upstreamURL string) model.Provider {
	return captureTestProviderWithIdentity("provider-1", "Provider One", upstreamURL)
}

func captureTestProviderWithIdentity(id, name, upstreamURL string) model.Provider {
	return model.Provider{
		ID:       id,
		Name:     name,
		APIKey:   "capture-secret",
		AuthMode: AuthModeBearer,
		Enabled:  true,
		APITypes: []model.ProviderAPIType{{
			ProviderID: id,
			APIType:    APITypeClaude,
			BaseURL:    upstreamURL,
		}},
	}
}

func startCaptureTestManager(
	t *testing.T,
	providers []requestcapture.ProviderIdentity,
) (*requestcapture.Manager, requestcapture.SessionInfo) {
	t.Helper()
	manager, err := requestcapture.NewManager(requestcapture.Config{})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	session, err := manager.Start(requestcapture.StartRequest{
		Providers:                 providers,
		AcknowledgeRawPayloadRisk: true,
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	return manager, session
}

func readCaptureTestPage(
	manager *requestcapture.Manager,
	session requestcapture.SessionInfo,
	query requestcapture.ListQuery,
) (requestcapture.RecordPage, error) {
	lease, err := manager.OpenRecordPage(context.Background(), session.SessionID, query)
	if err != nil {
		return requestcapture.RecordPage{}, err
	}
	defer lease.Close()
	var encoded bytes.Buffer
	if err := lease.WriteJSON(context.Background(), &encoded); err != nil {
		return requestcapture.RecordPage{}, err
	}
	var page requestcapture.RecordPage
	if err := json.Unmarshal(encoded.Bytes(), &page); err != nil {
		return requestcapture.RecordPage{}, err
	}
	return page, nil
}

func readCaptureTestDetail(
	manager *requestcapture.Manager,
	session requestcapture.SessionInfo,
	recordID string,
	previewBytes int,
) (requestcapture.RecordDetail, error) {
	lease, err := manager.OpenRecordDetail(context.Background(), session.SessionID, recordID, previewBytes)
	if err != nil {
		return requestcapture.RecordDetail{}, err
	}
	defer lease.Close()
	var encoded bytes.Buffer
	if err := lease.WriteJSON(context.Background(), &encoded); err != nil {
		return requestcapture.RecordDetail{}, err
	}
	var detail requestcapture.RecordDetail
	if err := json.Unmarshal(encoded.Bytes(), &detail); err != nil {
		return requestcapture.RecordDetail{}, err
	}
	return detail, nil
}

type refreshingCaptureAuthenticator struct {
	refreshed    bool
	applyCalls   int
	refreshCalls int
}

func (a *refreshingCaptureAuthenticator) ApplyProviderCredentials(
	_ context.Context,
	headers http.Header,
	_ *model.Provider,
	_, _ string,
	_ *http.Request,
) error {
	a.applyCalls++
	token := "initial-token"
	if a.refreshed {
		token = "refreshed-token"
	}
	headers.Set("Authorization", "Bearer "+token)
	return nil
}

func (a *refreshingCaptureAuthenticator) RefreshProviderCredentials(
	_ context.Context,
	_ *model.Provider,
) (bool, error) {
	a.refreshCalls++
	a.refreshed = true
	return true, nil
}

type failingPreparationCaptureAuthenticator struct {
	providerID string
	credential string
}

func (a *failingPreparationCaptureAuthenticator) ApplyProviderCredentials(
	_ context.Context,
	headers http.Header,
	provider *model.Provider,
	_, _ string,
	_ *http.Request,
) error {
	if provider.ID == a.providerID {
		headers.Set("Authorization", "Bearer "+a.credential)
		return errors.New("credential " + a.credential + " was rejected")
	}
	headers.Set("Authorization", "Bearer fallback-token")
	return nil
}

func (*failingPreparationCaptureAuthenticator) RefreshProviderCredentials(
	context.Context,
	*model.Provider,
) (bool, error) {
	return false, nil
}
