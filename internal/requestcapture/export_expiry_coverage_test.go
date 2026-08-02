package requestcapture

import (
	"errors"
	"math"
	"sync"
	"testing"
	"time"
)

type expiryCoverageTimer struct {
	mu          sync.Mutex
	stopResult  bool
	panicOnStop bool
	stops       int
}

func (timer *expiryCoverageTimer) Stop() bool {
	timer.mu.Lock()
	defer timer.mu.Unlock()
	timer.stops++
	if timer.panicOnStop {
		panic("expiry coverage timer stop")
	}
	return timer.stopResult
}

type expiryCoverageScheduler struct {
	mu                  sync.Mutex
	timer               Timer
	invokeSynchronously bool
	panicOnSchedule     bool
	afterSchedule       func()
	callback            func()
	delays              []time.Duration
}

func (scheduler *expiryCoverageScheduler) AfterFunc(delay time.Duration, callback func()) Timer {
	scheduler.mu.Lock()
	scheduler.callback = callback
	scheduler.delays = append(scheduler.delays, delay)
	timer := scheduler.timer
	invoke := scheduler.invokeSynchronously
	panicOnSchedule := scheduler.panicOnSchedule
	afterSchedule := scheduler.afterSchedule
	scheduler.mu.Unlock()

	if panicOnSchedule {
		panic("expiry coverage scheduler")
	}
	if afterSchedule != nil {
		afterSchedule()
	}
	if invoke {
		callback()
	}
	return timer
}

type expiryCoveragePanicClock struct{}

func (expiryCoveragePanicClock) WallNow() time.Time {
	return time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)
}

func (expiryCoveragePanicClock) MonotonicNow() time.Duration {
	panic("expiry coverage clock")
}

type expiryCoverageSequenceClock struct {
	mu      sync.Mutex
	calls   int
	panicAt int
	values  []time.Duration
}

func (clock *expiryCoverageSequenceClock) WallNow() time.Time {
	return time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)
}

func (clock *expiryCoverageSequenceClock) MonotonicNow() time.Duration {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.calls++
	if clock.calls == clock.panicAt {
		panic("expiry coverage sequence clock")
	}
	if len(clock.values) == 0 {
		return 0
	}
	index := min(clock.calls-1, len(clock.values)-1)
	return clock.values[index]
}

func newExpiryCoverageHarness(
	t *testing.T,
) (*Manager, *sessionState, *testClock, *expiryCoverageScheduler) {
	t.Helper()
	clock := &testClock{
		now:       time.Date(2026, 8, 2, 1, 2, 3, 0, time.UTC),
		monotonic: 0,
	}
	scheduler := &expiryCoverageScheduler{
		timer: &expiryCoverageTimer{stopResult: true},
	}
	manager := newTestManager(t, func(cfg *Config) {
		cfg.Clock = clock
		cfg.Scheduler = scheduler
		cfg.DownloadTokenTTL = time.Minute
	})
	startTestSession(t, manager, 8, 1<<20, "expiry-provider")
	return manager, manager.active.Load(), clock, scheduler
}

func insertExpiryCoverageState(
	t *testing.T,
	manager *Manager,
	session *sessionState,
	key string,
	phase exportPhase,
) *exportState {
	t.Helper()
	state := &exportState{
		manager:         manager,
		id:              key,
		registryKey:     key,
		sessionID:       session.id,
		session:         session,
		reservation:     1,
		phase:           phase,
		expiresDeadline: time.Minute,
		done:            make(chan struct{}),
	}
	insertExportStateForTest(t, manager, key, state)
	return state
}

func exportSlotForCoverage(t *testing.T, manager *Manager, key string, state *exportState) int {
	t.Helper()
	manager.exportMu.Lock()
	slot, found := manager.exports.IndexExact(key, state)
	manager.exportMu.Unlock()
	if !found {
		t.Fatalf("export %q has no registry slot", key)
	}
	return slot
}

