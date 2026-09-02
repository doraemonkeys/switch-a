package requestcapture

import (
	"errors"
	"math"
	"strings"
	"testing"

	"go.uber.org/zap"
)

func additionalAccountingSession() (*Manager, *sessionState) {
	manager := &Manager{cfg: normalizedConfig{
		processCeilingBytes: 4096,
		maxActiveRecords:    4,
		logger:              zap.NewNop(),
	}}
	session := &sessionState{
		manager:        manager,
		id:             "account-session",
		generation:     1,
		mutationEpoch:  1,
		accepting:      true,
		ownerAccepting: true,
		ownerCount:     1,
		quotaBytes:     4096,
	}
	manager.active.Store(session)
	return manager, session
}

func TestAdditionalCaptureAllocationRollbackAndCommitFailures(t *testing.T) {
	var nilSession *sessionState
	if plan := nilSession.newCapturePlanLocked(captureOperationBeginGateway, allocationProofBeginGateway, 1, 0); plan.operation != captureOperationUnknown {
		t.Fatal("nil session produced an allocation plan")
	}
	_, session := additionalAccountingSession()
	for _, plan := range []allocationPlan{
		session.newCapturePlanLocked(captureOperationBeginGateway, allocationProofBeginRecord, 1, 0),
		session.newCapturePlanLocked(captureOperationBeginGateway, allocationProofBeginGateway, -1, 0),
		session.newCapturePlanLocked(captureOperationBeginGateway, allocationProofBeginGateway, math.MaxInt64, 1),
	} {
		if plan.operation != captureOperationUnknown {
			t.Fatalf("invalid plan was valid: %#v", plan)
		}
	}

	plan := session.planBeginGatewayAllocationLocked(100, 50)
	if plan.operation != captureOperationBeginGateway {
		t.Fatal("valid capture plan was rejected")
	}
	if session.beginCaptureAllocationLocked(plan, nil) {
		t.Fatal("nil allocation token was admitted")
	}
	stale := plan
	stale.generation++
	if session.beginCaptureAllocationLocked(stale, &CaptureAllocation{}) {
		t.Fatal("stale-generation allocation was admitted")
	}
	stale = plan
	stale.mutationEpoch++
	if session.beginCaptureAllocationLocked(stale, &CaptureAllocation{}) {
		t.Fatal("stale-epoch allocation was admitted")
	}

	session.accepting = false
	if session.beginCaptureAllocationLocked(plan, &CaptureAllocation{}) {
		t.Fatal("nonaccepting session admitted an allocation")
	}
	session.accepting = true
	session.releasing = true
	if session.beginCaptureAllocationLocked(plan, &CaptureAllocation{}) {
		t.Fatal("releasing session admitted an allocation")
	}
	session.releasing = false
	session.quotaBytes = 149
	if session.beginCaptureAllocationLocked(plan, &CaptureAllocation{}) {
		t.Fatal("over-quota allocation was admitted")
	}
	session.quotaBytes = 4096

	var allocation CaptureAllocation
	if !session.beginCaptureAllocationLocked(plan, &allocation) {
		t.Fatal("valid allocation was rejected")
	}
	if allocation.claimCandidate(-1) || allocation.claimCandidate(101) || !allocation.claimCandidate(80) ||
		allocation.claimCandidate(21) {
		t.Fatal("candidate claim bounds were not enforced")
	}
	if allocation.claimScratch(-1) || allocation.claimScratch(51) || !allocation.claimScratch(30) ||
		allocation.claimScratch(21) {
		t.Fatal("scratch claim bounds were not enforced")
	}
	if allocation.releaseScratch(-1) || allocation.releaseScratch(31) || !allocation.releaseScratch(30) {
		t.Fatal("scratch release bounds were not enforced")
	}
	if !allocation.rollbackLocked() {
		t.Fatal("allocation rollback was not successful")
	}
	if !allocation.rollbackLocked() {
		t.Fatal("allocation rollback was not idempotent")
	}
	if session.chargedBytes != 0 || session.temporaryBytes != 0 || session.manager.processCharged != 0 {
		t.Fatal("rollback leaked account ownership")
	}

	commitPlan := session.planBeginGatewayAllocationLocked(100, 0)
	allocation = CaptureAllocation{}
	if !session.beginCaptureAllocationLocked(commitPlan, &allocation) || !allocation.claimCandidate(60) {
		t.Fatal("commit allocation setup failed")
	}
	if allocation.beginCommitAccountingLocked(nil, nil) {
		t.Fatal("nil commit token was admitted")
	}
	if allocation.beginCommitAccountingLocked(&ownedCharge{bytes: 1}, &captureCommit{}) {
		t.Fatal("dead retired owner was admitted")
	}
	var commit captureCommit
	if !allocation.beginCommitAccountingLocked(nil, &commit) {
		t.Fatal("valid commit accounting was rejected")
	}
	commit.finishLocked(&allocation)
	if allocation.state != allocationStateCommitted || session.chargedBytes != 60 || session.temporaryBytes != 0 {
		t.Fatalf("commit accounting = state %d charged %d temporary %d",
			allocation.state, session.chargedBytes, session.temporaryBytes)
	}
	session.releaseLocked(60)

	abortPlan := session.planBeginGatewayAllocationLocked(40, 0)
	allocation = CaptureAllocation{}
	if !session.beginCaptureAllocationLocked(abortPlan, &allocation) || !allocation.claimCandidate(20) {
		t.Fatal("abort allocation setup failed")
	}
	commit = captureCommit{}
	if !allocation.beginCommitAccountingLocked(nil, &commit) {
		t.Fatal("abort commit setup failed")
	}
	commit.finishLocked(nil)
	if !allocation.rollbackLocked() {
		t.Fatal("allocation could not roll back after commit abort")
	}
	(*captureCommit)(nil).finishLocked(nil)
	(*captureCommit)(nil).abortLocked()
	(&captureCommit{}).abortLocked()
}

