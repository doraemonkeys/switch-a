package requestcapture

import (
	"math"
	"time"
)

func (m *Manager) finalizeExportCreation(
	session *sessionState,
	state *exportState,
) *exportCreationFailure {
	if err := state.releaseAcquisitionAccounting(); err != nil {
		return &exportCreationFailure{cause: err, reason: "acquisition_accounting_failed"}
	}
	if err := m.completeExportAcquisition(session, state); err != nil {
		return &exportCreationFailure{cause: err, reason: "acquisition_canceled"}
	}
	if err := m.scheduleExportExpiry(state); err != nil {
		return &exportCreationFailure{cause: ErrInternalFailure, reason: "expiry_schedule_failed"}
	}
	return nil
}

func (m *Manager) completeExportAcquisition(
	session *sessionState,
	state *exportState,
) error {
	m.exportMu.Lock()
	var err error
	switch {
	case m.lookupExportLocked(state.registryKey) != state:
		err = ErrNoActiveSession
	case state.phase != exportPhaseAcquiring || state.canceled.Load():
		err = exportStateAcquisitionError(state)
		if err == nil {
			err = ErrNoActiveSession
		}
	case m.active.Load() != session:
		err = ErrNoActiveSession
	}
	if err != nil {
		if m.lookupExportLocked(state.registryKey) == state {
			m.removeExportLocked(state.registryKey, state)
		}
		state.cancelLocked(err)
		state.phase = exportPhaseReleased
	} else {
		state.phase = exportPhasePending
	}
	m.exportMu.Unlock()
	return err
}

func (m *Manager) exportLifecycleError() error {
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	if m.closed {
		return ErrManagerClosed
	}
	return ErrNoActiveSession
}

func (m *Manager) scheduleExportExpiry(state *exportState) error {
	if m == nil || state == nil {
		return ErrInternalFailure
	}
	var mutation statusEpochMutation
	if !m.beginStatusEpochMutation(&mutation) {
		return ErrInternalFailure
	}
	m.exportMu.Lock()
	if m.lookupExportLocked(state.registryKey) != state {
		m.exportMu.Unlock()
		mutation.finish()
		return ErrDownloadUnavailable
	}
	if state.expiryOwner {
		// A stopped-but-queued callback still owns the current arm and will either
		// expire or re-arm it. A second arm would escape the lease account.
		m.exportMu.Unlock()
		mutation.finish()
		return nil
	}
	if state.phase != exportPhasePending || state.expiryEpoch == math.MaxUint64 {
		m.removeExportLocked(state.registryKey, state)
		state.cancelLocked(ErrInternalFailure)
		state.phase = exportPhaseReleased
		state.tokenHash = downloadTokenHash{}
		m.exportMu.Unlock()
		mutation.finish()
		state.release("expiry_schedule_invariant")
		return ErrInternalFailure
	}
	state.phase = exportPhaseScheduling
	state.expiryEpoch++
	state.expiryOwner = true
	state.expiryTriggered = false
	registryKey := state.registryKey
	reservation := state.reservation
	expiryEpoch := state.expiryEpoch
	deadline := state.expiresDeadline
	m.exportMu.Unlock()
	mutation.finish()

	return m.materializeExportExpiryTimer(
		registryKey,
		reservation,
		expiryEpoch,
		deadline,
	)
}

