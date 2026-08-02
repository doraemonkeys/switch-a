package requestcapture

import (
	"crypto/sha256"
	"math"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/requestcapture/redaction"
)

const additionalNormalWebSocketCloseCode = 1000

type additionalCapacityLimits struct {
	sessionQuota   int64
	processCeiling int64
}

func constrainAdditionalCapacity(session *sessionState, remaining int64) func() {
	session.mu.Lock()
	session.manager.mu.Lock()
	limits := additionalCapacityLimits{
		sessionQuota:   session.quotaBytes,
		processCeiling: session.manager.cfg.processCeilingBytes,
	}
	session.quotaBytes = session.chargedBytes + remaining
	session.manager.cfg.processCeilingBytes = session.manager.processCharged + remaining
	session.manager.mu.Unlock()
	session.mu.Unlock()
	return func() {
		session.mu.Lock()
		session.manager.mu.Lock()
		session.quotaBytes = limits.sessionQuota
		session.manager.cfg.processCeilingBytes = limits.processCeiling
		session.manager.mu.Unlock()
		session.mu.Unlock()
	}
}

func additionalSuccessfulWebSocketHandshake() WebSocketHandshake {
	return WebSocketHandshake{
		StatusCode:         http.StatusSwitchingProtocols,
		SensitiveHeaders:   testSensitiveHeaderEvidence(),
		CredentialEvidence: testCredentialEvidence(),
	}
}

func additionalWebSocketRecorder(t *testing.T) (*Manager, *sessionState, GatewayRecorder, Recorder, *recordState) {
	t.Helper()
	manager := newTestManager(t, nil)
	startTestSession(t, manager, 8, 1<<20, "selected")
	session := manager.active.Load()
	gateway := manager.BeginGateway(GatewayStart{GatewayRequestID: "additional-websocket"})
	recorder := gateway.BeginWebSocket(RawWebSocketStart{
		Attempt:   AttemptMetadata{Provider: ProviderIdentity{ID: "selected"}},
		TargetURL: "wss://selected.test/socket",
		Request: RawRequest{
			SensitiveHeaders:   testSensitiveHeaderEvidence(),
			CredentialEvidence: testCredentialEvidence(),
		},
	})
	if !recorder.Valid() {
		t.Fatal("websocket recorder setup failed")
	}
	return manager, session, gateway, recorder, testRecordState(t, recorder)
}