func TestAdditionalCaptureAccountingCorruptionAndInvariantRejection(t *testing.T) {
	manager, session := additionalAccountingSession()
	plan := session.planTransitionAllocationLocked(20, 0)
	var allocation CaptureAllocation
	if !session.beginCaptureAllocationLocked(plan, &allocation) {
		t.Fatal("allocation setup failed")
	}
	session.temporaryBytes = 0
	if allocation.rollbackLocked() {
		t.Fatal("underflowing allocation rollback succeeded")
	}
	session.temporaryBytes = 20
	if !allocation.rollbackLocked() {
		t.Fatal("restored allocation rollback failed")
	}

	plan = session.planTransitionAllocationLocked(20, 0)
	allocation = CaptureAllocation{}
	if !session.beginCaptureAllocationLocked(plan, &allocation) {
		t.Fatal("releasing allocation setup failed")
	}
	session.releasing = true
	manager.processReleasing = 20
	if !allocation.rollbackLocked() || manager.processReleasing != 0 {
		t.Fatal("releasing rollback did not refund releasing ownership")
	}
	session.releasing = false

	if addRetainedCharge(1, -1) != math.MaxInt64 || addRetainedCharge64(1, -1) != math.MaxInt64 ||
		addRetainedCharge64(math.MaxInt64, 1) != math.MaxInt64 {
		t.Fatal("retained charge saturation failed")
	}
	if !session.reserveLocked(0, false) {
		t.Fatal("zero reservation failed")
	}
	session.quotaBytes = 0
	if session.reserveLocked(1, false) {
		t.Fatal("over-quota reservation succeeded without eviction")
	}
	session.releaseLocked(0)
	session.releaseLocked(1)
	session.quotaBytes = 4096

	session.markReleasingLocked()
	session.markReleasingLocked()
	session.pinLocked(0)
	session.unpinLocked(0)
	manager.pinAccountLocked(5)
	if manager.unpinAccountLocked(6) || !manager.unpinAccountLocked(5) || !manager.unpinAccountLocked(0) {
		t.Fatal("pin account bounds were not enforced")
	}
	session.unpinLocked(1)

	session.releasing = false
	session.chargedBytes = -1
	if err := session.debugInvariantLocked(); err == nil {
		t.Fatal("negative session charge passed invariants")
	}
	session.chargedBytes = 1
	session.temporaryBytes = 2
	if err := session.debugInvariantLocked(); err == nil {
		t.Fatal("temporary overcharge passed invariants")
	}
	session.temporaryBytes = 0
	session.activeRecords = manager.cfg.maxActiveRecords + 1
	if err := session.debugInvariantLocked(); err == nil {
		t.Fatal("active-record overflow passed invariants")
	}
}

