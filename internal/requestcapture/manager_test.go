package requestcapture

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
)

type testClock struct {
	mu        sync.Mutex
	now       time.Time
	monotonic time.Duration
}

func (c *testClock) WallNow() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) MonotonicNow() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.monotonic
}

func (c *testClock) advance(duration time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(duration)
	c.monotonic = saturatingDurationAdd(c.monotonic, duration)
	c.mu.Unlock()
}

func (c *testClock) advanceWall(duration time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(duration)
	c.mu.Unlock()
}

func (c *testClock) advanceMonotonic(duration time.Duration) {
	c.mu.Lock()
	c.monotonic = saturatingDurationAdd(c.monotonic, duration)
	c.mu.Unlock()
}

type testIDs struct {
	next atomic.Uint64
}

func testCredentialEvidence(values ...string) CredentialEvidence {
	var evidence CredentialEvidence
	for _, value := range values {
		evidence.Add(value)
	}
	evidence.Seal()
	return evidence
}

func testSensitiveHeaderEvidence(names ...string) SensitiveHeaderEvidence {
	var evidence SensitiveHeaderEvidence
	for _, name := range names {
		evidence.Add(name)
	}
	evidence.Seal()
	return evidence
}

func testParsedURL(raw string) *url.URL {
	parsed, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	return parsed
}

func testRecordState(t *testing.T, recorder Recorder) *recordState {
	t.Helper()
	access := recorder.acquire()
	record := access.record
	access.release()
	if record == nil {
		t.Fatal("value recorder lookup failed")
	}
	return record
}

func lookupRecordForTest(session *sessionState, recordID string) *recordState {
	for record := session.oldestRecord; record != nil; record = record.newer {
		if record.id == recordID && !record.evicted {
			return record
		}
	}
	return nil
}

func testFailure(message string) FailureObservation {
	return FailureObservation{Primary: FailureFact{
		Site: FailureSiteTransport, Peer: FailurePeerUpstream,
		Class: FailureClassTransport, Code: FailureCodeRoundTrip, Message: message,
	}}
}

func (g *testIDs) NewID() ([16]byte, error) {
	var id [16]byte
	binary.BigEndian.PutUint64(id[8:], g.next.Add(1))
	return id, nil
}

type failingIDs struct{}

type hostileIDError struct{}

func (hostileIDError) Error() string {
	panic("capture must not execute arbitrary ID error formatting")
}

func (failingIDs) NewID() ([16]byte, error) {
	return [16]byte{}, hostileIDError{}
}

type blockingIDs struct {
	entered chan struct{}
	release chan struct{}
}

func (ids *blockingIDs) NewID() ([16]byte, error) {
	close(ids.entered)
	<-ids.release
	return [16]byte{1}, nil
}

func newTestManager(t *testing.T, mutate func(*Config)) *Manager {
	t.Helper()
	cfg := Config{
		ProcessCeilingBytes:      2 << 20,
		DefaultSessionQuotaBytes: 1 << 20,
		ChunkBytes:               MinimumChunkBytes,
		ExportLineBytes:          4096,
		Clock:                    &testClock{now: time.Date(2026, 8, 1, 1, 2, 3, 0, time.UTC)},
		IDGenerator:              &testIDs{},
		Logger:                   zap.NewNop(),
	}
	if mutate != nil {
		mutate(&cfg)
	}
	manager, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	return manager
}

