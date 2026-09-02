package requestcapture

import (
	"encoding/binary"
	"errors"
	"math"
	"net/http"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

type gatewayAllocationIDGenerator struct {
	calls   atomic.Uint64
	failAt  uint64
	blockAt uint64
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (generator *gatewayAllocationIDGenerator) NewID() ([16]byte, error) {
	call := generator.calls.Add(1)
	if call == generator.blockAt {
		generator.once.Do(func() { close(generator.entered) })
		<-generator.release
	}
	if call == generator.failAt {
		return [16]byte{}, errors.New("credential-like generator detail must not be logged")
	}
	var id [16]byte
	binary.BigEndian.PutUint64(id[8:], call)
	return id, nil
}

type gatewayAllocationAccountSnapshot struct {
	sessionCharged   int64
	sessionTemporary int64
	processCharged   int64
	processTemporary int64
	processReleasing int64
	traceCount       int
	pendingCount     int
}

var (
	gatewayRequestIDShapeSink gatewayRequestIDShape
	gatewayRecorderSink       GatewayRecorder
	messageLineageSink        MessageLineage
)

func lookupGatewayForTest(recorder GatewayRecorder) *gatewayState {
	access := recorder.acquire()
	gateway := access.gateway
	access.release()
	return gateway
}

func snapshotGatewayAllocationAccount(session *sessionState, gateway *gatewayState) gatewayAllocationAccountSnapshot {
	session.mu.Lock()
	manager := session.manager
	snapshot := gatewayAllocationAccountSnapshot{
		sessionCharged:   session.chargedBytes,
		sessionTemporary: session.temporaryBytes,
		traceCount:       session.traceCount,
	}
	if gateway != nil {
		snapshot.pendingCount = gateway.pendingLineageCount
	}
	session.mu.Unlock()
	if manager != nil {
		manager.mu.Lock()
		snapshot.processCharged = manager.processCharged
		snapshot.processTemporary = manager.processTemporary
		snapshot.processReleasing = manager.processReleasing
		manager.mu.Unlock()
	}
	return snapshot
}

func setGatewayAllocationRemainingCapacity(session *sessionState, sessionBytes, processBytes int64) {
	session.mu.Lock()
	defer session.mu.Unlock()
	session.manager.mu.Lock()
	defer session.manager.mu.Unlock()
	session.quotaBytes = session.chargedBytes + sessionBytes
	session.manager.cfg.processCeilingBytes = session.manager.processCharged + processBytes
}

func gatewayCandidateBytesForTest(t *testing.T, session *sessionState, requestID string) int64 {
	t.Helper()
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.nextTraceSequence == math.MaxUint64 {
		t.Fatal("trace sequence exhausted in test setup")
	}
	candidate, ok := beginGatewayCandidateBytes(
		scanGatewayRequestID(requestID),
		session.generation,
		session.nextTraceSequence+1,
	)
	if !ok {
		t.Fatal("gateway candidate sizing failed")
	}
	return candidate
}

func TestGatewayRequestIDScanIsBoundedAndAllocationFree(t *testing.T) {
	cases := []struct {
		name          string
		value         string
		kind          gatewayRequestIDKind
		retainedBytes int64
		truncated     bool
	}{
		{name: "empty", kind: gatewayRequestIDGenerated, retainedBytes: int64(generatedGatewayRequestIDBytes)},
		{name: "whitespace", value: " \t ", kind: gatewayRequestIDTruncated, truncated: true},
		{name: "noncanonical_whitespace", value: " gateway ", kind: gatewayRequestIDTruncated, truncated: true},
		{name: "one", value: "x", kind: gatewayRequestIDBorrowed, retainedBytes: 1},
		{name: "cap_minus_one", value: strings.Repeat("x", maxRetainedIdentifierBytes-1), kind: gatewayRequestIDBorrowed, retainedBytes: maxRetainedIdentifierBytes - 1},
		{name: "cap", value: strings.Repeat("x", maxRetainedIdentifierBytes), kind: gatewayRequestIDBorrowed, retainedBytes: maxRetainedIdentifierBytes},
		{name: "cap_plus_one", value: strings.Repeat("x", maxRetainedIdentifierBytes+1), kind: gatewayRequestIDTruncated, truncated: true},
		{name: "huge", value: strings.Repeat("x", 8<<20), kind: gatewayRequestIDTruncated, truncated: true},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			allocations := testing.AllocsPerRun(1000, func() {
				gatewayRequestIDShapeSink = scanGatewayRequestID(testCase.value)
			})
			if allocations != 0 {
				t.Fatalf("scan allocations = %f, want 0", allocations)
			}
			shape := scanGatewayRequestID(testCase.value)
			if shape.kind != testCase.kind || shape.retainedBytes != testCase.retainedBytes ||
				shape.truncated != testCase.truncated || !shape.valid() {
				t.Fatalf("shape = %#v", shape)
			}
		})
	}
}

