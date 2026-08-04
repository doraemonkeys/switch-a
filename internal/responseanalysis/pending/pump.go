package pending

import (
	"errors"
	"io"

	"github.com/doraemonkeys/switch-a/internal/responseanalysis/allocation"
)

const maxConsecutiveEmptyReads = 100

var (
	errForwardOnly = errors.New("switch pump to raw forwarding")
	errPumpStopped = errors.New("stop response pump")
	errNilDriver   = errors.New("analysis driver factory returned a nil driver")
)

type pumpDirective uint8

const (
	directiveAnalyze pumpDirective = iota + 1
	directiveForwardOnly
	directiveStop
)

type pumpEventKind uint8

const (
	pumpReadStarted pumpEventKind = iota + 1
	pumpRawBytes
	pumpDecisiveObservation
	pumpAnalysisFailure
	pumpTerminated
)

type pumpEvent[T any] struct {
	kind            pumpEventKind
	bytes           []byte
	generation      uint64
	observation     T
	observationKind ObservationKind
	reason          BoundaryReason
	terminal        pumpTerminal
}

type pumpTerminal struct {
	readError     error
	upstreamBytes int64
	decodedBytes  int64
	reachedEOF    bool
}

type readStartAck struct {
	generation uint64
	directive  pumpDirective
}

type rawSource[T any] struct {
	body       io.Reader
	scratch    []byte
	events     chan<- pumpEvent[T]
	startAck   chan readStartAck
	rawAck     chan pumpDirective
	buffered   []byte
	pendingErr error
	rawErr     error
	upstream   int64
	directive  pumpDirective
	emptyReads int
}

func newRawSource[T any](
	body io.Reader,
	scratchBytes int,
	events chan<- pumpEvent[T],
	startAck chan readStartAck,
	rawAck chan pumpDirective,
) *rawSource[T] {
	return &rawSource[T]{
		body:      body,
		scratch:   make([]byte, scratchBytes),
		events:    events,
		startAck:  startAck,
		rawAck:    rawAck,
		directive: directiveAnalyze,
	}
}

func (s *rawSource[T]) Read(target []byte) (int, error) {
	if len(target) == 0 {
		return 0, nil
	}
	for len(s.buffered) == 0 {
		if s.pendingErr != nil {
			err := s.pendingErr
			s.pendingErr = nil
			return 0, err
		}
		if err := s.fill(true); err != nil {
			return 0, err
		}
	}
	n := copy(target, s.buffered)
	s.buffered = s.buffered[n:]
	return n, nil
}

func (s *rawSource[T]) fill(forAnalysis bool) error {
	s.events <- pumpEvent[T]{kind: pumpReadStarted}
	started := <-s.startAck
	if started.directive == directiveStop {
		s.directive = directiveStop
		return errPumpStopped
	}

	n, readErr := s.body.Read(s.scratch)
	if n == 0 && readErr == nil {
		s.emptyReads++
		if s.emptyReads >= maxConsecutiveEmptyReads {
			readErr = io.ErrNoProgress
		}
	} else {
		s.emptyReads = 0
	}
	if n < 0 || n > len(s.scratch) {
		n = 0
		readErr = errors.New("upstream reader returned an invalid byte count")
	}
	s.upstream += int64(n)
	if readErr != nil {
		s.rawErr = readErr
	}
	s.events <- pumpEvent[T]{
		kind:       pumpRawBytes,
		bytes:      s.scratch[:n],
		generation: started.generation,
	}
	directive := <-s.rawAck
	s.directive = directive
	if directive == directiveStop {
		return errPumpStopped
	}
	if readErr != nil {
		s.pendingErr = readErr
	}
	if forAnalysis && directive == directiveForwardOnly {
		return errForwardOnly
	}
	if n > 0 && forAnalysis {
		s.buffered = s.scratch[:n]
	}
	return nil
}

func (s *rawSource[T]) forward() error {
	// Any bytes buffered for the decoder have already crossed the raw handoff;
	// forwarding them again would duplicate the compressed response.
	s.buffered = nil
	if s.pendingErr != nil {
		err := s.pendingErr
		s.pendingErr = nil
		return err
	}
	for {
		if err := s.fill(false); err != nil {
			return err
		}
		if s.pendingErr != nil {
			err := s.pendingErr
			s.pendingErr = nil
			return err
		}
	}
}

type pumpConfig[T any] struct {
	body               io.Reader
	analyze            bool
	account            *requestAccount
	scratchBytes       int
	decodedBufferBytes int
	newDriver          DriverFactory[T]
	observations       ObservationOps[T]
	failureReason      func(error) BoundaryReason
	events             chan<- pumpEvent[T]
	usage              chan T
	startAck           chan readStartAck
	rawAck             chan pumpDirective
	observationAck     chan pumpDirective
}

func runPump[T any](config pumpConfig[T]) {
	source := newRawSource[T](
		config.body,
		config.scratchBytes,
		config.events,
		config.startAck,
		config.rawAck,
	)
	if !config.analyze {
		forwardPumpAndTerminate(config, source, 0)
		return
	}

	decoded, decodedGrant, ok := prepareDecodedBuffer(config, source)
	if !ok {
		return
	}
	defer decodedGrant.Release()

	driver, ok := prepareAnalysisDriver(config, source)
	if !ok {
		return
	}
	defer driver.Close()
	runAnalysisDriver(config, source, driver, decoded)
}

func prepareDecodedBuffer[T any](
	config pumpConfig[T],
	source *rawSource[T],
) ([]byte, allocation.Grant, bool) {
	grant, err := config.account.Reserve(allocation.ClassDecodedBuffer, config.decodedBufferBytes)
	if err == nil {
		return make([]byte, config.decodedBufferBytes), grant, true
	}
	directive := sendAnalysisFailure(config.events, config.observationAck, config.failureReason(err))
	if directive == directiveStop {
		sendPumpTerminal(config, source, 0, nil)
	} else {
		forwardPumpAndTerminate(config, source, 0)
	}
	return nil, nil, false
}

