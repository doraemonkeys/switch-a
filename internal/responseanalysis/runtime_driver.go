package responseanalysis

import (
	"errors"
	"io"
	"strings"

	"github.com/doraemonkeys/switch-a/internal/responseanalysis/allocation"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis/framing"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis/pending"
)

type pendingReserver = allocation.Reserver

type runtimeDriver struct {
	decoder *framing.Decoder
	stream  *Stream
}

func newRuntimeDriver(protocol Protocol, source io.Reader, reserver allocation.Reserver) (*runtimeDriver, error) {
	decoder, err := protocol.NewDecoder(source, reserver)
	if err != nil {
		return nil, err
	}
	stream, err := protocol.NewStream(reserver)
	if err != nil {
		if closeErr := decoder.Close(); closeErr != nil {
			return nil, errors.Join(err, closeErr)
		}
		return nil, err
	}
	return &runtimeDriver{decoder: decoder, stream: stream}, nil
}

func (d *runtimeDriver) Read(decoded []byte, emit func(Observation) bool) (int, error) {
	if d == nil || d.decoder == nil || d.stream == nil || emit == nil {
		return 0, &framing.Error{Reason: framing.FailureInternal, Cause: errors.New("runtime protocol driver is incomplete")}
	}
	n, readErr := d.decoder.Read(decoded)
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		// A decoder can return a complete-looking payload together with an
		// integrity failure (for example gzip.ErrChecksum). Letting those bytes
		// create a semantic match would discard an unverified upstream response.
		return n, readErr
	}
	keepGoing := true
	consume := func(observation Observation) bool {
		keepGoing = emit(observation)
		return keepGoing
	}
	if n > 0 {
		d.stream.Feed(decoded[:n], false, consume)
	}
	if errors.Is(readErr, io.EOF) && keepGoing {
		d.stream.Feed(nil, true, consume)
	}
	if !keepGoing {
		return n, pending.ErrAnalysisStopped
	}
	return n, readErr
}

func (d *runtimeDriver) Close() error {
	if d == nil {
		return nil
	}
	if d.stream != nil {
		d.stream.Release()
		d.stream = nil
	}
	if d.decoder != nil {
		err := d.decoder.Close()
		d.decoder = nil
		return err
	}
	return nil
}

func observationInspector(match SemanticMatchFunc) func(Observation) pending.ObservationKind {
	return func(observation Observation) pending.ObservationKind {
		switch observation.Class {
		case EventControl:
			return pending.ObservationIgnore
		case EventUsage:
			return pending.ObservationUsage
		case EventError:
			if match != nil && observation.Fields != nil && match(*observation.Fields) {
				return pending.ObservationSemanticMatch
			}
			return pending.ObservationClientVisible
		case EventClientVisible:
			return pending.ObservationClientVisible
		case EventFailOpen:
			return pending.ObservationFailOpen
		default:
			return pending.ObservationIgnore
		}
	}
}

func observationFailureReason(observation Observation) pending.BoundaryReason {
	if observation.AnalysisReason == "" {
		return pending.ReasonAnalysisInternal
	}
	return pending.BoundaryReason(observation.AnalysisReason)
}

func runtimeFailureReason(err error) pending.BoundaryReason {
	return pending.BoundaryReason(failureFromError(err))
}

func cloneRuntimeObservation(source Observation) Observation {
	clone := Observation{
		ProtocolID:     source.ProtocolID,
		Class:          source.Class,
		AnalysisReason: source.AnalysisReason,
	}
	if source.Fields != nil {
		fields := *source.Fields
		fields.Type = strings.Clone(fields.Type)
		fields.Code = strings.Clone(fields.Code)
		fields.Message = strings.Clone(fields.Message)
		fields.Reason = strings.Clone(fields.Reason)
		clone.Fields = &fields
	}
	if source.Usage != nil {
		clone.Usage = source.Usage.Clone()
		clone.Usage.ServiceTier = strings.Clone(clone.Usage.ServiceTier)
	}
	return clone
}
