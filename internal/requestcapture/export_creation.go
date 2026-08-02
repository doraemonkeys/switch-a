package requestcapture

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

	"go.uber.org/zap"
)

type exportCreationMaterial struct {
	rawToken        string
	tokenHash       downloadTokenHash
	exportID        string
	expiresAt       time.Time
	expiresDeadline time.Duration
}

type exportCreationFailure struct {
	cause  error
	reason string
}

func (m *Manager) prepareExportCreation(
	ctx context.Context,
	probe exportCancellationProbe,
	state *exportState,
) (exportCreationMaterial, *exportCreationFailure) {
	// Generators run before the snapshot graph is pinned so a blocking
	// dependency cannot strand session-owned capture data.
	rawToken, tokenHash, err := newDownloadToken(m.cfg.entropy)
	if err != nil {
		return exportCreationMaterial{}, &exportCreationFailure{
			cause:  ErrInternalFailure,
			reason: "token_generation_failed",
		}
	}
	if err := probe.lockedError(); err != nil {
		return exportCreationMaterial{}, &exportCreationFailure{
			cause:  resolveExportCancellation(ctx, err),
			reason: "acquisition_canceled",
		}
	}
	generatedExportID, err := m.cfg.idGenerator.NewID()
	if err != nil {
		m.cfg.logger.Error("request capture export ID generation failed",
			zap.String("session_id", state.sessionID),
			zap.String("reason", "dependency_failed"),
		)
		return exportCreationMaterial{}, &exportCreationFailure{
			cause:  ErrInternalFailure,
			reason: "id_generation_failed",
		}
	}
	exportID, err := makeExportID(generatedExportID)
	if err != nil {
		return exportCreationMaterial{}, &exportCreationFailure{
			cause:  ErrInternalFailure,
			reason: "id_generation_failed",
		}
	}
	if err := probe.lockedError(); err != nil {
		return exportCreationMaterial{}, &exportCreationFailure{
			cause:  resolveExportCancellation(ctx, err),
			reason: "acquisition_canceled",
		}
	}
	issuedAt, validWallClock := safeExportWallNow(m.cfg.clock)
	monotonicNow, validMonotonicClock := safeExportMonotonicNow(m.cfg.clock)
	if !validWallClock || !validMonotonicClock {
		return exportCreationMaterial{}, &exportCreationFailure{
			cause:  ErrInternalFailure,
			reason: "clock_failed",
		}
	}
	expiresAt := issuedAt.UTC().Add(m.cfg.downloadTokenTTL)
	return exportCreationMaterial{
		rawToken:        rawToken,
		tokenHash:       tokenHash,
		exportID:        exportID,
		expiresAt:       expiresAt,
		expiresDeadline: saturatingDurationAdd(monotonicNow, m.cfg.downloadTokenTTL),
	}, nil
}

