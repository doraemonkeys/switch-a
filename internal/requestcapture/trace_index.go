package requestcapture

import (
	"math"
	"strings"
	"time"

	"github.com/doraemonkeys/switch-a/internal/requestcapture/captureid"
)

const (
	truncatedOpaqueID          = captureid.TruncatedOpaqueID
	maxBase36Uint64Bytes       = captureid.MaxBase36Uint64Bytes
	maxSessionIDBytes          = captureid.MaxSessionIDBytes
	maxCanonicalMessageIDBytes = captureid.MaxCanonicalMessageIDBytes
)

func newUUID() ([16]byte, error) {
	return captureid.NewUUID()
}

func makeSessionID(generation uint64, generated [16]byte) string {
	return captureid.MakeSessionID(generation, generated)
}

func IsCanonicalSessionID(value string) bool {
	return captureid.IsCanonicalSessionID(value)
}

func makeTraceEntryID(generation, traceSequence, entrySequence uint64) string {
	return captureid.MakeTraceEntryID(generation, traceSequence, entrySequence)
}

func makeMessageID(generation, traceSequence, lineage uint64) string {
	return captureid.MakeMessageID(generation, traceSequence, lineage)
}

func parseMessageID(value string) (generation, traceSequence, lineage uint64, ok bool) {
	return captureid.ParseMessageID(value)
}

func boundedOpaqueID(_ string, value string) (string, bool) {
	return captureid.BoundedOpaqueID(value, maxRetainedIdentifierBytes)
}

const (
	generatedGatewayRequestIDBytes = captureid.GeneratedOpaqueIDBytes
)

type gatewayRequestIDKind uint8

const (
	gatewayRequestIDBorrowed gatewayRequestIDKind = iota
	gatewayRequestIDGenerated
	gatewayRequestIDTruncated
)

// gatewayRequestIDShape retains only a bounded borrowed view. Admission can
// therefore size the exact candidate before an ID generator or clone runs.
type gatewayRequestIDShape struct {
	value         string
	kind          gatewayRequestIDKind
	retainedBytes int64
	truncated     bool
}

type beginGatewayCandidate struct {
	gateway *gatewayState
	owner   ownedCharge
}

type pendingLineageCandidate struct {
	node  *pendingLineageState
	owner ownedCharge
}

type gatewayMaterializationFailure uint8

const (
	gatewayMaterializationSucceeded gatewayMaterializationFailure = iota
	gatewayMaterializationClaimRejected
	gatewayMaterializationIDGenerationFailed
)

func scanGatewayRequestID(value string) gatewayRequestIDShape {
	// Length is checked before whitespace scanning so hostile oversized IDs have
	// constant work and never reach a cloning or hashing helper.
	if len(value) > maxRetainedIdentifierBytes {
		return gatewayRequestIDShape{
			value:     truncatedOpaqueID,
			kind:      gatewayRequestIDTruncated,
			truncated: true,
		}
	}
	if value == "" {
		return gatewayRequestIDShape{
			kind:          gatewayRequestIDGenerated,
			retainedBytes: int64(generatedGatewayRequestIDBytes),
		}
	}
	// Canonical input is retained verbatim. Silently trimming would create an
	// alias whose stored identity differs from the gateway's own identifier.
	if strings.TrimSpace(value) != value {
		return gatewayRequestIDShape{
			value:     truncatedOpaqueID,
			kind:      gatewayRequestIDTruncated,
			truncated: true,
		}
	}
	return gatewayRequestIDShape{
		value:         value,
		kind:          gatewayRequestIDBorrowed,
		retainedBytes: int64(len(value)),
	}
}

func beginGatewayCandidateBytes(shape gatewayRequestIDShape, generation, sequence uint64) (int64, bool) {
	if !shape.valid() || generation == 0 || sequence == 0 {
		return 0, false
	}
	traceIDBytes := traceIDEncodedBytes(generation, sequence)
	total := addRetainedCharge64(traceBaseChargeBytes, shape.retainedBytes)
	total = addRetainedCharge64(total, int64(traceIDBytes))
	return total, total != math.MaxInt64
}

