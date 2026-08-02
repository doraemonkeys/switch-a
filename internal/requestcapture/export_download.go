package requestcapture

import (
	"errors"
	"math"
	"time"

	"go.uber.org/zap"
)

func classifyDownloadAdmissionError(err error) string {
	switch {
	case errors.Is(err, ErrDownloadUnavailable):
		return "unavailable"
	case errors.Is(err, ErrDownloadLimitReached):
		return "concurrency_limit"
	case errors.Is(err, ErrCapacityExceeded):
		return "capacity"
	case errors.Is(err, errExportReservationInvariant):
		return "reservation_invariant"
	case errors.Is(err, ErrInternalFailure):
		return "internal"
	default:
		return "unknown"
	}
}

type downloadClaim struct {
	exportID         string
	state            *exportState
	session          *sessionState
	workspace        []byte
	temporaryCharge  int64
	publicationClock time.Duration
}

func (m *Manager) acceptDownload(exportID, rawToken string) (Download, error) {
	if !isCanonicalExportID(exportID) {
		return Download{}, ErrDownloadUnavailable
	}
	workspaceBytes, temporaryCharge, validWorkspace := exportWorkspaceSizing(m.cfg.exportLineBytes)
	if !validWorkspace {
		return Download{}, ErrCapacityExceeded
	}
	now, validClock := safeExportMonotonicNow(m.cfg.clock)
	if !validClock {
		return Download{}, ErrDownloadUnavailable
	}
	claim, err := m.claimPendingDownload(exportID, rawToken, now)
	if err != nil {
		return Download{}, err
	}
	if err := m.reserveDownloadWorkspace(&claim, workspaceBytes, temporaryCharge); err != nil {
		return Download{}, err
	}
	return m.publishDownloadClaim(claim)
}

func (m *Manager) claimPendingDownload(
	exportID string,
	rawToken string,
	now time.Duration,
) (downloadClaim, error) {
	var mutation statusEpochMutation
	if !m.beginStatusEpochMutation(&mutation) {
		return downloadClaim{}, ErrInternalFailure
	}
	m.exportMu.Lock()
	state := m.lookupExportLocked(exportID)
	if state == nil ||
		state.phase != exportPhasePending ||
		!downloadTokenMatches(state.tokenHash, rawToken) {
		m.exportMu.Unlock()
		mutation.finish()
		return downloadClaim{}, ErrDownloadUnavailable
	}
	session := state.session
	if monotonicRemaining(state.expiresDeadline, now) == 0 ||
		m.active.Load() != session {
		state.phase = exportPhaseReleased
		state.cancelLocked(ErrNoActiveSession)
		timer := state.timer
		expiryEpoch := state.expiryEpoch
		state.timer = nil
		state.tokenHash = downloadTokenHash{}
		if !state.expiryOwner {
			m.removeExportLocked(exportID, state)
		}
		m.exportMu.Unlock()
		mutation.finish()
		state.release("expired_or_session_stopped")
		m.stopExportExpiryTimer(state, timer, expiryEpoch)
		return downloadClaim{}, ErrDownloadUnavailable
	}
	if m.activeDownloads+m.reservedDownloadSlots >= m.cfg.maxActiveDownloads {
		m.exportMu.Unlock()
		mutation.finish()
		return downloadClaim{}, ErrDownloadLimitReached
	}
	state.phase = exportPhaseClaiming
	state.downloadReservationOwned = true
	m.reservedDownloadSlots++
	timer := state.timer
	expiryEpoch := state.expiryEpoch
	state.timer = nil
	m.exportMu.Unlock()
	mutation.finish()
	m.stopExportExpiryTimer(state, timer, expiryEpoch)
	return downloadClaim{exportID: exportID, state: state, session: session}, nil
}