func TestAdditionalBlobFailurePathsPreserveOwnership(t *testing.T) {
	manager := newTestManager(t, nil)
	startTestSession(t, manager, 2, 1<<20, "selected")
	session := manager.active.Load()

	restoreCapacity := constrainAdditionalCapacity(session, 0)
	session.mu.Lock()
	if value, ok := newBlobLocked(session); ok || value != nil {
		session.mu.Unlock()
		t.Fatal("blob allocation succeeded without retained capacity")
	}
	if value, ok := newImmutableBlobLocked(session, []byte("denied")); ok || value != nil {
		session.mu.Unlock()
		t.Fatal("immutable blob allocation succeeded without retained capacity")
	}
	builder := blobBuilder{}
	if captured := builder.appendLocked(session, []byte("denied")); captured != 0 || !builder.overflowed {
		session.mu.Unlock()
		t.Fatalf("denied append captured=%d overflowed=%t", captured, builder.overflowed)
	}
	if captured := builder.appendLocked(session, []byte("retry")); captured != 0 {
		session.mu.Unlock()
		t.Fatalf("overflowed builder captured %d retry bytes", captured)
	}
	var nilBuilder *blobBuilder
	if captured := nilBuilder.appendLocked(session, []byte("ignored")); captured != 0 {
		session.mu.Unlock()
		t.Fatalf("nil builder captured %d bytes", captured)
	}
	session.mu.Unlock()
	restoreCapacity()

	session.mu.Lock()
	baseline := session.chargedBytes
	payload := make([]byte, manager.cfg.chunkBytes+1)
	value, complete := newImmutableBlobLocked(session, payload)
	if !complete || value == nil || value.first == nil {
		session.mu.Unlock()
		t.Fatal("ownership test blob setup failed")
	}
	chunk := value.first
	if !retainBlobLocked(nil) {
		session.mu.Unlock()
		t.Fatal("nil blob should not require ownership")
	}
	releaseChunkLocked(nil, false)
	logBlobInvariant(nil, "test", "nil_session")

	chunk.refs.Store(0)
	refFailure := snapshotBlobPrefixLocked(value, 1)
	chunk.refs.Store(1)
	if refFailure.failure != ErrInternalFailure || chunk.pins.Load() != 0 {
		session.mu.Unlock()
		t.Fatalf("released-chunk snapshot did not fail closed: %#v", refFailure)
	}

	chunk.pins.Store(-1)
	pinFailure := snapshotBlobPrefixLocked(value, 1)
	chunk.pins.Store(0)
	if pinFailure.failure != ErrInternalFailure || chunk.refs.Load() != 1 {
		session.mu.Unlock()
		t.Fatalf("corrupt-pin snapshot did not roll back: %#v refs=%d", pinFailure, chunk.refs.Load())
	}

	chunk.refs.Store(0)
	releaseChunkLocked(chunk, false)
	chunk.refs.Store(1)
	releaseChunkLocked(chunk, true)
	if chunk.refs.Load() != 1 || chunk.pins.Load() != 0 {
		session.mu.Unlock()
		t.Fatalf("rejected chunk releases changed ownership: refs=%d pins=%d", chunk.refs.Load(), chunk.pins.Load())
	}

	var counter atomic.Int64
	if value, ok := decrementPositiveAtomic(&counter); ok || value != 0 {
		session.mu.Unlock()
		t.Fatalf("zero ownership decrement = %d, %t", value, ok)
	}
	emptyPrefix := snapshotBlobPrefixLocked(value, -1)
	if len(emptyPrefix.segments) != 0 || emptyPrefix.size != int64(len(payload)) {
		session.mu.Unlock()
		t.Fatalf("negative prefix snapshot = %#v", emptyPrefix)
	}

	checksum := sha256.Sum256(nil)
	if preview := previewChunkChain(nil, 0, -1, checksum); preview.PreviewBytes != 0 {
		session.mu.Unlock()
		t.Fatalf("negative chunk preview = %#v", preview)
	}
	if preview := previewSegments(nil, 0, -1, checksum); preview.PreviewBytes != 0 {
		session.mu.Unlock()
		t.Fatalf("negative segment preview = %#v", preview)
	}
	if preview := previewSegments([]blobViewSegment{{data: []byte("unused")}}, 0, 1, checksum); preview.PreviewBytes != 0 {
		session.mu.Unlock()
		t.Fatalf("empty segment preview = %#v", preview)
	}

	releaseBlobLocked(value)
	if session.chargedBytes != baseline {
		session.mu.Unlock()
		t.Fatalf("failure-path blob ownership leaked: got=%d want=%d", session.chargedBytes, baseline)
	}
	session.mu.Unlock()

	var nilView *blobView
	nilView.release()
	(&blobView{}).release()
	emptyPrefix.release()
	refFailure.release()
	pinFailure.release()
}

func TestAdditionalNilCoreReceiversAreNoops(t *testing.T) {
	var session *sessionState
	if session.beginGateway(GatewayStart{}).Valid() {
		t.Fatal("nil session produced a gateway")
	}
	var gateway *gatewayState
	if gateway.newMessageIDLocked().Valid() {
		t.Fatal("nil gateway produced lineage")
	}
	if gateway.beginRecordLocked(ProtocolHTTP, AttemptMetadata{}, RawRequest{}, redaction.InvalidTarget()).Valid() {
		t.Fatal("nil gateway produced a recorder")
	}
	gateway.finishLocked(GatewayOutcome{})

	var record *recordState
	record.observeHTTPResponseLocked(HTTPResponseHead{})
	record.observeWebSocketHandshakeLocked(WebSocketHandshake{})
	record.observeUpstreamLocked([]byte("ignored"))
	record.observeClientWriteLocked(1)
	if record.messageReadLocked(MessageRead{}).Valid() {
		t.Fatal("nil record produced a message")
	}
	record.messageResultLocked(MessageRef{}, MessageResult{})
	record.consumeDeniedMessageLineageLocked(nil)

	var transition *transitionRecorderState
	transition.finishLocked(Outcome{})
	if transition.messageLineageLocked(MessageRead{}).Valid() {
		t.Fatal("nil transition produced a message capability")
	}

	manager := newTestManager(t, nil)
	startTestSession(t, manager, 1, 1<<20, "selected")
	active := manager.active.Load()
	active.mu.Lock()
	active.releaseRecordLocked(nil)
	orphan := &recordState{session: active, messages: []*messageState{nil}}
	orphan.boundSession.Store(active)
	active.releaseRecordLocked(orphan)
	active.mu.Unlock()
	if orphan.session != nil || orphan.boundSession.Load() != nil {
		t.Fatalf("orphan record ownership was not severed: %#v", orphan)
	}
}