func pendingLineageCandidateBytes(generation, traceSequence, lineage uint64) (int64, bool) {
	if generation == 0 || traceSequence == 0 || lineage == 0 {
		return 0, false
	}
	return lineageChargeBytes, true
}

func materializeBeginGatewayCandidate(
	session *sessionState,
	allocation *CaptureAllocation,
	shape gatewayRequestIDShape,
	sequence uint64,
	startedAt time.Time,
) (beginGatewayCandidate, gatewayMaterializationFailure) {
	if session == nil || allocation == nil || !shape.valid() || sequence == 0 ||
		allocation.plan.operation != captureOperationBeginGateway ||
		!allocation.claimCandidate(traceBaseChargeBytes) {
		return beginGatewayCandidate{}, gatewayMaterializationClaimRejected
	}
	gateway := new(gatewayState)
	candidate := beginGatewayCandidate{
		gateway: gateway,
		owner: ownedCharge{
			bytes: allocation.candidateUsed,
			live:  true,
		},
	}
	gateway.session = session
	gateway.boundSession.Store(session)
	gateway.traceSequence = sequence
	gateway.startedAt = startedAt

	requestID := shape.value
	switch shape.kind {
	case gatewayRequestIDBorrowed:
		if !allocation.claimCandidate(shape.retainedBytes) {
			return candidate, gatewayMaterializationClaimRejected
		}
		candidate.owner.bytes = allocation.candidateUsed
		requestID = strings.Clone(shape.value)
	case gatewayRequestIDGenerated:
		if !allocation.claimCandidate(shape.retainedBytes) {
			return candidate, gatewayMaterializationClaimRejected
		}
		candidate.owner.bytes = allocation.candidateUsed
		generated, err := session.manager.cfg.idGenerator.NewID()
		if err != nil {
			return candidate, gatewayMaterializationIDGenerationFailed
		}
		requestID = materializeGeneratedGatewayRequestID(generated)
	case gatewayRequestIDTruncated:
		// The sentinel is static process storage; the gateway base charge already
		// owns the string descriptor, so no dynamic bytes are charged or cloned.
	default:
		return candidate, gatewayMaterializationClaimRejected
	}
	gateway.requestID = requestID

	traceIDBytes := int64(traceIDEncodedBytes(session.generation, sequence))
	if !allocation.claimCandidate(traceIDBytes) {
		return candidate, gatewayMaterializationClaimRejected
	}
	candidate.owner.bytes = allocation.candidateUsed
	traceID := materializeTraceID(session.generation, sequence)
	gateway.id = traceID
	return candidate, gatewayMaterializationSucceeded
}

func materializePendingLineageCandidate(
	allocation *CaptureAllocation,
	generation, traceSequence, lineage uint64,
) (pendingLineageCandidate, bool) {
	if allocation == nil || generation == 0 || traceSequence == 0 || lineage == 0 ||
		allocation.plan.operation != captureOperationNewMessageID ||
		!allocation.claimCandidate(lineageChargeBytes) {
		return pendingLineageCandidate{}, false
	}
	node := new(pendingLineageState)
	candidate := pendingLineageCandidate{
		node: node,
		owner: ownedCharge{
			bytes: allocation.candidateUsed,
			live:  true,
		},
	}
	node.sequence = lineage
	return candidate, true
}

