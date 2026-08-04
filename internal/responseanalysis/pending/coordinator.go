package pending

import (
	"context"
	"errors"
	"fmt"
)

const (
	maxPumpReadBufferBytes      = 32 * 1024
	maxObservationQueueCapacity = 4
	maxCommandQueueCapacity     = 1
)

type coordinator[T any] struct {
	config  Config[T]
	input   StartInput[T]
	shared  *shared[T]
	account *requestAccount

	events         chan pumpEvent[T]
	usage          chan T
	startAck       chan readStartAck
	rawAck         chan pumpDirective
	observationAck chan pumpDirective
	timerSignals   *timerMailbox

	state           ResolutionState
	boundaryReason  BoundaryReason
	analysisFailure BoundaryReason
	prefix          rawPrefix

	pumpStarted bool
	pumpJoined  chan struct{}
	pumpPaused  bool

	probeTimer      Timer
	probeGeneration uint64
	idleTimer       Timer
	idleGeneration  uint64

	headersCommitted bool
	bodyClosed       bool
	bodyCloseErr     error
	upstreamBytes    int64
	decodedBytes     int64
	clientBytes      int64
	termination      Termination
	readTermination  ReadTermination
	reachedEOF       bool

	semantic    T
	hasSemantic bool
	usageValue  T
	hasUsage    bool

	discardReply chan discardResult
	discardCause TransitionCause
	finalized    bool
}

func ValidateConfig[T any](config Config[T]) error {
	switch {
	case config.ProcessBudget == nil:
		return fmt.Errorf("%w: process budget is required", ErrInvalidConfig)
	case config.Scheduler == nil:
		return fmt.Errorf("%w: scheduler is required", ErrInvalidConfig)
	case config.ProbeDuration <= 0:
		return fmt.Errorf("%w: probe duration must be positive", ErrInvalidConfig)
	case config.IdleDuration < 0:
		return fmt.Errorf("%w: idle duration cannot be negative", ErrInvalidConfig)
	case config.RequestMemoryLimit <= 0:
		return fmt.Errorf("%w: request memory limit must be positive", ErrInvalidConfig)
	case config.RequestMemoryLimit > maxRequestMemoryLimit:
		return fmt.Errorf("%w: request memory limit cannot exceed %d bytes", ErrInvalidConfig, maxRequestMemoryLimit)
	case config.DecodedBufferBytes <= 0 || config.DecodedBufferBytes > maxPumpReadBufferBytes:
		return fmt.Errorf("%w: decoded buffer size must be between 1 and %d bytes", ErrInvalidConfig, maxPumpReadBufferBytes)
	case config.ObservationQueueCapacity <= 0 || config.ObservationQueueCapacity > maxObservationQueueCapacity:
		return fmt.Errorf("%w: observation queue capacity must be between 1 and %d", ErrInvalidConfig, maxObservationQueueCapacity)
	case config.CommandQueueCapacity <= 0 || config.CommandQueueCapacity > maxCommandQueueCapacity:
		return fmt.Errorf("%w: command queue capacity must be between 1 and %d", ErrInvalidConfig, maxCommandQueueCapacity)
	case config.Observations.Inspect == nil,
		config.Observations.FailureReason == nil,
		config.Observations.Clone == nil,
		config.Observations.Release == nil:
		return fmt.Errorf("%w: observation operations are required", ErrInvalidConfig)
	case config.FailureReason == nil:
		return fmt.Errorf("%w: failure classifier is required", ErrInvalidConfig)
	default:
		return nil
	}
}

func Start[T any](ctx context.Context, config Config[T], input StartInput[T]) *Response[T] {
	// Clamp before validation because even the invalid-start result needs a
	// command cell; an invalid capacity must never become an allocation request.
	commandCapacity := min(max(config.CommandQueueCapacity, 1), maxCommandQueueCapacity)
	clone := config.Observations.Clone
	if clone == nil {
		clone = func(value T) T { return value }
	}
	shared := newShared(commandCapacity, clone)
	response := &Response[T]{shared: shared}

	input.Header = cloneHeader(input.Header)
	if err := ValidateConfig(config); err != nil || validateStartInput(ctx, input) != nil {
		publishInvalidStart(shared, input)
		return response
	}
	account, err := newRequestAccount(config.ProcessBudget, config.RequestMemoryLimit)
	if err != nil {
		publishInvalidStart(shared, input)
		return response
	}

	c := &coordinator[T]{
		config:         config,
		input:          input,
		shared:         shared,
		account:        account,
		events:         make(chan pumpEvent[T]),
		usage:          make(chan T, config.ObservationQueueCapacity),
		startAck:       make(chan readStartAck),
		rawAck:         make(chan pumpDirective),
		observationAck: make(chan pumpDirective),
		timerSignals:   newTimerMailbox(),
		state:          StateProbing,
	}
	go c.run(ctx)
	return response
}

