package responseanalysis

import "github.com/doraemonkeys/switch-a/internal/responseanalysis/pending"

type ResolutionState = pending.ResolutionState

const (
	StateProbing    = pending.StateProbing
	StateForwarding = pending.StateForwarding
	StateDiscarded  = pending.StateDiscarded
)

type BoundaryReason = pending.BoundaryReason

const (
	BoundaryNoRetryCandidate       = pending.ReasonNoRetryCandidate
	BoundaryPassthroughOnly        = pending.ReasonPassthroughOnly
	BoundarySemanticMatch          = pending.ReasonSemanticMatch
	BoundaryClientVisibleEvent     = pending.ReasonClientVisibleEvent
	BoundaryProbeDurationElapsed   = pending.ReasonProbeDurationElapsed
	BoundaryUpstreamEOFNoMatch     = pending.ReasonUpstreamEOFNoMatch
	BoundaryUpstreamReadFailure    = pending.ReasonUpstreamReadFailure
	BoundaryClientCancelled        = pending.ReasonClientCancelled
	BoundaryRequestMemoryExhausted = pending.ReasonRequestMemoryExhausted
	BoundaryProcessMemoryExhausted = pending.ReasonProcessMemoryExhausted
	BoundaryUnsupportedProtocol    = pending.ReasonUnsupportedProtocol
	BoundaryUnsupportedEncoding    = pending.ReasonUnsupportedEncoding
	BoundaryContentDecoding        = pending.ReasonContentDecoding
	BoundaryMalformedFrame         = pending.ReasonMalformedFrame
	BoundaryDecodedEventTooLarge   = pending.ReasonDecodedEventTooLarge
	BoundarySemanticFieldTooLarge  = pending.ReasonSemanticFieldTooLarge
	BoundaryAnalysisInternal       = pending.ReasonAnalysisInternal
)

type AnalysisMode = pending.AnalysisMode

func HoldMode() AnalysisMode {
	return pending.HoldMode()
}

func ProbeMode() AnalysisMode {
	return pending.ProbeMode()
}

func ProbeAndGateMode() AnalysisMode {
	return pending.ProbeAndGateMode()
}

func ObserveMode(reason BoundaryReason) (AnalysisMode, error) {
	return pending.ObserveMode(reason)
}

type TransitionCause = pending.TransitionCause

const (
	TransitionExecutorDecision = pending.TransitionExecutorDecision
	TransitionSemanticDecision = pending.TransitionSemanticDecision
	TransitionPassthrough      = pending.TransitionPassthrough
)

type Termination = pending.Termination
type ReadTermination = pending.ReadTermination

const (
	TerminationCompleted           = pending.TerminationCompleted
	TerminationDiscarded           = pending.TerminationDiscarded
	TerminationUpstreamReadFailure = pending.TerminationUpstreamReadFailure
	TerminationClientWriteFailure  = pending.TerminationClientWriteFailure
	TerminationClientCancelled     = pending.TerminationClientCancelled
	TerminationInternalFailure     = pending.TerminationInternalFailure
)

const (
	ReadTerminationNone        = pending.ReadTerminationNone
	ReadTerminationEOF         = pending.ReadTerminationEOF
	ReadTerminationFailure     = pending.ReadTerminationFailure
	ReadTerminationIdleTimeout = pending.ReadTerminationIdleTimeout
)

type ProcessMemoryBudget = pending.ProcessBudget

func NewProcessMemoryBudget(limit int) (*ProcessMemoryBudget, error) {
	return pending.NewProcessBudget(limit)
}

func NewDefaultProcessMemoryBudget() (*ProcessMemoryBudget, error) {
	return pending.NewProcessBudget(ResponseProbeMemoryBudget)
}

type Scheduler = pending.Scheduler
type Timer = pending.Timer
type RealScheduler = pending.RealScheduler
type TraceEvent = pending.TraceEvent
type TraceSink = pending.TraceSink
type ResponseWriter = pending.ResponseWriter
type AlreadyResolved = pending.AlreadyResolved
type PendingResponse = pending.Response[Observation]
type ForwardingResponse = pending.ForwardingResponse[Observation]
type Boundary = pending.Boundary[Observation]
type SemanticMilestone = pending.SemanticMilestone[Observation]
type Completion = pending.Completion[Observation]
type DiscardReceipt = pending.DiscardReceipt
