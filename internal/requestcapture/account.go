package requestcapture

import (
	"fmt"

	"go.uber.org/zap"
)

const (
	sessionBaseChargeBytes        int64 = 1024
	sessionRootChargeBytes              = sessionBaseChargeBytes + int64(maxSessionIDBytes)
	traceBaseChargeBytes          int64 = 512
	recordBaseChargeBytes         int64 = 1536
	traceEntryBaseChargeBytes     int64 = 256
	transitionRecorderChargeBytes int64 = 128
	messageBaseChargeBytes        int64 = 320
	blobBaseChargeBytes           int64 = 128
	checksumStateChargeBytes      int64 = 256
	snapshotBaseChargeBytes       int64 = 512
	snapshotRecordChargeBytes     int64 = 384
	exportBaseChargeBytes         int64 = 512
	tokenChargeBytes              int64 = 128
	mapEntryChargeBytes           int64 = 96
	sliceEntryChargeBytes         int64 = 32
	evictionRangeChargeBytes      int64 = 96
	lineageChargeBytes            int64 = 64
)

type captureOperation uint8

const (
	captureOperationUnknown captureOperation = iota
	captureOperationStart
	captureOperationBeginGateway
	captureOperationNewMessageID
	captureOperationBeginRecord
	captureOperationTransition
	captureOperationHTTPResponse
	captureOperationWebSocketHandshake
	captureOperationMessageRead
	captureOperationMessageResult
	captureOperationTransitionFinish
	captureOperationRecordFinish
)

type allocationPlan struct {
	operation      captureOperation
	candidateBytes int64
	scratchBytes   int64
	mutationEpoch  uint64
	generation     uint64
	proof          allocationProof
}

type allocationProof uint8

const (
	allocationProofInvalid allocationProof = iota
	allocationProofStart
	allocationProofBeginGateway
	allocationProofNewMessageID
	allocationProofBeginRecord
	allocationProofTransition
	allocationProofHTTPResponse
	allocationProofWebSocketHandshake
	allocationProofMessageRead
	allocationProofMessageResult
	allocationProofTransitionFinish
	allocationProofRecordFinish
)

type ownedCharge struct {
	bytes int64
	live  bool
}

type allocationState uint8

const (
	allocationStateEmpty allocationState = iota
	allocationStateReserved
	allocationStateCommitted
	allocationStateRolledBack
)

// CaptureAllocation owns the old+candidate+workspace peak for one mutation.
// Callers allocate the token in their own stack frame and pass its address only
// for mutation; the state machine rejects reuse after commit or rollback.
type CaptureAllocation struct {
	session *sessionState
	plan    allocationPlan

	reservedBytes int64
	candidateUsed int64
	scratchLive   int64
	scratchPeak   int64
	state         allocationState
}

func (s *sessionState) newCapturePlanLocked(
	operation captureOperation,
	proof allocationProof,
	candidateBytes, scratchBytes int64,
) allocationPlan {
	if s == nil || !validAllocationProof(operation, proof) ||
		candidateBytes < 0 || scratchBytes < 0 ||
		addRetainedCharge64(candidateBytes, scratchBytes) == int64(^uint64(0)>>1) {
		return allocationPlan{}
	}
	return allocationPlan{
		operation:      operation,
		candidateBytes: candidateBytes,
		scratchBytes:   scratchBytes,
		mutationEpoch:  s.mutationEpoch,
		generation:     s.generation,
		proof:          proof,
	}
}

func validAllocationProof(operation captureOperation, proof allocationProof) bool {
	return operation != captureOperationUnknown && allocationProof(operation) == proof
}

func (s *sessionState) planBeginGatewayAllocationLocked(candidate, scratch int64) allocationPlan {
	return s.newCapturePlanLocked(captureOperationBeginGateway, allocationProofBeginGateway, candidate, scratch)
}

func (s *sessionState) planNewMessageIDAllocationLocked(candidate int64) allocationPlan {
	return s.newCapturePlanLocked(captureOperationNewMessageID, allocationProofNewMessageID, candidate, 0)
}