func TestBeginGatewayAllocationExactBoundaries(t *testing.T) {
	cases := []struct {
		name              string
		sessionAdjustment int64
		processAdjustment int64
		wantValid         bool
	}{
		{name: "exact", wantValid: true},
		{name: "session_exact_minus_one", sessionAdjustment: -1},
		{name: "process_exact_minus_one", processAdjustment: -1},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			manager := newTestManager(t, nil)
			_ = startTestSession(t, manager, 1, 1<<20, "selected")
			session := manager.active.Load()
			candidate := gatewayCandidateBytesForTest(t, session, "x")
			if candidate >= traceBaseChargeBytes+maxRetainedIdentifierBytes {
				t.Fatalf("small gateway candidate %d used the global identifier maximum", candidate)
			}
			baseline := snapshotGatewayAllocationAccount(session, nil)
			setGatewayAllocationRemainingCapacity(
				session,
				candidate+testCase.sessionAdjustment,
				candidate+testCase.processAdjustment,
			)

			gateway := manager.BeginGateway(GatewayStart{
				GatewayRequestID: "x",
				StartedAt:        time.Unix(1, 0),
			})
			if gateway.Valid() != testCase.wantValid {
				t.Fatalf("gateway valid = %t, want %t", gateway.Valid(), testCase.wantValid)
			}
			after := snapshotGatewayAllocationAccount(session, lookupGatewayForTest(gateway))
			if after.sessionTemporary != 0 || after.processTemporary != 0 {
				t.Fatalf("temporary accounting leaked: %#v", after)
			}
			if testCase.wantValid {
				state := lookupGatewayForTest(gateway)
				if state == nil || state.charge != candidate || after.traceCount != 1 ||
					after.sessionCharged != baseline.sessionCharged+candidate ||
					after.processCharged != baseline.processCharged+candidate {
					t.Fatalf("exact admission state = %#v gateway=%#v", after, state)
				}
				return
			}
			if after.traceCount != 0 || after.sessionCharged != baseline.sessionCharged ||
				after.processCharged != baseline.processCharged {
				t.Fatalf("denied admission changed graph/account: before=%#v after=%#v", baseline, after)
			}
		})
	}
}

func TestBeginGatewayDenialDoesNotMaterialize(t *testing.T) {
	ids := &gatewayAllocationIDGenerator{}
	manager := newTestManager(t, func(cfg *Config) {
		cfg.IDGenerator = ids
	})
	_ = startTestSession(t, manager, 1, 1<<20, "selected")
	session := manager.active.Load()
	candidate := gatewayCandidateBytesForTest(t, session, "")
	setGatewayAllocationRemainingCapacity(session, candidate-1, candidate-1)
	baselineCalls := ids.calls.Load()

	allocations := testing.AllocsPerRun(1000, func() {
		gatewayRecorderSink = manager.BeginGateway(GatewayStart{StartedAt: time.Unix(1, 0)})
	})
	if allocations != 0 {
		t.Fatalf("denied BeginGateway allocations = %f, want 0", allocations)
	}
	if calls := ids.calls.Load(); calls != baselineCalls {
		t.Fatalf("denied BeginGateway invoked ID materializer %d times", calls-baselineCalls)
	}
	after := snapshotGatewayAllocationAccount(session, nil)
	if after.traceCount != 0 || after.sessionTemporary != 0 || after.processTemporary != 0 {
		t.Fatalf("denied BeginGateway changed graph/account: %#v", after)
	}
}