func TestAdditionalStartAllocationAndConfigValidationFailures(t *testing.T) {
	manager := &Manager{cfg: normalizedConfig{processCeilingBytes: 100}}
	if manager.beginStartAllocation(1, 1, nil) ||
		manager.beginStartAllocation(0, 1, &startAllocation{}) ||
		manager.beginStartAllocation(1, 0, &startAllocation{}) ||
		manager.beginStartAllocation(1, 2, &startAllocation{}) {
		t.Fatal("invalid start allocation was admitted")
	}
	manager.processCharged = 100
	if manager.beginStartAllocation(10, 10, &startAllocation{}) {
		t.Fatal("process-full start allocation was admitted")
	}
	manager.processCharged = 0
	var allocation startAllocation
	if !manager.beginStartAllocation(100, 20, &allocation) {
		t.Fatal("valid start allocation failed")
	}
	if !allocation.rollback() {
		t.Fatal("start allocation rollback was not successful")
	}
	if !allocation.rollback() {
		t.Fatal("start allocation rollback was not idempotent")
	}
	if (*startAllocation)(nil).rollback() {
		t.Fatal("nil start allocation rolled back")
	}
	discardUnpublishedSessionCandidate(nil)

	baseManager := newTestManager(t, nil)
	base := baseManager.cfg
	tests := []struct {
		name   string
		mutate func(*normalizedConfig)
	}{
		{name: "nonpositive", mutate: func(cfg *normalizedConfig) { cfg.previewBytes = 0 }},
		{name: "handle capacity", mutate: func(cfg *normalizedConfig) { cfg.processCeilingBytes = 1 }},
		{name: "session quota", mutate: func(cfg *normalizedConfig) { cfg.defaultSessionQuotaBytes = cfg.processCeilingBytes + 1 }},
		{name: "record defaults", mutate: func(cfg *normalizedConfig) { cfg.defaultRecordsPerProvider = cfg.maxRecordsPerProvider + 1 }},
		{name: "export line minimum", mutate: func(cfg *normalizedConfig) { cfg.exportLineBytes = minimumExportLineBytes() - 1 }},
		{name: "chunk minimum", mutate: func(cfg *normalizedConfig) { cfg.chunkBytes = MinimumChunkBytes - 1 }},
		{name: "chunk maximum", mutate: func(cfg *normalizedConfig) { cfg.chunkBytes = MaximumChunkBytes + 1 }},
		{name: "preview ceiling", mutate: func(cfg *normalizedConfig) { cfg.previewBytes = int(cfg.processCeilingBytes + 1) }},
		{name: "export ceiling", mutate: func(cfg *normalizedConfig) { cfg.exportLineBytes = int(cfg.processCeilingBytes + 1) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := base
			test.mutate(&cfg)
			if err := cfg.validate(); err == nil {
				t.Fatal("invalid normalized config passed validation")
			}
		})
	}
	if _, err := NewManager(Config{MaxPendingExports: math.MaxInt, MaxActiveDownloads: 1}); !errors.Is(err, ErrCapacityExceeded) {
		t.Fatalf("overflowing export capacity error = %v", err)
	}
	if defaultInt(7, 9) != 7 || defaultInt64(7, 9) != 7 {
		t.Fatal("nonzero defaults were replaced")
	}
}

