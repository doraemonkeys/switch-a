package pending

import "sync"

type timerSignalKind uint8

const (
	timerProbe timerSignalKind = iota + 1
	timerIdle
)

type timerSignal struct {
	kind       timerSignalKind
	generation uint64
}

const timerSignalSlotCount = int(timerIdle)

type queuedTimerSignal struct {
	signal timerSignal
	order  uint64
}

// Stopped timer callbacks may already be in flight. Keeping one replaceable
// slot per kind ensures those stale generations cannot crowd a live expiration
// out of the coordinator's bounded delivery state.
type timerMailbox struct {
	mu        sync.Mutex
	ready     chan struct{}
	nextOrder uint64
	pending   [timerSignalSlotCount]queuedTimerSignal
}

func newTimerMailbox() *timerMailbox {
	return &timerMailbox{ready: make(chan struct{}, 1)}
}

func (m *timerMailbox) enqueue(signal timerSignal, completed <-chan struct{}) {
	select {
	case <-completed:
		return
	default:
	}

	index := int(signal.kind - timerProbe)
	m.mu.Lock()
	queued := m.pending[index]
	updated := queued.signal.kind == 0 || signal.generation > queued.signal.generation
	if updated {
		m.nextOrder++
		m.pending[index] = queuedTimerSignal{signal: signal, order: m.nextOrder}
	}
	m.mu.Unlock()
	if !updated {
		return
	}

	select {
	case m.ready <- struct{}{}:
	case <-completed:
	default:
	}
}

func (m *timerMailbox) drain() ([timerSignalSlotCount]timerSignal, int) {
	m.mu.Lock()
	pending := m.pending
	m.pending = [timerSignalSlotCount]queuedTimerSignal{}
	m.mu.Unlock()

	var signals [timerSignalSlotCount]timerSignal
	count := 0
	for _, queued := range pending {
		if queued.signal.kind == 0 {
			continue
		}
		signals[count] = queued.signal
		count++
	}
	if count == 2 && pending[0].order > pending[1].order {
		signals[0], signals[1] = signals[1], signals[0]
	}
	return signals, count
}

func (c *coordinator[T]) armProbeTimer() {
	c.stopProbeTimer()
	c.probeGeneration++
	generation := c.probeGeneration
	c.probeTimer = c.config.Scheduler.AfterFunc(c.config.ProbeDuration, func() {
		c.enqueueTimer(timerSignal{kind: timerProbe, generation: generation})
	})
}

func (c *coordinator[T]) stopProbeTimer() {
	if c.probeTimer != nil {
		c.probeTimer.Stop()
		c.probeTimer = nil
	}
	c.probeGeneration++
}

func (c *coordinator[T]) armIdleTimer() uint64 {
	c.stopIdleTimer()
	if c.config.IdleDuration == 0 {
		return 0
	}
	c.idleGeneration++
	generation := c.idleGeneration
	c.idleTimer = c.config.Scheduler.AfterFunc(c.config.IdleDuration, func() {
		c.enqueueTimer(timerSignal{kind: timerIdle, generation: generation})
	})
	return generation
}

func (c *coordinator[T]) finishRead(generation uint64) {
	if generation == 0 || generation != c.idleGeneration {
		return
	}
	c.stopIdleTimer()
}

func (c *coordinator[T]) stopIdleTimer() {
	if c.idleTimer != nil {
		c.idleTimer.Stop()
		c.idleTimer = nil
	}
	c.idleGeneration++
}

func (c *coordinator[T]) enqueueTimer(signal timerSignal) {
	c.timerSignals.enqueue(signal, c.shared.completion.ready)
}

func (c *coordinator[T]) handlePendingTimerSignals() {
	signals, count := c.timerSignals.drain()
	for index := 0; index < count; index++ {
		c.handleTimerSignal(signals[index])
	}
}

func (c *coordinator[T]) handleTimerSignal(signal timerSignal) {
	switch signal.kind {
	case timerProbe:
		if signal.generation != c.probeGeneration || c.state != StateProbing {
			return
		}
		_ = c.commitForwarding(ReasonProbeDurationElapsed, nil, nil)
	case timerIdle:
		if signal.generation != c.idleGeneration || c.state == StateDiscarded {
			return
		}
		c.stopIdleTimer()
		c.readTermination = ReadTerminationIdleTimeout
		if c.state == StateProbing {
			c.termination = TerminationUpstreamReadFailure
			_ = c.commitForwarding(ReasonUpstreamReadFailure, nil, nil)
		} else if c.termination == "" {
			c.termination = TerminationUpstreamReadFailure
		}
		c.closeBody()
	}
}