func TestBeginGatewayDenialDoesNotUseOldGraphAsReplacementFunding(t *testing.T) {
	manager := newTestManager(t, nil)
	_ = startTestSession(t, manager, 2, 1<<20, "selected")
	retainedGateway, retainedRecord := beginTestHTTP(manager, "retained", "selected", nil)
	completeHTTP(retainedRecord, []byte("retained payload"))
	retainedGateway.Finish(GatewayOutcome{})
	session := manager.active.Load()
	candidate := gatewayCandidateBytesForTest(t, session, "x")
	baseline := snapshotGatewayAllocationAccount(session, nil)
	session.mu.Lock()
	baselineEvicted := session.evictedCount
	baselineRecordCount := session.retainedRecordCount
	recordPresent := lookupRecordForTest(session, retainedRecord.ID()) != nil
	session.mu.Unlock()
	if !recordPresent || baselineRecordCount != 1 {
		t.Fatal("completed record setup was not retained")
	}
	setGatewayAllocationRemainingCapacity(session, candidate-1, candidate-1)

	if gateway := manager.BeginGateway(GatewayStart{
		GatewayRequestID: "x",
		StartedAt:        time.Unix(2, 0),
	}); gateway.Valid() {
		t.Fatal("gateway succeeded below exact old+candidate peak")
	}
	after := snapshotGatewayAllocationAccount(session, nil)
	session.mu.Lock()
	afterEvicted := session.evictedCount
	afterRecordCount := session.retainedRecordCount
	recordPresent = lookupRecordForTest(session, retainedRecord.ID()) != nil
	session.mu.Unlock()
	if afterEvicted != baselineEvicted || afterRecordCount != baselineRecordCount || !recordPresent ||
		after.sessionCharged != baseline.sessionCharged || after.processCharged != baseline.processCharged {
		t.Fatalf(
			"denied add evicted old ownership: evicted %d->%d records %d->%d account %#v->%#v",
			baselineEvicted,
			afterEvicted,
			baselineRecordCount,
			afterRecordCount,
			baseline,
			after,
		)
	}
}

func TestBeginGatewayGeneratorFailureRollsBackAndLogsStableReason(t *testing.T) {
	ids := &gatewayAllocationIDGenerator{failAt: 2}
	logCore, observed := observer.New(zap.WarnLevel)
	manager := newTestManager(t, func(cfg *Config) {
		cfg.IDGenerator = ids
		cfg.Logger = zap.New(logCore)
	})
	_ = startTestSession(t, manager, 1, 1<<20, "selected")
	session := manager.active.Load()
	candidate := gatewayCandidateBytesForTest(t, session, "")
	setGatewayAllocationRemainingCapacity(session, candidate, candidate)
	baseline := snapshotGatewayAllocationAccount(session, nil)

	gateway := manager.BeginGateway(GatewayStart{StartedAt: time.Unix(1, 0)})
	if gateway.Valid() {
		t.Fatal("gateway with failed generated ID is valid")
	}
	after := snapshotGatewayAllocationAccount(session, nil)
	if after.sessionCharged != baseline.sessionCharged || after.processCharged != baseline.processCharged ||
		after.sessionTemporary != 0 || after.processTemporary != 0 || after.traceCount != 0 {
		t.Fatalf("generator failure leaked graph/account: before=%#v after=%#v", baseline, after)
	}
	entries := observed.FilterMessage("request capture gateway identifier generation failed").All()
	if len(entries) != 1 {
		t.Fatalf("stable generator failure logs = %d, want 1", len(entries))
	}
	context := entries[0].ContextMap()
	if context["operation"] != "begin_gateway" || context["reason"] != "id_generation_failed" {
		t.Fatalf("generator failure context = %#v", context)
	}
	for _, field := range entries[0].Context {
		if field.Key == "error" || strings.Contains(field.String, "credential-like") {
			t.Fatalf("generator error detail escaped into logs: %#v", entries[0].Context)
		}
	}
}