func (m *Manager) freezeExportCreationSnapshot(
	ctx context.Context,
	probe exportCancellationProbe,
	session *sessionState,
	state *exportState,
	selection *exportSelection,
) (*exportSnapshot, bool, *exportCreationFailure) {
	snapshotAt, validClock := safeExportWallNow(m.cfg.clock)
	if !validClock {
		return nil, false, &exportCreationFailure{cause: ErrInternalFailure, reason: "clock_failed"}
	}
	state.snapshotAt = snapshotAt.UTC()
	if err := lockExportFreezeState(probe, session, state); err != nil {
		return nil, false, &exportCreationFailure{
			cause:  resolveExportCancellation(ctx, err),
			reason: "session_lock_canceled",
		}
	}
	if err := probe.lockedError(); err != nil {
		session.mu.Unlock()
		session.gate.RUnlock()
		return nil, false, &exportCreationFailure{
			cause:  resolveExportCancellation(ctx, err),
			reason: "acquisition_canceled",
		}
	}
	if !session.accepting || m.active.Load() != session {
		session.mu.Unlock()
		session.gate.RUnlock()
		return nil, false, &exportCreationFailure{
			cause:  m.exportLifecycleError(),
			reason: "session_unavailable",
		}
	}
	attachFault, attachErr := state.attachLeaseLocked()
	if attachErr != nil {
		session.mu.Unlock()
		session.gate.RUnlock()
		if attachFault != exportInvariantNone {
			state.failInvariant(attachFault)
		}
		return nil, false, &exportCreationFailure{cause: attachErr, reason: "lease_attach_failed"}
	}

	source, freezeErr := freezeExportReadSourceLocked(probe, session, selection, state)
	selection.clear()
	selectionFault := state.releaseSelectionAccountingLocked()
	if selectionFault != exportInvariantNone && source != nil {
		source.releaseLocked()
		source = nil
	}
	session.mu.Unlock()
	session.gate.RUnlock()
	if selectionFault != exportInvariantNone {
		state.failInvariant(selectionFault)
		return nil, true, &exportCreationFailure{
			cause:  ErrInternalFailure,
			reason: "selection_accounting_failed",
		}
	}
	if freezeErr != nil {
		return nil, true, &exportCreationFailure{
			cause:  resolveExportCancellation(ctx, freezeErr),
			reason: "snapshot_freeze_failed",
		}
	}

	snapshot, err := acquireExportSnapshot(probe, source)
	if err != nil {
		return nil, true, &exportCreationFailure{
			cause:  resolveExportCancellation(ctx, err),
			reason: "snapshot_failed",
		}
	}
	if err := probe.lockedError(); err != nil {
		snapshot.release()
		return nil, true, &exportCreationFailure{
			cause:  resolveExportCancellation(ctx, err),
			reason: "acquisition_canceled",
		}
	}
	return snapshot, true, nil
}

func (m *Manager) publishExportSnapshotLocked(
	session *sessionState,
	state *exportState,
	material exportCreationMaterial,
	snapshot *exportSnapshot,
	recordCount int,
	acquisitionErr error,
) error {
	if acquisitionErr == nil {
		switch {
		case m.closed:
			acquisitionErr = ErrManagerClosed
		case m.active.Load() != session:
			acquisitionErr = ErrNoActiveSession
		case m.lookupExportLocked(state.registryKey) != state:
			acquisitionErr = ErrNoActiveSession
		case state.phase != exportPhaseAcquiring || state.canceled.Load():
			acquisitionErr = exportStateAcquisitionError(state)
			if acquisitionErr == nil {
				acquisitionErr = ErrNoActiveSession
			}
		case m.lookupExportLocked(material.exportID) != nil:
			acquisitionErr = errExportIDCollision
		}
	}
	if acquisitionErr != nil {
		m.removeExportLocked(state.registryKey, state)
		state.cancelLocked(acquisitionErr)
		state.phase = exportPhaseReleased
		return acquisitionErr
	}
	if !m.moveExportLocked(state.registryKey, material.exportID, state) {
		m.removeExportLocked(state.registryKey, state)
		state.cancelLocked(ErrInternalFailure)
		state.phase = exportPhaseReleased
		return errExportReservationInvariant
	}

	state.id = material.exportID
	state.registryKey = material.exportID
	state.tokenHash = material.tokenHash
	state.snapshot = snapshot
	state.recordCount = recordCount
	state.expiresAt = material.expiresAt
	state.expiresDeadline = material.expiresDeadline
	// The acquiring worker remains the sole release owner until its registry
	// component is severed and refunded.
	return nil
}

