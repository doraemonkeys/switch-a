package requestcapture

import (
	"context"
	"errors"
	"math"
	"testing"
)

type ownershipCoveragePanickingIsError struct{}

func (ownershipCoveragePanickingIsError) Error() string {
	return "panicking Is"
}

func (ownershipCoveragePanickingIsError) Is(error) bool {
	panic("ownership coverage Is")
}

func TestExportAcquisitionAccountingCoverage(t *testing.T) {
	t.Run("reserve and rollback", func(t *testing.T) {
		manager, session, _, _ := newExpiryCoverageHarness(t)
		const charge = int64(64)
		manager.mu.Lock()
		processChargedBefore := manager.processCharged
		processTemporaryBefore := manager.processTemporary
		manager.mu.Unlock()
		session.mu.Lock()
		sessionChargedBefore := session.chargedBytes
		sessionTemporaryBefore := session.temporaryBytes
		session.mu.Unlock()
		if err := manager.reserveExportAcquisitionAccounting(session, charge); err != nil {
			t.Fatalf("reserveExportAcquisitionAccounting() error = %v", err)
		}
		if err := manager.rollbackExportAcquisitionAccounting(session, charge); err != nil {
			t.Fatalf("rollbackExportAcquisitionAccounting() error = %v", err)
		}
		if manager.processCharged != processChargedBefore ||
			manager.processTemporary != processTemporaryBefore ||
			session.chargedBytes != sessionChargedBefore ||
			session.temporaryBytes != sessionTemporaryBefore {
			t.Fatalf(
				"accounting changed: process=(%d,%d) session=(%d,%d)",
				manager.processCharged,
				manager.processTemporary,
				session.chargedBytes,
				session.temporaryBytes,
			)
		}
	})

	t.Run("rollback releasing account", func(t *testing.T) {
		manager, session, _, _ := newExpiryCoverageHarness(t)
		const charge = int64(32)
		if err := manager.reserveExportAcquisitionAccounting(session, charge); err != nil {
			t.Fatalf("reserveExportAcquisitionAccounting() error = %v", err)
		}
		session.mu.Lock()
		session.releasing = true
		session.mu.Unlock()
		manager.mu.Lock()
		manager.processReleasing = charge
		manager.mu.Unlock()
		if err := manager.rollbackExportAcquisitionAccounting(session, charge); err != nil {
			t.Fatalf("rollbackExportAcquisitionAccounting() error = %v", err)
		}
		session.mu.Lock()
		session.releasing = false
		session.mu.Unlock()
	})

	t.Run("invalid reservations", func(t *testing.T) {
		manager, session, _, _ := newExpiryCoverageHarness(t)
		if err := manager.reserveExportAcquisitionAccounting(session, 0); !errors.Is(
			err,
			errExportReservationInvariant,
		) {
			t.Fatalf("zero reserve error = %v", err)
		}
		if err := manager.rollbackExportAcquisitionAccounting(session, 1); !errors.Is(
			err,
			errExportReservationInvariant,
		) {
			t.Fatalf("unowned rollback error = %v", err)
		}
	})

	t.Run("inactive and over capacity", func(t *testing.T) {
		manager, session, _, _ := newExpiryCoverageHarness(t)
		session.mu.Lock()
		session.accepting = false
		session.mu.Unlock()
		if err := manager.reserveExportAcquisitionAccounting(session, 1); !errors.Is(
			err,
			ErrNoActiveSession,
		) {
			t.Fatalf("inactive reserve error = %v", err)
		}
		session.mu.Lock()
		session.accepting = true
		session.mu.Unlock()
		if err := manager.reserveExportAcquisitionAccounting(
			session,
			session.quotaBytes+1,
		); !errors.Is(err, ErrCapacityExceeded) {
			t.Fatalf("capacity reserve error = %v", err)
		}
	})

	t.Run("reserved slot release is total", func(t *testing.T) {
		manager, _, _, _ := newExpiryCoverageHarness(t)
		manager.exportMu.Lock()
		manager.reservedExportSlots = 1
		manager.exportMu.Unlock()
		manager.releaseReservedExportSlot()
		manager.releaseReservedExportSlot()
		if manager.reservedExportSlots != 0 {
			t.Fatalf("reserved slots = %d", manager.reservedExportSlots)
		}
	})
}