func startTestSession(t *testing.T, manager *Manager, records int, quota int64, providers ...string) SessionInfo {
	t.Helper()
	identities := make([]ProviderIdentity, len(providers))
	for index, providerID := range providers {
		identities[index] = ProviderIdentity{ID: providerID, Name: "Name " + providerID}
	}
	info, err := manager.Start(StartRequest{
		Providers:                   identities,
		CompletedRecordsPerProvider: records,
		RetainedBytesLimit:          quota,
		AcknowledgeRawPayloadRisk:   true,
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	return info
}

func beginTestHTTP(manager *Manager, gatewayID, providerID string, body []byte) (GatewayRecorder, Recorder) {
	gateway := manager.BeginGateway(GatewayStart{GatewayRequestID: gatewayID})
	recorder := gateway.BeginHTTP(RawHTTPStart{
		URL: testParsedURL("https://user:password@example.test/v1/messages?api_key=secret&safe=yes"),
		Attempt: AttemptMetadata{
			Provider:             ProviderIdentity{ID: providerID, Name: "Provider " + providerID},
			APIType:              "claude",
			SelectionMode:        SelectionModeInitial,
			SelectionSource:      SelectionSourceStrategy,
			ProviderAttemptIndex: 0,
			CredentialPhase:      CredentialPhaseInitial,
		},
		Request: RawRequest{
			Method:             http.MethodPost,
			Headers:            http.Header{"Authorization": {"Bearer secret"}, "X-Safe": {"visible"}},
			ContentLength:      int64(len(body)),
			Body:               body,
			SensitiveHeaders:   testSensitiveHeaderEvidence(),
			CredentialEvidence: testCredentialEvidence("secret"),
		},
	})
	return gateway, recorder
}

func completeHTTP(recorder Recorder, payload []byte) {
	recorder.ObserveResponse(HTTPResponseHead{
		StatusCode:         http.StatusOK,
		Protocol:           "HTTP/2.0",
		Headers:            http.Header{"Content-Type": {"application/json"}},
		ContentLength:      int64(len(payload)),
		SensitiveHeaders:   testSensitiveHeaderEvidence(),
		CredentialEvidence: testCredentialEvidence(),
	})
	recorder.ObserveUpstream(payload)
	recorder.ObserveClientWrite(len(payload))
	recorder.Finish(Outcome{
		SourceCompletion:  SourceCompletionComplete,
		TerminationReason: TerminationReasonEOF,
	})
}

func TestManagerStartStopAndDefaults(t *testing.T) {
	manager := newTestManager(t, nil)
	if manager.Enabled() {
		t.Fatal("new manager is enabled")
	}

	info, err := manager.Start(StartRequest{
		Providers:                 []ProviderIdentity{{ID: "provider-a", Name: "Provider A"}},
		AcknowledgeRawPayloadRisk: true,
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if !manager.Enabled() {
		t.Fatal("manager is not enabled after Start")
	}
	if info.CompletedRecordsPerProvider != DefaultRecordsPerProvider {
		t.Fatalf("records per provider = %d", info.CompletedRecordsPerProvider)
	}
	if info.RetainedBytesLimit != manager.cfg.defaultSessionQuotaBytes {
		t.Fatalf("quota = %d", info.RetainedBytesLimit)
	}
	if info.ProviderIDs[0] != "provider-a" || info.Providers[0].Name != "Provider A" {
		t.Fatalf("provider catalog changed: %#v", info.Providers)
	}
	if _, err := manager.Start(StartRequest{
		Providers:                 []ProviderIdentity{{ID: "provider-a"}},
		AcknowledgeRawPayloadRisk: true,
	}); !errors.Is(err, ErrSessionActive) {
		t.Fatalf("duplicate Start error = %v", err)
	}
	if err := manager.Stop("stale"); !errors.Is(err, ErrSessionMismatch) {
		t.Fatalf("stale Stop error = %v", err)
	}
	if err := manager.Stop(info.SessionID); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	status := manager.Status()
	if status.State != SessionStateStopped || status.HasSession {
		t.Fatalf("stopped status = %#v", status)
	}
	if status.ProcessMemory.ChargedBytes != 0 || status.ProcessMemory.ReleasingBytes != 0 {
		t.Fatalf("memory after Stop = %#v", status.ProcessMemory)
	}
	if err := manager.Stop(info.SessionID); !errors.Is(err, ErrNoActiveSession) {
		t.Fatalf("second Stop error = %v", err)
	}
}

func TestManagerStartValidationAndCapacity(t *testing.T) {
	manager := newTestManager(t, nil)
	tests := []struct {
		name  string
		input StartRequest
	}{
		{name: "risk acknowledgement", input: StartRequest{Providers: []ProviderIdentity{{ID: "a"}}}},
		{name: "providers", input: StartRequest{AcknowledgeRawPayloadRisk: true}},
		{name: "empty provider", input: StartRequest{Providers: []ProviderIdentity{{ID: " "}}, AcknowledgeRawPayloadRisk: true}},
		{name: "duplicate provider", input: StartRequest{Providers: []ProviderIdentity{{ID: "a"}, {ID: "a"}}, AcknowledgeRawPayloadRisk: true}},
		{name: "records low", input: StartRequest{Providers: []ProviderIdentity{{ID: "a"}}, CompletedRecordsPerProvider: -1, AcknowledgeRawPayloadRisk: true}},
		{name: "records high", input: StartRequest{Providers: []ProviderIdentity{{ID: "a"}}, CompletedRecordsPerProvider: manager.cfg.maxRecordsPerProvider + 1, AcknowledgeRawPayloadRisk: true}},
		{name: "quota", input: StartRequest{Providers: []ProviderIdentity{{ID: "a"}}, RetainedBytesLimit: manager.cfg.processCeilingBytes + 1, AcknowledgeRawPayloadRisk: true}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := manager.Start(test.input); err == nil {
				t.Fatal("Start() succeeded")
			} else {
				var validation *ValidationError
				if !errors.As(err, &validation) {
					t.Fatalf("error = %v, want ValidationError", err)
				}
			}
		})
	}

	tiny := newTestManager(t, func(cfg *Config) {
		cfg.ProcessCeilingBytes = 1 << 20
		cfg.DefaultSessionQuotaBytes = 1 << 20
		cfg.ChunkBytes = MinimumChunkBytes
		cfg.PreviewBytes = 64
		cfg.ExportLineBytes = minimumExportLineBytes() + 64
	})
	if _, err := tiny.Start(StartRequest{
		Providers:                 []ProviderIdentity{{ID: "a"}},
		RetainedBytesLimit:        128,
		AcknowledgeRawPayloadRisk: true,
	}); !errors.Is(err, ErrCapacityExceeded) {
		t.Fatalf("tiny Start error = %v", err)
	}
	if tiny.Enabled() {
		t.Fatal("failed Start published a session")
	}
}

func TestStartDenialAndConcurrentLosersDoNotMaterialize(t *testing.T) {
	ids := &testIDs{}
	manager := newTestManager(t, func(cfg *Config) {
		cfg.IDGenerator = ids
	})
	request := StartRequest{
		Providers:                   []ProviderIdentity{{ID: "selected", Name: "Selected"}},
		CompletedRecordsPerProvider: 1,
		AcknowledgeRawPayloadRisk:   true,
	}
	shape, err := manager.scanStart(request)
	if err != nil {
		t.Fatalf("scanStart() error = %v", err)
	}
	candidate := sessionBaseChargeBytes + int64(maxSessionIDBytes) + shape.providerBytes +
		int64(len(request.Providers))*(2*mapEntryChargeBytes+sliceEntryChargeBytes)
	request.RetainedBytesLimit = candidate - 1
	if _, err := manager.Start(request); !errors.Is(err, ErrCapacityExceeded) {
		t.Fatalf("denied Start() error = %v", err)
	}
	if calls := ids.next.Load(); calls != 0 {
		t.Fatalf("denied Start materialized %d IDs", calls)
	}

	request.RetainedBytesLimit = 1 << 20
	const contenders = 32
	results := make(chan error, contenders)
	var wait sync.WaitGroup
	for index := 0; index < contenders; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, startErr := manager.Start(request)
			results <- startErr
		}()
	}
	wait.Wait()
	close(results)
	succeeded := 0
	for startErr := range results {
		switch {
		case startErr == nil:
			succeeded++
		case errors.Is(startErr, ErrSessionActive):
		default:
			t.Fatalf("concurrent Start() error = %v", startErr)
		}
	}
	if succeeded != 1 {
		t.Fatalf("successful Start calls = %d, want 1", succeeded)
	}
	if calls := ids.next.Load(); calls != 1 {
		t.Fatalf("concurrent Start materialized %d IDs, want 1", calls)
	}
}

func TestStartRejectsNonCanonicalProviderIdentity(t *testing.T) {
	manager := newTestManager(t, nil)
	for _, provider := range []ProviderIdentity{
		{ID: " selected", Name: "Selected"},
		{ID: "selected", Name: "Selected "},
	} {
		_, err := manager.Start(StartRequest{
			Providers:                 []ProviderIdentity{provider},
			AcknowledgeRawPayloadRisk: true,
		})
		var validation *ValidationError
		if !errors.As(err, &validation) || validation.Field != "providers" {
			t.Fatalf("Start(%#v) error = %v, want provider validation", provider, err)
		}
	}
}

func TestStartMaterializationDoesNotHoldLifecycleLock(t *testing.T) {
	ids := &blockingIDs{entered: make(chan struct{}), release: make(chan struct{})}
	manager := newTestManager(t, func(cfg *Config) {
		cfg.IDGenerator = ids
	})
	started := make(chan error, 1)
	go func() {
		_, err := manager.Start(StartRequest{
			Providers:                 []ProviderIdentity{{ID: "selected"}},
			AcknowledgeRawPayloadRisk: true,
		})
		started <- err
	}()
	<-ids.entered

	closed := make(chan error, 1)
	go func() {
		closed <- manager.Close()
	}()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close blocked behind injected ID generator")
	}
	close(ids.release)
	if err := <-started; !errors.Is(err, ErrManagerClosed) {
		t.Fatalf("Start() after concurrent Close error = %v, want ErrManagerClosed", err)
	}
	status := manager.Status()
	if status.ProcessMemory.ChargedBytes != 0 || status.ProcessMemory.TemporaryBytes != 0 {
		t.Fatalf("canceled Start leaked accounting: %#v", status.ProcessMemory)
	}
}