func (m *Manager) createExport(
	ctx context.Context,
	session *sessionState,
	request ExportRequest,
) (ExportTicket, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	probe := newExportCancellationProbe(ctx, nil)
	if err := probe.lockedError(); err != nil {
		return ExportTicket{}, resolveExportCancellation(ctx, err)
	}
	selectionRequest, err := inspectExportSelectionRequest(probe, request)
	if err != nil {
		return ExportTicket{}, resolveExportCancellation(ctx, err)
	}

	state, err := m.beginExportAcquisition(session, selectionRequest.selectionCharge)
	if err != nil {
		return ExportTicket{}, err
	}
	probe = probe.withState(state)
	var selection *exportSelection
	acquisitionGuardActive := true
	releaseSelection := func() error {
		if selection != nil {
			selection.clear()
			selection = nil
		}
		return state.releaseSelectionAccounting()
	}
	defer func() {
		if !acquisitionGuardActive {
			return
		}
		_ = releaseSelection()
		_ = m.abortExportAcquisition(
			state,
			ErrInternalFailure,
			"acquisition_unwound",
		)
	}()
	abortSelection := func(cause error, reason string) error {
		if releaseErr := releaseSelection(); releaseErr != nil {
			cause = releaseErr
			reason = "selection_accounting_failed"
		}
		result := m.abortExportAcquisition(state, cause, reason)
		acquisitionGuardActive = false
		return result
	}

	material, failure := m.prepareExportCreation(ctx, probe, state)
	if failure != nil {
		return ExportTicket{}, abortSelection(failure.cause, failure.reason)
	}

	selection, err = materializeExportSelection(probe, selectionRequest)
	if err != nil {
		return ExportTicket{}, abortSelection(err, "selection_failed")
	}
	state.selectionMaterialized.Store(true)
	var admission exportAdmissionGuard
	if err := acquireExportAdmission(probe, session, state, &admission); err != nil {
		return ExportTicket{}, abortSelection(
			resolveExportCancellation(ctx, err),
			"admission_canceled",
		)
	}
	defer admission.release()

	snapshot, selectionConsumed, failure := m.freezeExportCreationSnapshot(
		ctx,
		probe,
		session,
		state,
		selection,
	)
	if selectionConsumed {
		selection = nil
	}
	if failure != nil {
		return ExportTicket{}, abortSelection(failure.cause, failure.reason)
	}

	recordCount := len(snapshot.records)
	ticket := ExportTicket{
		ExportID:      strings.Clone(material.exportID),
		SessionID:     strings.Clone(state.sessionID),
		RecordCount:   recordCount,
		ExpiresAt:     material.expiresAt,
		DownloadToken: material.rawToken,
	}
	acquisitionErr := resolveExportCancellation(ctx, probe.lockedError())
	var publicationMutation statusEpochMutation
	if !m.beginStatusEpochMutation(&publicationMutation) {
		snapshot.release()
		return ExportTicket{}, abortSelection(
			ErrInternalFailure,
			"status_transaction_failed",
		)
	}

	m.lifecycleMu.Lock()
	m.exportMu.Lock()
	acquisitionErr = m.publishExportSnapshotLocked(
		session,
		state,
		material,
		snapshot,
		recordCount,
		acquisitionErr,
	)
	m.exportMu.Unlock()
	m.lifecycleMu.Unlock()
	if acquisitionErr != nil {
		snapshot.release()
		publicationMutation.finish()
		cleanupErr := abortSelection(acquisitionErr, "acquisition_canceled")
		if errors.Is(acquisitionErr, errExportIDCollision) {
			m.cfg.logger.Error("request capture export ID collision",
				zap.String("session_id", ticket.SessionID),
				zap.String("export_id", material.exportID),
			)
			return ExportTicket{}, ErrInternalFailure
		}
		return ExportTicket{}, cleanupErr
	}
	publicationMutation.finish()

	if failure := m.finalizeExportCreation(session, state); failure != nil {
		return ExportTicket{}, abortSelection(failure.cause, failure.reason)
	}
	acquisitionGuardActive = false
	m.cfg.logger.Info("request capture export snapshot created",
		zap.String("session_id", ticket.SessionID),
		zap.String("export_id", material.exportID),
		zap.Int("record_count", recordCount),
		zap.Time("expires_at", material.expiresAt),
	)
	return ticket, nil
}

func exportLeaseCharge(sessionID string, selectionCharge int64) (int64, bool) {
	baseCharge := exportBaseChargeBytes + tokenChargeBytes + mapEntryChargeBytes +
		int64(canonicalExportIDBytes+len(sessionID))
	if selectionCharge < 0 || baseCharge > math.MaxInt64-selectionCharge {
		return 0, false
	}
	return baseCharge + selectionCharge, true
}