func (m *Manager) materializeExportExpiryTimer(
	registryKey string,
	reservation uint64,
	expiryEpoch uint64,
	deadline time.Duration,
) error {
	now, validClock := safeExportMonotonicNow(m.cfg.clock)
	if !validClock {
		return m.failExportExpiryArm(registryKey, reservation, expiryEpoch, "clock_failed")
	}
	delay := monotonicRemaining(deadline, now)
	timer, validTimer := safeExportAfterFunc(m.cfg.scheduler, delay, func() {
		m.runExportExpiryCallback(registryKey, reservation, expiryEpoch)
	})
	if !validTimer {
		return m.failExportExpiryArm(registryKey, reservation, expiryEpoch, "scheduler_failed")
	}
	completionNow, validCompletionClock := safeExportMonotonicNow(m.cfg.clock)
	if !validCompletionClock {
		_, _ = safeExportTimerStop(timer)
		return m.failExportExpiryArm(registryKey, reservation, expiryEpoch, "clock_failed")
	}

	var mutation statusEpochMutation
	mutating := m.beginStatusEpochMutation(&mutation)
	m.exportMu.Lock()
	state := m.lookupExportLocked(registryKey)
	if state == nil ||
		state.reservation != reservation ||
		!state.expiryOwner ||
		state.expiryEpoch != expiryEpoch {
		m.exportMu.Unlock()
		if mutating {
			mutation.finish()
		}
		_, _ = safeExportTimerStop(timer)
		return ErrInternalFailure
	}
	if state.phase == exportPhaseReleased {
		triggered := state.expiryTriggered
		m.exportMu.Unlock()
		if mutating {
			mutation.finish()
		}
		stopped, validStop := safeExportTimerStop(timer)
		if triggered || stopped || !validStop {
			m.finishExpiryOwner(state, expiryEpoch)
		}
		return ErrDownloadUnavailable
	}
	if state.phase != exportPhaseScheduling {
		m.exportMu.Unlock()
		if mutating {
			mutation.finish()
		}
		_, _ = safeExportTimerStop(timer)
		return m.failExportExpiryArm(registryKey, reservation, expiryEpoch, "phase_changed")
	}
	if state.expiryTriggered {
		completion := m.resolveTriggeredExportExpiryLocked(
			registryKey,
			state,
			deadline,
			completionNow,
		)
		m.exportMu.Unlock()
		if mutating {
			mutation.finish()
		}
		_, _ = safeExportTimerStop(timer)
		return completion.finish(state)
	}
	state.timer = timer
	state.phase = exportPhasePending
	m.exportMu.Unlock()
	if mutating {
		mutation.finish()
	}
	return nil
}

type triggeredExportExpiryCompletion struct {
	expired        bool
	pendingRelease bool
	releaseReason  string
}

func (m *Manager) resolveTriggeredExportExpiryLocked(
	registryKey string,
	state *exportState,
	deadline time.Duration,
	now time.Duration,
) triggeredExportExpiryCompletion {
	m.removeExportLocked(registryKey, state)
	state.phase = exportPhaseReleased
	state.tokenHash = downloadTokenHash{}
	state.expiryOwner = false
	state.expiryTriggered = false

	completion := triggeredExportExpiryCompletion{
		expired: monotonicRemaining(deadline, now) == 0,
	}
	if completion.expired {
		state.cancelLocked(nil)
	} else {
		state.cancelLocked(ErrInternalFailure)
	}
	completion.pendingRelease, completion.releaseReason = state.takePendingReleaseLocked()
	return completion
}

func (completion triggeredExportExpiryCompletion) finish(state *exportState) error {
	switch {
	case completion.pendingRelease:
		state.releaseNow(completion.releaseReason)
	case completion.expired:
		state.release("expired")
	default:
		state.release("expiry_scheduler_fired_early")
	}
	if completion.expired {
		return ErrDownloadUnavailable
	}
	return ErrInternalFailure
}

func (m *Manager) runExportExpiryCallback(
	registryKey string,
	reservation uint64,
	expiryEpoch uint64,
) {
	defer func() {
		if recover() != nil {
			_ = m.failExportExpiryArm(
				registryKey,
				reservation,
				expiryEpoch,
				"callback_panicked",
			)
		}
	}()
	m.expireExport(registryKey, reservation, expiryEpoch)
}

func (m *Manager) failExportExpiryArm(
	registryKey string,
	reservation uint64,
	expiryEpoch uint64,
	reason string,
) error {
	if m == nil {
		return ErrInternalFailure
	}
	var mutation statusEpochMutation
	mutating := m.beginStatusEpochMutation(&mutation)
	m.exportMu.Lock()
	state := m.lookupExportLocked(registryKey)
	if state == nil || state.reservation != reservation ||
		!state.expiryOwner || state.expiryEpoch != expiryEpoch {
		m.exportMu.Unlock()
		if mutating {
			mutation.finish()
		}
		return ErrInternalFailure
	}
	m.removeExportLocked(registryKey, state)
	state.cancelLocked(ErrInternalFailure)
	state.phase = exportPhaseReleased
	state.tokenHash = downloadTokenHash{}
	state.expiryOwner = false
	state.expiryTriggered = false
	pending, pendingReason := state.takePendingReleaseLocked()
	m.exportMu.Unlock()
	if mutating {
		mutation.finish()
	}
	if pending {
		state.releaseNow(pendingReason)
	} else {
		state.release(reason)
	}
	return ErrInternalFailure
}

func (state *exportState) takePendingReleaseLocked() (bool, string) {
	if state == nil || !state.releasePending {
		return false, ""
	}
	reason := state.releaseReason
	state.releasePending = false
	state.releaseReason = ""
	return true, reason
}