func TestAdditionalRecorderCapabilitiesFailClosedWhenSessionRevoked(t *testing.T) {
	manager := newTestManager(t, nil)
	startTestSession(t, manager, 2, 1<<20, "selected")
	session := manager.active.Load()
	gateway, recorder := beginTestHTTP(manager, "capability", "selected", nil)

	if !recorder.CapturesPayload() || (Recorder{}).CapturesPayload() {
		t.Fatalf("payload capability classification record=%t zero=%t", recorder.CapturesPayload(), (Recorder{}).CapturesPayload())
	}
	malformed := recorder
	malformed.kind = recorderKind(255)
	if malformed.validShape() || malformed.Valid() {
		t.Fatal("unknown recorder kind resolved a capability")
	}

	session.mu.Lock()
	session.accepting = false
	session.mu.Unlock()
	if gateway.Valid() || recorder.Valid() {
		t.Fatal("capability resolved while the session rejected mutations")
	}
	session.mu.Lock()
	session.accepting = true
	session.mu.Unlock()
	if !gateway.Valid() || !recorder.Valid() {
		t.Fatal("restored session did not resolve live capabilities")
	}
}

func TestAdditionalGatewayGuardsLimitsAndSafetyFinalization(t *testing.T) {
	t.Run("session gate and bounded request identifier", func(t *testing.T) {
		manager := newTestManager(t, nil)
		startTestSession(t, manager, 2, 1<<20, "selected")
		session := manager.active.Load()
		session.mu.Lock()
		session.accepting = false
		session.mu.Unlock()
		if session.beginGateway(GatewayStart{GatewayRequestID: "rejected"}).Valid() {
			t.Fatal("non-accepting session produced a gateway")
		}
		session.mu.Lock()
		session.accepting = true
		session.mu.Unlock()

		gateway := manager.BeginGateway(GatewayStart{
			GatewayRequestID: strings.Repeat("x", maxRetainedIdentifierBytes+1),
		})
		if !gateway.Valid() {
			t.Fatal("bounded request identifier rejected the whole gateway")
		}
		gateway.Finish(GatewayOutcome{})
	})

	t.Run("active record limit", func(t *testing.T) {
		manager := newTestManager(t, func(cfg *Config) { cfg.MaxActiveRecords = 1 })
		startTestSession(t, manager, 2, 1<<20, "selected")
		gateway, first := beginTestHTTP(manager, "record-limit", "selected", nil)
		second := gateway.BeginHTTP(RawHTTPStart{
			Attempt: AttemptMetadata{Provider: ProviderIdentity{ID: "selected"}},
			URL:     testParsedURL("https://selected.test/second"),
		})
		if !first.Valid() || second.Valid() {
			t.Fatalf("record limit first=%t second=%t", first.Valid(), second.Valid())
		}
		first.Finish(Outcome{TerminationReason: TerminationReasonEOF})
		gateway.Finish(GatewayOutcome{})
	})

	t.Run("record admission denial", func(t *testing.T) {
		manager := newTestManager(t, nil)
		startTestSession(t, manager, 2, 1<<20, "selected")
		session := manager.active.Load()
		gateway := manager.BeginGateway(GatewayStart{GatewayRequestID: "record-denial"})
		restore := constrainAdditionalCapacity(session, 0)
		recorder := gateway.BeginHTTP(RawHTTPStart{
			Attempt: AttemptMetadata{Provider: ProviderIdentity{ID: "selected"}},
			URL:     testParsedURL("https://selected.test/denied"),
		})
		restore()
		if recorder.Valid() {
			t.Fatal("record metadata was admitted without capacity")
		}
		gateway.Finish(GatewayOutcome{})
	})

	t.Run("gateway safety net", func(t *testing.T) {
		manager := newTestManager(t, nil)
		startTestSession(t, manager, 4, 1<<20, "selected")
		gateway, first := beginTestHTTP(manager, "safety-net", "selected", []byte("shared"))
		second := gateway.BeginHTTP(RawHTTPStart{
			Attempt: AttemptMetadata{Provider: ProviderIdentity{ID: "selected"}},
			URL:     testParsedURL("https://selected.test/retry"),
			Request: RawRequest{Body: []byte("different")},
		})
		firstState := testRecordState(t, first)
		secondState := testRecordState(t, second)
		gatewayState := lookupGatewayForTest(gateway)
		gateway.Finish(GatewayOutcome{Failure: testFailure("gateway abandoned active records")})
		if !firstState.completed || !secondState.completed ||
			secondState.summary.CaptureCompletion != CaptureCompletionOverflowed {
			t.Fatalf("safety finalization first=%t second=%t completion=%q", firstState.completed, secondState.completed, secondState.summary.CaptureCompletion)
		}
		if gatewayState.sharedRequest != nil || gatewayState.sharedRequestInitialized == false {
			t.Fatalf("shared request ownership survived gateway finish: %#v", gatewayState.sharedRequest)
		}
	})
}