type exportCancellationProbe struct {
	done         <-chan struct{}
	state        *exportState
	contextFault bool
}

func newExportCancellationProbe(
	ctx context.Context,
	state *exportState,
) (probe exportCancellationProbe) {
	probe.state = state
	if ctx == nil {
		return probe
	}
	// Done is executable caller code. Capture it exactly once before any export
	// reservation and translate a panic into the same stable cancellation sentinel.
	defer func() {
		if recover() != nil {
			probe.done = nil
			probe.contextFault = true
		}
	}()
	probe.done = ctx.Done()
	return probe
}

func (probe exportCancellationProbe) withState(state *exportState) exportCancellationProbe {
	probe.state = state
	return probe
}

func (probe exportCancellationProbe) lockedError() error {
	if probe.contextFault {
		return errExportContextCanceled
	}
	if probe.state != nil && probe.state.canceled.Load() {
		if probe.state.acquisitionErr != nil {
			return probe.state.acquisitionErr
		}
		return ErrNoActiveSession
	}
	if probe.done != nil {
		select {
		case <-probe.done:
			return errExportContextCanceled
		default:
		}
	}
	return nil
}

func normalizedContextError(ctx context.Context) (result error) {
	if ctx == nil {
		return nil
	}
	result = context.Canceled
	defer func() {
		if recover() != nil {
			result = context.Canceled
		}
	}()
	err := ctx.Err()
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return context.Canceled
}

// A dependency-controlled error may implement a panicking Is method; matching
// must preserve cancellation cleanup instead of unwinding the export worker.
func safeExportErrorIs(err, target error) (matches bool) {
	defer func() { _ = recover() }()
	return errors.Is(err, target)
}

func resolveExportCancellation(
	ctx context.Context,
	err error,
) error {
	if safeExportErrorIs(err, errExportContextCanceled) {
		if normalized := normalizedContextError(ctx); normalized != nil {
			return normalized
		}
		return context.Canceled
	}
	return err
}

func exportStateAcquisitionError(state *exportState) error {
	if state == nil || !state.canceled.Load() {
		return nil
	}
	if state.acquisitionErr != nil {
		return state.acquisitionErr
	}
	return ErrNoActiveSession
}

func (m *Manager) abortExportAcquisition(
	state *exportState,
	cause error,
	reason string,
) error {
	var mutation statusEpochMutation
	mutating := m.beginStatusEpochMutation(&mutation)
	m.exportMu.Lock()
	if state.phase == exportPhaseAcquiring {
		if m.lookupExportLocked(state.registryKey) == state {
			m.removeExportLocked(state.registryKey, state)
		}
		state.cancelLocked(cause)
		state.phase = exportPhaseReleased
		state.tokenHash = downloadTokenHash{}
	}
	result := state.acquisitionErr
	m.exportMu.Unlock()
	if mutating {
		mutation.finish()
	}
	state.release(reason)
	if result != nil {
		return result
	}
	return cause
}

const (
	// A selected-record export is an adversarial hashing surface distinct from
	// retention. Scope "all" remains available for larger configured sessions,
	// while this hard ceiling keeps targeted requests bounded independently of
	// caller-controlled configuration.
	maximumExportSelectedRecordIDs = maxRetainedProviders * DefaultMaxRecordsPerProvider
	maximumExportSelectionIDBytes  = 2 << 20

	exportSelectionCancellationBatch = 256
	exportSelectionBaseChargeBytes   = int64(128)
	exportSelectionEntryChargeBytes  = mapEntryChargeBytes + sliceEntryChargeBytes
)

type exportSelectionRequest struct {
	scope           ExportScope
	recordIDs       []string
	identifierBytes int64
	selectionCharge int64
}

type exportSelection struct {
	scope     ExportScope
	recordIDs []string
	records   map[string]*recordState
}