func (s *sessionState) commitBeginGatewayAllocationLocked(
	allocation *CaptureAllocation,
	candidate *beginGatewayCandidate,
) bool {
	if s == nil || allocation == nil || candidate == nil || candidate.gateway == nil ||
		allocation.session != s || allocation.plan.operation != captureOperationBeginGateway ||
		!candidate.owner.live || candidate.owner.bytes != allocation.candidateUsed ||
		candidate.owner.bytes != allocation.plan.candidateBytes ||
		candidate.gateway.session != s || candidate.gateway.boundSession.Load() != s ||
		!s.accepting || s.releasing ||
		s.manager.active.Load() != s || s.activeTraces >= s.manager.cfg.maxActiveTraces ||
		s.nextTraceSequence == math.MaxUint64 ||
		candidate.gateway.traceSequence != s.nextTraceSequence+1 {
		return false
	}

	gateway := candidate.gateway
	charge := candidate.owner.bytes
	if _, claimed := s.claimGatewayHandleSlotLocked(gateway); !claimed {
		return false
	}
	var commit captureCommit
	if !allocation.beginCommitAccountingLocked(nil, &commit) {
		_ = s.releaseGatewayHandleSlotLocked(gateway)
		return false
	}
	// This account-locked region is intentionally limited to prebuilt graph
	// publication. It contains no calls, formatting, logging, or allocation.
	gateway.charge = charge
	s.nextTraceSequence = gateway.traceSequence
	gateway.before = s.traceLast
	gateway.attached = true
	if s.traceLast == nil {
		s.traceFirst = gateway
	} else {
		s.traceLast.after = gateway
	}
	s.traceLast = gateway
	s.traceCount++
	s.activeTraces++
	commit.finishLocked(allocation)
	candidate.owner = ownedCharge{}
	candidate.gateway = nil
	return true
}

func (g *gatewayState) commitPendingLineageAllocationLocked(
	allocation *CaptureAllocation,
	candidate *pendingLineageCandidate,
) bool {
	if g == nil || g.session == nil || allocation == nil || candidate == nil || candidate.node == nil ||
		allocation.session != g.session || allocation.plan.operation != captureOperationNewMessageID ||
		!candidate.owner.live || candidate.owner.bytes != allocation.candidateUsed ||
		candidate.owner.bytes != allocation.plan.candidateBytes ||
		!g.attached || g.finished || g.session.releasing || !g.session.accepting ||
		g.session.manager.active.Load() != g.session ||
		g.pendingLineageCount >= maxPendingLineagesPerTrace || g.nextLineage == math.MaxUint64 ||
		candidate.node.sequence != g.nextLineage+1 ||
		candidate.owner.bytes > math.MaxInt64-g.charge {
		return false
	}

	node := candidate.node
	charge := candidate.owner.bytes
	var commit captureCommit
	if !allocation.beginCommitAccountingLocked(nil, &commit) {
		return false
	}
	// As above, only already-materialized pointer/scalar wiring is permitted while
	// the process account mutex is held.
	node.charge = charge
	g.nextLineage = node.sequence
	node.before = g.pendingLineageLast
	node.attached = true
	if g.pendingLineageLast == nil {
		g.pendingLineageFirst = node
	} else {
		g.pendingLineageLast.after = node
	}
	g.pendingLineageLast = node
	g.pendingLineageCount++
	g.charge += charge
	commit.finishLocked(allocation)
	candidate.owner = ownedCharge{}
	candidate.node = nil
	return true
}

func (candidate *beginGatewayCandidate) rollbackLocked(allocation *CaptureAllocation) bool {
	if candidate == nil || allocation == nil {
		return false
	}
	if gateway := candidate.gateway; gateway != nil {
		gateway.boundSession.Store(nil)
		gateway.session = nil
		gateway.id = ""
		gateway.requestID = ""
		gateway.startedAt = time.Time{}
		gateway.traceSequence = 0
	}
	if !allocation.rollbackLocked() {
		return false
	}
	candidate.gateway = nil
	candidate.owner = ownedCharge{}
	return true
}

func (candidate *pendingLineageCandidate) rollbackLocked(allocation *CaptureAllocation) bool {
	if candidate == nil || allocation == nil {
		return false
	}
	if node := candidate.node; node != nil {
		node.sequence = 0
		node.before = nil
		node.after = nil
		node.attached = false
		node.charge = 0
	}
	if !allocation.rollbackLocked() {
		return false
	}
	candidate.node = nil
	candidate.owner = ownedCharge{}
	return true
}

func materializeGeneratedGatewayRequestID(generated [16]byte) string {
	return captureid.MaterializeGeneratedOpaqueID(generated)
}

func materializeTraceID(generation, sequence uint64) string {
	return captureid.MaterializeTraceID(generation, sequence)
}

func materializeMessageID(generation, traceSequence, lineage uint64) string {
	return captureid.MaterializeMessageID(generation, traceSequence, lineage)
}

func traceIDEncodedBytes(generation, sequence uint64) int {
	return captureid.TraceIDEncodedBytes(generation, sequence)
}