func TestAdditionalTransitionRollbackAndLineageValidation(t *testing.T) {
	t.Run("stub finish denial", func(t *testing.T) {
		manager := newTestManager(t, nil)
		startTestSession(t, manager, 2, 1<<20, "selected")
		session := manager.active.Load()
		gateway := manager.BeginGateway(GatewayStart{GatewayRequestID: "transition-lineage"})
		lineage := gateway.NewMessageID()
		stub := gateway.BeginHTTP(RawHTTPStart{
			Attempt: AttemptMetadata{Provider: ProviderIdentity{ID: "unselected"}},
			URL:     testParsedURL("https://unselected.test/path"),
		})
		if !stub.Valid() || stub.CapturesPayload() || !lineage.Valid() {
			t.Fatalf("transition setup stub=%t payload=%t lineage=%t", stub.Valid(), stub.CapturesPayload(), lineage.Valid())
		}
		for _, input := range []MessageRead{
			{Direction: MessageDirectionUpstreamToClient, Type: MessageTypeText, Source: MessageSourceLive, Lineage: lineage},
			{Direction: MessageDirectionClientToUpstream, Type: MessageType("invalid"), Source: MessageSourceLive, Lineage: lineage},
			{Direction: MessageDirectionClientToUpstream, Type: MessageTypeText, Source: MessageSourceReplay, Lineage: lineage},
			{Direction: MessageDirectionClientToUpstream, Type: MessageTypeText, Source: MessageSourceLive},
		} {
			if ref := stub.MessageRead(input); ref.manager != nil {
				t.Fatalf("invalid transition message produced capability: %#v", ref)
			}
		}
		lineageRef := stub.MessageRead(MessageRead{
			Direction: MessageDirectionClientToUpstream,
			Type:      MessageTypeText,
			Source:    MessageSourceLive,
			Lineage:   lineage,
		})
		if lineageRef.manager != manager || lineageRef.generation != lineage.generation ||
			lineageRef.traceSequence != lineage.traceSequence || lineageRef.lineage != lineage.lineage {
			t.Fatalf("transition lineage capability = %#v", lineageRef)
		}
		if duplicate := gateway.BeginHTTP(RawHTTPStart{
			Attempt: AttemptMetadata{Provider: ProviderIdentity{ID: "also-unselected"}},
		}); duplicate.Valid() {
			t.Fatal("second live transition recorder was admitted")
		}

		restore := constrainAdditionalCapacity(session, 0)
		stub.Finish(Outcome{
			TerminationReason: TerminationReasonTransportError,
			Failure:           testFailure("transition failure"),
		})
		restore()
		state := lookupGatewayForTest(gateway)
		if state == nil || state.activeTransition != nil || !state.entryFirst.snapshot.MetadataTruncated {
			t.Fatalf("denied transition finish did not fail closed: %#v", state)
		}
		gateway.Finish(GatewayOutcome{})
	})

	t.Run("stub allocation rollback", func(t *testing.T) {
		manager := newTestManager(t, nil)
		startTestSession(t, manager, 2, 1<<20, "selected")
		session := manager.active.Load()
		gatewayRecorder := manager.BeginGateway(GatewayStart{GatewayRequestID: "transition-rollback"})
		gateway := lookupGatewayForTest(gatewayRecorder)
		input := RawHTTPStart{
			Attempt: AttemptMetadata{Provider: ProviderIdentity{ID: "unselected"}},
			URL:     testParsedURL("https://unselected.test/rollback"),
		}

		session.mu.Lock()
		trial := gateway.appendTransitionTargetLocked(
			TransitionStart{Attempt: input.Attempt},
			redaction.BorrowedHTTPTarget(input.URL),
		)
		if trial == nil {
			session.mu.Unlock()
			t.Fatal("transition charge probe failed")
		}
		entryCharge := trial.charge
		gateway.releaseEntryLocked(trial)
		baselineCharge := session.chargedBytes
		session.mu.Unlock()

		restore := constrainAdditionalCapacity(session, entryCharge)
		denied := gatewayRecorder.BeginHTTP(input)
		restore()
		if denied.Valid() {
			t.Fatal("underfunded transition stub was admitted")
		}
		session.mu.Lock()
		defer session.mu.Unlock()
		if gateway.entryCount != 0 || gateway.transitionCount != 0 || gateway.activeTransition != nil ||
			session.chargedBytes != baselineCharge {
			t.Fatalf("transition rollback leaked graph/account ownership: gateway=%#v charged=%d want=%d", gateway, session.chargedBytes, baselineCharge)
		}
	})
}