func TestStartIDGenerationFailureRollsBackWithoutFormattingError(t *testing.T) {
	manager := newTestManager(t, func(cfg *Config) {
		cfg.IDGenerator = failingIDs{}
	})
	_, err := manager.Start(StartRequest{
		Providers:                   []ProviderIdentity{{ID: "selected"}},
		CompletedRecordsPerProvider: 1,
		AcknowledgeRawPayloadRisk:   true,
	})
	if !errors.Is(err, ErrInternalFailure) {
		t.Fatalf("Start() error = %v, want ErrInternalFailure", err)
	}
	if manager.Enabled() {
		t.Fatal("ID generation failure published a session")
	}
	status := manager.Status()
	if status.ProcessMemory.ChargedBytes != 0 || status.ProcessMemory.TemporaryBytes != 0 ||
		status.ProcessMemory.RetainedBytes != 0 {
		t.Fatalf("ID generation failure leaked process accounting: %#v", status.ProcessMemory)
	}
}

func TestHTTPRecordDetailSanitizationAndCounters(t *testing.T) {
	manager := newTestManager(t, nil)
	session := startTestSession(t, manager, 2, 1<<20, "selected")
	gateway, recorder := beginTestHTTP(manager, "gateway-1", "selected", []byte("request"))
	if !gateway.Valid() || !recorder.Valid() {
		t.Fatal("recorders are invalid")
	}
	recorder.ObserveResponse(HTTPResponseHead{
		StatusCode:         http.StatusOK,
		Protocol:           "HTTP/2.0",
		Headers:            http.Header{"ChatGPT-Account-Id": {"account-id"}, "Set-Cookie": {"cookie-value"}},
		ContentLength:      8,
		DeclaredTrailers:   http.Header{"ChatGPT-Account-Id": nil},
		SensitiveHeaders:   testSensitiveHeaderEvidence(),
		CredentialEvidence: testCredentialEvidence("account-id"),
	})
	recorder.ObserveUpstream([]byte("response"))
	recorder.ObserveClientWrite(8)
	recorder.Finish(Outcome{
		SourceCompletion:   SourceCompletionComplete,
		TerminationReason:  TerminationReasonEOF,
		ResponseTrailers:   http.Header{"ChatGPT-Account-Id": {"account-id"}},
		CredentialEvidence: testCredentialEvidence("account-id"),
	})
	gateway.Finish(GatewayOutcome{})

	detail, err := readRecordDetailForTest(t, manager, session.SessionID, recorder.ID(), 64)
	if err != nil {
		t.Fatalf("GetRecord() error = %v", err)
	}
	if detail.SnapshotState != SnapshotStateFinal || detail.Summary.CaptureCompletion != CaptureCompletionComplete {
		t.Fatalf("completion = %#v", detail)
	}
	if detail.Summary.UpstreamObservedBytes != 8 || detail.Summary.ApplicationWriteConfirmedBytes != 8 {
		t.Fatalf("byte counters = %#v", detail.Summary)
	}
	sanitizedURL, err := url.Parse(detail.HTTP.Request.URL)
	if err != nil {
		t.Fatalf("sanitized URL parse error = %v", err)
	}
	if sanitizedURL.User == nil || sanitizedURL.User.Username() != "user" ||
		sanitizedURL.Query().Get("api_key") != redactedValue ||
		sanitizedURL.Query().Get("safe") != "yes" {
		t.Fatalf("sanitized URL = %q", detail.HTTP.Request.URL)
	}
	if detail.HTTP.Request.Headers["Authorization"][0] != "Bearer "+redactedValue {
		t.Fatalf("authorization leaked: %#v", detail.HTTP.Request.Headers)
	}
	if detail.HTTP.Response.Headers["ChatGPT-Account-Id"][0] != "account-id" ||
		detail.HTTP.Response.Trailers["ChatGPT-Account-Id"][0] != "account-id" {
		t.Fatalf("provider-owned response header was redacted: %#v", detail.HTTP.Response)
	}

	// Returned maps and pointers are detached from retained state.
	detail.HTTP.Request.Headers["X-Safe"][0] = "mutated"
	*detail.Summary.CompletedAt = time.Time{}
	second, err := readRecordDetailForTest(t, manager, session.SessionID, recorder.ID(), 64)
	if err != nil {
		t.Fatalf("second GetRecord() error = %v", err)
	}
	if second.HTTP.Request.Headers["X-Safe"][0] != "visible" || second.Summary.CompletedAt.IsZero() {
		t.Fatal("caller mutation changed retained record")
	}
}