func (m *Manager) reserveDownloadWorkspace(
	claim *downloadClaim,
	workspaceBytes int,
	temporaryCharge int64,
) error {
	var mutation statusEpochMutation
	if !m.beginStatusEpochMutation(&mutation) {
		workerRelease := m.rollbackDownloadClaimReservation(claim.state)
		if workerRelease {
			claim.state.release("download_claim_canceled")
		}
		return ErrInternalFailure
	}
	reservationErr := m.reserveExportAcquisitionAccounting(claim.session, temporaryCharge)
	mutation.finish()
	if reservationErr != nil {
		workerRelease := m.rollbackDownloadClaimReservation(claim.state)
		if workerRelease {
			claim.state.release("download_claim_canceled")
		} else {
			_ = m.scheduleExportExpiry(claim.state)
		}
		if errors.Is(reservationErr, errExportReservationInvariant) {
			m.logExportReservationFault("download_reserve")
			return ErrInternalFailure
		}
		return reservationErr
	}

	// Delaying allocation until the charge is owned keeps every live byte
	// represented in capacity accounting.
	claim.workspace = make([]byte, workspaceBytes)
	claim.temporaryCharge = temporaryCharge
	publishNow, validClock := safeExportMonotonicNow(m.cfg.clock)
	if validClock {
		claim.publicationClock = publishNow
		return nil
	}
	_ = m.rollbackExportAcquisitionAccounting(claim.session, temporaryCharge)
	workerRelease := m.rollbackDownloadClaimReservation(claim.state)
	if workerRelease {
		claim.state.release("download_claim_canceled")
	} else {
		_ = m.scheduleExportExpiry(claim.state)
	}
	return ErrDownloadUnavailable
}

func (m *Manager) publishDownloadClaim(claim downloadClaim) (Download, error) {
	var mutation statusEpochMutation
	if !m.beginStatusEpochMutation(&mutation) {
		_ = m.rollbackExportAcquisitionAccounting(claim.session, claim.temporaryCharge)
		workerRelease := m.rollbackDownloadClaimReservation(claim.state)
		if workerRelease {
			claim.state.release("download_claim_canceled")
		}
		return Download{}, ErrInternalFailure
	}
	m.exportMu.Lock()
	result := m.publishDownloadClaimLocked(claim)
	m.exportMu.Unlock()
	if result.err != nil {
		if rollbackErr := m.rollbackExportAcquisitionAccounting(
			claim.session,
			claim.temporaryCharge,
		); rollbackErr != nil {
			m.logExportReservationFault("download_rollback")
			result.err = ErrInternalFailure
		}
		mutation.finish()
		if result.workerRelease {
			claim.state.release("download_claim_canceled")
		}
		m.stopExportExpiryTimer(claim.state, result.timer, result.expiryEpoch)
		if errors.Is(result.err, errExportReservationInvariant) {
			m.logExportReservationFault("download_publish")
			return Download{}, ErrInternalFailure
		}
		return Download{}, result.err
	}

	mutation.finish()
	m.stopExportExpiryTimer(claim.state, result.timer, result.expiryEpoch)
	m.cfg.logger.Info("request capture export download claimed",
		zap.String("session_id", result.sessionID),
		zap.String("export_id", result.exportID),
		zap.Int("record_count", result.recordCount),
	)
	return Download{manager: m, slot: result.slot, epoch: result.epoch}, nil
}

type downloadClaimResult struct {
	err           error
	timer         Timer
	expiryEpoch   uint64
	slot          int
	epoch         uint64
	recordCount   int
	sessionID     string
	exportID      string
	workerRelease bool
}