func TestAdditionalRecordStateRejectsInvalidTransitions(t *testing.T) {
	tests := []struct {
		name     string
		protocol Protocol
		apply    func(Recorder)
	}{
		{name: "upstream before response", protocol: ProtocolHTTP, apply: func(recorder Recorder) {
			recorder.ObserveUpstream([]byte("early"))
		}},
		{name: "client write before response", protocol: ProtocolHTTP, apply: func(recorder Recorder) {
			recorder.ObserveClientWrite(1)
		}},
		{name: "websocket handshake on HTTP", protocol: ProtocolHTTP, apply: func(recorder Recorder) {
			recorder.ObserveWebSocketHandshake(additionalSuccessfulWebSocketHandshake())
		}},
		{name: "HTTP response on websocket", protocol: ProtocolWebSocket, apply: func(recorder Recorder) {
			recorder.ObserveHTTPResponse(HTTPResponseHead{StatusCode: http.StatusOK})
		}},
		{name: "duplicate HTTP response", protocol: ProtocolHTTP, apply: func(recorder Recorder) {
			recorder.ObserveHTTPResponse(HTTPResponseHead{StatusCode: http.StatusOK})
			recorder.ObserveHTTPResponse(HTTPResponseHead{StatusCode: http.StatusOK})
		}},
		{name: "duplicate websocket handshake", protocol: ProtocolWebSocket, apply: func(recorder Recorder) {
			recorder.ObserveWebSocketHandshake(additionalSuccessfulWebSocketHandshake())
			recorder.ObserveWebSocketHandshake(additionalSuccessfulWebSocketHandshake())
		}},
		{name: "message before websocket handshake", protocol: ProtocolWebSocket, apply: func(recorder Recorder) {
			recorder.MessageRead(MessageRead{Direction: MessageDirectionClientToUpstream, Type: MessageTypeText})
		}},
		{name: "invalid message direction", protocol: ProtocolWebSocket, apply: func(recorder Recorder) {
			recorder.ObserveWebSocketHandshake(additionalSuccessfulWebSocketHandshake())
			recorder.MessageRead(MessageRead{Direction: MessageDirection("sideways"), Type: MessageTypeText})
		}},
		{name: "invalid message type", protocol: ProtocolWebSocket, apply: func(recorder Recorder) {
			recorder.ObserveWebSocketHandshake(additionalSuccessfulWebSocketHandshake())
			recorder.MessageRead(MessageRead{Direction: MessageDirectionClientToUpstream, Type: MessageType("unknown")})
		}},
		{name: "invalid message source", protocol: ProtocolWebSocket, apply: func(recorder Recorder) {
			recorder.ObserveWebSocketHandshake(additionalSuccessfulWebSocketHandshake())
			recorder.MessageRead(MessageRead{
				Direction: MessageDirectionClientToUpstream,
				Type:      MessageTypeText,
				Source:    MessageSource("unknown"),
			})
		}},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			manager := newTestManager(t, nil)
			startTestSession(t, manager, 2, 1<<20, "selected")
			gateway := manager.BeginGateway(GatewayStart{GatewayRequestID: testCase.name})
			var recorder Recorder
			if testCase.protocol == ProtocolHTTP {
				recorder = gateway.BeginHTTP(RawHTTPStart{
					Attempt: AttemptMetadata{Provider: ProviderIdentity{ID: "selected"}},
					URL:     testParsedURL("https://selected.test/http"),
				})
			} else {
				recorder = gateway.BeginWebSocket(RawWebSocketStart{
					Attempt:   AttemptMetadata{Provider: ProviderIdentity{ID: "selected"}},
					TargetURL: "wss://selected.test/socket",
				})
			}
			state := testRecordState(t, recorder)
			testCase.apply(recorder)
			if !state.disabled || !state.overflowCounted ||
				state.summary.CaptureCompletion != CaptureCompletionOverflowed {
				t.Fatalf("invalid transition did not disable capture: %#v", state)
			}
		})
	}
}