func TestExportExpirySchedulingCoverage(t *testing.T) {
	t.Run("nil receiver and state", func(t *testing.T) {
		var manager *Manager
		if err := manager.scheduleExportExpiry(nil); !errors.Is(err, ErrInternalFailure) {
			t.Fatalf("scheduleExportExpiry() error = %v", err)
		}
	})

	t.Run("status epoch unavailable", func(t *testing.T) {
		manager, session, _, _ := newExpiryCoverageHarness(t)
		state := &exportState{manager: manager, session: session}
		manager.statusEpochMu.Lock()
		manager.statusEpochWriters = math.MaxUint64
		manager.statusEpochMu.Unlock()
		err := manager.scheduleExportExpiry(state)
		manager.statusEpochMu.Lock()
		manager.statusEpochWriters = 0
		manager.statusEpochMu.Unlock()
		if !errors.Is(err, ErrInternalFailure) {
			t.Fatalf("scheduleExportExpiry() error = %v", err)
		}
	})

	t.Run("missing registry state", func(t *testing.T) {
		manager, session, _, _ := newExpiryCoverageHarness(t)
		state := &exportState{manager: manager, session: session, registryKey: "missing"}
		if err := manager.scheduleExportExpiry(state); !errors.Is(err, ErrDownloadUnavailable) {
			t.Fatalf("scheduleExportExpiry() error = %v", err)
		}
	})

	t.Run("existing expiry owner", func(t *testing.T) {
		manager, session, _, _ := newExpiryCoverageHarness(t)
		state := insertExpiryCoverageState(t, manager, session, "owner", exportPhasePending)
		state.expiryOwner = true
		state.timer = &expiryCoverageTimer{stopResult: true}
		if err := manager.scheduleExportExpiry(state); err != nil {
			t.Fatalf("scheduleExportExpiry() error = %v", err)
		}
	})

	for _, test := range []struct {
		name  string
		phase exportPhase
		epoch uint64
	}{
		{name: "invalid phase", phase: exportPhaseAcquiring},
		{name: "exhausted epoch", phase: exportPhasePending, epoch: math.MaxUint64},
	} {
		t.Run(test.name, func(t *testing.T) {
			manager, session, _, _ := newExpiryCoverageHarness(t)
			state := insertExpiryCoverageState(t, manager, session, "invalid", test.phase)
			state.expiryEpoch = test.epoch
			if err := manager.scheduleExportExpiry(state); !errors.Is(err, ErrInternalFailure) {
				t.Fatalf("scheduleExportExpiry() error = %v", err)
			}
			if state.phase != exportPhaseReleased || !state.canceled.Load() {
				t.Fatalf("invalid state = phase %v canceled %v", state.phase, state.canceled.Load())
			}
		})
	}

	t.Run("successful arm", func(t *testing.T) {
		manager, session, _, scheduler := newExpiryCoverageHarness(t)
		state := insertExpiryCoverageState(t, manager, session, "armed", exportPhasePending)
		if err := manager.scheduleExportExpiry(state); err != nil {
			t.Fatalf("scheduleExportExpiry() error = %v", err)
		}
		if state.phase != exportPhasePending || state.timer == nil ||
			!state.expiryOwner || state.expiryEpoch != 1 {
			t.Fatalf("armed state = %#v", state)
		}
		scheduler.mu.Lock()
		delayCount := len(scheduler.delays)
		scheduler.mu.Unlock()
		if delayCount != 1 {
			t.Fatalf("scheduled delays = %d", delayCount)
		}
	})
}