func preparePublishAcquisitionCoverage(
	t *testing.T,
	manager *Manager,
) {
	t.Helper()
	manager.exportMu.Lock()
	err := manager.materializeExportRegistryLocked()
	manager.reservedExportSlots = 1
	manager.exportMu.Unlock()
	if err != nil {
		t.Fatalf("materializeExportRegistryLocked() error = %v", err)
	}
}

func TestPublishExportAcquisitionCoverage(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		manager, session, _, _ := newExpiryCoverageHarness(t)
		preparePublishAcquisitionCoverage(t, manager)
		state := &exportState{manager: manager, session: session, registryKey: "publish-success"}
		if err := manager.publishExportAcquisition(session, state); err != nil {
			t.Fatalf("publishExportAcquisition() error = %v", err)
		}
		manager.exportMu.Lock()
		manager.removeExportLocked(state.registryKey, state)
		manager.exportMu.Unlock()
	})

	t.Run("missing reserved slot", func(t *testing.T) {
		manager, session, _, _ := newExpiryCoverageHarness(t)
		manager.exportMu.Lock()
		if err := manager.materializeExportRegistryLocked(); err != nil {
			manager.exportMu.Unlock()
			t.Fatalf("materializeExportRegistryLocked() error = %v", err)
		}
		manager.exportMu.Unlock()
		state := &exportState{manager: manager, session: session, registryKey: "no-slot"}
		if err := manager.publishExportAcquisition(session, state); !errors.Is(err, ErrInternalFailure) {
			t.Fatalf("publishExportAcquisition() error = %v", err)
		}
	})

	t.Run("closed manager", func(t *testing.T) {
		manager, session, _, _ := newExpiryCoverageHarness(t)
		preparePublishAcquisitionCoverage(t, manager)
		manager.lifecycleMu.Lock()
		manager.closed = true
		manager.lifecycleMu.Unlock()
		state := &exportState{manager: manager, session: session, registryKey: "closed"}
		err := manager.publishExportAcquisition(session, state)
		manager.lifecycleMu.Lock()
		manager.closed = false
		manager.lifecycleMu.Unlock()
		if !errors.Is(err, ErrManagerClosed) {
			t.Fatalf("publishExportAcquisition() error = %v", err)
		}
	})

	t.Run("inactive generation", func(t *testing.T) {
		manager, session, _, _ := newExpiryCoverageHarness(t)
		preparePublishAcquisitionCoverage(t, manager)
		manager.active.Store(nil)
		state := &exportState{manager: manager, session: session, registryKey: "inactive"}
		err := manager.publishExportAcquisition(session, state)
		manager.active.Store(session)
		if !errors.Is(err, ErrNoActiveSession) {
			t.Fatalf("publishExportAcquisition() error = %v", err)
		}
	})

	t.Run("duplicate key", func(t *testing.T) {
		manager, session, _, _ := newExpiryCoverageHarness(t)
		existing := insertExpiryCoverageState(
			t,
			manager,
			session,
			"duplicate",
			exportPhasePending,
		)
		manager.exportMu.Lock()
		manager.reservedExportSlots = 1
		manager.exportMu.Unlock()
		state := &exportState{manager: manager, session: session, registryKey: existing.registryKey}
		if err := manager.publishExportAcquisition(session, state); !errors.Is(
			err,
			ErrInternalFailure,
		) {
			t.Fatalf("publishExportAcquisition() error = %v", err)
		}
	})
}

func setAcquisitionCoverageAccount(
	manager *Manager,
	session *sessionState,
	charge int64,
) {
	manager.mu.Lock()
	manager.processCharged += charge
	manager.processTemporary += charge
	manager.mu.Unlock()
	session.mu.Lock()
	session.chargedBytes += charge
	session.temporaryBytes += charge
	session.mu.Unlock()
}

