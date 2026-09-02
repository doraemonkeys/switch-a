package requestcapture

import (
	"net/http"
	"strings"
	"testing"
)

func TestWebSocketTranscriptLineageAndVisibility(t *testing.T) {
	manager := newTestManager(t, nil)
	session := startTestSession(t, manager, 2, 1<<20, "selected")
	gateway := manager.BeginGateway(GatewayStart{GatewayRequestID: "ws-gateway"})
	lineage := gateway.NewMessageID()
	if !lineage.Valid() {
		t.Fatal("NewMessageID() returned invalid lineage")
	}
	lineageID := materializeMessageID(lineage.generation, lineage.traceSequence, lineage.lineage)
	recorder := gateway.BeginWebSocket(RawWebSocketStart{
		TargetURL: "wss://example.test/realtime?access_token=secret",
		Attempt: AttemptMetadata{
			Provider:        ProviderIdentity{ID: "selected", Name: "Selected"},
			APIType:         "codex",
			SelectionMode:   SelectionModeInitial,
			SelectionSource: SelectionSourceStrategy,
			CredentialPhase: CredentialPhaseInitial,
		},
		Request: RawRequest{
			Method:             http.MethodGet,
			Headers:            http.Header{"Authorization": {"Bearer secret"}},
			SensitiveHeaders:   testSensitiveHeaderEvidence("Authorization"),
			CredentialEvidence: testCredentialEvidence("secret"),
		},
	})
	recorder.ObserveWebSocketHandshake(WebSocketHandshake{
		StatusCode: http.StatusSwitchingProtocols,
		Protocol:   "HTTP/1.1",
		Headers:    http.Header{"Set-Cookie": {"secret-cookie"}},
	})

	first := recorder.MessageRead(MessageRead{
		Lineage:   lineage,
		Direction: MessageDirectionUpstreamToClient,
		Type:      MessageTypeText,
		Payload:   []byte("first"),
		Source:    MessageSourceLive,
	})
	if first.ID() != lineageID || !first.Valid() || first.Lineage() != lineage {
		t.Fatalf("first message ref = %#v lineage=%#v", first, first.Lineage())
	}
	recorder.MessageResult(first, MessageResult{
		Disposition:    MessageDispositionForwarded,
		WriteConfirmed: true,
	})
	// The two-stage result is idempotent.
	recorder.MessageResult(first, MessageResult{
		Disposition:    MessageDispositionWriteFailed,
		WriteConfirmed: true,
	})

	replay := recorder.MessageRead(MessageRead{
		Direction:     MessageDirectionUpstreamToClient,
		Type:          MessageTypeText,
		Payload:       []byte("replay"),
		Source:        MessageSourceReplay,
		SourceLineage: lineage,
	})
	if actual := replay.Lineage(); !actual.Valid() ||
		replay.ID() != materializeMessageID(actual.generation, actual.traceSequence, actual.lineage) {
		t.Fatalf("fallback replay lineage = %#v ref=%#v", actual, replay)
	}
	recorder.MessageResult(replay, MessageResult{Disposition: MessageDispositionSuppressed})

	outbound := recorder.MessageRead(MessageRead{
		Direction: MessageDirectionClientToUpstream,
		Type:      MessageTypeBinary,
		Payload:   []byte{0, 1, 2},
		Source:    MessageSourceLive,
	})
	recorder.MessageResult(outbound, MessageResult{
		Disposition:    MessageDispositionForwarded,
		WriteConfirmed: true,
	})

	active, err := readRecordDetailForTest(t, manager, session.SessionID, recorder.ID(), 64)
	if err != nil {
		t.Fatalf("active GetRecord() error = %v", err)
	}
	if active.SnapshotState != SnapshotStateActivePartial ||
		active.Summary.CaptureCompletion != CaptureCompletionComplete {
		t.Fatalf("active completion axes = %#v", active)
	}
	if len(active.WebSocket.Messages) != 3 {
		t.Fatalf("active messages = %d", len(active.WebSocket.Messages))
	}
	if !active.WebSocket.Messages[0].ClientVisible ||
		active.WebSocket.Messages[1].ClientVisible ||
		active.WebSocket.Messages[2].ClientVisible {
		t.Fatalf("client visibility = %#v", active.WebSocket.Messages)
	}
	if active.Summary.UpstreamObservedBytes != int64(len("first")+len("replay")) {
		t.Fatalf("observed bytes = %d", active.Summary.UpstreamObservedBytes)
	}
	if active.Summary.ApplicationWriteConfirmedBytes != int64(len("first")+3) {
		t.Fatalf("confirmed bytes = %d", active.Summary.ApplicationWriteConfirmedBytes)
	}

	recorder.Finish(Outcome{
		SourceCompletion:  SourceCompletionComplete,
		TerminationReason: TerminationReasonWebSocketClose,
	})
	recorder.Finish(Outcome{TerminationReason: TerminationReasonReadError})
	gateway.Finish(GatewayOutcome{})
	completed, err := readRecordDetailForTest(t, manager, session.SessionID, recorder.ID(), 64)
	if err != nil {
		t.Fatalf("completed GetRecord() error = %v", err)
	}
	if completed.Summary.TerminationReason != TerminationReasonWebSocketClose {
		t.Fatalf("idempotent Finish changed reason: %q", completed.Summary.TerminationReason)
	}
	if completed.WebSocket.Messages[1].SourceMessageID != lineageID {
		t.Fatalf("replay source ID = %q", completed.WebSocket.Messages[1].SourceMessageID)
	}
}