func TestPerProviderRetentionEvictsByCompletedSequence(t *testing.T) {
	manager := newTestManager(t, nil)
	session := startTestSession(t, manager, 1, 1<<20, "selected")
	var firstID string
	for index := 0; index < 2; index++ {
		gateway, recorder := beginTestHTTP(manager, fmt.Sprintf("gateway-%d", index), "selected", nil)
		completeHTTP(recorder, []byte(fmt.Sprintf("response-%d", index)))
		gateway.Finish(GatewayOutcome{})
		if index == 0 {
			firstID = recorder.ID()
		}
	}
	if _, err := readRecordDetailForTest(t, manager, session.SessionID, firstID, 64); !errors.Is(err, ErrRecordEvicted) {
		t.Fatalf("first detail error = %v", err)
	}
	status := manager.Status()
	if status.Session.CompletedRecordCount != 1 || status.Session.EvictedRecordCount != 1 {
		t.Fatalf("retention status = %#v", status.Session)
	}
}

func TestTraceProvisionalReleaseAndActiveLimit(t *testing.T) {
	manager := newTestManager(t, func(cfg *Config) {
		cfg.MaxActiveTraces = 1
	})
	session := startTestSession(t, manager, 2, 1<<20, "selected")

	provisional := manager.BeginGateway(GatewayStart{GatewayRequestID: "provisional"})
	if !provisional.Valid() {
		t.Fatal("first gateway invalid")
	}
	if blocked := manager.BeginGateway(GatewayStart{GatewayRequestID: "blocked"}); blocked.Valid() {
		t.Fatal("active trace limit did not block second gateway")
	}
	provisional.BeginHTTP(RawHTTPStart{
		Attempt: AttemptMetadata{Provider: ProviderIdentity{ID: "other"}},
		URL:     testParsedURL("https://other.test"),
	}).Finish(Outcome{TerminationReason: TerminationReasonTransportError})
	provisional.Finish(GatewayOutcome{})
	if manager.Status().Session.GatewayTraceCount != 0 {
		t.Fatal("unselected provisional trace was retained")
	}

	retained, recorder := beginTestHTTP(manager, "retained", "selected", nil)
	completeHTTP(recorder, []byte("ok"))
	retained.Finish(GatewayOutcome{})
	if next := manager.BeginGateway(GatewayStart{GatewayRequestID: "next"}); !next.Valid() {
		t.Fatal("retained completed trace consumed active trace limit")
	}
	if manager.Status().Session.DroppedTraceCount != 1 {
		t.Fatalf("dropped traces = %d", manager.Status().Session.DroppedTraceCount)
	}
	_ = session
}

