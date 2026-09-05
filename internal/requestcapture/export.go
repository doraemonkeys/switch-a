package requestcapture

import (
	"context"
	"errors"
	"io"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/doraemonkeys/switch-a/internal/requestcapture/downloadcapability"
	"github.com/doraemonkeys/switch-a/internal/requestcapture/exportwire"
	"go.uber.org/zap"
)

const (
	exportFormatVersion      = exportwire.FormatVersion
	exportAcquiringKeyPrefix = "acquiring:"

	exportEventManifest  = "manifest"
	exportEventRecord    = "record"
	exportEventBlobChunk = "blob_chunk"
	exportEventRecordEnd = "record_end"
	exportEventExportEnd = "export_end"

	exportPartBegin         = "begin"
	exportPartMetadataChunk = "metadata_chunk"
	exportPartMetadataEnd   = "metadata_end"
	exportPartData          = "data"
	exportPartEnd           = "end"

	snapshotWhileActive = "snapshot_while_active"
)

var (
	errExportLineTooLarge         = exportwire.ErrLineTooLarge
	errExportReservationExhausted = errors.New("request capture export reservation sequence exhausted")
	errExportReservationInvariant = errors.New("request capture export reservation accounting invariant failed")
	errExportIDCollision          = errors.New("request capture export ID collision")
	errExportContextCanceled      = errors.New("request capture export context canceled")
)

const (
	downloadTokenEntropyBytes = downloadcapability.EntropyBytes
	canonicalExportIDBytes    = downloadcapability.CanonicalExportIDBytes
)

type downloadTokenHash = downloadcapability.Hash

func makeExportID(generated [16]byte) (string, error) {
	return downloadcapability.MakeExportID(generated)
}

func IsCanonicalExportID(value string) bool {
	return downloadcapability.IsCanonicalExportID(value)
}

func IsCanonicalDownloadToken(value string) bool {
	return downloadcapability.IsCanonicalToken(value)
}

func isCanonicalExportID(value string) bool {
	return downloadcapability.IsCanonicalExportID(value)
}

func newDownloadToken(entropy io.Reader) (string, downloadTokenHash, error) {
	return downloadcapability.NewToken(entropy)
}

func hashDownloadToken(rawToken string) downloadTokenHash {
	return downloadcapability.HashToken(rawToken)
}

func downloadTokenMatches(expected downloadTokenHash, rawToken string) bool {
	return downloadcapability.Matches(expected, rawToken)
}

func saturatingDurationAdd(base, delta time.Duration) time.Duration {
	if delta > 0 && base > time.Duration(math.MaxInt64)-delta {
		return time.Duration(math.MaxInt64)
	}
	if delta < 0 && base < time.Duration(math.MinInt64)-delta {
		return time.Duration(math.MinInt64)
	}
	return base + delta
}

func monotonicRemaining(deadline, now time.Duration) time.Duration {
	if now >= deadline {
		return 0
	}
	// A monotonic source is origin-relative and therefore non-negative. Clamping
	// a broken injected clock preserves the fail-closed upper bound.
	if now < 0 {
		now = 0
	}
	return deadline - now
}

func safeExportWallNow(clock Clock) (value time.Time, valid bool) {
	if clock == nil {
		return time.Time{}, false
	}
	defer func() {
		if recover() != nil {
			value = time.Time{}
			valid = false
		}
	}()
	return clock.WallNow(), true
}

func safeExportMonotonicNow(clock Clock) (value time.Duration, valid bool) {
	if clock == nil {
		return 0, false
	}
	defer func() {
		if recover() != nil {
			value = 0
			valid = false
		}
	}()
	return clock.MonotonicNow(), true
}

func safeExportAfterFunc(
	scheduler Scheduler,
	delay time.Duration,
	callback func(),
) (timer Timer, valid bool) {
	if scheduler == nil || callback == nil {
		return nil, false
	}
	defer func() {
		if recover() != nil {
			timer = nil
			valid = false
		}
	}()
	timer = scheduler.AfterFunc(delay, callback)
	return timer, timer != nil
}

func safeExportTimerStop(timer Timer) (stopped bool, valid bool) {
	if timer == nil {
		return false, true
	}
	defer func() {
		if recover() != nil {
			stopped = false
			valid = false
		}
	}()
	return timer.Stop(), true
}

type exportPhase uint8

const (
	exportPhaseAcquiring exportPhase = iota
	exportPhasePending
	exportPhaseScheduling
	exportPhaseClaiming
	exportPhaseClaimed
	exportPhaseStreaming
	exportPhaseRequeueing
	exportPhaseReleased
)

type exportInvariantFault uint8

const (
	exportInvariantNone exportInvariantFault = iota
	exportInvariantAttachProcessAccount
	exportInvariantAttachSessionAccount
	exportInvariantSelectionAccount
	exportInvariantSelectionPin
	exportInvariantSelectionTemporary
	exportInvariantSelectionUnpin
	exportInvariantAcquisitionAccount
	exportInvariantAcquisitionPin
	exportInvariantAcquisitionTemporary
	exportInvariantAcquisitionUnpin
	exportInvariantDownloadSlot
	exportInvariantDownloadAccount
	exportInvariantLeaseAccount
	exportInvariantAttachedLeasePin
	exportInvariantAcquiringLeaseAccount
	exportInvariantSessionOwner
	exportInvariantUnexpectedStreamPhase
)