func validateStartInput[T any](ctx context.Context, input StartInput[T]) error {
	if ctx == nil {
		return errors.New("context is required")
	}
	if err := input.Mode.validate(); err != nil {
		return err
	}
	if input.StatusCode < 100 || input.StatusCode > 999 {
		return fmt.Errorf("status code %d is invalid", input.StatusCode)
	}
	if input.Body == nil {
		return errors.New("upstream body is required")
	}
	if input.Writer == nil {
		return errors.New("client writer is required")
	}
	if input.Mode.kind != modeHold && input.InitialFailure == "" && input.NewDriver == nil {
		return errors.New("analysis driver is required")
	}
	return nil
}

func publishInvalidStart[T any](shared *shared[T], input StartInput[T]) {
	bodyClosed := false
	if input.Body != nil {
		_ = input.Body.Close()
		bodyClosed = true
	}
	shared.boundary.publish(Boundary[T]{State: StateDiscarded, Reason: ReasonAnalysisInternal})
	shared.semantic.publish(SemanticMilestone[T]{Completed: true, State: StateDiscarded})
	shared.completion.publish(Completion[T]{
		State:           StateDiscarded,
		StatusCode:      input.StatusCode,
		Header:          cloneHeader(input.Header),
		BodyClosed:      bodyClosed,
		Termination:     TerminationInternalFailure,
		BoundaryReason:  ReasonAnalysisInternal,
		AnalysisFailure: ReasonAnalysisInternal,
	})
}

func (c *coordinator[T]) run(ctx context.Context) {
	cancelled := ctx.Done()
	switch {
	case c.input.Mode.kind == modeHold:
		// The executor already owns the status/credential decision. Avoiding a
		// dormant pump here guarantees hold mode cannot read ahead before it acts.
	case c.input.InitialFailure != "":
		c.analysisFailure = c.input.InitialFailure
		_ = c.commitForwarding(c.input.InitialFailure, nil, nil)
		if c.termination == "" {
			c.startPump(false)
		} else {
			c.finalize()
		}
	case c.input.Mode.kind == modeObserve:
		_ = c.commitForwarding(c.input.Mode.releaseReason, nil, nil)
		if c.termination == "" {
			c.startPump(true)
		} else {
			c.finalize()
		}
	case c.input.Mode.kind == modeProbe:
		c.armProbeTimer()
		c.startPump(true)
	}

	for !c.finalized {
		select {
		case command := <-c.shared.commands:
			c.handleCommand(command)
		case event := <-c.events:
			c.handlePumpEvent(event)
		case observation := <-c.usage:
			c.storeUsage(observation)
		case <-c.timerSignals.ready:
			c.handlePendingTimerSignals()
		case <-cancelled:
			cancelled = nil
			c.handleCancellation()
		}
	}
}

func (c *coordinator[T]) startPump(analyze bool) {
	if c.pumpStarted || c.finalized {
		return
	}
	c.pumpStarted = true
	joined := make(chan struct{})
	c.pumpJoined = joined
	config := pumpConfig[T]{
		body:               c.input.Body,
		analyze:            analyze,
		account:            c.account,
		scratchBytes:       c.config.DecodedBufferBytes,
		decodedBufferBytes: c.config.DecodedBufferBytes,
		newDriver:          c.input.NewDriver,
		observations:       c.config.Observations,
		failureReason:      c.config.FailureReason,
		events:             c.events,
		usage:              c.usage,
		startAck:           c.startAck,
		rawAck:             c.rawAck,
		observationAck:     c.observationAck,
	}
	go func() {
		defer close(joined)
		runPump(config)
	}()
}