func messageIDEncodedBytes(generation, traceSequence, lineage uint64) int {
	return captureid.MessageIDEncodedBytes(generation, traceSequence, lineage)
}

func (shape gatewayRequestIDShape) valid() bool {
	switch shape.kind {
	case gatewayRequestIDBorrowed:
		return !shape.truncated && shape.value != "" && len(shape.value) <= maxRetainedIdentifierBytes &&
			shape.retainedBytes == int64(len(shape.value)) && strings.TrimSpace(shape.value) == shape.value
	case gatewayRequestIDGenerated:
		return !shape.truncated && shape.value == "" &&
			shape.retainedBytes == int64(generatedGatewayRequestIDBytes)
	case gatewayRequestIDTruncated:
		return shape.truncated && shape.value == truncatedOpaqueID && shape.retainedBytes == 0
	default:
		return false
	}
}

func (s *sessionState) removeGatewayLocked(gateway *gatewayState) {
	if gateway == nil || !gateway.attached {
		return
	}
	if gateway.before == nil {
		s.traceFirst = gateway.after
	} else {
		gateway.before.after = gateway.after
	}
	if gateway.after == nil {
		s.traceLast = gateway.before
	} else {
		gateway.after.before = gateway.before
	}
	gateway.before = nil
	gateway.after = nil
	gateway.attached = false
	if s.traceCount > 0 {
		s.traceCount--
	}
}

func (g *gatewayState) appendEntryLocked(entry *traceEntryState) {
	entry.before = g.entryLast
	if g.entryLast == nil {
		g.entryFirst = entry
	} else {
		g.entryLast.after = entry
	}
	g.entryLast = entry
	g.entryCount++
	if entry.snapshot.Kind == TraceEntryTransition {
		g.transitionCount++
	}
}

func (g *gatewayState) removeEntryLocked(entry *traceEntryState) {
	if entry == nil {
		return
	}
	if entry.before == nil {
		g.entryFirst = entry.after
	} else {
		entry.before.after = entry.after
	}
	if entry.after == nil {
		g.entryLast = entry.before
	} else {
		entry.after.before = entry.before
	}
	entry.before = nil
	entry.after = nil
	if g.entryCount > 0 {
		g.entryCount--
	}
	if entry.snapshot.Kind == TraceEntryTransition && g.transitionCount > 0 {
		g.transitionCount--
	}
}

func (g *gatewayState) liveRecordsAroundLocked(target *traceEntryState) (before, after bool) {
	for entry := target.before; entry != nil; entry = entry.before {
		if entry.record != nil && !entry.record.evicted {
			before = true
			break
		}
	}
	for entry := target.after; entry != nil; entry = entry.after {
		if entry.record != nil && !entry.record.evicted {
			after = true
			break
		}
	}
	return before, after
}

func (g *gatewayState) markHistoryTruncatedLocked(before, after bool) {
	if before {
		g.historyBefore = true
	}
	if after {
		g.historyAfter = true
	}
	if !g.truncationCounted {
		g.truncationCounted = true
		g.session.truncatedTraceCount++
	}
}

func (g *gatewayState) releaseEntryLocked(entry *traceEntryState) {
	charge := g.severEntryLocked(entry)
	if charge > 0 {
		g.session.releaseLocked(charge)
	}
}

// severEntryLocked removes every graph edge without consuming the account token.
// Destructors use this split phase so no refunded object remains discoverable
// through a sibling record or transition capability at the refund boundary.
func (g *gatewayState) severEntryLocked(entry *traceEntryState) int64 {
	if entry == nil {
		return 0
	}
	g.removeEntryLocked(entry)
	charge := entry.charge
	if entry.record != nil && entry.record.traceEntry == entry {
		entry.record.traceEntry = nil
	}
	entry.record = nil
	if stub := entry.stubOwner; stub != nil {
		if g.activeTransition == stub {
			g.activeTransition = nil
		}
		stub.boundSession.Store(nil)
		stub.session = nil
		stub.gateway = nil
		stub.entry = nil
		stub.generation = 0
		stub.credentialEvidence = CredentialEvidence{}
		stub.completed = true
		entry.stubOwner = nil
	}
	entry.snapshot = TraceEntry{}
	entry.before = nil
	entry.after = nil
	entry.charge = 0
	return charge
}