func (m *Manager) publishDownloadClaimLocked(claim downloadClaim) downloadClaimResult {
	state := claim.state
	result := downloadClaimResult{expiryEpoch: state.expiryEpoch}
	reservationValid := m.consumeDownloadReservationLocked(state)
	downloadSlot, registrySlotValid := m.exports.IndexExact(claim.exportID, state)
	result.slot = downloadSlot
	switch {
	case !reservationValid:
		result.err = errExportReservationInvariant
		if m.lookupExportLocked(claim.exportID) == state && !state.expiryOwner {
			m.removeExportLocked(claim.exportID, state)
		}
		state.cancelLocked(ErrInternalFailure)
		state.phase = exportPhaseReleased
		result.timer = state.timer
		state.timer = nil
		state.tokenHash = downloadTokenHash{}
		result.workerRelease = true
	case m.lookupExportLocked(claim.exportID) != state ||
		state.phase != exportPhaseClaiming ||
		state.canceled.Load():
		result.err = ErrDownloadUnavailable
		result.workerRelease = true
	case monotonicRemaining(state.expiresDeadline, claim.publicationClock) == 0 ||
		m.active.Load() != claim.session:
		result.err = ErrDownloadUnavailable
		if !state.expiryOwner {
			m.removeExportLocked(claim.exportID, state)
		}
		state.cancelLocked(ErrNoActiveSession)
		state.phase = exportPhaseReleased
		result.timer = state.timer
		state.timer = nil
		state.tokenHash = downloadTokenHash{}
		result.workerRelease = true
	case m.activeDownloads >= m.cfg.maxActiveDownloads ||
		!registrySlotValid ||
		m.nextDownloadEpoch == math.MaxUint64:
		// Consuming a reservation guarantees this slot; failure here proves the
		// registry ownership model was violated rather than ordinary contention.
		result.err = errExportReservationInvariant
		if !state.expiryOwner {
			m.removeExportLocked(claim.exportID, state)
		}
		state.cancelLocked(ErrInternalFailure)
		state.phase = exportPhaseReleased
		result.timer = state.timer
		state.timer = nil
		state.tokenHash = downloadTokenHash{}
		result.workerRelease = true
	default:
		m.nextDownloadEpoch++
		result.epoch = m.nextDownloadEpoch
		m.activeDownloads++
		state.downloadSlotOwned = true
		state.downloadEpoch = result.epoch
		state.phase = exportPhaseClaimed
		state.workspace = claim.workspace
		state.lineBytes = m.cfg.exportLineBytes
		state.temporaryCharge = claim.temporaryCharge
		result.timer = state.timer
		state.timer = nil
		state.tokenHash = downloadTokenHash{}
	}
	result.recordCount = state.recordCount
	result.sessionID = state.sessionID
	result.exportID = state.id
	return result
}

// consumeDownloadReservationLocked transfers exactly one state-owned claim
// reservation. The per-state ownership bit prevents a failed worker from
// decrementing a scalar slot reserved by another export.
func (m *Manager) consumeDownloadReservationLocked(state *exportState) bool {
	if state == nil || !state.downloadReservationOwned || m.reservedDownloadSlots <= 0 {
		return false
	}
	state.downloadReservationOwned = false
	m.reservedDownloadSlots--
	return true
}

func (m *Manager) rollbackDownloadClaimReservation(state *exportState) bool {
	m.exportMu.Lock()
	workerRelease := true
	validReservation := m.consumeDownloadReservationLocked(state)
	if validReservation &&
		m.lookupExportLocked(state.registryKey) == state &&
		state.phase == exportPhaseClaiming &&
		!state.canceled.Load() {
		state.phase = exportPhasePending
		workerRelease = false
	} else if !validReservation && state != nil {
		if m.lookupExportLocked(state.registryKey) == state {
			m.removeExportLocked(state.registryKey, state)
		}
		state.cancelLocked(ErrInternalFailure)
		state.phase = exportPhaseReleased
	}
	m.exportMu.Unlock()
	if !validReservation {
		m.logExportReservationFault("download_reservation_rollback")
	}
	return workerRelease
}