func TestAdditionalHandleSlotsRejectCorruptionAndABA(t *testing.T) {
	if _, ok := scanHandleSlotShape(0, 1, 1, 1); ok {
		t.Fatal("zero provider shape was accepted")
	}
	if _, ok := scanHandleSlotShape(math.MaxInt, 2, 1, 1); ok {
		t.Fatal("multiplication-overflow shape was accepted")
	}
	if _, ok := scanHandleSlotShape(1, int(math.MaxUint32)+1, 1, 1); ok {
		t.Fatal("oversized retained-record shape was accepted")
	}
	shape, ok := scanHandleSlotShape(1, 1, 1, 1)
	if !ok {
		t.Fatal("minimal handle shape was rejected")
	}
	if initializeHandleSlots(nil, shape) || initializeHandleSlots(&sessionState{}, handleSlotShape{}) {
		t.Fatal("invalid handle storage was initialized")
	}
	session := &sessionState{}
	if !initializeHandleSlots(session, shape) {
		t.Fatal("valid handle storage was rejected")
	}

	gateway := &gatewayState{traceSequence: 1}
	if _, claimed := session.claimGatewayHandleSlotLocked(nil); claimed {
		t.Fatal("nil gateway claimed a slot")
	}
	gatewaySlot, claimed := session.claimGatewayHandleSlotLocked(gateway)
	if !claimed || session.gatewayHandleLocked(gatewaySlot, 1) != gateway {
		t.Fatal("gateway slot claim failed")
	}
	if _, claimed = session.claimGatewayHandleSlotLocked(gateway); claimed ||
		session.gatewayHandleLocked(0, 1) != nil || session.gatewayHandleLocked(gatewaySlot, 2) != nil {
		t.Fatal("gateway slot accepted reuse or stale sequence")
	}
	session.gatewayHandleSlots[gatewaySlot-1].sequence = 2
	if session.releaseGatewayHandleSlotLocked(gateway) {
		t.Fatal("gateway slot released through a stale sequence")
	}
	session.gatewayHandleSlots[gatewaySlot-1].sequence = 1
	if !session.releaseGatewayHandleSlotLocked(gateway) || session.releaseGatewayHandleSlotLocked(gateway) {
		t.Fatal("gateway slot release was not exactly once")
	}

	record := &recordState{summary: RecordSummary{RecordSequence: 1}}
	if _, claimed := session.claimRecordHandleSlotLocked(nil); claimed {
		t.Fatal("nil record claimed a slot")
	}
	recordSlot, claimed := session.claimRecordHandleSlotLocked(record)
	if !claimed || session.recordHandleLocked(recordSlot, 1) != record {
		t.Fatal("record slot claim failed")
	}
	if _, claimed = session.claimRecordHandleSlotLocked(record); claimed ||
		session.recordHandleLocked(0, 1) != nil || session.recordHandleLocked(recordSlot, 2) != nil {
		t.Fatal("record slot accepted reuse or stale sequence")
	}
	session.recordHandleSlots[recordSlot-1].sequence = 2
	if session.releaseRecordHandleSlotLocked(record) {
		t.Fatal("record slot released through a stale sequence")
	}
	session.recordHandleSlots[recordSlot-1].sequence = 1
	if !session.releaseRecordHandleSlotLocked(record) || session.releaseRecordHandleSlotLocked(record) {
		t.Fatal("record slot release was not exactly once")
	}
	session.severHandleSlotsLocked()
}