func TestNewMessageIDAllocationExactBoundaries(t *testing.T) {
	cases := []struct {
		name              string
		sessionAdjustment int64
		processAdjustment int64
		wantValid         bool
	}{
		{name: "exact", wantValid: true},
		{name: "session_exact_minus_one", sessionAdjustment: -1},
		{name: "process_exact_minus_one", processAdjustment: -1},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			manager := newTestManager(t, nil)
			_ = startTestSession(t, manager, 1, 1<<20, "selected")
			session := manager.active.Load()
			gateway := manager.BeginGateway(GatewayStart{
				GatewayRequestID: "lineage",
				StartedAt:        time.Unix(1, 0),
			})
			state := lookupGatewayForTest(gateway)
			if state == nil {
				t.Fatal("gateway setup failed")
			}
			candidate, ok := pendingLineageCandidateBytes(session.generation, state.traceSequence, 1)
			if !ok || candidate != lineageChargeBytes {
				t.Fatalf("lineage candidate = %d valid=%t, want fixed node charge %d", candidate, ok, lineageChargeBytes)
			}
			baselineGatewayCharge := state.charge
			baseline := snapshotGatewayAllocationAccount(session, state)
			setGatewayAllocationRemainingCapacity(
				session,
				candidate+testCase.sessionAdjustment,
				candidate+testCase.processAdjustment,
			)

			lineage := gateway.NewMessageID()
			if lineage.Valid() != testCase.wantValid {
				t.Fatalf("lineage valid = %t, want %t", lineage.Valid(), testCase.wantValid)
			}
			after := snapshotGatewayAllocationAccount(session, state)
			if after.sessionTemporary != 0 || after.processTemporary != 0 {
				t.Fatalf("temporary lineage accounting leaked: %#v", after)
			}
			if testCase.wantValid {
				if lineage.generation != session.generation || lineage.traceSequence != state.traceSequence ||
					lineage.lineage != 1 || state.pendingLineageFirst == nil ||
					state.pendingLineageFirst.charge != candidate || state.charge != baselineGatewayCharge+candidate ||
					after.pendingCount != 1 || after.sessionCharged != baseline.sessionCharged+candidate ||
					after.processCharged != baseline.processCharged+candidate {
					t.Fatalf("exact lineage state = %#v lineage=%#v gateway=%#v", after, lineage, state)
				}
				return
			}
			if after.pendingCount != 0 || state.nextLineage != 0 || state.charge != baselineGatewayCharge ||
				after.sessionCharged != baseline.sessionCharged || after.processCharged != baseline.processCharged {
				t.Fatalf("denied lineage changed graph/account: before=%#v after=%#v", baseline, after)
			}
		})
	}
}

func TestNewMessageIDDenialDoesNotMaterialize(t *testing.T) {
	manager := newTestManager(t, nil)
	_ = startTestSession(t, manager, 1, 1<<20, "selected")
	session := manager.active.Load()
	gateway := manager.BeginGateway(GatewayStart{GatewayRequestID: "lineage", StartedAt: time.Unix(1, 0)})
	state := lookupGatewayForTest(gateway)
	if state == nil {
		t.Fatal("gateway setup failed")
	}
	setGatewayAllocationRemainingCapacity(session, lineageChargeBytes-1, lineageChargeBytes-1)

	allocations := testing.AllocsPerRun(1000, func() {
		messageLineageSink = gateway.NewMessageID()
	})
	if allocations != 0 {
		t.Fatalf("denied NewMessageID allocations = %f, want 0", allocations)
	}
	after := snapshotGatewayAllocationAccount(session, state)
	if after.pendingCount != 0 || after.sessionTemporary != 0 || after.processTemporary != 0 ||
		state.nextLineage != 0 {
		t.Fatalf("denied NewMessageID materialized state: %#v", after)
	}
}

