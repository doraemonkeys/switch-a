package requestcapture

import (
	"crypto/sha256"
	"errors"
	"math"
	"strconv"
	"time"

	"go.uber.org/zap"
)

type exportAdmissionGuard struct {
	slot    chan struct{}
	session *sessionState
	logger  *zap.Logger
	active  bool
}

func acquireExportAdmission(
	probe exportCancellationProbe,
	session *sessionState,
	state *exportState,
	guard *exportAdmissionGuard,
) error {
	if session == nil || guard == nil || guard.active {
		return ErrInternalFailure
	}
	if err := probe.lockedError(); err != nil {
		return err
	}
	// Admission is an independent operation owner. It prevents export cleanup from
	// retiring the session account before this guard returns its channel token.
	if !session.retainOwner() {
		return ErrNoActiveSession
	}
	slot := session.exportAdmission
	manager := session.manager
	if slot == nil || manager == nil {
		_ = session.releaseOwner()
		return ErrNoActiveSession
	}
	select {
	case slot <- struct{}{}:
		*guard = exportAdmissionGuard{
			slot:    slot,
			session: session,
			logger:  manager.cfg.logger,
			active:  true,
		}
		return nil
	case <-probe.done:
		_ = session.releaseOwner()
		return errExportContextCanceled
	case <-state.done:
		_ = session.releaseOwner()
		return probe.lockedError()
	}
}

func (guard *exportAdmissionGuard) release() {
	if guard == nil || !guard.active {
		return
	}
	slot := guard.slot
	session := guard.session
	logger := guard.logger
	*guard = exportAdmissionGuard{}
	select {
	case <-slot:
	default:
		if logger != nil {
			logger.Error("request capture export admission invariant failed",
				zap.String("reason", "release_without_owner"),
			)
		}
	}
	if session != nil && !session.releaseOwner() && logger != nil {
		logger.Error("request capture export admission invariant failed",
			zap.String("reason", "session_owner_release_failed"),
		)
	}
}

func lockExportFreezeState(
	probe exportCancellationProbe,
	session *sessionState,
	state *exportState,
) error {
	ticker := time.NewTicker(exportSessionLockRetryInterval)
	defer ticker.Stop()
	for {
		if err := probe.lockedError(); err != nil {
			return err
		}
		if session.gate.TryRLock() {
			if session.mu.TryLock() {
				return nil
			}
			session.gate.RUnlock()
		}
		select {
		case <-probe.done:
			return errExportContextCanceled
		case <-state.done:
			return probe.lockedError()
		case <-ticker.C:
		}
	}
}

func lockExportSessionState(
	probe exportCancellationProbe,
	session *sessionState,
	state *exportState,
) error {
	ticker := time.NewTicker(exportSessionLockRetryInterval)
	defer ticker.Stop()
	for {
		if err := probe.lockedError(); err != nil {
			return err
		}
		if session.mu.TryLock() {
			return nil
		}
		select {
		case <-probe.done:
			return errExportContextCanceled
		case <-state.done:
			return probe.lockedError()
		case <-ticker.C:
		}
	}
}

func (m *Manager) beginExportAcquisition(
	session *sessionState,
	selectionCharge int64,
) (*exportState, error) {
	if m == nil {
		return nil, ErrInternalFailure
	}
	var mutation statusEpochMutation
	if !m.beginStatusEpochMutation(&mutation) {
		return nil, ErrInternalFailure
	}
	defer mutation.finish()
	if session == nil || !session.retainOwner() {
		return nil, ErrNoActiveSession
	}
	reservation, err := m.reserveExportSlot(session)
	if err != nil {
		_ = session.releaseOwner()
		return nil, err
	}
	// The owner contract keeps identity immutable until this export releases it.
	sessionID := session.id
	acquisitionCharge := int64(exportAcquiringKeyBytes(reservation))
	leaseCharge, validLeaseCharge := exportLeaseCharge(sessionID, selectionCharge)
	if !validLeaseCharge || acquisitionCharge > math.MaxInt64-leaseCharge {
		m.releaseReservedExportSlot()
		_ = session.releaseOwner()
		return nil, ErrCapacityExceeded
	}
	leaseCharge += acquisitionCharge
	if err := m.reserveExportAcquisitionAccounting(session, leaseCharge); err != nil {
		m.releaseReservedExportSlot()
		_ = session.releaseOwner()
		return nil, err
	}

	// Every allocation below is covered by the temporary reservation. Publication
	// happens under the registry lock only after the active generation is checked
	// again, so Stop cannot miss a materializing export.
	registryKey := exportAcquiringKeyPrefix + strconv.FormatUint(reservation, 36)
	state := &exportState{
		manager:             m,
		registryKey:         registryKey,
		sessionID:           sessionID,
		session:             session,
		phase:               exportPhaseAcquiring,
		reservation:         reservation,
		leaseCharge:         leaseCharge,
		selectionCharge:     selectionCharge,
		acquisitionCharge:   acquisitionCharge,
		leaseSessionCharged: true,
		sessionOwnerHeld:    true,
		done:                make(chan struct{}),
	}
	if err := m.publishExportAcquisition(session, state); err != nil {
		state.registryKey = ""
		state.sessionID = ""
		state.done = nil
		state.session = nil
		state.manager = nil
		if rollbackErr := m.rollbackExportAcquisitionAccounting(session, leaseCharge); rollbackErr != nil {
			m.logExportReservationFault("rollback")
			return nil, ErrInternalFailure
		}
		_ = session.releaseOwner()
		return nil, err
	}
	return state, nil
}