func inspectExportSelectionRequest(
	probe exportCancellationProbe,
	request ExportRequest,
) (exportSelectionRequest, error) {
	switch request.Scope {
	case ExportScopeAll:
		if len(request.RecordIDs) != 0 {
			return exportSelectionRequest{}, &ValidationError{
				Field:  "record_ids",
				Reason: "must be omitted when scope is all",
			}
		}
		return exportSelectionRequest{scope: ExportScopeAll}, nil
	case ExportScopeRecords:
		if len(request.RecordIDs) == 0 {
			return exportSelectionRequest{}, &ValidationError{
				Field:  "record_ids",
				Reason: "must contain at least one record when scope is records",
			}
		}
	default:
		return exportSelectionRequest{}, &ValidationError{
			Field:  "scope",
			Reason: "must be all or records",
		}
	}

	if len(request.RecordIDs) > maximumExportSelectedRecordIDs {
		return exportSelectionRequest{}, &ValidationError{
			Field:  "record_ids",
			Reason: "contains too many selected records",
		}
	}

	identifierBytes := 0
	for index, recordID := range request.RecordIDs {
		if err := exportSelectionCancellationError(probe, index); err != nil {
			return exportSelectionRequest{}, err
		}
		if len(recordID) > maximumExportSelectionIDBytes-identifierBytes {
			return exportSelectionRequest{}, &ValidationError{
				Field:  "record_ids",
				Reason: "exceeds the selected-record identifier byte limit",
			}
		}
		identifierBytes += len(recordID)
		if _, canonical := parseRecordID(recordID); !canonical {
			return exportSelectionRequest{}, &ValidationError{
				Field:  "record_ids",
				Reason: "must contain canonical record IDs",
			}
		}
	}
	if err := exportSelectionCancellationError(probe, len(request.RecordIDs)); err != nil {
		return exportSelectionRequest{}, err
	}

	entryCount := int64(len(request.RecordIDs))
	if entryCount > (math.MaxInt64-exportSelectionBaseChargeBytes)/exportSelectionEntryChargeBytes {
		return exportSelectionRequest{}, ErrCapacityExceeded
	}
	selectionCharge := exportSelectionBaseChargeBytes + entryCount*exportSelectionEntryChargeBytes
	if int64(identifierBytes) > math.MaxInt64-selectionCharge {
		return exportSelectionRequest{}, ErrCapacityExceeded
	}
	return exportSelectionRequest{
		scope:           ExportScopeRecords,
		recordIDs:       request.RecordIDs,
		identifierBytes: int64(identifierBytes),
		selectionCharge: selectionCharge + int64(identifierBytes),
	}, nil
}

func materializeExportSelection(
	probe exportCancellationProbe,
	request exportSelectionRequest,
) (*exportSelection, error) {
	selection := &exportSelection{scope: request.scope}
	if request.scope == ExportScopeAll {
		return selection, nil
	}

	// Admission owns the full map, slice, and string charge before these
	// allocations. Tight clones prevent a bounded substring from retaining an
	// arbitrarily large caller-owned backing allocation.
	selection.recordIDs = make([]string, len(request.recordIDs))
	selection.records = make(map[string]*recordState, len(request.recordIDs))
	materialized := false
	defer func() {
		if !materialized {
			selection.clear()
		}
	}()

	remainingIdentifierBytes := request.identifierBytes
	for index, borrowedRecordID := range request.recordIDs {
		if err := exportSelectionCancellationError(probe, index); err != nil {
			return nil, err
		}
		if int64(len(borrowedRecordID)) > remainingIdentifierBytes {
			return nil, ErrInternalFailure
		}
		remainingIdentifierBytes -= int64(len(borrowedRecordID))
		recordID := strings.Clone(borrowedRecordID)
		if _, duplicate := selection.records[recordID]; duplicate {
			return nil, &ValidationError{
				Field:  "record_ids",
				Reason: "must contain unique values",
			}
		}
		selection.recordIDs[index] = recordID
		selection.records[recordID] = nil
	}
	if remainingIdentifierBytes != 0 {
		return nil, ErrInternalFailure
	}
	if err := exportSelectionCancellationError(probe, len(request.recordIDs)); err != nil {
		return nil, err
	}
	materialized = true
	return selection, nil
}