func TestNewMessageIDDenialDoesNotEvictCompletedOwnership(t *testing.T) {
	manager := newTestManager(t, nil)
	_ = startTestSession(t, manager, 2, 1<<20, "selected")
	retainedGateway, retainedRecord := beginTestHTTP(manager, "retained", "selected", nil)
	completeHTTP(retainedRecord, nil)
	retainedGateway.Finish(GatewayOutcome{})
	activeGateway := manager.BeginGateway(GatewayStart{GatewayRequestID: "active"})
	state := lookupGatewayForTest(activeGateway)
	if state == nil {
		t.Fatal("active gateway setup failed")
	}
	session := manager.active.Load()
	baseline := snapshotGatewayAllocationAccount(session, state)
	session.mu.Lock()
	baselineEvicted := session.evictedCount
	recordPresent := lookupRecordForTest(session, retainedRecord.ID()) != nil
	session.mu.Unlock()
	if !recordPresent {
		t.Fatal("completed record setup was not retained")
	}
	setGatewayAllocationRemainingCapacity(session, lineageChargeBytes-1, lineageChargeBytes-1)

	if lineage := activeGateway.NewMessageID(); lineage.Valid() {
		t.Fatal("lineage succeeded below the old+candidate peak")
	}
	after := snapshotGatewayAllocationAccount(session, state)
	session.mu.Lock()
	afterEvicted := session.evictedCount
	recordPresent = lookupRecordForTest(session, retainedRecord.ID()) != nil
	session.mu.Unlock()
	if afterEvicted != baselineEvicted || !recordPresent || after.pendingCount != 0 ||
		after.sessionCharged != baseline.sessionCharged || after.processCharged != baseline.processCharged {
		t.Fatalf("denied lineage evicted old ownership: before=%#v after=%#v", baseline, after)
	}
}

func TestNewMessageIDMaterializesOnlyTheOwnedNode(t *testing.T) {
	manager := newTestManager(t, nil)
	_ = startTestSession(t, manager, 1, 1<<20, "selected")
	gateway := manager.BeginGateway(GatewayStart{GatewayRequestID: "lineage-allocations"})
	state := lookupGatewayForTest(gateway)
	if state == nil {
		t.Fatal("gateway setup failed")
	}

	const runs = 100
	allocations := testing.AllocsPerRun(runs, func() {
		messageLineageSink = gateway.NewMessageID()
		if !messageLineageSink.Valid() {
			panic("lineage allocation unexpectedly failed")
		}
	})
	if allocations != 1 {
		t.Fatalf("NewMessageID allocations = %f, want exactly the owned lineage node", allocations)
	}
	// AllocsPerRun executes one warm-up call before the measured runs.
	if state.pendingLineageCount != runs+1 {
		t.Fatalf("pending lineages = %d, want %d", state.pendingLineageCount, runs+1)
	}
}

func TestMessageReadDenialConsumesOnlyThePrimaryPendingLineage(t *testing.T) {
	manager := newTestManager(t, nil)
	_ = startTestSession(t, manager, 1, 1<<20, "selected")
	session := manager.active.Load()
	gateway := manager.BeginGateway(GatewayStart{GatewayRequestID: "lineage-denial"})
	recorder := gateway.BeginWebSocket(RawWebSocketStart{
		Attempt:   AttemptMetadata{Provider: ProviderIdentity{ID: "selected"}},
		TargetURL: "wss://selected.test",
	})
	if !recorder.Valid() {
		t.Fatal("websocket recorder setup failed")
	}
	recorder.ObserveWebSocketHandshake(WebSocketHandshake{StatusCode: http.StatusSwitchingProtocols})
	primary := gateway.NewMessageID()
	source := gateway.NewMessageID()
	state := lookupGatewayForTest(gateway)
	if state == nil || !primary.Valid() || !source.Valid() {
		t.Fatal("pending lineage setup failed")
	}
	session.mu.Lock()
	primaryNode := state.findPendingLineageLocked(primary.lineage)
	sourceNode := state.findPendingLineageLocked(source.lineage)
	session.mu.Unlock()
	if primaryNode == nil || sourceNode == nil {
		t.Fatal("pending lineage nodes were not published")
	}
	baselineGatewayCharge := state.charge
	baseline := snapshotGatewayAllocationAccount(session, state)
	setGatewayAllocationRemainingCapacity(session, 0, 0)

	ref := recorder.MessageRead(MessageRead{
		Lineage:       primary,
		Direction:     MessageDirectionClientToUpstream,
		Type:          MessageTypeText,
		Source:        MessageSourceReplay,
		SourceLineage: source,
	})
	if ref.Valid() {
		t.Fatal("MessageRead unexpectedly succeeded without capacity")
	}
	afterFirst := snapshotGatewayAllocationAccount(session, state)
	session.mu.Lock()
	primaryDetached := !primaryNode.attached && primaryNode.charge == 0 && primaryNode.sequence == 0
	sourceAttached := sourceNode.attached && sourceNode.charge == lineageChargeBytes &&
		state.findPendingLineageLocked(source.lineage) == sourceNode
	session.mu.Unlock()
	if !primaryDetached || !sourceAttached || afterFirst.pendingCount != baseline.pendingCount-1 ||
		state.charge != baselineGatewayCharge-lineageChargeBytes ||
		afterFirst.sessionCharged != baseline.sessionCharged-lineageChargeBytes ||
		afterFirst.processCharged != baseline.processCharged-lineageChargeBytes {
		t.Fatalf(
			"denial did not transfer exactly one primary lineage: before=%#v after=%#v primaryDetached=%t sourceAttached=%t",
			baseline,
			afterFirst,
			primaryDetached,
			sourceAttached,
		)
	}

	// Retrying a consumed value must not release the reference-only source token.
	if retry := recorder.MessageRead(MessageRead{
		Lineage:       primary,
		Direction:     MessageDirectionClientToUpstream,
		Type:          MessageTypeText,
		Source:        MessageSourceReplay,
		SourceLineage: source,
	}); retry.Valid() {
		t.Fatal("retried MessageRead unexpectedly succeeded without capacity")
	}
	afterRetry := snapshotGatewayAllocationAccount(session, state)
	if afterRetry != afterFirst {
		t.Fatalf("retry consumed lineage ownership twice: first=%#v retry=%#v", afterFirst, afterRetry)
	}
}