func (s *sessionState) planBeginRecordAllocationLocked(candidate, scratch int64) allocationPlan {
	return s.newCapturePlanLocked(captureOperationBeginRecord, allocationProofBeginRecord, candidate, scratch)
}

func (s *sessionState) planTransitionAllocationLocked(candidate, scratch int64) allocationPlan {
	return s.newCapturePlanLocked(captureOperationTransition, allocationProofTransition, candidate, scratch)
}

func (s *sessionState) planHTTPResponseAllocationLocked(candidate, scratch int64) allocationPlan {
	return s.newCapturePlanLocked(captureOperationHTTPResponse, allocationProofHTTPResponse, candidate, scratch)
}

func (s *sessionState) planMessageResultAllocationLocked(candidate, scratch int64) allocationPlan {
	return s.newCapturePlanLocked(captureOperationMessageResult, allocationProofMessageResult, candidate, scratch)
}

func (s *sessionState) beginCaptureAllocationLocked(
	plan allocationPlan,
	allocation *CaptureAllocation,
) bool {
	if s == nil || allocation == nil || allocation.state != allocationStateEmpty ||
		!validAllocationProof(plan.operation, plan.proof) ||
		plan.generation != s.generation || plan.mutationEpoch != s.mutationEpoch {
		return false
	}
	if !s.accepting || s.releasing || s.manager.active.Load() != s {
		return false
	}
	reserved := addRetainedCharge64(plan.candidateBytes, plan.scratchBytes)
	if reserved == int64(^uint64(0)>>1) {
		return false
	}

	m := s.manager
	m.mu.Lock()
	if reserved > s.quotaBytes-s.chargedBytes ||
		reserved > m.cfg.processCeilingBytes-m.processCharged {
		m.mu.Unlock()
		return false
	}
	s.chargedBytes += reserved
	s.temporaryBytes += reserved
	m.processCharged += reserved
	m.processTemporary += reserved
	m.mu.Unlock()

	*allocation = CaptureAllocation{
		session:       s,
		plan:          plan,
		reservedBytes: reserved,
		state:         allocationStateReserved,
	}
	return true
}

func (allocation *CaptureAllocation) claimCandidate(bytes int64) bool {
	if allocation == nil || allocation.state != allocationStateReserved || bytes < 0 ||
		bytes > allocation.plan.candidateBytes-allocation.candidateUsed {
		return false
	}
	allocation.candidateUsed += bytes
	return true
}

func (allocation *CaptureAllocation) claimScratch(bytes int64) bool {
	if allocation == nil || allocation.state != allocationStateReserved || bytes < 0 ||
		bytes > allocation.plan.scratchBytes-allocation.scratchLive {
		return false
	}
	allocation.scratchLive += bytes
	if allocation.scratchLive > allocation.scratchPeak {
		allocation.scratchPeak = allocation.scratchLive
	}
	return true
}

func (allocation *CaptureAllocation) releaseScratch(bytes int64) bool {
	if allocation == nil || allocation.state != allocationStateReserved ||
		bytes < 0 || bytes > allocation.scratchLive {
		return false
	}
	allocation.scratchLive -= bytes
	return true
}

// captureCommit represents an account mutex held across a typed, allocation-free
// publication. It is deliberately unexported: only operation-specific commit
// methods may place graph assignments between begin and finish.
type captureCommit struct {
	retired       *ownedCharge
	session       *sessionState
	manager       *Manager
	reservedBytes int64
	releaseBytes  int64
	active        bool
}