func (m *Manager) finishExpiryOwner(state *exportState, expiryEpoch uint64) {
	if m == nil || state == nil {
		return
	}
	var mutation statusEpochMutation
	mutating := m.beginStatusEpochMutation(&mutation)
	m.exportMu.Lock()
	if !state.expiryOwner || state.expiryEpoch != expiryEpoch {
		m.exportMu.Unlock()
		if mutating {
			mutation.finish()
		}
		return
	}
	state.expiryOwner = false
	state.expiryTriggered = false
	state.timer = nil
	if state.phase == exportPhaseReleased && m.lookupExportLocked(state.registryKey) == state {
		m.removeExportLocked(state.registryKey, state)
	}
	pending, reason := state.takePendingReleaseLocked()
	m.exportMu.Unlock()
	if mutating {
		mutation.finish()
	}
	if pending {
		state.releaseNow(reason)
	}
}

func (m *Manager) stopExportExpiryTimer(
	state *exportState,
	timer Timer,
	expiryEpoch uint64,
) {
	if timer == nil {
		return
	}
	stopped, valid := safeExportTimerStop(timer)
	if stopped || !valid {
		m.finishExpiryOwner(state, expiryEpoch)
	}
}

type exportExpiryReschedule struct {
	exhausted      bool
	pendingRelease bool
	releaseReason  string
	nextEpoch      uint64
	deadline       time.Duration
}

func (m *Manager) prepareExportExpiryRescheduleLocked(
	registryKey string,
	state *exportState,
) exportExpiryReschedule {
	if state.expiryEpoch == math.MaxUint64 {
		m.removeExportLocked(registryKey, state)
		state.phase = exportPhaseReleased
		state.expiryOwner = false
		state.tokenHash = downloadTokenHash{}
		state.cancelLocked(ErrInternalFailure)
		pending, reason := state.takePendingReleaseLocked()
		return exportExpiryReschedule{
			exhausted:      true,
			pendingRelease: pending,
			releaseReason:  reason,
		}
	}
	state.phase = exportPhaseScheduling
	state.expiryEpoch++
	state.expiryTriggered = false
	return exportExpiryReschedule{
		nextEpoch: state.expiryEpoch,
		deadline:  state.expiresDeadline,
	}
}

func (m *Manager) finishExportExpiryReschedule(
	registryKey string,
	reservation uint64,
	state *exportState,
	reschedule exportExpiryReschedule,
) {
	if !reschedule.exhausted {
		_ = m.materializeExportExpiryTimer(
			registryKey,
			reservation,
			reschedule.nextEpoch,
			reschedule.deadline,
		)
		return
	}
	if reschedule.pendingRelease {
		state.releaseNow(reschedule.releaseReason)
		return
	}
	state.release("expiry_epoch_exhausted")
}

func (m *Manager) expireExport(
	registryKey string,
	reservation uint64,
	expiryEpoch uint64,
) {
	if m == nil {
		return
	}
	now, validClock := safeExportMonotonicNow(m.cfg.clock)
	if !validClock {
		_ = m.failExportExpiryArm(registryKey, reservation, expiryEpoch, "clock_failed")
		return
	}
	var mutation statusEpochMutation
	mutating := m.beginStatusEpochMutation(&mutation)
	finishMutation := func() {
		if mutating {
			mutation.finish()
			mutating = false
		}
	}
	m.exportMu.Lock()
	state := m.lookupExportLocked(registryKey)
	if state == nil ||
		state.reservation != reservation ||
		!state.expiryOwner ||
		state.expiryEpoch != expiryEpoch {
		m.exportMu.Unlock()
		finishMutation()
		return
	}

	switch state.phase {
	case exportPhaseScheduling:
		// AfterFunc may invoke synchronously. The materializer owns cleanup until
		// the Timer value is published, so the callback records only its arrival.
		state.expiryTriggered = true
		m.exportMu.Unlock()
		finishMutation()
		return
	case exportPhaseReleased:
		state.expiryOwner = false
		state.expiryTriggered = false
		state.timer = nil
		m.removeExportLocked(registryKey, state)
		pending, reason := state.takePendingReleaseLocked()
		m.exportMu.Unlock()
		finishMutation()
		if pending {
			state.releaseNow(reason)
		}
		return
	case exportPhasePending:
		state.timer = nil
		if monotonicRemaining(state.expiresDeadline, now) > 0 {
			reschedule := m.prepareExportExpiryRescheduleLocked(registryKey, state)
			m.exportMu.Unlock()
			finishMutation()
			m.finishExportExpiryReschedule(registryKey, reservation, state, reschedule)
			return
		}
		m.removeExportLocked(registryKey, state)
		state.phase = exportPhaseReleased
		state.expiryOwner = false
		state.expiryTriggered = false
		state.tokenHash = downloadTokenHash{}
		pending, reason := state.takePendingReleaseLocked()
		m.exportMu.Unlock()
		finishMutation()
		if pending {
			state.releaseNow(reason)
		} else {
			state.release("expired")
		}
		return
	default:
		// Claiming or handle-owned phases supersede expiry. Their worker/capability
		// remains the sole physical release owner.
		state.expiryOwner = false
		state.expiryTriggered = false
		state.timer = nil
		pending, reason := state.takePendingReleaseLocked()
		m.exportMu.Unlock()
		finishMutation()
		if pending {
			state.releaseNow(reason)
		}
	}
}