func TestExportExpiryMaterializationCoverage(t *testing.T) {
	newArm := func(t *testing.T) (*Manager, *exportState, *expiryCoverageScheduler) {
		t.Helper()
		manager, session, _, scheduler := newExpiryCoverageHarness(t)
		state := insertExpiryCoverageState(t, manager, session, "materialize", exportPhaseScheduling)
		state.expiryOwner = true
		state.expiryEpoch = 2
		return manager, state, scheduler
	}

	t.Run("clock failure", func(t *testing.T) {
		manager, state, _ := newArm(t)
		manager.cfg.clock = expiryCoveragePanicClock{}
		err := manager.materializeExportExpiryTimer(state.registryKey, state.reservation, 2, time.Minute)
		if !errors.Is(err, ErrInternalFailure) || !state.canceled.Load() {
			t.Fatalf("materializeExportExpiryTimer() error = %v canceled = %v", err, state.canceled.Load())
		}
	})

	for _, test := range []struct {
		name     string
		panic    bool
		nilTimer bool
	}{
		{name: "nil timer", nilTimer: true},
		{name: "scheduler panic", panic: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			manager, state, scheduler := newArm(t)
			scheduler.panicOnSchedule = test.panic
			if test.nilTimer {
				scheduler.timer = nil
			}
			err := manager.materializeExportExpiryTimer(
				state.registryKey,
				state.reservation,
				state.expiryEpoch,
				time.Minute,
			)
			if !errors.Is(err, ErrInternalFailure) || !state.canceled.Load() {
				t.Fatalf("materializeExportExpiryTimer() error = %v canceled = %v", err, state.canceled.Load())
			}
		})
	}

	t.Run("completion clock failure", func(t *testing.T) {
		manager, state, _ := newArm(t)
		manager.cfg.clock = &expiryCoverageSequenceClock{
			panicAt: 2,
			values:  []time.Duration{0},
		}
		err := manager.materializeExportExpiryTimer(
			state.registryKey,
			state.reservation,
			state.expiryEpoch,
			time.Minute,
		)
		if !errors.Is(err, ErrInternalFailure) || !state.canceled.Load() {
			t.Fatalf("materializeExportExpiryTimer() error = %v canceled = %v", err, state.canceled.Load())
		}
	})

	t.Run("registry changes while scheduler runs", func(t *testing.T) {
		manager, state, scheduler := newArm(t)
		scheduler.afterSchedule = func() {
			manager.exportMu.Lock()
			manager.removeExportLocked(state.registryKey, state)
			manager.exportMu.Unlock()
		}
		err := manager.materializeExportExpiryTimer(
			state.registryKey,
			state.reservation,
			state.expiryEpoch,
			time.Minute,
		)
		if !errors.Is(err, ErrInternalFailure) {
			t.Fatalf("materializeExportExpiryTimer() error = %v", err)
		}
		state.expiryOwner = false
		state.releaseNow("coverage_cleanup")
	})

	t.Run("released before timer publication", func(t *testing.T) {
		manager, state, _ := newArm(t)
		state.phase = exportPhaseReleased
		if err := manager.materializeExportExpiryTimer(
			state.registryKey,
			state.reservation,
			state.expiryEpoch,
			time.Minute,
		); !errors.Is(err, ErrDownloadUnavailable) {
			t.Fatalf("materializeExportExpiryTimer() error = %v", err)
		}
	})

	t.Run("phase changes before timer publication", func(t *testing.T) {
		manager, state, _ := newArm(t)
		state.phase = exportPhasePending
		if err := manager.materializeExportExpiryTimer(
			state.registryKey,
			state.reservation,
			state.expiryEpoch,
			time.Minute,
		); !errors.Is(err, ErrInternalFailure) {
			t.Fatalf("materializeExportExpiryTimer() error = %v", err)
		}
		if !state.canceled.Load() {
			t.Fatal("phase-changed state was not canceled")
		}
	})

	for _, test := range []struct {
		name           string
		deadline       time.Duration
		pendingRelease bool
		want           error
	}{
		{name: "callback fires early", deadline: time.Minute, want: ErrInternalFailure},
		{name: "callback fires at deadline", deadline: 0, want: ErrDownloadUnavailable},
		{
			name:           "callback consumes pending release",
			deadline:       time.Minute,
			pendingRelease: true,
			want:           ErrInternalFailure,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			manager, state, scheduler := newArm(t)
			scheduler.invokeSynchronously = true
			state.releasePending = test.pendingRelease
			state.releaseReason = "pending_coverage_release"
			err := manager.materializeExportExpiryTimer(
				state.registryKey,
				state.reservation,
				state.expiryEpoch,
				test.deadline,
			)
			if !errors.Is(err, test.want) {
				t.Fatalf("materializeExportExpiryTimer() error = %v, want %v", err, test.want)
			}
			if state.expiryOwner || state.phase != exportPhaseReleased {
				t.Fatalf("callback state = phase %v owner %v", state.phase, state.expiryOwner)
			}
		})
	}
}

