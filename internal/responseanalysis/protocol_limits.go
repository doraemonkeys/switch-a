package responseanalysis

import "time"

const (
	MaxSemanticTypeBytes    = 256
	MaxSemanticCodeBytes    = 256
	MaxSemanticReasonBytes  = 256
	MaxSemanticMessageBytes = 4 * 1024

	DefaultProbeDuration      = 2 * time.Second
	DefaultProbeMemoryLimit   = 256 * 1024
	MaxProbeMemoryLimit       = 1024 * 1024
	ResponseProbeMemoryBudget = 64 * 1024 * 1024
	MaxDecodedEventBytes      = 256 * 1024
	PumpReadBufferBytes       = 32 * 1024
	PumpCommandQueueCapacity  = 1
	ObservationQueueCapacity  = 4
	MaxContentEncodingLayers  = 1

	MaxTestMessageRequestBytes         = 1024 * 1024
	MaxTestMessageWireBodyBytes        = 512 * 1024
	MaxTestMessageDecodedBodyBytes     = 1024 * 1024
	MaxTestMessageErrors               = 32
	MaxTestMessageRetainedObservations = MaxTestMessageErrors + 2
)

// AnalysisLimits bounds work after transport decoding. Request and wire-body
// limits belong at ingress because the protocol analyzer cannot observe them.
type AnalysisLimits struct {
	MaxDecodedBodyBytes  int
	MaxErrorObservations int
}

func DefaultTestMessageAnalysisLimits() AnalysisLimits {
	return AnalysisLimits{
		MaxDecodedBodyBytes:  MaxTestMessageDecodedBodyBytes,
		MaxErrorObservations: MaxTestMessageErrors,
	}
}