func (m *Manager) reserveExportSlot(session *sessionState) (uint64, error) {
	m.lifecycleMu.Lock()
	closed := m.closed
	active := m.active.Load()
	m.lifecycleMu.Unlock()
	if closed {
		return 0, ErrManagerClosed
	}
	if active != session {
		return 0, ErrNoActiveSession
	}

	m.exportMu.Lock()
	defer m.exportMu.Unlock()
	if m.pendingExportCountLocked() >= m.cfg.maxPendingExports ||
		m.exports.Count()+m.reservedExportSlots >= m.exports.Capacity() {
		return 0, ErrExportLimitReached
	}
	if m.nextExportReservation == math.MaxUint64 {
		return 0, errExportReservationExhausted
	}
	if err := m.materializeExportRegistryLocked(); err != nil {
		return 0, err
	}
	m.nextExportReservation++
	m.reservedExportSlots++
	return m.nextExportReservation, nil
}

func (m *Manager) releaseReservedExportSlot() {
	m.exportMu.Lock()
	valid := m.reservedExportSlots > 0
	if valid {
		m.reservedExportSlots--
		m.releaseEmptyExportRegistryLocked()
	}
	m.exportMu.Unlock()
	if !valid {
		m.cfg.logger.Error("request capture export reservation invariant failed",
			zap.String("operation", "release_slot"),
			zap.String("reason", "missing_reserved_slot"),
		)
	}
}