func TestMessageReadSequenceOverflowConsumesPendingLineage(t *testing.T) {
	manager := newTestManager(t, nil)
	_ = startTestSession(t, manager, 1, 1<<20, "selected")
	session := manager.active.Load()
	gateway := manager.BeginGateway(GatewayStart{GatewayRequestID: "lineage-sequence-overflow"})
	recorder := gateway.BeginWebSocket(RawWebSocketStart{
		Attempt:   AttemptMetadata{Provider: ProviderIdentity{ID: "selected"}},
		TargetURL: "wss://selected.test",
	})
	if !recorder.Valid() {
		t.Fatal("websocket recorder setup failed")
	}
	recorder.ObserveWebSocketHandshake(WebSocketHandshake{StatusCode: http.StatusSwitchingProtocols})
	lineage := gateway.NewMessageID()
	state := lookupGatewayForTest(gateway)
	if state == nil || !lineage.Valid() {
		t.Fatal("pending lineage setup failed")
	}
	session.mu.Lock()
	node := state.findPendingLineageLocked(lineage.lineage)
	state.nextMessageSequence = math.MaxUint64
	session.mu.Unlock()
	if node == nil {
		t.Fatal("pending lineage node was not published")
	}
	baselineGatewayCharge := state.charge
	baseline := snapshotGatewayAllocationAccount(session, state)

	if ref := recorder.MessageRead(MessageRead{
		Lineage:   lineage,
		Direction: MessageDirectionClientToUpstream,
		Type:      MessageTypeText,
		Source:    MessageSourceLive,
	}); ref.Valid() {
		t.Fatal("sequence-overflowed MessageRead returned a reference")
	}
	after := snapshotGatewayAllocationAccount(session, state)
	session.mu.Lock()
	nodeDetached := !node.attached && node.charge == 0 && node.sequence == 0
	session.mu.Unlock()
	if !nodeDetached || after.pendingCount != baseline.pendingCount-1 ||
		state.charge != baselineGatewayCharge-lineageChargeBytes ||
		after.sessionCharged != baseline.sessionCharged-lineageChargeBytes ||
		after.processCharged != baseline.processCharged-lineageChargeBytes {
		t.Fatalf(
			"sequence overflow did not consume lineage exactly once: before=%#v after=%#v detached=%t",
			baseline,
			after,
			nodeDetached,
		)
	}
}

