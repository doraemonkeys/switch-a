package requestcapture

import (
	"context"
	"errors"
	"math"
	"time"

	"go.uber.org/zap"
)

type downloadAttemptSettlement struct {
	temporaryCharge int64
	armExpiry       bool
	expiryEpoch     uint64
	deadline        time.Duration
	retryable       bool
	releaseReason   string
}

func (state *exportState) finishDownload(
	epoch uint64,
	expectedPhase exportPhase,
	streamErr error,
) error {
	manager := state.manager
	if manager == nil {
		return ErrInternalFailure
	}
	now, validClock := safeExportMonotonicNow(manager.cfg.clock)
	var mutation statusEpochMutation
	mutating := manager.beginStatusEpochMutation(&mutation)
	manager.exportMu.Lock()
	registered := manager.lookupExportLocked(state.registryKey) == state
	normalCompletion := registered &&
		state.phase == expectedPhase &&
		state.downloadEpoch == epoch &&
		state.downloadSlotOwned
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
	if stoppedAndDetached {
		manager.exportMu.Unlock()
		if mutating {
			mutation.finish()
		}
		state.release(classifyExportStreamRelease(true, streamErr))
		return nil
	}

	settlement := downloadAttemptSettlement{
		temporaryCharge: state.temporaryCharge,
		releaseReason:   classifyExportStreamRelease(state.canceled.Load(), streamErr),
	}
	if settlement.temporaryCharge <= 0 || manager.activeDownloads <= 0 {
		if registered && !state.expiryOwner {
			manager.removeExportLocked(state.registryKey, state)
		}
		state.cancelLocked(ErrInternalFailure)
		state.phase = exportPhaseReleased
		state.tokenHash = downloadTokenHash{}
		facts := state.invariantFactsLocked(exportInvariantDownloadAccount)
		manager.exportMu.Unlock()
		if mutating {
			mutation.finish()
		}
		manager.logExportInvariant(facts)
		state.release("download_settlement_invariant_failed")
		return ErrInternalFailure
	}

	// The attempt owns only its fixed streaming workspace. The immutable snapshot
	// and capability remain attached so browser-to-download-manager handoff, retry,
	// and a second deliberate click do not destroy the export.
	state.phase = exportPhaseRequeueing
	state.workspace = nil
	state.lineBytes = 0
	state.temporaryCharge = 0
	state.downloadEpoch = 0
	state.downloadSlotOwned = false
	manager.activeDownloads--
	manager.exportMu.Unlock()

	accountingErr := manager.rollbackExportAcquisitionAccounting(
		state.session,
		settlement.temporaryCharge,
	)
	manager.exportMu.Lock()
	state.completeDownloadAttemptSettlementLocked(
		manager,
		&settlement,
		accountingErr,
		now,
		validClock,
	)
	registryKey := state.registryKey
	reservation := state.reservation
	manager.exportMu.Unlock()
	if mutating {
		mutation.finish()
	}

	if settlement.retryable {
		if settlement.armExpiry {
			if err := manager.materializeExportExpiryTimer(
				registryKey,
				reservation,
				settlement.expiryEpoch,
				settlement.deadline,
			); err != nil {
				return ErrInternalFailure
			}
		}
		manager.cfg.logger.Info("request capture export download attempt finished",
			zap.String("session_id", state.sessionID),
			zap.String("export_id", state.id),
			zap.String("result", stableDownloadAttemptResult(streamErr)),
			zap.Bool("capability_reusable", true),
		)
		return nil
	}
	state.release(settlement.releaseReason)
	if accountingErr != nil {
		manager.logExportReservationFault("download_workspace_release")
		return ErrInternalFailure
	}
	return nil
}

func (state *exportState) completeDownloadAttemptSettlementLocked(
	manager *Manager,
	settlement *downloadAttemptSettlement,
	accountingErr error,
	now time.Duration,
	validClock bool,
) {
	if accountingErr == nil &&
		state.downloadAttemptCanRetryLocked(manager, now, validClock) &&
		state.expiryEpoch != math.MaxUint64 {
		settlement.retryable = true
		state.requeueDownloadAttemptLocked(settlement)
		return
	}
	state.terminateDownloadAttemptLocked(manager, settlement, accountingErr)
}

func (state *exportState) downloadAttemptCanRetryLocked(
	manager *Manager,
	now time.Duration,
	validClock bool,
) bool {
	return manager.lookupExportLocked(state.registryKey) == state &&
		state.phase == exportPhaseRequeueing &&
		!state.canceled.Load() &&
		validClock &&
		monotonicRemaining(state.expiresDeadline, now) > 0 &&
		manager.active.Load() == state.session
}

func (state *exportState) requeueDownloadAttemptLocked(
	settlement *downloadAttemptSettlement,
) {
	if state.expiryOwner {
		// A callback that raced Timer.Stop still owns the original deadline and
		// will publish or expire the pending state.
		state.phase = exportPhasePending
		return
	}
	state.phase = exportPhaseScheduling
	state.expiryEpoch++
	state.expiryOwner = true
	state.expiryTriggered = false
	settlement.armExpiry = true
	settlement.expiryEpoch = state.expiryEpoch
	settlement.deadline = state.expiresDeadline
}

func (state *exportState) terminateDownloadAttemptLocked(
	manager *Manager,
	settlement *downloadAttemptSettlement,
	accountingErr error,
) {
	if accountingErr != nil {
		// rollbackExportAcquisitionAccounting is transactional; restoring the
		// owned amount lets terminal release account for it exactly once.
		state.temporaryCharge = settlement.temporaryCharge
		state.cancelLocked(ErrInternalFailure)
		settlement.releaseReason = "download_workspace_rollback_failed"
	} else {
		state.cancelLocked(nil)
	}
	state.phase = exportPhaseReleased
	state.tokenHash = downloadTokenHash{}
	if manager.lookupExportLocked(state.registryKey) == state && !state.expiryOwner {
		manager.removeExportLocked(state.registryKey, state)
	}
}

func stableDownloadAttemptResult(err error) string {
	switch {
	case err == nil:
		return "complete"
	case errors.Is(err, errDownloadAttemptAbandoned):
		return "abandoned"
	case errors.Is(err, context.Canceled):
		return "request_canceled"
	default:
		return "stream_error"
	}
}