func TestExportLeaseAttachmentAndComponentReleaseCoverage(t *testing.T) {
	t.Run("nil and canceled states", func(t *testing.T) {
		var state *exportState
		if _, err := state.attachLeaseLocked(); !errors.Is(err, ErrInternalFailure) {
			t.Fatalf("nil attach error = %v", err)
		}
		manager, session, _, _ := newExpiryCoverageHarness(t)
		state = &exportState{manager: manager, session: session}
		state.canceled.Store(true)
		state.acquisitionErr = ErrManagerClosed
		if _, err := state.attachLeaseLocked(); !errors.Is(err, ErrManagerClosed) {
			t.Fatalf("canceled attach error = %v", err)
		}
	})

	t.Run("invalid process and session accounts", func(t *testing.T) {
		manager, session, _, _ := newExpiryCoverageHarness(t)
		state := &exportState{
			manager:             manager,
			session:             session,
			leaseCharge:         8,
			leaseSessionCharged: true,
		}
		if fault, err := state.attachLeaseLocked(); fault != exportInvariantAttachProcessAccount ||
			!errors.Is(err, ErrInternalFailure) {
			t.Fatalf("process attach = fault %v error %v", fault, err)
		}
		manager.mu.Lock()
		processTemporaryBefore := manager.processTemporary
		manager.processTemporary += 8
		manager.mu.Unlock()
		if fault, err := state.attachLeaseLocked(); fault != exportInvariantAttachSessionAccount ||
			!errors.Is(err, ErrInternalFailure) {
			t.Fatalf("session attach = fault %v error %v", fault, err)
		}
		manager.mu.Lock()
		manager.processTemporary = processTemporaryBefore
		manager.mu.Unlock()
	})

	t.Run("attach and release selection components", func(t *testing.T) {
		manager, session, _, _ := newExpiryCoverageHarness(t)
		const charge = int64(10)
		manager.mu.Lock()
		processChargedBefore := manager.processCharged
		manager.mu.Unlock()
		setAcquisitionCoverageAccount(manager, session, charge)
		state := &exportState{
			manager:             manager,
			session:             session,
			leaseCharge:         charge,
			selectionCharge:     4,
			acquisitionCharge:   6,
			leaseSessionCharged: true,
		}
		if err := state.releaseSelectionAccounting(); err != nil {
			t.Fatalf("releaseSelectionAccounting() error = %v", err)
		}
		if err := state.releaseAcquisitionAccounting(); err != nil {
			t.Fatalf("releaseAcquisitionAccounting() error = %v", err)
		}
		if state.leaseCharge != 0 || manager.processCharged != processChargedBefore {
			t.Fatalf("component accounting remains: lease=%d process=%d", state.leaseCharge, manager.processCharged)
		}
	})

	t.Run("attached lease unpins components", func(t *testing.T) {
		manager, session, _, _ := newExpiryCoverageHarness(t)
		const charge = int64(7)
		setAcquisitionCoverageAccount(manager, session, charge)
		state := &exportState{
			manager:             manager,
			session:             session,
			leaseCharge:         charge,
			selectionCharge:     charge,
			leaseSessionCharged: true,
		}
		session.mu.Lock()
		fault, err := state.attachLeaseLocked()
		session.mu.Unlock()
		if err != nil || fault != exportInvariantNone {
			t.Fatalf("attachLeaseLocked() = fault %v error %v", fault, err)
		}
		session.mu.Lock()
		fault = state.releaseSelectionAccountingLocked()
		session.mu.Unlock()
		if fault != exportInvariantNone {
			t.Fatalf("releaseSelectionAccountingLocked() fault = %v", fault)
		}
		if manager.processPinned != 0 || state.leaseCharge != 0 {
			t.Fatalf("attached accounting remains: pinned=%d lease=%d", manager.processPinned, state.leaseCharge)
		}
	})

	t.Run("invalid component fails closed", func(t *testing.T) {
		manager, session, _, _ := newExpiryCoverageHarness(t)
		state := &exportState{
			manager:             manager,
			session:             session,
			leaseCharge:         1,
			selectionCharge:     -1,
			leaseSessionCharged: true,
		}
		if err := state.releaseSelectionAccounting(); !errors.Is(err, ErrInternalFailure) {
			t.Fatalf("releaseSelectionAccounting() error = %v", err)
		}
	})
}