func (c *coordinator[T]) handleCommand(command command[T]) {
	close(command.accepted)
	if c.state != StateProbing {
		err := &AlreadyResolved{State: c.state}
		if command.kind == commandCommit {
			command.commitReply <- commitResult[T]{err: err}
		} else {
			command.discardReply <- discardResult{err: err}
		}
		return
	}

	switch command.kind {
	case commandCommit:
		writeErr := c.commitForwarding("", nil, nil)
		if c.pumpPaused {
			c.resumePumpAfterResolution()
		}
		if !c.pumpStarted && c.termination == "" {
			c.startPump(false)
		}
		if !c.pumpStarted && c.termination != "" {
			c.finalize()
		}
		command.commitReply <- commitResult[T]{forwarding: &c.shared.forwarding, err: writeErr}
	case commandDiscard:
		c.discardCause = command.cause
		c.discardReply = command.discardReply
		c.discard()
	default:
		if command.commitReply != nil {
			command.commitReply <- commitResult[T]{err: errors.New("unknown response command")}
		}
	}
}

func (c *coordinator[T]) discard() {
	c.stopProbeTimer()
	c.stopIdleTimer()
	c.state = StateDiscarded
	if c.termination == "" {
		c.termination = TerminationDiscarded
	}
	c.releasePrefix()
	c.closeBody()
	c.shared.boundary.publish(Boundary[T]{State: StateDiscarded, Reason: c.boundaryReason})
	c.trace(traceProbeReleased, c.boundaryReason)
	if c.pumpPaused {
		c.pumpPaused = false
		c.observationAck <- directiveStop
	}
	if !c.pumpStarted {
		c.finalize()
	}
}

func (c *coordinator[T]) handlePumpEvent(event pumpEvent[T]) {
	switch event.kind {
	case pumpReadStarted:
		c.handleReadStarted()
	case pumpRawBytes:
		c.handleRaw(event.bytes, event.generation)
	case pumpDecisiveObservation:
		c.handleDecisiveObservation(event)
	case pumpAnalysisFailure:
		c.handleAnalysisFailure(event.reason)
	case pumpTerminated:
		c.handlePumpTermination(event.terminal)
	}
}

func (c *coordinator[T]) handleReadStarted() {
	if c.state == StateDiscarded || c.termination == TerminationClientWriteFailure || c.termination == TerminationClientCancelled {
		c.startAck <- readStartAck{directive: directiveStop}
		return
	}
	generation := c.armIdleTimer()
	c.startAck <- readStartAck{generation: generation, directive: directiveAnalyze}
}

func (c *coordinator[T]) handleRaw(raw []byte, generation uint64) {
	c.finishRead(generation)
	c.upstreamBytes += int64(len(raw))
	if c.state == StateDiscarded || c.termination == TerminationClientWriteFailure || c.termination == TerminationClientCancelled {
		c.rawAck <- directiveStop
		return
	}

	switch c.state {
	case StateProbing:
		retained, err := c.retainRaw(raw)
		if err != nil {
			reason := c.config.FailureReason(err)
			if reason == "" {
				reason = ReasonAnalysisInternal
			}
			c.analysisFailure = reason
			_ = c.commitForwarding(reason, nil, raw[retained:])
			if c.termination == TerminationClientWriteFailure {
				c.rawAck <- directiveStop
			} else {
				c.rawAck <- directiveForwardOnly
			}
			return
		}
		c.rawAck <- directiveAnalyze
	case StateForwarding:
		if err := c.writeRaw(raw); err != nil {
			c.termination = TerminationClientWriteFailure
			c.closeBody()
			c.rawAck <- directiveStop
			return
		}
		if c.analysisFailure != "" {
			c.rawAck <- directiveForwardOnly
		} else {
			c.rawAck <- directiveAnalyze
		}
	}
}