func TestUnselectedAttemptBecomesTransitionStub(t *testing.T) {
	manager := newTestManager(t, nil)
	session := startTestSession(t, manager, 2, 1<<20, "selected")
	gateway := manager.BeginGateway(GatewayStart{GatewayRequestID: "trace"})
	stub := gateway.BeginHTTP(RawHTTPStart{
		Attempt: AttemptMetadata{
			Provider:             ProviderIdentity{ID: "unselected", Name: "Unselected"},
			APIType:              "claude",
			SelectionMode:        SelectionModeInitial,
			ProviderAttemptIndex: 1,
			CredentialPhase:      CredentialPhaseInitial,
		},
		URL:     testParsedURL("https://unselected.test/path?token=secret"),
		Request: RawRequest{CredentialEvidence: testCredentialEvidence("secret")},
	})
	if !stub.Valid() || stub.ID() != "" {
		t.Fatalf("stub recorder = valid %v ID %q", stub.Valid(), stub.ID())
	}
	stub.ObserveUpstream([]byte("must-not-be-retained"))
	stub.Finish(Outcome{
		TerminationReason:  TerminationReasonTransportError,
		Failure:            testFailure("Bearer secret failed"),
		CredentialEvidence: testCredentialEvidence("secret"),
	})

	selected := gateway.BeginHTTP(RawHTTPStart{
		Attempt: AttemptMetadata{
			Provider:             ProviderIdentity{ID: "selected", Name: "Selected"},
			APIType:              "claude",
			SelectionMode:        SelectionModeFailover,
			ProviderAttemptIndex: 0,
			CredentialPhase:      CredentialPhaseInitial,
		},
		URL: testParsedURL("https://selected.test/path"),
	})
	completeHTTP(selected, []byte("ok"))
	gateway.Finish(GatewayOutcome{})

	detail, err := readRecordDetailForTest(t, manager, session.SessionID, selected.ID(), 64)
	if err != nil {
		t.Fatalf("GetRecord() error = %v", err)
	}
	if detail.Summary.ExchangeIndex != 2 {
		t.Fatalf("selected exchange index = %d, want 2 physical attempts", detail.Summary.ExchangeIndex)
	}
	if detail.GatewayTrace == nil || len(detail.GatewayTrace.Entries) != 2 {
		t.Fatalf("trace = %#v", detail.GatewayTrace)
	}
	transition := detail.GatewayTrace.Entries[0]
	if transition.Kind != TraceEntryTransition ||
		transition.TerminationReason != TerminationReasonTransportError ||
		strings.Contains(transition.Failure.Primary.Message, "secret") {
		t.Fatalf("transition = %#v", transition)
	}
}

func TestExplicitTransitionDoesNotConsumeExchangeIndex(t *testing.T) {
	manager := newTestManager(t, nil)
	session := startTestSession(t, manager, 2, 1<<20, "selected")
	gateway := manager.BeginGateway(GatewayStart{GatewayRequestID: "trace"})
	gateway.Transition(TransitionStart{
		Attempt:           AttemptMetadata{Provider: ProviderIdentity{ID: "preparation"}},
		TerminationReason: TerminationReasonTransportError,
	})
	recorder := gateway.BeginHTTP(RawHTTPStart{
		Attempt: AttemptMetadata{Provider: ProviderIdentity{ID: "selected"}},
		URL:     testParsedURL("https://selected.test"),
	})
	completeHTTP(recorder, nil)
	gateway.Finish(GatewayOutcome{})
	detail, err := readRecordDetailForTest(t, manager, session.SessionID, recorder.ID(), 64)
	if err != nil {
		t.Fatalf("GetRecord() error = %v", err)
	}
	if detail.Summary.ExchangeIndex != 1 {
		t.Fatalf("exchange index = %d", detail.Summary.ExchangeIndex)
	}
}