func (allocation *CaptureAllocation) beginCommitAccountingLocked(
	retired *ownedCharge,
	commit *captureCommit,
) bool {
	if commit == nil || commit.active {
		return false
	}
	retiredBytes := int64(0)
	if retired != nil {
		if !retired.live || retired.bytes < 0 {
			return false
		}
		retiredBytes = retired.bytes
	}
	if allocation == nil || allocation.state != allocationStateReserved ||
		allocation.session == nil || allocation.scratchLive != 0 ||
		allocation.plan.generation != allocation.session.generation ||
		allocation.plan.mutationEpoch != allocation.session.mutationEpoch ||
		allocation.candidateUsed > allocation.plan.candidateBytes {
		return false
	}

	s := allocation.session
	m := s.manager
	m.mu.Lock()
	retainedBefore := s.chargedBytes - s.temporaryBytes
	refund := allocation.reservedBytes - allocation.candidateUsed
	if retiredBytes > retainedBefore || refund < 0 || retiredBytes > int64(^uint64(0)>>1)-refund ||
		allocation.reservedBytes > s.temporaryBytes ||
		allocation.reservedBytes > m.processTemporary ||
		refund > s.chargedBytes-retiredBytes ||
		refund > m.processCharged-retiredBytes ||
		(s.releasing && refund+retiredBytes > m.processReleasing) {
		m.mu.Unlock()
		return false
	}
	*commit = captureCommit{
		retired:       retired,
		session:       s,
		manager:       m,
		reservedBytes: allocation.reservedBytes,
		releaseBytes:  refund + retiredBytes,
		active:        true,
	}
	return true
}

func (commit *captureCommit) finishLocked(allocation *CaptureAllocation) {
	if commit == nil || !commit.active {
		return
	}
	if allocation == nil || allocation.state != allocationStateReserved ||
		allocation.session != commit.session || allocation.reservedBytes != commit.reservedBytes {
		commit.abortLocked()
		return
	}
	s := commit.session
	m := commit.manager
	s.temporaryBytes -= commit.reservedBytes
	m.processTemporary -= commit.reservedBytes
	s.chargedBytes -= commit.releaseBytes
	m.processCharged -= commit.releaseBytes
	if s.releasing {
		m.processReleasing -= commit.releaseBytes
	}
	s.mutationEpoch++
	if commit.retired != nil {
		commit.retired.bytes = 0
		commit.retired.live = false
	}
	allocation.state = allocationStateCommitted
	allocation.session = nil
	commit.active = false
	m.mu.Unlock()
	*commit = captureCommit{}
}

func (commit *captureCommit) abortLocked() {
	if commit == nil || !commit.active {
		return
	}
	m := commit.manager
	*commit = captureCommit{}
	m.mu.Unlock()
}

func (allocation *CaptureAllocation) rollbackLocked() bool {
	if allocation == nil || allocation.state != allocationStateReserved || allocation.session == nil {
		return allocation != nil && allocation.state == allocationStateRolledBack
	}
	s := allocation.session
	m := s.manager
	m.mu.Lock()
	if allocation.reservedBytes > s.temporaryBytes ||
		allocation.reservedBytes > s.chargedBytes ||
		allocation.reservedBytes > m.processTemporary ||
		allocation.reservedBytes > m.processCharged ||
		(s.releasing && allocation.reservedBytes > m.processReleasing) {
		m.mu.Unlock()
		return false
	}
	s.temporaryBytes -= allocation.reservedBytes
	s.chargedBytes -= allocation.reservedBytes
	m.processTemporary -= allocation.reservedBytes
	m.processCharged -= allocation.reservedBytes
	if s.releasing {
		m.processReleasing -= allocation.reservedBytes
	}
	m.mu.Unlock()
	allocation.state = allocationStateRolledBack
	allocation.session = nil
	return true
}

func estimateRecordCharge(request RequestSnapshot, summary RecordSummary, sensitiveHeaderNames []string) int64 {
	total := recordBaseChargeBytes + 2*mapEntryChargeBytes + sliceEntryChargeBytes
	total = addRetainedCharge(total,
		len(summary.SessionID)+len(summary.RecordID)+len(summary.GatewayTraceID)+len(summary.GatewayRequestID)+
			len(summary.Provider.ID)+len(summary.Provider.Name)+len(summary.Provider.APIType)+len(summary.Provider.TargetURL)+
			len(summary.Protocol)+len(summary.SelectionMode)+len(summary.SelectionSource)+len(summary.CredentialPhase)+
			len(request.Method)+len(request.URL)+len(request.Host),
	)
	total = addRetainedCharge64(total, estimateHeaderCharge(request.Headers))
	total = addRetainedCharge64(total, estimateHeaderCharge(request.Trailers))
	return addRetainedCharge64(total, estimateStringSliceCharge(sensitiveHeaderNames))
}

