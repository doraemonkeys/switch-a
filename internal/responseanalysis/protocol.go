// Package responseanalysis selects and incrementally analyzes bounded provider
// response protocols without owning client forwarding or retry policy.
package responseanalysis

import (
	"errors"
	"io"

	"github.com/doraemonkeys/switch-a/internal/apicontract"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis/adapters"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis/allocation"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis/framing"
)

type Protocol struct {
	id     apicontract.ResponseProtocolID
	family apicontract.ErrorFamily
	kind   framing.Kind
	coding framing.ContentCoding
}

func (p Protocol) ID() apicontract.ResponseProtocolID {
	return p.id
}

func (p Protocol) NewDecoder(source io.Reader, reserver allocation.Reserver) (*framing.Decoder, error) {
	if p.id == "" {
		return nil, &framing.Error{Reason: framing.FailureInternal, Cause: errors.New("unresolved response protocol")}
	}
	return framing.NewDecoderWithReserver(p.coding, source, reserver)
}

func (p Protocol) NewStream(reserver allocation.Reserver) (*Stream, error) {
	if p.id == "" {
		return nil, &framing.Error{Reason: framing.FailureInternal, Cause: errors.New("unresolved response protocol")}
	}
	framer, err := framing.NewWithReserver(p.kind, MaxDecodedEventBytes, reserver)
	if err != nil {
		return nil, err
	}
	limits := adapters.Limits{
		TypeBytes:    MaxSemanticTypeBytes,
		CodeBytes:    MaxSemanticCodeBytes,
		MessageBytes: MaxSemanticMessageBytes,
		ReasonBytes:  MaxSemanticReasonBytes,
	}
	adapter, err := adapters.NewWithReserver(p.family, p.kind, limits, reserver)
	if err != nil {
		framer.Release()
		return nil, err
	}
	return &Stream{
		protocolID: p.id,
		framer:     framer,
		adapter:    adapter,
	}, nil
}

// Analyze is the bounded convenience used by admin Test Message. Runtime
// forwarding consumes NewDecoder and NewStream directly so it retains sole
// ownership of raw bytes and backpressure.
func (p Protocol) Analyze(source io.Reader, reserver allocation.Reserver) []Observation {
	return p.AnalyzeBounded(source, reserver, DefaultTestMessageAnalysisLimits())
}

func (p Protocol) AnalyzeBounded(
	source io.Reader,
	reserver allocation.Reserver,
	limits AnalysisLimits,
) []Observation {
	if limits.MaxDecodedBodyBytes <= 0 ||
		limits.MaxDecodedBodyBytes > MaxTestMessageDecodedBodyBytes ||
		limits.MaxErrorObservations <= 0 ||
		limits.MaxErrorObservations > MaxTestMessageErrors {
		return []Observation{failureObservation(p.id, FailureAnalysisInternal)}
	}

	decoder, err := p.NewDecoder(source, reserver)
	if err != nil {
		return []Observation{failureObservation(p.id, failureFromError(err))}
	}
	defer decoder.Close()

	stream, err := p.NewStream(reserver)
	if err != nil {
		return []Observation{failureObservation(p.id, failureFromError(err))}
	}
	defer stream.Release()

	scratchGrant, err := reserver.Reserve(allocation.ClassDecodedBuffer, PumpReadBufferBytes)
	if err != nil {
		return []Observation{failureObservation(p.id, failureFromError(err))}
	}
	if scratchGrant == nil {
		return []Observation{failureObservation(p.id, FailureAnalysisInternal)}
	}
	defer scratchGrant.Release()
	buffer := make([]byte, PumpReadBufferBytes)

	observations := make([]Observation, 0, limits.MaxErrorObservations+2)
	errorCount := 0
	visibleRetained := false
	stopped := false
	consume := func(observation Observation) bool {
		switch observation.Class {
		case EventFailOpen:
			observations = append(observations, observation)
			stopped = true
			return false
		case EventError:
			if errorCount == limits.MaxErrorObservations {
				observation.Release()
				observations = append(observations, failureObservation(p.id, FailureRequestMemoryExhausted))
				stopped = true
				return false
			}
			errorCount++
			observations = append(observations, observation)
		case EventClientVisible:
			if visibleRetained {
				observation.Release()
			} else {
				visibleRetained = true
				observations = append(observations, observation)
			}
		default:
			observation.Release()
		}
		return true
	}

	decodedBodyBytes := 0
	for {
		remaining := limits.MaxDecodedBodyBytes - decodedBodyBytes
		readCapacity := min(PumpReadBufferBytes, remaining+1)
		n, readErr := decoder.Read(buffer[:readCapacity])
		allowed := min(n, remaining)
		if allowed > 0 {
			decodedBodyBytes += allowed
			stream.Feed(buffer[:allowed], false, consume)
			if stopped {
				return observations
			}
		}
		if n > allowed {
			observations = append(observations, failureObservation(p.id, FailureRequestMemoryExhausted))
			return observations
		}
		if readErr == nil {
			continue
		}
		if errors.Is(readErr, io.EOF) {
			stream.Feed(nil, true, consume)
			return observations
		}
		if !stopped {
			observations = append(observations, failureObservation(p.id, failureFromError(readErr)))
		}
		return observations
	}
}

type Stream struct {
	protocolID apicontract.ResponseProtocolID
	framer     *framing.Stream
	adapter    *adapters.Dispatcher
	terminal   bool
}

// Feed synchronously transfers each observation to consume. The consumer owns
// every observation even when it returns false, which makes the release boundary
// explicit without allocating an unbounded result slice inside the analyzer.
func (s *Stream) Feed(decoded []byte, eof bool, consume ObservationConsumer) {
	if s == nil || s.terminal {
		return
	}
	if consume == nil {
		s.Release()
		return
	}

	batch, err := s.framer.Feed(decoded, eof)
	defer batch.Release()
	if eof || err != nil {
		s.terminal = true
	}

	stopped := false
	for index := range batch.Frames {
		frame, ok := batch.Take(index)
		if !ok {
			continue
		}
		result := s.adapter.Observe(frame)
		frame.Release()

		if result.AllocationError != nil {
			allocationErr := result.AllocationError
			result.Release()
			stopped = true
			s.terminal = true
			consume(failureObservation(s.protocolID, failureFromError(allocationErr)))
			break
		}
		observation := Observation{
			ProtocolID: s.protocolID,
			Class:      result.Class,
			Fields:     result.Fields,
			Usage:      result.Usage,
			resources:  result.TakeResources(),
		}
		if result.Failure != "" {
			observation.AnalysisReason = failureFromFraming(result.Failure)
		}
		result.Release()

		keepGoing := consume(observation)
		if !keepGoing || observation.Class == EventFailOpen {
			stopped = true
			s.terminal = true
			break
		}
	}
	if err != nil && !stopped {
		s.terminal = true
		consume(failureObservation(s.protocolID, failureFromError(err)))
	}
	if s.terminal {
		s.framer.Release()
	}
}

func (s *Stream) Release() {
	if s == nil {
		return
	}
	s.terminal = true
	if s.framer != nil {
		s.framer.Release()
		s.framer = nil
	}
	s.adapter = nil
}

func lastIsFailOpen(observations []Observation) bool {
	return len(observations) > 0 && observations[len(observations)-1].Class == EventFailOpen
}