func TestTransitionLimitMarksTraceTruncated(t *testing.T) {
	manager := newTestManager(t, func(cfg *Config) {
		cfg.MaxTransitionsPerTrace = 1
	})
	session := startTestSession(t, manager, 2, 1<<20, "selected")
	gateway := manager.BeginGateway(GatewayStart{GatewayRequestID: "trace"})
	for range 2 {
		gateway.Transition(TransitionStart{
			Attempt: AttemptMetadata{Provider: ProviderIdentity{ID: "other"}},
		})
	}
	recorder := gateway.BeginHTTP(RawHTTPStart{
		Attempt: AttemptMetadata{Provider: ProviderIdentity{ID: "selected"}},
		URL:     testParsedURL("https://selected.test"),
	})
	completeHTTP(recorder, nil)
	gateway.Finish(GatewayOutcome{})
	detail, err := readRecordDetailForTest(t, manager, session.SessionID, recorder.ID(), 64)
	if err != nil {
		t.Fatalf("GetRecord() error = %v", err)
	}
	if !detail.GatewayTrace.HistoryTruncatedAfter ||
		manager.Status().Session.DroppedTransitionCount != 1 ||
		manager.Status().Session.HistoryTruncatedTraceCount != 1 {
		t.Fatalf("truncation status = trace %#v session %#v", detail.GatewayTrace, manager.Status().Session)
	}
}

func TestPayloadOverflowKeepsTerminationMetadata(t *testing.T) {
	manager := newTestManager(t, func(cfg *Config) {
		cfg.ProcessCeilingBytes = 64 << 10
		cfg.DefaultSessionQuotaBytes = 32 << 10
		cfg.ChunkBytes = MinimumChunkBytes
		cfg.PreviewBytes = 64
		cfg.ExportLineBytes = 4096
		cfg.MaxActiveTraces = 1
		cfg.MaxActiveRecords = 1
	})
	session := startTestSession(t, manager, 2, 24<<10, "selected")
	gateway, recorder := beginTestHTTP(manager, "overflow", "selected", nil)
	payload := []byte(strings.Repeat("x", 64<<10))
	recorder.ObserveResponse(HTTPResponseHead{StatusCode: http.StatusOK, ContentLength: int64(len(payload))})
	recorder.ObserveUpstream(payload)
	recorder.Finish(Outcome{
		SourceCompletion:  SourceCompletionPartial,
		TerminationReason: TerminationReasonReadError,
		Failure:           testFailure("upstream failed"),
	})
	gateway.Finish(GatewayOutcome{})
	// Overflow deliberately exhausts the capture budget. Re-open headroom only
	// for the independently accounted query lease used to inspect the result.
	state := manager.active.Load()
	state.mu.Lock()
	manager.mu.Lock()
	state.quotaBytes = 1 << 20
	manager.cfg.processCeilingBytes = 1 << 20
	manager.mu.Unlock()
	state.mu.Unlock()
	detail, err := readRecordDetailForTest(t, manager, session.SessionID, recorder.ID(), 64)
	if err != nil {
		t.Fatalf("GetRecord() error = %v", err)
	}
	if detail.Summary.CaptureCompletion != CaptureCompletionOverflowed ||
		detail.Summary.SourceCompletion != SourceCompletionPartial ||
		detail.Summary.TerminationReason != TerminationReasonReadError {
		t.Fatalf("overflow completion = %#v", detail.Summary)
	}
	if detail.Summary.UpstreamObservedBytes != int64(len(payload)) {
		t.Fatalf("observed bytes = %d", detail.Summary.UpstreamObservedBytes)
	}
	if detail.HTTP.ResponseBody.CapturedBytes >= int64(len(payload)) ||
		detail.HTTP.ResponseBody.CapturedBytes == 0 {
		t.Fatalf("captured bytes = %d", detail.HTTP.ResponseBody.CapturedBytes)
	}
}