func (g *gatewayState) findPendingLineageLocked(sequence uint64) *pendingLineageState {
	for node := g.pendingLineageFirst; node != nil; node = node.after {
		if node.sequence == sequence {
			return node
		}
	}
	return nil
}

func (g *gatewayState) removePendingLineageLocked(node *pendingLineageState) {
	charge := g.severPendingLineageLocked(node)
	if charge > 0 {
		g.session.releaseLocked(charge)
	}
}

// severPendingLineageLocked detaches the lookup node before its charge is
// refunded. MessageRef values carry only numeric identity, so clearing this
// registry edge is the capability revocation point.
func (g *gatewayState) severPendingLineageLocked(node *pendingLineageState) int64 {
	if node == nil || !node.attached {
		return 0
	}
	if node.before == nil {
		g.pendingLineageFirst = node.after
	} else {
		node.before.after = node.after
	}
	if node.after == nil {
		g.pendingLineageLast = node.before
	} else {
		node.after.before = node.before
	}
	node.before = nil
	node.after = nil
	node.attached = false
	if g.pendingLineageCount > 0 {
		g.pendingLineageCount--
	}
	charge := node.charge
	node.charge = 0
	node.sequence = 0
	if charge > 0 && g.charge >= charge {
		g.charge -= charge
		return charge
	}
	return 0
}

func (g *gatewayState) appendTransitionLocked(input TransitionStart) *traceEntryState {
	return g.appendTransitionTargetLocked(input, borrowedTransitionTarget(input.Target))
}

func (g *gatewayState) transitionLocked(input TransitionStart) {
	if g == nil || g.session == nil || g.boundSession.Load() != g.session ||
		!g.session.accepting || g.finished || !g.attached {
		return
	}
	session := g.session
	if _, selected := session.providers[input.Attempt.Provider.ID]; selected {
		g.selectedProvider = true
	}
	g.appendTransitionLocked(input)
}

func (s *sessionState) releaseTraceLocked(gateway *gatewayState) {
	if gateway == nil || !gateway.attached {
		return
	}
	_ = s.releaseGatewayHandleSlotLocked(gateway)
	s.removeGatewayLocked(gateway)
	releaseBlobLocked(gateway.sharedRequest)
	gateway.sharedRequest = nil
	for gateway.entryFirst != nil {
		gateway.releaseEntryLocked(gateway.entryFirst)
	}
	for gateway.pendingLineageFirst != nil {
		gateway.removePendingLineageLocked(gateway.pendingLineageFirst)
	}
	charge := gateway.charge
	// External gateway handles contain only numeric identity. Clearing the bound
	// session first makes every racing lookup fail before dynamic graph ownership
	// is severed and its account token is consumed.
	gateway.boundSession.Store(nil)
	gateway.session = nil
	gateway.before = nil
	gateway.after = nil
	gateway.attached = false
	gateway.id = ""
	gateway.requestID = ""
	gateway.traceSequence = 0
	gateway.startedAt = time.Time{}
	gateway.nextExchange = 0
	gateway.nextEntry = 0
	gateway.nextLineage = 0
	gateway.nextMessageSequence = 0
	gateway.pendingLineageFirst = nil
	gateway.pendingLineageLast = nil
	gateway.pendingLineageCount = 0
	gateway.activeTransition = nil
	gateway.finished = false
	gateway.selectedProvider = false
	gateway.truncationCounted = false
	gateway.historyBefore = false
	gateway.historyAfter = false
	gateway.exportSnapshotOwner = nil
	gateway.exportSnapshotIndex = 0
	gateway.exportSnapshotMaterialized = false
	gateway.liveRecords = 0
	gateway.entryCount = 0
	gateway.transitionCount = 0
	gateway.charge = 0
	gateway.entryFirst = nil
	gateway.entryLast = nil
	gateway.sharedRequest = nil
	gateway.sharedRequestInitialized = false
	gateway.sharedRequestComplete = false
	gateway.sharedRequestExpected = 0
	s.releaseLocked(charge)
}
