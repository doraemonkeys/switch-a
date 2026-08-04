package responseanalysis

import (
	"github.com/doraemonkeys/switch-a/internal/apicontract"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis/adapters"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis/allocation"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis/framing"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis/tokenusage"
)

type EventClass = adapters.EventClass

const (
	EventControl       = adapters.EventControl
	EventUsage         = adapters.EventUsage
	EventError         = adapters.EventError
	EventClientVisible = adapters.EventClientVisible
	EventFailOpen      = adapters.EventFailOpen
)

type SemanticFields = adapters.SemanticFields

// AnalysisFailureReason is deliberately separate from release/boundary reasons;
// only these facts indicate that semantic analysis stopped and raw forwarding
// must continue unchanged.
type AnalysisFailureReason string

const (
	FailureRequestMemoryExhausted AnalysisFailureReason = "request_probe_memory_exhausted"
	FailureProcessMemoryExhausted AnalysisFailureReason = "process_probe_memory_exhausted"
	FailureUnsupportedProtocol    AnalysisFailureReason = "unsupported_response_protocol"
	FailureUnsupportedEncoding    AnalysisFailureReason = "unsupported_content_encoding"
	FailureContentDecoding        AnalysisFailureReason = "content_decoding_failed"
	FailureMalformedFrame         AnalysisFailureReason = "malformed_protocol_frame"
	FailureDecodedEventTooLarge   AnalysisFailureReason = "decoded_event_too_large"
	FailureSemanticFieldTooLarge  AnalysisFailureReason = "semantic_field_too_large"
	FailureAnalysisInternal       AnalysisFailureReason = "analysis_internal_error"
)

type Observation struct {
	ProtocolID     apicontract.ResponseProtocolID `json:"response_protocol_id,omitempty"`
	Class          EventClass                     `json:"event_class"`
	Fields         *SemanticFields                `json:"fields,omitempty"`
	Usage          *tokenusage.TokenUsage         `json:"usage,omitempty"`
	AnalysisReason AnalysisFailureReason          `json:"analysis_reason,omitempty"`
	resources      allocation.Bundle
}

// Release ends the lifetime of copied semantic values carried by an
// observation. The operation is idempotent so cancellation and terminal
// cleanup paths can converge without coordinating which path arrived first.
func (o *Observation) Release() {
	if o == nil {
		return
	}
	o.resources.Release()
	*o = Observation{}
}

func ReleaseObservations(observations []Observation) {
	for index := range observations {
		observations[index].Release()
	}
}

// ObservationConsumer takes ownership of every observation it receives. A
// false return asks the stream to stop analysis after that observation; raw
// response forwarding remains the coordinator's separate responsibility.
type ObservationConsumer func(Observation) bool

// ObserveUsage is the temporary HTTP-interceptor seam and the eventual runtime
// adapter entry point. Keeping it here prevents proxy from regaining a second
// provider-payload parser.
func ObserveUsage(data []byte, logger tokenusage.Logger) *tokenusage.TokenUsage {
	return adapters.ExtractUsage(data, logger)
}

func failureObservation(protocolID apicontract.ResponseProtocolID, reason AnalysisFailureReason) Observation {
	return Observation{ProtocolID: protocolID, Class: EventFailOpen, AnalysisReason: reason}
}

func failureFromError(err error) AnalysisFailureReason {
	if reason, ok := allocation.DenialReasonOf(err); ok {
		switch reason {
		case allocation.DenialRequestMemoryExhausted:
			return FailureRequestMemoryExhausted
		case allocation.DenialProcessMemoryExhausted:
			return FailureProcessMemoryExhausted
		}
	}
	return failureFromFraming(framing.ReasonOf(err))
}

func failureFromFraming(reason framing.FailureReason) AnalysisFailureReason {
	switch reason {
	case framing.FailureUnsupportedEncoding:
		return FailureUnsupportedEncoding
	case framing.FailureContentDecoding:
		return FailureContentDecoding
	case framing.FailureMalformedFrame:
		return FailureMalformedFrame
	case framing.FailureDecodedEventTooLarge:
		return FailureDecodedEventTooLarge
	case framing.FailureSemanticFieldTooLarge:
		return FailureSemanticFieldTooLarge
	default:
		return FailureAnalysisInternal
	}
}