func TestAdditionalEvictionIntervalsAndProviderIndexes(t *testing.T) {
	manager, session := additionalAccountingSession()
	for _, sequence := range []uint64{1, 3, 2, 5, 4} {
		session.noteEvictionLocked(&recordState{summary: RecordSummary{RecordSequence: sequence}})
	}
	if session.evictionRangeCount != 1 || session.evictionRangeFirst.first != 1 || session.evictionRangeLast.last != 5 ||
		session.evictionCountBetweenLocked(2, 4) != 3 || session.evictionCountBetweenLocked(5, 4) != 0 {
		t.Fatalf("merged eviction intervals = count %d first %#v last %#v",
			session.evictionRangeCount, session.evictionRangeFirst, session.evictionRangeLast)
	}

	clamped := &sessionState{evictionRangeFirst: &evictionRange{first: 1, last: 10}}
	clamped.evictionRangeLast = clamped.evictionRangeFirst
	if got := clamped.evictionCountBetweenLocked(3, 7); got != 5 {
		t.Fatalf("clamped eviction count = %d", got)
	}
	overflowFirst := &evictionRange{first: 0, last: math.MaxUint64 - 1}
	overflowLast := &evictionRange{first: math.MaxUint64, last: math.MaxUint64, before: overflowFirst}
	overflowFirst.after = overflowLast
	overflow := &sessionState{evictionRangeFirst: overflowFirst, evictionRangeLast: overflowLast}
	if got := overflow.evictionCountBetweenLocked(0, math.MaxUint64); got != math.MaxUint64 {
		t.Fatalf("saturated eviction count = %d", got)
	}

	charged := &sessionState{manager: manager, quotaBytes: evictionRangeChargeBytes, chargedBytes: evictionRangeChargeBytes,
		evictionIndexCharge: evictionRangeChargeBytes}
	manager.processCharged = evictionRangeChargeBytes
	charged.releaseEvictionIndexLocked()
	if charged.evictionIndexCharge != 0 || charged.chargedBytes != 0 || manager.processCharged != 0 {
		t.Fatal("eviction index charge was not released")
	}

	index := &providerRecordIndex{}
	providerSession := &sessionState{providerRecords: map[string]*providerRecordIndex{"provider": index}}
	first := &recordState{}
	middle := &recordState{}
	last := &recordState{}
	first.summary.Provider.ID = "provider"
	middle.summary.Provider.ID = "provider"
	last.summary.Provider.ID = "provider"
	if providerSession.appendProviderRecordLocked("missing", first) || !providerSession.appendProviderRecordLocked("provider", first) ||
		!providerSession.appendProviderRecordLocked("provider", middle) || !providerSession.appendProviderRecordLocked("provider", last) {
		t.Fatal("provider index append failed")
	}
	if !providerSession.removeProviderRecordLocked(middle) || !providerSession.removeProviderRecordLocked(first) ||
		!providerSession.removeProviderRecordLocked(last) || providerSession.removeProviderRecordLocked(last) ||
		providerSession.removeProviderRecordLocked(nil) {
		t.Fatal("provider index removal did not enforce membership")
	}

	list := &sessionState{}
	list.removeRecordLocked(nil)
	one, two, three := &recordState{}, &recordState{}, &recordState{}
	list.appendRecordLocked(one)
	list.appendRecordLocked(two)
	list.appendRecordLocked(three)
	list.removeRecordLocked(two)
	list.removeRecordLocked(one)
	list.removeRecordLocked(three)
	if list.oldestRecord != nil || list.newestRecord != nil || list.retainedRecordCount != 0 {
		t.Fatal("record list retained detached nodes")
	}
}

func TestAdditionalRecordIdentityAndRequestShapeBoundaries(t *testing.T) {
	if IsCanonicalRecordID("not-a-record") {
		t.Fatal("noncanonical record ID passed validation")
	}
	session := &sessionState{id: "session", generation: 2, nextRecordSequence: 1}
	future := makeRecordIDValue(session.id, session.generation, 2).String()
	if session.ownsRecordID(future) {
		t.Fatal("future record ID was owned")
	}
	if shape := scanGatewayRequestID(strings.Repeat("x", maxRetainedIdentifierBytes+1)); !shape.truncated || shape.valid() == false {
		t.Fatalf("oversized gateway ID shape = %#v", shape)
	}
}