func estimateTraceEntryCharge(entry TraceEntry) int64 {
	return addRetainedCharge(traceEntryBaseChargeBytes+sliceEntryChargeBytes,
		len(entry.EntryID)+len(entry.RecordID)+
			len(entry.Provider.ID)+len(entry.Provider.Name)+len(entry.Provider.APIType)+len(entry.Provider.TargetURL)+
			len(entry.SelectionMode)+len(entry.SelectionSource)+len(entry.CredentialPhase)+
			len(entry.TerminationReason)+int(estimateFailureCharge(entry.Failure)),
	)
}

func estimateFailureCharge(failure FailureObservation) int64 {
	total := estimateFailureFactCharge(failure.Primary)
	if failure.HasSecondary {
		total = addRetainedCharge64(total, estimateFailureFactCharge(failure.Secondary))
	}
	return total
}

func estimateFailureFactCharge(fact FailureFact) int64 {
	return addRetainedCharge(
		0,
		len(fact.ProviderErrorType)+len(fact.ProviderErrorCode)+len(fact.Message),
	)
}

func estimateHTTPResponseCharge(snapshot HTTPResponseSnapshot, sensitiveHeaderNames []string) int64 {
	return int64(len(snapshot.Protocol)) +
		estimateHeaderCharge(snapshot.Headers) +
		estimateStringSliceCharge(snapshot.DeclaredTrailerKeys) +
		estimateStringSliceCharge(sensitiveHeaderNames)
}

func estimateWebSocketHandshakeCharge(snapshot WebSocketHandshakeSnapshot, sensitiveHeaderNames []string) int64 {
	return int64(len(snapshot.Protocol)) +
		estimateHeaderCharge(snapshot.Headers) +
		estimateStringSliceCharge(sensitiveHeaderNames)
}

func estimateStringSliceCharge(values []string) int64 {
	total := addRetainedCharge(0, len(values)*int(sliceEntryChargeBytes))
	for _, value := range values {
		total = addRetainedCharge(total, len(value))
	}
	return total
}

func estimateHeaderCharge(header map[string][]string) int64 {
	total := int64(0)
	for name, values := range header {
		total = addRetainedCharge64(total, mapEntryChargeBytes)
		total = addRetainedCharge(total, len(name))
		for _, value := range values {
			total = addRetainedCharge64(total, sliceEntryChargeBytes)
			total = addRetainedCharge(total, len(value))
		}
	}
	return total
}

func addRetainedCharge(total int64, addition int) int64 {
	if addition < 0 || int64(addition) > int64(^uint64(0)>>1)-total {
		return int64(^uint64(0) >> 1)
	}
	return total + int64(addition)
}

func addRetainedCharge64(total, addition int64) int64 {
	if addition < 0 || addition > int64(^uint64(0)>>1)-total {
		return int64(^uint64(0) >> 1)
	}
	return total + addition
}

// reserveLocked atomically charges both the process and session accounts. The
// caller holds s.mu, which makes the child quota check part of the same logical
// transaction as retained-object publication.
func (s *sessionState) reserveLocked(bytes int64, allowEviction bool) bool {
	if bytes <= 0 {
		return true
	}
	if s.tryReserveLocked(bytes) {
		return true
	}
	if !allowEviction {
		return false
	}
	for s.evictOldestCompletedLocked(nil) {
		if s.tryReserveLocked(bytes) {
			return true
		}
	}
	return false
}

func (s *sessionState) tryReserveLocked(bytes int64) bool {
	m := s.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	if bytes > s.quotaBytes-s.chargedBytes || bytes > m.cfg.processCeilingBytes-m.processCharged {
		return false
	}
	s.chargedBytes += bytes
	m.processCharged += bytes
	return true
}