func TestStopStartABARejectsStaleRecorders(t *testing.T) {
	manager := newTestManager(t, nil)
	first := startTestSession(t, manager, 2, 1<<20, "selected")
	oldGateway, oldRecorder := beginTestHTTP(manager, "old-gateway", "selected", nil)
	oldRecorder.ObserveUpstream([]byte("before-stop"))
	if err := manager.Stop(first.SessionID); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	second := startTestSession(t, manager, 2, 1<<20, "selected")
	oldRecorder.ObserveUpstream([]byte("after-stop"))
	oldRecorder.Finish(Outcome{SourceCompletion: SourceCompletionComplete, TerminationReason: TerminationReasonEOF})
	oldGateway.Finish(GatewayOutcome{})
	page, err := readRecordPageForTest(t, manager, second.SessionID, ListQuery{})
	if err != nil {
		t.Fatalf("ListRecords() error = %v", err)
	}
	if len(page.Records) != 0 {
		t.Fatalf("stale recorder wrote into generation %d: %#v", second.Generation, page.Records)
	}
	if second.Generation <= first.Generation {
		t.Fatalf("generations did not increase: first=%d second=%d", first.Generation, second.Generation)
	}
}

func TestStatusDoesNotBlockDetachAndRetriesAcrossGeneration(t *testing.T) {
	manager := newTestManager(t, nil)
	first := startTestSession(t, manager, 2, 1<<20, "selected")
	oldSession := manager.active.Load()

	oldSession.mu.Lock()
	locked := true
	defer func() {
		if locked {
			oldSession.mu.Unlock()
		}
	}()

	statusStarted := make(chan struct{})
	statusResult := make(chan Status, 1)
	go func() {
		close(statusStarted)
		statusResult <- manager.Status()
	}()
	<-statusStarted
	// Give Status time to sample the old generation and block on its mutex.
	time.Sleep(10 * time.Millisecond)

	stopResult := make(chan error, 1)
	go func() {
		stopResult <- manager.Stop(first.SessionID)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for manager.Enabled() {
		if time.Now().After(deadline) {
			t.Fatal("Status held lifecycle ownership while waiting for the session mutex")
		}
		time.Sleep(time.Millisecond)
	}

	second := startTestSession(t, manager, 2, 1<<20, "selected")
	oldSession.mu.Unlock()
	locked = false

	select {
	case err := <-stopResult:
		if err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not finish after the old session mutex was released")
	}
	select {
	case status := <-statusResult:
		if status.State != SessionStateActive || !status.HasSession ||
			status.Session.Generation != second.Generation ||
			!status.Session.SessionID.Equal(second.SessionID) {
			t.Fatalf("Status returned a torn or stale generation: %#v", status)
		}
		if status.Session.ProviderCount != len(second.ProviderIDs) {
			t.Fatalf("Status provider count = %d, want %d", status.Session.ProviderCount, len(second.ProviderIDs))
		}
		if status.ProcessMemory.RetainedBytes !=
			status.ProcessMemory.ChargedBytes-status.ProcessMemory.TemporaryBytes {
			t.Fatalf("process retained semantics are inconsistent: %#v", status.ProcessMemory)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Status did not retry after generation changed")
	}
}

func TestManagerCloseIsIdempotent(t *testing.T) {
	manager := newTestManager(t, nil)
	session := startTestSession(t, manager, 2, 1<<20, "selected")
	if err := manager.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if _, err := manager.Start(StartRequest{
		Providers:                 []ProviderIdentity{{ID: "selected"}},
		AcknowledgeRawPayloadRisk: true,
	}); !errors.Is(err, ErrManagerClosed) {
		t.Fatalf("Start after Close error = %v", err)
	}
	if err := manager.Stop(session.SessionID); !errors.Is(err, ErrNoActiveSession) {
		t.Fatalf("Stop after Close error = %v", err)
	}
}