func TestExportStreamClassificationAndInvariantCoverage(t *testing.T) {
	faults := []exportInvariantFault{
		exportInvariantAttachProcessAccount,
		exportInvariantAttachSessionAccount,
		exportInvariantSelectionAccount,
		exportInvariantSelectionPin,
		exportInvariantSelectionTemporary,
		exportInvariantSelectionUnpin,
		exportInvariantAcquisitionAccount,
		exportInvariantAcquisitionPin,
		exportInvariantAcquisitionTemporary,
		exportInvariantAcquisitionUnpin,
		exportInvariantDownloadSlot,
		exportInvariantDownloadAccount,
		exportInvariantLeaseAccount,
		exportInvariantAttachedLeasePin,
		exportInvariantAcquiringLeaseAccount,
		exportInvariantSessionOwner,
		exportInvariantUnexpectedStreamPhase,
		exportInvariantNone,
	}
	for _, fault := range faults {
		operation, reason := exportInvariantDescription(fault)
		if operation == "" || reason == "" {
			t.Fatalf("fault %v has empty description", fault)
		}
	}

	if reason := classifyExportStreamRelease(false, ownershipCoveragePanickingIsError{}); reason != "stream_failed" {
		t.Fatalf("panicking error reason = %q", reason)
	}
	if reason := classifyExportStreamRelease(false, context.DeadlineExceeded); reason != "request_canceled" {
		t.Fatalf("deadline reason = %q", reason)
	}
	if size := messagePayloadBlobIDSourceBytes(exportMessageSource{messageID: "id"}); size <= len(messageBlobPrefix) {
		t.Fatalf("named payload ID size = %d", size)
	}
	if size := messagePayloadBlobIDSourceBytes(exportMessageSource{sequence: 12345}); size != len(messageBlobPrefix)+5 {
		t.Fatalf("numeric payload ID size = %d", size)
	}

	var snapshot *exportSnapshot
	snapshot.release()
	var source *exportReadSource
	source.release()
	releaseBlobViewLocked(nil)
}

func TestFrozenBlobRollbackCoverage(t *testing.T) {
	manager, session, _, _ := newExpiryCoverageHarness(t)

	t.Run("nil materialization and canceled probe", func(t *testing.T) {
		var source *frozenBlobPrefix
		if _, err := source.materialize(newExportCancellationProbe(context.Background(), nil)); err != nil {
			t.Fatalf("nil materialize error = %v", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := source.materialize(newExportCancellationProbe(ctx, nil)); err == nil {
			t.Fatal("canceled materialize succeeded")
		}
	})

	t.Run("rollback refunds pin reservation", func(t *testing.T) {
		manager.mu.Lock()
		manager.pinAccountLocked(5)
		manager.mu.Unlock()
		source := frozenBlobPrefix{session: session}
		if _, err := rollbackFrozenBlobPrefixLocked(
			session,
			&source,
			5,
			0,
			"coverage",
		); !errors.Is(err, ErrInternalFailure) {
			t.Fatalf("rollbackFrozenBlobPrefixLocked() error = %v", err)
		}
	})

	t.Run("refund underflow", func(t *testing.T) {
		if err := refundFrozenBlobPins(session, 1); !errors.Is(err, ErrInternalFailure) {
			t.Fatalf("refundFrozenBlobPins() error = %v", err)
		}
	})

	t.Run("invalid and overflowing chunk graphs", func(t *testing.T) {
		invalid := &blob{session: session, chunkCount: 1}
		if _, err := freezeBlobPrefixLocked(invalid); !errors.Is(err, ErrInternalFailure) {
			t.Fatalf("invalid freeze error = %v", err)
		}

		first := &blobChunk{session: session, charge: math.MaxInt64}
		first.refs.Store(1)
		second := &blobChunk{session: session, charge: 1}
		second.refs.Store(1)
		first.next = second
		overflow := &blob{
			session:    session,
			first:      first,
			last:       second,
			chunkCount: 2,
		}
		if _, err := freezeBlobPrefixLocked(overflow); !errors.Is(err, ErrCapacityExceeded) {
			t.Fatalf("overflow freeze error = %v", err)
		}
	})
}