func (c *coordinator[T]) handleDecisiveObservation(event pumpEvent[T]) {
	switch event.observationKind {
	case ObservationSemanticMatch:
		if c.hasSemantic {
			c.config.Observations.Release(&event.observation)
			c.observationAck <- c.analysisDirective()
			return
		}
		c.semantic = event.observation
		c.hasSemantic = true
		milestone := SemanticMilestone[T]{
			Matched:     true,
			State:       c.state,
			Observation: c.config.Observations.Clone(event.observation),
		}
		c.shared.semantic.publish(milestone)
		if c.state == StateProbing {
			c.stopProbeTimer()
			c.boundaryReason = ReasonSemanticMatch
			c.shared.boundary.publish(Boundary[T]{
				State:          StateProbing,
				Reason:         ReasonSemanticMatch,
				Observation:    c.config.Observations.Clone(event.observation),
				HasObservation: true,
			})
			c.pumpPaused = true
			return
		}
		c.observationAck <- c.analysisDirective()
	case ObservationClientVisible:
		if c.state == StateProbing {
			_ = c.commitForwarding(ReasonClientVisibleEvent, &event.observation, nil)
		}
		c.config.Observations.Release(&event.observation)
		c.observationAck <- c.analysisDirective()
	case ObservationFailOpen:
		reason := event.reason
		if reason == "" {
			reason = ReasonAnalysisInternal
		}
		c.analysisFailure = reason
		if c.state == StateProbing {
			_ = c.commitForwarding(reason, &event.observation, nil)
		}
		c.config.Observations.Release(&event.observation)
		c.observationAck <- c.analysisDirective()
	default:
		c.config.Observations.Release(&event.observation)
		c.observationAck <- c.analysisDirective()
	}
}

func (c *coordinator[T]) handleAnalysisFailure(reason BoundaryReason) {
	if reason == "" {
		reason = ReasonAnalysisInternal
	}
	c.analysisFailure = reason
	if c.state == StateProbing {
		_ = c.commitForwarding(reason, nil, nil)
	}
	if c.termination == TerminationClientWriteFailure || c.state == StateDiscarded {
		c.observationAck <- directiveStop
	} else {
		c.observationAck <- directiveForwardOnly
	}
}

func (c *coordinator[T]) analysisDirective() pumpDirective {
	if c.state == StateDiscarded || c.termination == TerminationClientWriteFailure || c.termination == TerminationClientCancelled {
		return directiveStop
	}
	if c.analysisFailure != "" {
		return directiveForwardOnly
	}
	return directiveAnalyze
}

func (c *coordinator[T]) resumePumpAfterResolution() {
	if !c.pumpPaused {
		return
	}
	c.pumpPaused = false
	c.observationAck <- c.analysisDirective()
}

func (c *coordinator[T]) handlePumpTermination(terminal pumpTerminal) {
	if c.pumpJoined != nil {
		<-c.pumpJoined
	}
	c.drainUsage()
	c.upstreamBytes = terminal.upstreamBytes
	c.decodedBytes = terminal.decodedBytes
	c.reachedEOF = terminal.reachedEOF
	if c.state != StateDiscarded && c.readTermination == ReadTerminationNone {
		switch {
		case terminal.readError != nil:
			c.readTermination = ReadTerminationFailure
		case terminal.reachedEOF:
			c.readTermination = ReadTerminationEOF
		}
	}

	switch c.state {
	case StateProbing:
		if terminal.readError != nil {
			c.termination = TerminationUpstreamReadFailure
			_ = c.commitForwarding(ReasonUpstreamReadFailure, nil, nil)
		} else {
			c.termination = TerminationCompleted
			_ = c.commitForwarding(ReasonUpstreamEOFNoMatch, nil, nil)
		}
	case StateForwarding:
		if c.termination == "" {
			if terminal.readError != nil {
				c.termination = TerminationUpstreamReadFailure
			} else {
				c.termination = TerminationCompleted
			}
		}
	case StateDiscarded:
		if c.termination == "" {
			c.termination = TerminationDiscarded
		}
	}
	c.finalize()
}

func (c *coordinator[T]) storeUsage(observation T) {
	if c.hasUsage {
		c.config.Observations.Release(&c.usageValue)
	}
	c.usageValue = observation
	c.hasUsage = true
}

func (c *coordinator[T]) drainUsage() {
	for {
		select {
		case observation := <-c.usage:
			c.storeUsage(observation)
		default:
			return
		}
	}
}

func (c *coordinator[T]) handleCancellation() {
	if c.state == StateDiscarded || c.finalized {
		return
	}
	c.termination = TerminationClientCancelled
	if c.state == StateProbing {
		c.boundaryReason = ReasonClientCancelled
		c.discard()
		return
	}
	c.stopIdleTimer()
	c.closeBody()
}