func TestExportExpiryStateMachineCoverage(t *testing.T) {
	t.Run("nil receiver", func(t *testing.T) {
		var manager *Manager
		manager.expireExport("missing", 1, 1)
		if err := manager.failExportExpiryArm("missing", 1, 1, "coverage"); !errors.Is(err, ErrInternalFailure) {
			t.Fatalf("failExportExpiryArm() error = %v", err)
		}
		manager.finishExpiryOwner(nil, 1)
	})

	t.Run("unknown callback identity", func(t *testing.T) {
		manager, _, _, _ := newExpiryCoverageHarness(t)
		manager.expireExport("missing", 1, 1)
		if err := manager.failExportExpiryArm("missing", 1, 1, "coverage"); !errors.Is(err, ErrInternalFailure) {
			t.Fatalf("failExportExpiryArm() error = %v", err)
		}
	})

	t.Run("clock failure fails the owner arm", func(t *testing.T) {
		manager, session, _, _ := newExpiryCoverageHarness(t)
		state := insertExpiryCoverageState(t, manager, session, "clock-fault", exportPhaseScheduling)
		state.expiryOwner = true
		state.expiryEpoch = 3
		manager.cfg.clock = expiryCoveragePanicClock{}
		manager.expireExport(state.registryKey, state.reservation, state.expiryEpoch)
		if !state.canceled.Load() || state.expiryOwner {
			t.Fatalf("clock-fault state = canceled %v owner %v", state.canceled.Load(), state.expiryOwner)
		}
	})

	for _, test := range []struct {
		name           string
		phase          exportPhase
		deadline       time.Duration
		epoch          uint64
		pendingRelease bool
		wantPhase      exportPhase
		wantOwner      bool
		wantTriggered  bool
	}{
		{
			name: "scheduling callback records arrival", phase: exportPhaseScheduling,
			deadline: time.Minute, epoch: 4, wantPhase: exportPhaseScheduling,
			wantOwner: true, wantTriggered: true,
		},
		{
			name: "released callback consumes pending release", phase: exportPhaseReleased,
			deadline: time.Minute, epoch: 4, pendingRelease: true, wantPhase: exportPhaseReleased,
		},
		{
			name: "pending callback reschedules", phase: exportPhasePending,
			deadline: time.Minute, epoch: 4, wantPhase: exportPhasePending, wantOwner: true,
		},
		{
			name: "pending callback exhausts epoch", phase: exportPhasePending,
			deadline: time.Minute, epoch: math.MaxUint64, wantPhase: exportPhaseReleased,
		},
		{
			name: "exhausted epoch consumes pending release", phase: exportPhasePending,
			deadline: time.Minute, epoch: math.MaxUint64, pendingRelease: true,
			wantPhase: exportPhaseReleased,
		},
		{
			name: "pending callback expires", phase: exportPhasePending,
			deadline: 0, epoch: 4, wantPhase: exportPhaseReleased,
		},
		{
			name: "expired callback consumes pending release", phase: exportPhasePending,
			deadline: 0, epoch: 4, pendingRelease: true, wantPhase: exportPhaseReleased,
		},
		{
			name: "claiming supersedes expiry", phase: exportPhaseClaiming,
			deadline: time.Minute, epoch: 4, wantPhase: exportPhaseClaiming,
		},
		{
			name: "claimed worker consumes pending release", phase: exportPhaseClaimed,
			deadline: time.Minute, epoch: 4, pendingRelease: true, wantPhase: exportPhaseReleased,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			manager, session, _, _ := newExpiryCoverageHarness(t)
			state := insertExpiryCoverageState(t, manager, session, "phase", test.phase)
			state.expiryOwner = true
			state.expiryEpoch = test.epoch
			state.expiresDeadline = test.deadline
			state.releasePending = test.pendingRelease
			state.releaseReason = "phase_pending_release"
			state.timer = &expiryCoverageTimer{stopResult: true}
			manager.expireExport(state.registryKey, state.reservation, state.expiryEpoch)
			if state.phase != test.wantPhase ||
				state.expiryOwner != test.wantOwner ||
				state.expiryTriggered != test.wantTriggered {
				t.Fatalf(
					"expire state = phase %v owner %v triggered %v",
					state.phase,
					state.expiryOwner,
					state.expiryTriggered,
				)
			}
		})
	}

	t.Run("stale finish owner is ignored", func(t *testing.T) {
		manager, session, _, _ := newExpiryCoverageHarness(t)
		state := insertExpiryCoverageState(t, manager, session, "stale-owner", exportPhasePending)
		manager.finishExpiryOwner(state, 99)
		if state.phase != exportPhasePending {
			t.Fatalf("state phase = %v", state.phase)
		}
	})
}