func TestAdditionalMessageAdmissionAndResultFailures(t *testing.T) {
	t.Run("payload allocation degrades after message admission", func(t *testing.T) {
		_, session, gateway, recorder, state := additionalWebSocketRecorder(t)
		recorder.ObserveWebSocketHandshake(additionalSuccessfulWebSocketHandshake())
		gatewayState := lookupGatewayForTest(gateway)
		nextLineage := gatewayState.nextLineage + 1
		messageCharge := addRetainedCharge(
			messageBaseChargeBytes,
			messageIDEncodedBytes(session.generation, gatewayState.traceSequence, nextLineage),
		)
		restore := constrainAdditionalCapacity(session, messageCharge)
		ref := recorder.MessageRead(MessageRead{
			Direction: MessageDirectionUpstreamToClient,
			Type:      MessageTypeText,
			Payload:   []byte("payload cannot be retained"),
		})
		restore()
		if !ref.Valid() || len(state.messages) != 1 || state.messages[0].payload != nil ||
			state.summary.CaptureCompletion != CaptureCompletionOverflowed {
			t.Fatalf("partial message admission = ref:%#v record:%#v", ref, state)
		}

		second := recorder.MessageRead(MessageRead{
			Lineage:   ref.Lineage(),
			Direction: MessageDirectionUpstreamToClient,
			Type:      MessageTypeText,
		})
		if !second.Valid() || second.lineage == ref.lineage {
			t.Fatalf("used lineage was reused: first=%#v second=%#v", ref, second)
		}
	})

	t.Run("invalid result disposition", func(t *testing.T) {
		_, _, _, recorder, state := additionalWebSocketRecorder(t)
		recorder.ObserveWebSocketHandshake(additionalSuccessfulWebSocketHandshake())
		ref := recorder.MessageRead(MessageRead{Direction: MessageDirectionUpstreamToClient, Type: MessageTypeText})
		recorder.MessageResult(MessageRef{}, MessageResult{Disposition: MessageDispositionForwarded})
		recorder.MessageResult(ref, MessageResult{Disposition: MessageDisposition("invalid")})
		if !state.disabled || state.messages[0].resultSet {
			t.Fatalf("invalid disposition was accepted: %#v", state.messages[0])
		}
	})

	t.Run("write confirmation requires forwarding", func(t *testing.T) {
		_, _, _, recorder, state := additionalWebSocketRecorder(t)
		recorder.ObserveWebSocketHandshake(additionalSuccessfulWebSocketHandshake())
		ref := recorder.MessageRead(MessageRead{Direction: MessageDirectionUpstreamToClient, Type: MessageTypeText})
		recorder.MessageResult(ref, MessageResult{
			Disposition:    MessageDispositionSuppressed,
			WriteConfirmed: true,
		})
		if !state.disabled || state.messages[0].resultSet {
			t.Fatalf("contradictory result was accepted: %#v", state.messages[0])
		}
	})

	t.Run("failure metadata denial", func(t *testing.T) {
		_, session, _, recorder, state := additionalWebSocketRecorder(t)
		recorder.ObserveWebSocketHandshake(additionalSuccessfulWebSocketHandshake())
		ref := recorder.MessageRead(MessageRead{Direction: MessageDirectionUpstreamToClient, Type: MessageTypeText})
		restore := constrainAdditionalCapacity(session, 0)
		recorder.MessageResult(ref, MessageResult{
			Disposition:        MessageDispositionWriteFailed,
			Failure:            testFailure("failure cannot be retained"),
			CredentialEvidence: testCredentialEvidence(),
		})
		restore()
		message := state.messages[0]
		if !message.resultSet || message.hasFailure ||
			state.summary.CaptureCompletion != CaptureCompletionOverflowed {
			t.Fatalf("denied result metadata: resultSet=%t hasFailure=%t completion=%q failureTruncated=%t", message.resultSet, message.hasFailure, state.summary.CaptureCompletion, message.failure.Truncated)
		}
	})

	t.Run("confirmed client message consumes pending lineage", func(t *testing.T) {
		_, session, gateway, recorder, state := additionalWebSocketRecorder(t)
		recorder.ObserveWebSocketHandshake(additionalSuccessfulWebSocketHandshake())
		lineage := gateway.NewMessageID()
		gatewayState := lookupGatewayForTest(gateway)
		ref := recorder.MessageRead(MessageRead{
			Lineage:   lineage,
			Direction: MessageDirectionClientToUpstream,
			Type:      MessageTypeBinary,
		})
		recorder.MessageResult(ref, MessageResult{
			Disposition:    MessageDispositionForwarded,
			WriteConfirmed: true,
		})
		session.mu.Lock()
		pending := gatewayState.findPendingLineageLocked(lineage.lineage)
		session.mu.Unlock()
		if pending != nil || !state.messages[0].resultSet {
			t.Fatalf("confirmed lineage remained pending: pending=%#v message=%#v", pending, state.messages[0])
		}
	})
}