func (state *exportState) releaseOwnedAccounting(
	session *sessionState,
	manager *Manager,
) exportInvariantFault {
	session.mu.Lock()
	defer session.mu.Unlock()
	manager.mu.Lock()
	defer manager.mu.Unlock()

	temporaryCharge := state.temporaryCharge
	leaseCharge := state.leaseCharge
	if temporaryCharge < 0 ||
		temporaryCharge > manager.processTemporary ||
		temporaryCharge > session.temporaryBytes {
		return exportInvariantDownloadAccount
	}
	if leaseCharge < 0 ||
		(leaseCharge > 0 && !state.leaseSessionCharged) {
		return exportInvariantLeaseAccount
	}
	totalCharge := temporaryCharge
	if leaseCharge > math.MaxInt64-totalCharge {
		return exportInvariantLeaseAccount
	}
	totalCharge += leaseCharge
	if totalCharge > manager.processCharged ||
		totalCharge > session.chargedBytes ||
		(session.releasing && totalCharge > manager.processReleasing) {
		return exportInvariantLeaseAccount
	}
	if state.leaseAttached {
		if leaseCharge > manager.processPinned {
			return exportInvariantAttachedLeasePin
		}
	} else if leaseCharge > manager.processTemporary-temporaryCharge ||
		leaseCharge > session.temporaryBytes-temporaryCharge {
		return exportInvariantAcquiringLeaseAccount
	}

	manager.processTemporary -= temporaryCharge
	session.temporaryBytes -= temporaryCharge
	if state.leaseAttached {
		if !manager.unpinAccountLocked(leaseCharge) {
			return exportInvariantAttachedLeasePin
		}
	} else {
		manager.processTemporary -= leaseCharge
		session.temporaryBytes -= leaseCharge
	}
	manager.processCharged -= totalCharge
	session.chargedBytes -= totalCharge
	if session.releasing {
		manager.processReleasing -= totalCharge
	}
	state.temporaryCharge = 0
	state.leaseCharge = 0
	state.selectionCharge = 0
	state.acquisitionCharge = 0
	state.leaseSessionCharged = false
	state.leaseAttached = false
	return exportInvariantNone
}

func (state *exportState) release(reason string) {
	if state == nil {
		return
	}
	manager := state.manager
	if manager != nil {
		manager.exportMu.Lock()
		if state.expiryOwner {
			if !state.releasePending {
				state.releasePending = true
				state.releaseReason = reason
			}
			manager.exportMu.Unlock()
			return
		}
		manager.exportMu.Unlock()
	}
	state.releaseNow(reason)
}

func (state *exportState) releaseNow(reason string) {
	if state == nil {
		return
	}
	state.releaseOnce.Do(func() {
		manager := state.manager
		session := state.session
		if manager == nil || session == nil {
			return
		}
		var mutation statusEpochMutation
		mutating := manager.beginStatusEpochMutation(&mutation)
		finishMutation := func() {
			if mutating {
				mutation.finish()
				mutating = false
			}
		}

		manager.exportMu.Lock()
		facts := state.invariantFactsLocked(exportInvariantNone)
		snapshot := state.snapshot
		timer := state.timer
		state.snapshot = nil
		state.timer = nil
		state.workspace = nil
		state.lineBytes = 0
		state.tokenHash = downloadTokenHash{}
		state.done = nil
		state.id = ""
		state.registryKey = ""
		state.sessionID = ""
		state.reservation = 0
		state.downloadEpoch = 0
		state.phase = exportPhaseReleased
		slotFault := exportInvariantNone
		if state.downloadSlotOwned {
			if manager.activeDownloads <= 0 {
				slotFault = exportInvariantDownloadSlot
			} else {
				manager.activeDownloads--
			}
			state.downloadSlotOwned = false
		}
		manager.exportMu.Unlock()

		if snapshot != nil {
			snapshot.release()
		}
		accountFault := state.releaseOwnedAccounting(session, manager)
		if accountFault != exportInvariantNone {
			finishMutation()
			_, _ = safeExportTimerStop(timer)
			if slotFault != exportInvariantNone {
				state.failInvariantWithFacts(slotFault, facts)
			}
			state.failInvariantWithFacts(accountFault, facts)
			return
		}

		ownerHeld := state.sessionOwnerHeld
		manager.exportMu.Lock()
		state.sessionOwnerHeld = false
		state.session = nil
		// manager remains immutable so every concurrent terminal request can still
		// serialize through exportMu after the charged graph is severed.
		manager.exportMu.Unlock()
		ownerFault := ownerHeld && !session.releaseOwner()
		if ownerFault {
			manager.exportMu.Lock()
			state.sessionOwnerHeld = true
			state.session = session
			manager.exportMu.Unlock()
		}
		finishMutation()
		_, _ = safeExportTimerStop(timer)

		if slotFault != exportInvariantNone {
			state.failInvariantWithFacts(slotFault, facts)
		}
		if ownerFault {
			facts.fault = exportInvariantSessionOwner
			manager.logExportInvariant(facts)
			return
		}
		manager.cfg.logger.Info("request capture export lease released",
			zap.String("session_id", facts.sessionID),
			zap.String("export_id", facts.exportID),
			zap.String("reason", reason),
		)
	})
}