func (m *Manager) reserveExportAcquisitionAccounting(
	session *sessionState,
	charge int64,
) error {
	session.mu.Lock()
	defer session.mu.Unlock()
	if !session.accepting || m.active.Load() != session {
		return ErrNoActiveSession
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if charge <= 0 ||
		session.chargedBytes < 0 ||
		session.temporaryBytes < 0 ||
		m.processCharged < 0 ||
		m.processTemporary < 0 {
		return errExportReservationInvariant
	}
	if charge > session.quotaBytes-session.chargedBytes ||
		charge > m.cfg.processCeilingBytes-m.processCharged {
		return ErrCapacityExceeded
	}
	session.chargedBytes += charge
	session.temporaryBytes += charge
	m.processCharged += charge
	m.processTemporary += charge
	return nil
}

func (m *Manager) rollbackExportAcquisitionAccounting(
	session *sessionState,
	charge int64,
) error {
	session.mu.Lock()
	defer session.mu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if charge <= 0 ||
		charge > session.chargedBytes ||
		charge > session.temporaryBytes ||
		charge > m.processCharged ||
		charge > m.processTemporary ||
		(session.releasing && charge > m.processReleasing) {
		return errExportReservationInvariant
	}
	session.chargedBytes -= charge
	session.temporaryBytes -= charge
	m.processCharged -= charge
	m.processTemporary -= charge
	if session.releasing {
		m.processReleasing -= charge
	}
	return nil
}

func (m *Manager) publishExportAcquisition(
	session *sessionState,
	state *exportState,
) error {
	m.lifecycleMu.Lock()
	m.exportMu.Lock()
	var err error
	switch {
	case m.reservedExportSlots <= 0:
		err = errExportReservationInvariant
	case m.closed:
		err = ErrManagerClosed
	case m.active.Load() != session:
		err = ErrNoActiveSession
	case m.lookupExportLocked(state.registryKey) != nil:
		err = errExportReservationInvariant
	}
	if m.reservedExportSlots > 0 {
		m.reservedExportSlots--
	}
	if err == nil && !m.insertExportLocked(state.registryKey, state) {
		err = errExportReservationInvariant
	}
	if err != nil {
		m.releaseEmptyExportRegistryLocked()
	}
	m.exportMu.Unlock()
	m.lifecycleMu.Unlock()
	if errors.Is(err, errExportReservationInvariant) {
		m.logExportReservationFault("publish")
		return ErrInternalFailure
	}
	return err
}

func exportAcquiringKeyBytes(reservation uint64) int {
	digits := 1
	for reservation >= 36 {
		reservation /= 36
		digits++
	}
	return len(exportAcquiringKeyPrefix) + digits
}

func (m *Manager) logExportReservationFault(operation string) {
	m.cfg.logger.Error("request capture export reservation invariant failed",
		zap.String("operation", operation),
		zap.String("reason", "accounting_invariant"),
	)
}

// attachLeaseLocked requires session.mu and performs only the terminal account
// transition. The caller reports any returned fault after releasing session.mu.
func (state *exportState) attachLeaseLocked() (exportInvariantFault, error) {
	if state == nil || state.manager == nil || state.session == nil {
		return exportInvariantAttachProcessAccount, ErrInternalFailure
	}
	if err := exportStateAcquisitionError(state); err != nil {
		return exportInvariantNone, err
	}
	session := state.session
	manager := state.manager
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if state.leaseAttached {
		return exportInvariantNone, nil
	}
	if !state.leaseSessionCharged ||
		state.leaseCharge <= 0 ||
		state.leaseCharge > manager.processTemporary {
		return exportInvariantAttachProcessAccount, ErrInternalFailure
	}
	if state.leaseCharge > session.temporaryBytes {
		return exportInvariantAttachSessionAccount, ErrInternalFailure
	}
	manager.processTemporary -= state.leaseCharge
	session.temporaryBytes -= state.leaseCharge
	manager.pinAccountLocked(state.leaseCharge)
	state.leaseAttached = true
	return exportInvariantNone, nil
}

func (state *exportState) releaseSelectionAccounting() error {
	if state == nil || state.manager == nil || state.session == nil {
		return ErrInternalFailure
	}
	session := state.session
	session.mu.Lock()
	fault := state.releaseSelectionAccountingLocked()
	session.mu.Unlock()
	if fault != exportInvariantNone {
		state.failInvariant(fault)
		return ErrInternalFailure
	}
	return nil
}

// releaseSelectionAccountingLocked requires session.mu. Selection references
// are severed by the caller before this terminal account transition.
func (state *exportState) releaseSelectionAccountingLocked() exportInvariantFault {
	return state.releaseLeaseComponentAccountingLocked(
		&state.selectionCharge,
		exportLeaseComponentFaults{
			account:   exportInvariantSelectionAccount,
			pin:       exportInvariantSelectionPin,
			temporary: exportInvariantSelectionTemporary,
			unpin:     exportInvariantSelectionUnpin,
		},
	)
}

func (state *exportState) releaseAcquisitionAccounting() error {
	if state == nil || state.manager == nil || state.session == nil {
		return ErrInternalFailure
	}
	session := state.session
	session.mu.Lock()
	fault := state.releaseLeaseComponentAccountingLocked(
		&state.acquisitionCharge,
		exportLeaseComponentFaults{
			account:   exportInvariantAcquisitionAccount,
			pin:       exportInvariantAcquisitionPin,
			temporary: exportInvariantAcquisitionTemporary,
			unpin:     exportInvariantAcquisitionUnpin,
		},
	)
	session.mu.Unlock()
	if fault != exportInvariantNone {
		state.failInvariant(fault)
		return ErrInternalFailure
	}
	return nil
}

// releaseLeaseComponentAccountingLocked requires session.mu and is the single
// terminal transition for short-lived components of an export lease.
func (state *exportState) releaseLeaseComponentAccountingLocked(
	componentCharge *int64,
	faults exportLeaseComponentFaults,
) exportInvariantFault {
	manager := state.manager
	session := state.session
	manager.mu.Lock()
	defer manager.mu.Unlock()

	charge := *componentCharge
	if charge == 0 {
		return exportInvariantNone
	}
	if charge < 0 ||
		!state.leaseSessionCharged ||
		charge > state.leaseCharge ||
		charge > manager.processCharged ||
		charge > session.chargedBytes ||
		(session.releasing && charge > manager.processReleasing) {
		return faults.account
	}
	if state.leaseAttached {
		if charge > manager.processPinned {
			return faults.pin
		}
	} else if charge > manager.processTemporary || charge > session.temporaryBytes {
		return faults.temporary
	}

	if state.leaseAttached {
		if !manager.unpinAccountLocked(charge) {
			return faults.unpin
		}
	} else {
		manager.processTemporary -= charge
		session.temporaryBytes -= charge
	}
	manager.processCharged -= charge
	session.chargedBytes -= charge
	if session.releasing {
		manager.processReleasing -= charge
	}
	state.leaseCharge -= charge
	*componentCharge = 0
	return exportInvariantNone
}

func (selection *exportSelection) resolveRecordsLocked(
	probe exportCancellationProbe,
	session *sessionState,
) error {
	if selection == nil {
		return ErrInternalFailure
	}
	if selection.scope == ExportScopeAll {
		return nil
	}
	for index, recordID := range selection.recordIDs {
		if err := exportSelectionLockedCancellationError(probe, index); err != nil {
			return err
		}
		if !session.ownsRecordID(recordID) {
			return ErrRecordNotFound
		}
	}

	resolved := 0
	scanIndex := 0
	for record := session.oldestRecord; record != nil; record = record.newer {
		if err := exportSelectionLockedCancellationError(probe, scanIndex); err != nil {
			return err
		}
		scanIndex++
		if _, selected := selection.records[record.id]; !selected {
			continue
		}
		selection.records[record.id] = record
		resolved++
	}
	if resolved != len(selection.recordIDs) {
		return ErrRecordEvicted
	}
	return exportSelectionLockedCancellationError(probe, scanIndex)
}

func exportSelectionLockedCancellationError(
	probe exportCancellationProbe,
	index int,
) error {
	if index%exportSelectionCancellationBatch != 0 {
		return nil
	}
	return probe.lockedError()
}

func (selection *exportSelection) selectsRecord(record *recordState) bool {
	if selection == nil || record == nil {
		return false
	}
	if selection.scope == ExportScopeAll {
		return true
	}
	selected, exists := selection.records[record.id]
	return exists && selected == record
}

func (selection *exportSelection) clear() {
	if selection == nil {
		return
	}
	selection.recordIDs = nil
	selection.records = nil
}

func (selection *exportSelection) selectedRecordCount() int {
	if selection == nil || selection.scope == ExportScopeAll {
		return 0
	}
	return len(selection.recordIDs)
}

func exportSelectionCancellationError(
	probe exportCancellationProbe,
	index int,
) error {
	if index%exportSelectionCancellationBatch != 0 {
		return nil
	}
	return probe.lockedError()
}

const (
	exportReadSourceBaseChargeBytes    int64 = 512
	exportReadSourceRecordChargeBytes  int64 = 1536
	exportReadSourceTraceChargeBytes   int64 = 256
	exportReadSourceEntryChargeBytes   int64 = 512
	exportReadSourceMessageChargeBytes int64 = 384
)

type frozenBlobPrefix struct {
	session  *sessionState
	segments []blobViewSegment
	size     int64
	checksum [sha256.Size]byte
}

type exportReadSource struct {
	session      *sessionState
	sessionID    string
	records      []exportRecordSource
	traces       []exportTraceSource
	messages     []exportMessageSource
	entries      []TraceEntry
	chargedBytes int64
}

type exportRecordSource struct {
	summary        RecordSummary
	snapshotState  SnapshotState
	traceIndex     int
	protocol       Protocol
	request        RequestSnapshot
	requestBody    frozenBlobPrefix
	responseBody   frozenBlobPrefix
	httpResponse   HTTPResponseSnapshot
	hasHTTP        bool
	wsHandshake    WebSocketHandshakeSnapshot
	hasWSHandshake bool
	wsClose        WebSocketCloseSnapshot
	hasWSClose     bool
	messageOffset  int
	messageCount   int
}

type exportTraceSource struct {
	gatewayTraceID         string
	gatewayRequestID       string
	historyTruncatedBefore bool
	historyTruncatedAfter  bool
	entryOffset            int
	entryCount             int
}

type exportMessageSource struct {
	messageID            string
	sequence             uint64
	relativeMillis       int64
	direction            MessageDirection
	messageType          MessageType
	source               MessageSource
	sourceMessageID      string
	disposition          MessageDisposition
	clientVisible        bool
	failure              FailureObservation
	hasFailure           bool
	observedPayloadBytes int64
	payload              frozenBlobPrefix
}