func TestConcurrentMessageReadDenialConsumesPendingLineageExactlyOnce(t *testing.T) {
	manager := newTestManager(t, nil)
	_ = startTestSession(t, manager, 1, 1<<20, "selected")
	session := manager.active.Load()
	gateway := manager.BeginGateway(GatewayStart{GatewayRequestID: "lineage-denial-race"})
	recorder := gateway.BeginWebSocket(RawWebSocketStart{
		Attempt:   AttemptMetadata{Provider: ProviderIdentity{ID: "selected"}},
		TargetURL: "wss://selected.test",
	})
	if !recorder.Valid() {
		t.Fatal("websocket recorder setup failed")
	}
	recorder.ObserveWebSocketHandshake(WebSocketHandshake{StatusCode: http.StatusSwitchingProtocols})
	lineage := gateway.NewMessageID()
	state := lookupGatewayForTest(gateway)
	if state == nil || !lineage.Valid() {
		t.Fatal("pending lineage setup failed")
	}
	session.mu.Lock()
	node := state.findPendingLineageLocked(lineage.lineage)
	session.mu.Unlock()
	if node == nil {
		t.Fatal("pending lineage node was not published")
	}
	baselineGatewayCharge := state.charge
	baseline := snapshotGatewayAllocationAccount(session, state)
	setGatewayAllocationRemainingCapacity(session, 0, 0)

	const contenders = 64
	start := make(chan struct{})
	var wait sync.WaitGroup
	var admitted atomic.Int64
	for range contenders {
		wait.Go(func() {
			<-start
			if recorder.MessageRead(MessageRead{
				Lineage:   lineage,
				Direction: MessageDirectionClientToUpstream,
				Type:      MessageTypeText,
				Source:    MessageSourceLive,
			}).Valid() {
				admitted.Add(1)
			}
		})
	}
	close(start)
	wait.Wait()
	if admitted.Load() != 0 {
		t.Fatalf("MessageRead admissions without capacity = %d", admitted.Load())
	}
	after := snapshotGatewayAllocationAccount(session, state)
	session.mu.Lock()
	nodeDetached := !node.attached && node.charge == 0 && node.sequence == 0
	session.mu.Unlock()
	if !nodeDetached || after.pendingCount != baseline.pendingCount-1 ||
		state.charge != baselineGatewayCharge-lineageChargeBytes ||
		after.sessionCharged != baseline.sessionCharged-lineageChargeBytes ||
		after.processCharged != baseline.processCharged-lineageChargeBytes {
		t.Fatalf(
			"concurrent denial did not consume lineage exactly once: before=%#v after=%#v detached=%t",
			baseline,
			after,
			nodeDetached,
		)
	}
}

func TestGatewayAndLineageSequencesRejectOverflow(t *testing.T) {
	manager := newTestManager(t, nil)
	_ = startTestSession(t, manager, 1, 1<<20, "selected")
	session := manager.active.Load()
	baseline := snapshotGatewayAllocationAccount(session, nil)
	session.mu.Lock()
	session.nextTraceSequence = math.MaxUint64
	session.mu.Unlock()
	if gateway := manager.BeginGateway(GatewayStart{GatewayRequestID: "overflow"}); gateway.Valid() {
		t.Fatal("overflowed trace sequence produced a gateway")
	}
	afterTrace := snapshotGatewayAllocationAccount(session, nil)
	if afterTrace.sessionCharged != baseline.sessionCharged || afterTrace.processCharged != baseline.processCharged {
		t.Fatalf("trace overflow changed accounting: before=%#v after=%#v", baseline, afterTrace)
	}

	session.mu.Lock()
	session.nextTraceSequence = 0
	session.mu.Unlock()
	gateway := manager.BeginGateway(GatewayStart{GatewayRequestID: "lineage-overflow"})
	state := lookupGatewayForTest(gateway)
	if state == nil {
		t.Fatal("gateway setup failed")
	}
	session.mu.Lock()
	state.nextLineage = math.MaxUint64
	session.mu.Unlock()
	lineageBaseline := snapshotGatewayAllocationAccount(session, state)
	if lineage := gateway.NewMessageID(); lineage.Valid() {
		t.Fatal("overflowed lineage sequence produced a handle")
	}
	afterLineage := snapshotGatewayAllocationAccount(session, state)
	if afterLineage.sessionCharged != lineageBaseline.sessionCharged ||
		afterLineage.processCharged != lineageBaseline.processCharged || afterLineage.pendingCount != 0 {
		t.Fatalf("lineage overflow changed accounting: before=%#v after=%#v", lineageBaseline, afterLineage)
	}
}