type exportInvariantFacts struct {
	fault                 exportInvariantFault
	sessionID             string
	exportID              string
	registryKey           string
	phase                 exportPhase
	selectionMaterialized bool
}

type exportLeaseComponentFaults struct {
	account   exportInvariantFault
	pin       exportInvariantFault
	temporary exportInvariantFault
	unpin     exportInvariantFault
}

type exportState struct {
	manager                  *Manager
	id                       string
	registryKey              string
	sessionID                string
	session                  *sessionState
	snapshotAt               time.Time
	expiresAt                time.Time
	expiresDeadline          time.Duration
	recordCount              int
	reservation              uint64
	tokenHash                downloadTokenHash
	snapshot                 *exportSnapshot
	timer                    Timer
	phase                    exportPhase
	expiryEpoch              uint64
	expiryOwner              bool
	expiryTriggered          bool
	releasePending           bool
	releaseReason            string
	workspace                []byte
	lineBytes                int
	temporaryCharge          int64
	leaseCharge              int64
	selectionCharge          int64
	acquisitionCharge        int64
	leaseSessionCharged      bool
	sessionOwnerHeld         bool
	leaseAttached            bool
	downloadReservationOwned bool
	downloadSlotOwned        bool
	downloadEpoch            uint64
	acquisitionErr           error
	selectionMaterialized    atomic.Bool
	canceled                 atomic.Bool
	done                     chan struct{}
	cancellationOnce         sync.Once

	releaseOnce sync.Once
}

func (m *Manager) lookupExportLocked(registryKey string) *exportState {
	state, _ := m.exports.Get(registryKey)
	return state
}

// materializeExportRegistryLocked reserves the process account before allocating
// the arena. exportMu serializes the backing ticket with every slot reservation.
func (m *Manager) materializeExportRegistryLocked() error {
	if m == nil {
		return ErrInternalFailure
	}
	var mutation statusEpochMutation
	if !m.beginStatusEpochMutation(&mutation) {
		return ErrInternalFailure
	}
	defer mutation.finish()
	if m.exports.IsMaterialized() {
		return nil
	}
	if m.exportRegistryCharged {
		if m.exports.Materialize() {
			return nil
		}
		return ErrInternalFailure
	}
	charge := m.exportRegistryCharge
	m.mu.Lock()
	if charge <= 0 || m.processCharged < 0 ||
		charge > m.cfg.processCeilingBytes-m.processCharged {
		m.mu.Unlock()
		return ErrCapacityExceeded
	}
	m.processCharged += charge
	m.processTemporary += charge
	m.mu.Unlock()

	if !m.exports.Materialize() {
		m.mu.Lock()
		m.processTemporary -= charge
		m.processCharged -= charge
		m.mu.Unlock()
		return ErrInternalFailure
	}

	m.mu.Lock()
	m.processTemporary -= charge
	m.mu.Unlock()
	m.exportRegistryCharged = true
	return nil
}

// releaseEmptyExportRegistryLocked severs every state pointer before refunding
// the process-only arena ticket. Tombstones keep Count non-zero until their
// scheduler owner has consumed the final callback.
func (m *Manager) releaseEmptyExportRegistryLocked() bool {
	if m == nil || m.exports.Count() != 0 || m.reservedExportSlots != 0 {
		return false
	}
	var mutation statusEpochMutation
	if !m.beginStatusEpochMutation(&mutation) {
		return false
	}
	defer mutation.finish()
	if !m.exports.Dematerialize() {
		return false
	}
	if !m.exportRegistryCharged {
		return false
	}
	charge := m.exportRegistryCharge
	m.mu.Lock()
	valid := charge > 0 && charge <= m.processCharged
	if valid {
		m.processCharged -= charge
	}
	m.mu.Unlock()
	if valid {
		m.exportRegistryCharged = false
	}
	return valid
}

func (m *Manager) insertExportLocked(registryKey string, state *exportState) bool {
	return m.exports.Put(registryKey, state)
}

func (m *Manager) removeExportLocked(registryKey string, state *exportState) bool {
	removed := m.exports.RemoveExact(registryKey, state)
	if removed {
		m.releaseEmptyExportRegistryLocked()
	}
	return removed
}

func (m *Manager) moveExportLocked(
	oldRegistryKey string,
	newRegistryKey string,
	state *exportState,
) bool {
	return m.exports.Move(oldRegistryKey, newRegistryKey, state)
}

func (m *Manager) pendingExportCountLocked() int {
	count := 0
	for index := 0; index < m.exports.Capacity(); index++ {
		_, state, occupied := m.exports.Entry(index)
		if occupied && state != nil &&
			(state.phase == exportPhaseAcquiring ||
				state.phase == exportPhasePending ||
				state.phase == exportPhaseScheduling ||
				state.phase == exportPhaseClaiming) {
			count++
		}
	}
	return count + m.reservedExportSlots
}

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