func (s *sessionState) releaseLocked(bytes int64) {
	if bytes <= 0 {
		return
	}
	m := s.manager
	m.mu.Lock()
	if bytes > s.chargedBytes || bytes > m.processCharged ||
		(s.releasing && bytes > m.processReleasing) {
		m.mu.Unlock()
		m.cfg.logger.Error("request capture accounting release rejected",
			zap.String("session_id", s.id),
			zap.Uint64("generation", s.generation),
			zap.Int64("release_bytes", bytes),
			zap.Int64("session_charged_bytes", s.chargedBytes),
		)
		return
	}
	s.chargedBytes -= bytes
	m.processCharged -= bytes
	if s.releasing {
		m.processReleasing -= bytes
	}
	m.mu.Unlock()
}

func (s *sessionState) markReleasingLocked() {
	m := s.manager
	m.mu.Lock()
	if !s.releasing {
		s.releasing = true
		m.processReleasing += s.chargedBytes
	}
	m.mu.Unlock()
}

// pinAccountLocked and unpinAccountLocked are the sole mutation boundary for
// pinned process memory. Callers hold Manager.mu so compound charged/pinned
// ownership transitions can be published as one account transaction.
func (m *Manager) pinAccountLocked(bytes int64) {
	if bytes > 0 {
		m.processPinned += bytes
	}
}

func (m *Manager) unpinAccountLocked(bytes int64) bool {
	if bytes <= 0 {
		return true
	}
	if bytes > m.processPinned {
		return false
	}
	m.processPinned -= bytes
	return true
}

func (s *sessionState) pinLocked(bytes int64) {
	if bytes <= 0 {
		return
	}
	m := s.manager
	m.mu.Lock()
	m.pinAccountLocked(bytes)
	m.mu.Unlock()
}

func (s *sessionState) unpinLocked(bytes int64) {
	if bytes <= 0 {
		return
	}
	m := s.manager
	m.mu.Lock()
	if m.unpinAccountLocked(bytes) {
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()
	m.cfg.logger.Error("request capture pin release rejected",
		zap.String("session_id", s.id),
		zap.Uint64("generation", s.generation),
		zap.Int64("release_bytes", bytes),
	)
}

func (s *sessionState) releaseAllLocked() {
	// A checked-out status slot is a bounded session owner. Retiring it before
	// catalog teardown lets Stop proceed without waiting for the external writer;
	// the lease releases its own charge and the final root owner when it finishes.
	s.retireStatusSlotLocked()
	for s.oldestRecord != nil {
		s.detachAndReleaseRecordLocked(s.oldestRecord, false)
	}
	s.oldestRecord = nil
	s.newestRecord = nil
	s.retainedRecordCount = 0
	s.providerRecords = nil
	s.providerRecordIndex = nil
	s.providers = nil
	s.providerOrder = nil
	s.releaseEvictionIndexLocked()
	s.activeRecords = 0
	for s.traceFirst != nil {
		s.releaseTraceLocked(s.traceFirst)
	}
	s.activeTraces = 0
	s.severHandleSlotsLocked()
	if s.baseCharge > 0 {
		s.releaseLocked(s.baseCharge)
		s.baseCharge = 0
	}
	// The fixed session shell remains charged while query/export owners still
	// need it as their account anchor.
	_ = s.releaseActiveOwner()
}

func (s *sessionState) debugInvariantLocked() error {
	s.manager.mu.Lock()
	charged := s.chargedBytes
	temporary := s.temporaryBytes
	s.manager.mu.Unlock()
	if charged < 0 || charged > s.quotaBytes {
		return fmt.Errorf("session charge %d outside [0,%d]", charged, s.quotaBytes)
	}
	if temporary < 0 || temporary > charged {
		return fmt.Errorf("session temporary charge %d outside [0,%d]", temporary, charged)
	}
	if s.activeRecords < 0 || s.activeRecords > s.manager.cfg.maxActiveRecords {
		return fmt.Errorf("active record count %d outside bounds", s.activeRecords)
	}
	return nil
}