func TestAdditionalFinishMetadataFailuresAreFailClosed(t *testing.T) {
	t.Run("HTTP outcome metadata denial", func(t *testing.T) {
		manager := newTestManager(t, nil)
		startTestSession(t, manager, 2, 1<<20, "selected")
		session := manager.active.Load()
		gateway := manager.BeginGateway(GatewayStart{GatewayRequestID: "finish-denial"})
		recorder := gateway.BeginHTTP(RawHTTPStart{
			Attempt: AttemptMetadata{Provider: ProviderIdentity{ID: "selected"}},
			URL:     testParsedURL("https://selected.test/safe"),
			Request: RawRequest{
				SensitiveHeaders:   testSensitiveHeaderEvidence(),
				CredentialEvidence: testCredentialEvidence(),
			},
		})
		state := testRecordState(t, recorder)
		recorder.ObserveHTTPResponse(HTTPResponseHead{
			StatusCode:         http.StatusOK,
			SensitiveHeaders:   testSensitiveHeaderEvidence(),
			CredentialEvidence: testCredentialEvidence(),
		})
		restore := constrainAdditionalCapacity(session, 0)
		recorder.Finish(Outcome{
			SourceCompletion:   SourceCompletionPartial,
			TerminationReason:  TerminationReasonReadError,
			ResponseTrailers:   http.Header{"X-Safe": {"visible"}},
			Failure:            testFailure("failure cannot be retained"),
			CredentialEvidence: testCredentialEvidence(),
		})
		restore()
		if !state.completed || state.summary.HasFailure || len(state.httpResponse.Trailers) != 0 ||
			state.summary.TerminationReason != TerminationReasonGatewayFinished ||
			state.summary.CaptureCompletion != CaptureCompletionOverflowed {
			t.Fatalf("denied HTTP finish metadata: completed=%t hasFailure=%t trailers=%d termination=%q completion=%q", state.completed, state.summary.HasFailure, len(state.httpResponse.Trailers), state.summary.TerminationReason, state.summary.CaptureCompletion)
		}
	})

	t.Run("websocket close reason denial", func(t *testing.T) {
		_, session, _, recorder, state := additionalWebSocketRecorder(t)
		recorder.ObserveWebSocketHandshake(additionalSuccessfulWebSocketHandshake())
		restore := constrainAdditionalCapacity(session, 0)
		recorder.Finish(Outcome{
			TerminationReason:  TerminationReasonWebSocketClose,
			CredentialEvidence: testCredentialEvidence(),
			WebSocketClose: &WebSocketCloseObservation{
				Direction: MessageDirectionUpstreamToClient,
				Code:      additionalNormalWebSocketCloseCode,
				Reason:    "ordinary close",
				Clean:     true,
			},
		})
		restore()
		if state.wsClose == nil || !state.wsClose.ReasonTruncated || state.wsClose.Reason != "" ||
			state.summary.CaptureCompletion != CaptureCompletionOverflowed {
			t.Fatalf("denied websocket close metadata = %#v record=%#v", state.wsClose, state.summary)
		}
	})

	for _, testCase := range []struct {
		name      string
		protocol  Protocol
		direction MessageDirection
	}{
		{name: "close on HTTP", protocol: ProtocolHTTP, direction: MessageDirectionUpstreamToClient},
		{name: "invalid close direction", protocol: ProtocolWebSocket, direction: MessageDirection("sideways")},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			manager := newTestManager(t, nil)
			startTestSession(t, manager, 2, 1<<20, "selected")
			gateway := manager.BeginGateway(GatewayStart{GatewayRequestID: testCase.name})
			var recorder Recorder
			if testCase.protocol == ProtocolHTTP {
				recorder = gateway.BeginHTTP(RawHTTPStart{
					Attempt: AttemptMetadata{Provider: ProviderIdentity{ID: "selected"}},
					URL:     testParsedURL("https://selected.test/http"),
				})
			} else {
				recorder = gateway.BeginWebSocket(RawWebSocketStart{
					Attempt:   AttemptMetadata{Provider: ProviderIdentity{ID: "selected"}},
					TargetURL: "wss://selected.test/socket",
				})
			}
			state := testRecordState(t, recorder)
			recorder.Finish(Outcome{
				WebSocketClose: &WebSocketCloseObservation{Direction: testCase.direction, Reason: "close"},
			})
			if !state.disabled || !state.completed || state.summary.CaptureCompletion != CaptureCompletionOverflowed {
				t.Fatalf("invalid close transition did not fail closed: %#v", state)
			}
		})
	}
}

func TestAdditionalFallbackLineageOverflowFailsClosed(t *testing.T) {
	_, session, gateway, recorder, state := additionalWebSocketRecorder(t)
	recorder.ObserveWebSocketHandshake(additionalSuccessfulWebSocketHandshake())
	gatewayState := lookupGatewayForTest(gateway)
	session.mu.Lock()
	gatewayState.nextLineage = math.MaxUint64
	session.mu.Unlock()
	if ref := recorder.MessageRead(MessageRead{
		Direction: MessageDirectionUpstreamToClient,
		Type:      MessageTypeText,
	}); ref.Valid() {
		t.Fatal("fallback lineage overflow produced a message")
	}
	if state.summary.CaptureCompletion != CaptureCompletionOverflowed || len(state.messages) != 0 {
		t.Fatalf("fallback lineage overflow state = %#v messages=%d", state.summary, len(state.messages))
	}
}
