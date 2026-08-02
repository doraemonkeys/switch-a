package requestcapture

import (
	"net/http"
	"runtime"
	"testing"
	"time"
)

func TestGatewayAllocationStopStartABARollsBackAndSeversGraph(t *testing.T) {
	ids := &gatewayAllocationIDGenerator{
		blockAt: 2,
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	manager := newTestManager(t, func(cfg *Config) {
		cfg.IDGenerator = ids
	})
	first := startTestSession(t, manager, 2, 1<<20, "selected")
	oldSession := manager.active.Load()
	oldGateway := manager.BeginGateway(GatewayStart{
		GatewayRequestID: "old",
		StartedAt:        time.Unix(1, 0),
	})
	oldState := lookupGatewayForTest(oldGateway)
	if oldState == nil {
		t.Fatal("old gateway setup failed")
	}
	oldLineage := oldGateway.NewMessageID()
	if !oldLineage.Valid() {
		t.Fatal("old lineage setup failed")
	}
	oldLineageID := materializeMessageID(
		oldLineage.generation,
		oldLineage.traceSequence,
		oldLineage.lineage,
	)
	blockedCandidate := gatewayCandidateBytesForTest(t, oldSession, "")
	blockedResult := make(chan GatewayRecorder, 1)
	go func() {
		blockedResult <- manager.BeginGateway(GatewayStart{StartedAt: time.Unix(2, 0)})
	}()
	select {
	case <-ids.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("generated gateway did not reach the post-reservation barrier")
	}
	manager.mu.Lock()
	blockedSessionTemporary := oldSession.temporaryBytes
	blockedProcessTemporary := manager.processTemporary
	manager.mu.Unlock()
	if blockedSessionTemporary != blockedCandidate || blockedProcessTemporary != blockedCandidate {
		t.Fatalf(
			"blocked temporary ownership = session:%d process:%d, want %d",
			blockedSessionTemporary,
			blockedProcessTemporary,
			blockedCandidate,
		)
	}

	stopResult := make(chan error, 1)
	go func() { stopResult <- manager.Stop(first.SessionID) }()
	deadline := time.Now().Add(5 * time.Second)
	for manager.active.Load() != nil {
		if time.Now().After(deadline) {
			t.Fatal("Stop did not detach the old generation")
		}
		runtime.Gosched()
	}
	second := startTestSession(t, manager, 2, 1<<20, "selected")
	manager.mu.Lock()
	if manager.processTemporary != blockedCandidate {
		manager.mu.Unlock()
		t.Fatalf("old temporary ticket disappeared during ABA: got %d want %d", manager.processTemporary, blockedCandidate)
	}
	manager.mu.Unlock()
	close(ids.release)

	select {
	case blockedGateway := <-blockedResult:
		if blockedGateway.Valid() {
			t.Fatal("detached generation published its materialized gateway")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("blocked BeginGateway did not roll back")
	}
	select {
	case err := <-stopResult:
		if err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not finish after gateway rollback")
	}

	if oldGateway.Valid() || oldGateway.NewMessageID().Valid() ||
		oldGateway.BeginHTTP(RawHTTPStart{}).Valid() || oldGateway.BeginWebSocket(RawWebSocketStart{}).Valid() {
		t.Fatal("stale gateway recorder resolved into the replacement generation")
	}
	if !oldLineage.Valid() {
		t.Fatal("lineage value unexpectedly depended on the destroyed session graph")
	}
	oldGateway.Transition(TransitionStart{})
	oldGateway.Finish(GatewayOutcome{})
	if oldState.boundSession.Load() != nil || oldState.session != nil || oldState.id != "" ||
		oldState.requestID != "" || oldState.sharedRequest != nil || oldState.pendingLineageFirst != nil ||
		oldState.pendingLineageLast != nil || oldState.entryFirst != nil || oldState.entryLast != nil ||
		oldState.charge != 0 || oldState.attached {
		t.Fatalf("old gateway graph was not severed: %#v", oldState)
	}
	oldAccount := snapshotGatewayAllocationAccount(oldSession, nil)
	if oldAccount.sessionCharged != 0 || oldAccount.sessionTemporary != 0 {
		t.Fatalf("old session ownership remains after cleanup: %#v", oldAccount)
	}

	newGateway := manager.BeginGateway(GatewayStart{GatewayRequestID: "new", StartedAt: time.Unix(3, 0)})
	newRecorder := newGateway.BeginWebSocket(RawWebSocketStart{
		Attempt:   AttemptMetadata{Provider: ProviderIdentity{ID: "selected"}},
		TargetURL: "wss://selected.test",
	})
	newRecorder.ObserveWebSocketHandshake(WebSocketHandshake{StatusCode: http.StatusSwitchingProtocols})
	ref := newRecorder.MessageRead(MessageRead{
		Lineage:   oldLineage,
		Direction: MessageDirectionClientToUpstream,
		Type:      MessageTypeText,
		Source:    MessageSourceLive,
	})
	if !ref.Valid() || ref.ID() == oldLineageID {
		t.Fatalf("stale lineage was reused in replacement generation: ref=%#v old=%q", ref, oldLineageID)
	}
	newRecorder.MessageResult(ref, MessageResult{
		Disposition:    MessageDispositionForwarded,
		WriteConfirmed: true,
	})
	newRecorder.Finish(Outcome{
		SourceCompletion:  SourceCompletionComplete,
		TerminationReason: TerminationReasonWebSocketClose,
	})
	newGateway.Finish(GatewayOutcome{})
	if err := manager.Stop(second.SessionID); err != nil {
		t.Fatalf("second Stop() error = %v", err)
	}
	manager.mu.Lock()
	charged, temporary, releasing := manager.processCharged, manager.processTemporary, manager.processReleasing
	manager.mu.Unlock()
	if charged != 0 || temporary != 0 || releasing != 0 {
		t.Fatalf("ABA final ownership = charged:%d temporary:%d releasing:%d", charged, temporary, releasing)
	}
}

func TestGatewayTypedCommitRejectsStaleEpochAndRollsBack(t *testing.T) {
	manager := newTestManager(t, nil)
	_ = startTestSession(t, manager, 1, 1<<20, "selected")
	session := manager.active.Load()
	shape := scanGatewayRequestID("stale")

	session.mu.Lock()
	baselineSession := session.chargedBytes
	manager.mu.Lock()
	baselineProcess := manager.processCharged
	manager.mu.Unlock()
	candidateBytes, ok := beginGatewayCandidateBytes(shape, session.generation, 1)
	if !ok {
		session.mu.Unlock()
		t.Fatal("candidate sizing failed")
	}
	plan := session.planBeginGatewayAllocationLocked(candidateBytes, 0)
	var allocation CaptureAllocation
	if !session.beginCaptureAllocationLocked(plan, &allocation) {
		session.mu.Unlock()
		t.Fatal("exact stale setup reservation failed")
	}
	candidate, failure := materializeBeginGatewayCandidate(
		session,
		&allocation,
		shape,
		1,
		time.Unix(1, 0),
	)
	if failure != gatewayMaterializationSucceeded {
		_ = candidate.rollbackLocked(&allocation)
		session.mu.Unlock()
		t.Fatalf("materialization failure = %d", failure)
	}
	session.mutationEpoch++
	if session.commitBeginGatewayAllocationLocked(&allocation, &candidate) {
		session.mu.Unlock()
		t.Fatal("stale gateway allocation committed")
	}
	if !candidate.rollbackLocked(&allocation) {
		session.mu.Unlock()
		t.Fatal("stale gateway allocation did not roll back")
	}
	manager.mu.Lock()
	processCharged, processTemporary := manager.processCharged, manager.processTemporary
	manager.mu.Unlock()
	if session.traceFirst != nil || session.traceLast != nil || session.traceCount != 0 ||
		session.chargedBytes != baselineSession || session.temporaryBytes != 0 ||
		processCharged != baselineProcess || processTemporary != 0 ||
		candidate.gateway != nil || candidate.owner.live {
		session.mu.Unlock()
		t.Fatalf("stale rollback left graph/account ownership")
	}
	session.mu.Unlock()
}

func TestBeginGatewayClaimFailuresLeaveGraphAndAccountUnchanged(t *testing.T) {
	manager := newTestManager(t, nil)
	_ = startTestSession(t, manager, 1, 1<<20, "selected")
	session := manager.active.Load()
	shape := scanGatewayRequestID("abc")
	limits := []int64{
		traceBaseChargeBytes - 1,
		traceBaseChargeBytes,
		traceBaseChargeBytes + shape.retainedBytes,
	}

	for _, candidateLimit := range limits {
		session.mu.Lock()
		baselineSession := session.chargedBytes
		manager.mu.Lock()
		baselineProcess := manager.processCharged
		manager.mu.Unlock()
		plan := session.planBeginGatewayAllocationLocked(candidateLimit, 0)
		var allocation CaptureAllocation
		if !session.beginCaptureAllocationLocked(plan, &allocation) {
			session.mu.Unlock()
			t.Fatalf("claim-failure reservation %d was rejected", candidateLimit)
		}
		candidate, failure := materializeBeginGatewayCandidate(
			session,
			&allocation,
			shape,
			1,
			time.Unix(1, 0),
		)
		if failure != gatewayMaterializationClaimRejected {
			_ = candidate.rollbackLocked(&allocation)
			session.mu.Unlock()
			t.Fatalf("candidate limit %d failure = %d", candidateLimit, failure)
		}
		if !candidate.rollbackLocked(&allocation) {
			session.mu.Unlock()
			t.Fatalf("candidate limit %d did not roll back", candidateLimit)
		}
		manager.mu.Lock()
		processCharged, processTemporary := manager.processCharged, manager.processTemporary
		manager.mu.Unlock()
		if session.traceFirst != nil || session.traceLast != nil || session.traceCount != 0 ||
			session.chargedBytes != baselineSession || session.temporaryBytes != 0 ||
			processCharged != baselineProcess || processTemporary != 0 ||
			candidate.gateway != nil || candidate.owner.live {
			session.mu.Unlock()
			t.Fatalf("candidate limit %d left graph/account ownership", candidateLimit)
		}
		session.mu.Unlock()
	}
}

func TestPendingLineageTypedCommitRejectsStaleEpochAndRollsBack(t *testing.T) {
	manager := newTestManager(t, nil)
	_ = startTestSession(t, manager, 1, 1<<20, "selected")
	session := manager.active.Load()
	gatewayRecorder := manager.BeginGateway(GatewayStart{GatewayRequestID: "stale-lineage"})
	gateway := lookupGatewayForTest(gatewayRecorder)
	if gateway == nil {
		t.Fatal("gateway setup failed")
	}

	session.mu.Lock()
	baselineSession := session.chargedBytes
	baselineGateway := gateway.charge
	manager.mu.Lock()
	baselineProcess := manager.processCharged
	manager.mu.Unlock()
	candidateBytes, ok := pendingLineageCandidateBytes(session.generation, gateway.traceSequence, 1)
	if !ok {
		session.mu.Unlock()
		t.Fatal("lineage candidate sizing failed")
	}
	plan := session.planNewMessageIDAllocationLocked(candidateBytes)
	var allocation CaptureAllocation
	if !session.beginCaptureAllocationLocked(plan, &allocation) {
		session.mu.Unlock()
		t.Fatal("exact lineage stale setup reservation failed")
	}
	candidate, materialized := materializePendingLineageCandidate(
		&allocation,
		session.generation,
		gateway.traceSequence,
		1,
	)
	if !materialized {
		_ = candidate.rollbackLocked(&allocation)
		session.mu.Unlock()
		t.Fatal("lineage materialization failed")
	}
	session.mutationEpoch++
	if gateway.commitPendingLineageAllocationLocked(&allocation, &candidate) {
		session.mu.Unlock()
		t.Fatal("stale lineage allocation committed")
	}
	if !candidate.rollbackLocked(&allocation) {
		session.mu.Unlock()
		t.Fatal("stale lineage allocation did not roll back")
	}
	manager.mu.Lock()
	processCharged, processTemporary := manager.processCharged, manager.processTemporary
	manager.mu.Unlock()
	if gateway.pendingLineageFirst != nil || gateway.pendingLineageLast != nil ||
		gateway.pendingLineageCount != 0 || gateway.nextLineage != 0 || gateway.charge != baselineGateway ||
		session.chargedBytes != baselineSession || session.temporaryBytes != 0 ||
		processCharged != baselineProcess || processTemporary != 0 ||
		candidate.node != nil || candidate.owner.live {
		session.mu.Unlock()
		t.Fatal("stale lineage rollback left graph/account ownership")
	}
	session.mu.Unlock()
}

func TestPendingLineageClaimFailureLeavesGraphAndAccountUnchanged(t *testing.T) {
	manager := newTestManager(t, nil)
	_ = startTestSession(t, manager, 1, 1<<20, "selected")
	session := manager.active.Load()
	gatewayRecorder := manager.BeginGateway(GatewayStart{GatewayRequestID: "claim-lineage"})
	gateway := lookupGatewayForTest(gatewayRecorder)
	if gateway == nil {
		t.Fatal("gateway setup failed")
	}

	session.mu.Lock()
	baselineSession := session.chargedBytes
	baselineGateway := gateway.charge
	manager.mu.Lock()
	baselineProcess := manager.processCharged
	manager.mu.Unlock()
	plan := session.planNewMessageIDAllocationLocked(lineageChargeBytes - 1)
	var allocation CaptureAllocation
	if !session.beginCaptureAllocationLocked(plan, &allocation) {
		session.mu.Unlock()
		t.Fatal("lineage claim-failure reservation was rejected")
	}
	candidate, materialized := materializePendingLineageCandidate(
		&allocation,
		session.generation,
		gateway.traceSequence,
		1,
	)
	if materialized {
		_ = candidate.rollbackLocked(&allocation)
		session.mu.Unlock()
		t.Fatal("underfunded lineage candidate materialized")
	}
	if !candidate.rollbackLocked(&allocation) {
		session.mu.Unlock()
		t.Fatal("underfunded lineage candidate did not roll back")
	}
	manager.mu.Lock()
	processCharged, processTemporary := manager.processCharged, manager.processTemporary
	manager.mu.Unlock()
	if gateway.pendingLineageFirst != nil || gateway.pendingLineageLast != nil ||
		gateway.pendingLineageCount != 0 || gateway.nextLineage != 0 || gateway.charge != baselineGateway ||
		session.chargedBytes != baselineSession || session.temporaryBytes != 0 ||
		processCharged != baselineProcess || processTemporary != 0 ||
		candidate.node != nil || candidate.owner.live {
		session.mu.Unlock()
		t.Fatal("underfunded lineage rollback left graph/account ownership")
	}
	session.mu.Unlock()
}