func prepareAnalysisDriver[T any](
	config pumpConfig[T],
	source *rawSource[T],
) (Driver[T], bool) {
	driver, err := config.newDriver(source, config.account)
	if err == nil && driver == nil {
		err = errNilDriver
	}
	if err == nil {
		return driver, true
	}
	if source.directive == directiveStop {
		sendPumpTerminal(config, source, 0, nil)
		return nil, false
	}
	if source.rawErr != nil && !errors.Is(source.rawErr, io.EOF) {
		sendPumpTerminal(config, source, 0, source.rawErr)
		return nil, false
	}

	directive := sendAnalysisFailure(config.events, config.observationAck, config.failureReason(err))
	if directive != directiveStop {
		err = source.forward()
	}
	sendPumpTerminal(config, source, 0, normalizePumpError(err))
	return nil, false
}

func runAnalysisDriver[T any](
	config pumpConfig[T],
	source *rawSource[T],
	driver Driver[T],
	decoded []byte,
) {
	decodedBytes := int64(0)
	lastDirective := directiveAnalyze
	emit := newObservationEmitter(config, &lastDirective)
	for {
		n, readErr := driver.Read(decoded, emit)
		decodedBytes += int64(n)
		if readErr == nil {
			continue
		}
		finishAnalysisRead(config, source, decodedBytes, lastDirective, readErr)
		return
	}
}

func newObservationEmitter[T any](
	config pumpConfig[T],
	lastDirective *pumpDirective,
) func(T) bool {
	return func(observation T) bool {
		kind := config.observations.Inspect(observation)
		switch kind {
		case ObservationIgnore:
			config.observations.Release(&observation)
			return true
		case ObservationUsage:
			coalesceUsage(config.usage, observation, config.observations.Release)
			return true
		case ObservationSemanticMatch, ObservationClientVisible, ObservationFailOpen:
			reason := BoundaryReason("")
			if kind == ObservationFailOpen {
				reason = config.observations.FailureReason(observation)
			}
			config.events <- pumpEvent[T]{
				kind:            pumpDecisiveObservation,
				observation:     observation,
				observationKind: kind,
				reason:          reason,
			}
			*lastDirective = <-config.observationAck
			return *lastDirective == directiveAnalyze
		default:
			config.observations.Release(&observation)
			return true
		}
	}
}

func finishAnalysisRead[T any](
	config pumpConfig[T],
	source *rawSource[T],
	decodedBytes int64,
	lastDirective pumpDirective,
	readErr error,
) {
	switch {
	case errors.Is(readErr, ErrAnalysisStopped):
		if lastDirective == directiveStop {
			sendPumpTerminal(config, source, decodedBytes, nil)
			return
		}
		forwardPumpAndTerminate(config, source, decodedBytes)
	case source.directive == directiveForwardOnly || errors.Is(readErr, errForwardOnly):
		forwardPumpAndTerminate(config, source, decodedBytes)
	case source.directive == directiveStop || errors.Is(readErr, errPumpStopped):
		sendPumpTerminal(config, source, decodedBytes, nil)
	case source.rawErr != nil && !errors.Is(source.rawErr, io.EOF):
		sendPumpTerminal(config, source, decodedBytes, source.rawErr)
	case errors.Is(readErr, io.EOF):
		sendPumpTerminal(config, source, decodedBytes, nil)
	default:
		handleAnalysisReadFailure(config, source, decodedBytes, readErr)
	}
}

func handleAnalysisReadFailure[T any](
	config pumpConfig[T],
	source *rawSource[T],
	decodedBytes int64,
	readErr error,
) {
	directive := sendAnalysisFailure(config.events, config.observationAck, config.failureReason(readErr))
	if directive == directiveStop {
		sendPumpTerminal(config, source, decodedBytes, nil)
		return
	}
	forwardPumpAndTerminate(config, source, decodedBytes)
}

func forwardPumpAndTerminate[T any](
	config pumpConfig[T],
	source *rawSource[T],
	decodedBytes int64,
) {
	err := source.forward()
	sendPumpTerminal(config, source, decodedBytes, normalizePumpError(err))
}

func sendPumpTerminal[T any](
	config pumpConfig[T],
	source *rawSource[T],
	decodedBytes int64,
	err error,
) {
	config.events <- terminalEvent[T](source, decodedBytes, err)
}

func coalesceUsage[T any](queue chan T, observation T, release func(*T)) {
	select {
	case queue <- observation:
		return
	default:
	}

	// Completion represents final usage, so saturation replaces the oldest
	// queued sample. The pump is the only producer; the coordinator only drains.
	select {
	case stale := <-queue:
		release(&stale)
	default:
	}
	queue <- observation
}

func sendAnalysisFailure[T any](
	events chan<- pumpEvent[T],
	ack <-chan pumpDirective,
	reason BoundaryReason,
) pumpDirective {
	events <- pumpEvent[T]{kind: pumpAnalysisFailure, reason: reason}
	return <-ack
}

func terminalEvent[T any](source *rawSource[T], decodedBytes int64, err error) pumpEvent[T] {
	return pumpEvent[T]{
		kind: pumpTerminated,
		terminal: pumpTerminal{
			readError:     err,
			upstreamBytes: source.upstream,
			decodedBytes:  decodedBytes,
			reachedEOF:    errors.Is(source.rawErr, io.EOF),
		},
	}
}

func normalizePumpError(err error) error {
	if errors.Is(err, io.EOF) || errors.Is(err, errPumpStopped) {
		return nil
	}
	return err
}