func TestDetachExportAtCoversEveryOwnershipPhase(t *testing.T) {
	tests := []struct {
		name        string
		phase       exportPhase
		expiryOwner bool
		wantFound   bool
		wantRelease bool
	}{
		{name: "acquiring", phase: exportPhaseAcquiring, wantFound: true},
		{name: "pending worker-owned", phase: exportPhasePending, wantFound: true, wantRelease: true},
		{
			name: "pending expiry-owned", phase: exportPhasePending,
			expiryOwner: true, wantFound: true, wantRelease: true,
		},
		{name: "scheduling", phase: exportPhaseScheduling, expiryOwner: true, wantFound: true, wantRelease: true},
		{name: "claiming worker-owned", phase: exportPhaseClaiming, wantFound: true},
		{name: "claiming expiry-owned", phase: exportPhaseClaiming, expiryOwner: true, wantFound: true},
		{name: "claimed", phase: exportPhaseClaimed, wantFound: true},
		{name: "streaming", phase: exportPhaseStreaming, wantFound: true},
		{name: "released", phase: exportPhaseReleased},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager, session, _, _ := newExpiryCoverageHarness(t)
			state := insertExpiryCoverageState(t, manager, session, "detach", test.phase)
			state.expiryOwner = test.expiryOwner
			state.timer = &expiryCoverageTimer{stopResult: true}
			slot := exportSlotForCoverage(t, manager, state.registryKey, state)
			got, _, release, found := manager.detachExportAt(
				slot,
				state.sessionID,
				false,
				ErrNoActiveSession,
			)
			if found != test.wantFound || release != test.wantRelease {
				t.Fatalf("detachExportAt() = found %v release %v", found, release)
			}
			if found && got != state {
				t.Fatal("detachExportAt() returned another state")
			}
		})
	}

	t.Run("session filter does not detach", func(t *testing.T) {
		manager, session, _, _ := newExpiryCoverageHarness(t)
		state := insertExpiryCoverageState(t, manager, session, "filtered", exportPhasePending)
		slot := exportSlotForCoverage(t, manager, state.registryKey, state)
		if _, _, _, found := manager.detachExportAt(
			slot,
			"another-session",
			false,
			ErrNoActiveSession,
		); found {
			t.Fatal("detachExportAt() ignored session filter")
		}
	})
}