func TestConcurrentGatewayAdmissionNeverExceedsCeiling(t *testing.T) {
	const (
		contenders = 64
		capacity   = 8
	)
	manager := newTestManager(t, func(cfg *Config) {
		cfg.MaxActiveTraces = contenders
	})
	sessionInfo := startTestSession(t, manager, 1, 1<<20, "selected")
	session := manager.active.Load()
	candidate := gatewayCandidateBytesForTest(t, session, "same")
	baseline := snapshotGatewayAllocationAccount(session, nil)
	remaining := int64(capacity) * candidate
	setGatewayAllocationRemainingCapacity(session, remaining, remaining)
	ceiling := baseline.processCharged + remaining

	var successes atomic.Int64
	var maximumObserved atomic.Int64
	stopSampling := make(chan struct{})
	samplingDone := make(chan struct{})
	go func() {
		defer close(samplingDone)
		for {
			select {
			case <-stopSampling:
				return
			default:
				manager.mu.Lock()
				charged := manager.processCharged
				manager.mu.Unlock()
				for previous := maximumObserved.Load(); charged > previous; previous = maximumObserved.Load() {
					if maximumObserved.CompareAndSwap(previous, charged) {
						break
					}
				}
				runtime.Gosched()
			}
		}
	}()

	var wait sync.WaitGroup
	start := make(chan struct{})
	for range contenders {
		wait.Go(func() {
			<-start
			if manager.BeginGateway(GatewayStart{
				GatewayRequestID: "same",
				StartedAt:        time.Unix(1, 0),
			}).Valid() {
				successes.Add(1)
			}
		})
	}
	close(start)
	wait.Wait()
	close(stopSampling)
	<-samplingDone

	if successes.Load() != capacity {
		t.Fatalf("successful gateways = %d, want %d", successes.Load(), capacity)
	}
	after := snapshotGatewayAllocationAccount(session, nil)
	if after.traceCount != capacity || after.sessionTemporary != 0 || after.processTemporary != 0 ||
		after.sessionCharged != baseline.sessionCharged+remaining ||
		after.processCharged != baseline.processCharged+remaining {
		t.Fatalf("concurrent accounting = %#v", after)
	}
	if observed := maximumObserved.Load(); observed > ceiling {
		t.Fatalf("observed process charge %d exceeded ceiling %d", observed, ceiling)
	}
	if err := manager.Stop(sessionInfo.SessionID); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	manager.mu.Lock()
	charged, temporary, releasing := manager.processCharged, manager.processTemporary, manager.processReleasing
	manager.mu.Unlock()
	if charged != 0 || temporary != 0 || releasing != 0 {
		t.Fatalf("final process ownership = charged:%d temporary:%d releasing:%d", charged, temporary, releasing)
	}
}

func TestGatewayRecorderAndLineageAreValueHandles(t *testing.T) {
	lineageType := reflect.TypeFor[MessageLineage]()
	if lineageType.NumField() != 3 {
		t.Fatalf("MessageLineage fields = %d, want 3 numeric components", lineageType.NumField())
	}
	for index := 0; index < lineageType.NumField(); index++ {
		if lineageType.Field(index).Type.Kind() != reflect.Uint64 {
			t.Fatalf("MessageLineage field %q type = %s", lineageType.Field(index).Name, lineageType.Field(index).Type)
		}
	}
	for _, handle := range []struct {
		name      string
		valueType reflect.Type
	}{
		{name: "GatewayRecorder", valueType: reflect.TypeFor[GatewayRecorder]()},
		{name: "Recorder", valueType: reflect.TypeFor[Recorder]()},
		{name: "MessageRef", valueType: reflect.TypeFor[MessageRef]()},
	} {
		for index := 0; index < handle.valueType.NumField(); index++ {
			fieldType := handle.valueType.Field(index).Type
			if fieldType == reflect.TypeFor[*gatewayState]() ||
				fieldType == reflect.TypeFor[*recordState]() ||
				fieldType == reflect.TypeFor[*transitionRecorderState]() ||
				fieldType == reflect.TypeFor[*sessionState]() ||
				fieldType.Kind() == reflect.String {
				t.Fatalf("%s retains capture graph/string through field %q", handle.name, handle.valueType.Field(index).Name)
			}
		}
	}
}