func (m *Manager) cancelSessionExports(sessionID string) {
	m.cancelExports(sessionID, false, ErrNoActiveSession, "session_stopped")
}

func (m *Manager) cancelAllExports() {
	m.cancelExports("", true, ErrManagerClosed, "manager_closed")
}

// cancelExports detaches one export per lock acquisition. Stop/Close therefore
// require no scratch slices and never execute timer callbacks or release logic
// while the registry is locked.
func (m *Manager) cancelExports(
	sessionID string,
	all bool,
	acquisitionErr error,
	reason string,
) {
	if m == nil {
		return
	}
	for index := 0; index < m.exports.Capacity(); index++ {
		var mutation statusEpochMutation
		mutating := m.beginStatusEpochMutation(&mutation)
		state, timer, release, found := m.detachExportAt(
			index,
			sessionID,
			all,
			acquisitionErr,
		)
		if mutating {
			mutation.finish()
		}
		if found && release {
			state.release(reason)
		}
		if !found {
			continue
		}
		m.stopExportExpiryTimer(state, timer, state.expiryEpoch)
	}
}

func (m *Manager) detachExportAt(
	index int,
	sessionID string,
	all bool,
	acquisitionErr error,
) (*exportState, Timer, bool, bool) {
	m.exportMu.Lock()
	defer m.exportMu.Unlock()
	registryKey, state, occupied := m.exports.Entry(index)
	if !occupied || state == nil || (!all && state.sessionID != sessionID) {
		return nil, nil, false, false
	}
	switch state.phase {
	case exportPhaseAcquiring:
		m.removeExportLocked(registryKey, state)
		state.cancelLocked(acquisitionErr)
		state.phase = exportPhaseReleased
		state.tokenHash = downloadTokenHash{}
		// The acquisition worker owns all not-yet-frozen references.
		return state, nil, false, true
	case exportPhasePending:
		state.cancelLocked(acquisitionErr)
		state.phase = exportPhaseReleased
		state.tokenHash = downloadTokenHash{}
		timer := state.timer
		state.timer = nil
		if !state.expiryOwner {
			m.removeExportLocked(registryKey, state)
		}
		return state, timer, true, true
	case exportPhaseScheduling:
		// The scheduler-owner token keeps this tombstone and its account live
		// until AfterFunc returns or the queued callback consumes the token.
		state.cancelLocked(acquisitionErr)
		state.phase = exportPhaseReleased
		state.tokenHash = downloadTokenHash{}
		return state, nil, true, true
	case exportPhaseClaiming:
		if !state.expiryOwner {
			m.removeExportLocked(registryKey, state)
		}
		state.cancelLocked(acquisitionErr)
		state.phase = exportPhaseReleased
		state.tokenHash = downloadTokenHash{}
		timer := state.timer
		state.timer = nil
		// The claim worker consumes its scalar reservation and clears its
		// handle/workspace before releasing the export.
		return state, timer, false, true
	case exportPhaseClaimed:
		// The numeric Download capability can recover the graph only through this
		// manager-owned slot. Stop cancels it but leaves release to Close/WriteTo.
		state.cancelLocked(acquisitionErr)
		state.tokenHash = downloadTokenHash{}
		return state, nil, false, true
	case exportPhaseStreaming:
		// The stream stack is the physical release owner; keeping the registry slot
		// lets Close signal cancellation without receiving a graph pointer.
		state.cancelLocked(acquisitionErr)
		state.tokenHash = downloadTokenHash{}
		return state, nil, false, true
	case exportPhaseReleased:
		// Expiry-owner tombstones consume physical admission capacity until their
		// queued callback returns the scheduler token.
		return nil, nil, false, false
	}
	return nil, nil, false, false
}

// releaseOwnedAccounting is called only after the state graph and snapshot have
// been severed. It acquires session.mu before the terminal account lock.