func (state *exportState) finishDownload(streamErr error) error {
	manager := state.manager
	if manager == nil {
		return ErrInternalFailure
	}
	var mutation statusEpochMutation
	mutating := manager.beginStatusEpochMutation(&mutation)
	manager.exportMu.Lock()
	registered := manager.lookupExportLocked(state.registryKey) == state
	normalCompletion := registered && state.phase == exportPhaseStreaming
	stoppedAndDetached := state.phase == exportPhaseReleased &&
		state.canceled.Load() &&
		(!registered || state.expiryOwner)
	if !normalCompletion && !stoppedAndDetached {
		if registered && !state.expiryOwner {
			manager.removeExportLocked(state.registryKey, state)
		}
		state.cancelLocked(ErrInternalFailure)
		state.phase = exportPhaseReleased
		facts := state.invariantFactsLocked(exportInvariantUnexpectedStreamPhase)
		manager.exportMu.Unlock()
		if mutating {
			mutation.finish()
		}
		manager.logExportInvariant(facts)
		state.release("stream_invariant_failed")
		return ErrInternalFailure
	}
	if normalCompletion {
		if !state.expiryOwner {
			manager.removeExportLocked(state.registryKey, state)
		}
		state.phase = exportPhaseReleased
	}
	manager.exportMu.Unlock()
	if mutating {
		mutation.finish()
	}

	reason := classifyExportStreamRelease(state.canceled.Load(), streamErr)
	state.release(reason)
	return nil
}

func (state *exportState) writeSnapshot(writer *exportStreamWriter) error {
	if state == nil || state.snapshot == nil {
		return ErrDownloadUnavailable
	}
	snapshot := state.snapshot
	if err := writer.WriteManifestBegin(len(snapshot.records)); err != nil {
		return err
	}
	manifestStream := newMetadataChunkStream(writer, true, 0)
	manifestJSON := jsonDocumentWriter{sink: manifestStream}
	writeManifestMetadata(&manifestJSON, state)
	if manifestJSON.err != nil {
		return manifestJSON.err
	}
	manifestDigest, err := manifestStream.finish()
	if err != nil {
		return err
	}
	if err := writer.WriteMetadataEnd(true, 0, manifestDigest); err != nil {
		return err
	}

	for recordIndex := range snapshot.records {
		record := &snapshot.records[recordIndex]
		if err := writer.WriteRecordBegin(recordIndex, len(record.blobs)); err != nil {
			return err
		}
		metadataStream := newMetadataChunkStream(writer, false, recordIndex)
		metadataJSON := jsonDocumentWriter{sink: metadataStream}
		writeRecordMetadata(&metadataJSON, record)
		if metadataJSON.err != nil {
			return metadataJSON.err
		}
		metadataDigest, err := metadataStream.finish()
		if err != nil {
			return err
		}
		if err := writer.WriteMetadataEnd(false, recordIndex, metadataDigest); err != nil {
			return err
		}

		rawPayloadSize := int64(0)
		for blobIndex := range record.blobs {
			blobDigest, err := state.writeBlob(writer, recordIndex, blobIndex, &record.blobs[blobIndex])
			if err != nil {
				return err
			}
			rawPayloadSize += blobDigest.RawSize
		}
		if err := writer.WriteRecordEnd(
			recordIndex,
			len(record.blobs),
			rawPayloadSize,
			metadataDigest,
		); err != nil {
			return err
		}
	}
	return writer.WriteExportEnd(len(snapshot.records))
}
