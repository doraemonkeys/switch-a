// Package pending owns the response body pump and client writer state machine.
// It is generic over protocol observations so transport ownership stays below
// the responseanalysis facade without importing provider semantics.
package pending

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/doraemonkeys/switch-a/internal/responseanalysis/allocation"
)

type ResolutionState uint8

const (
	StateProbing ResolutionState = iota + 1
	StateForwarding
	StateDiscarded
)

type BoundaryReason string

const (
	ReasonNoRetryCandidate     BoundaryReason = "no_retry_candidate"
	ReasonPassthroughOnly      BoundaryReason = "passthrough_only"
	ReasonSemanticMatch        BoundaryReason = "semantic_match"
	ReasonClientVisibleEvent   BoundaryReason = "client_visible_event"
	ReasonProbeDurationElapsed BoundaryReason = "probe_duration_elapsed"
	ReasonUpstreamEOFNoMatch   BoundaryReason = "upstream_eof_no_match"
	ReasonUpstreamReadFailure  BoundaryReason = "upstream_read_failure"
	ReasonClientCancelled      BoundaryReason = "client_cancelled"

	ReasonRequestMemoryExhausted BoundaryReason = "request_probe_memory_exhausted"
	ReasonProcessMemoryExhausted BoundaryReason = "process_probe_memory_exhausted"
	ReasonUnsupportedProtocol    BoundaryReason = "unsupported_response_protocol"
	ReasonUnsupportedEncoding    BoundaryReason = "unsupported_content_encoding"
	ReasonContentDecoding        BoundaryReason = "content_decoding_failed"
	ReasonMalformedFrame         BoundaryReason = "malformed_protocol_frame"
	ReasonDecodedEventTooLarge   BoundaryReason = "decoded_event_too_large"
	ReasonSemanticFieldTooLarge  BoundaryReason = "semantic_field_too_large"
	ReasonAnalysisInternal       BoundaryReason = "analysis_internal_error"
)

type modeKind uint8

const (
	modeHold modeKind = iota + 1
	modeProbe
	modeObserve
)

// AnalysisMode is closed because buffering is a resource-ownership decision,
// not an optional flag that callers may combine inconsistently.
type AnalysisMode struct {
	kind             modeKind
	releaseReason    BoundaryReason
	gateLateSemantic bool
}

func HoldMode() AnalysisMode {
	return AnalysisMode{kind: modeHold}
}

func ProbeMode() AnalysisMode {
	return AnalysisMode{kind: modeProbe}
}

func ProbeAndGateMode() AnalysisMode {
	return AnalysisMode{kind: modeProbe, gateLateSemantic: true}
}

func (m AnalysisMode) Analyzes() bool {
	return m.kind == modeProbe || m.kind == modeObserve
}

func (m AnalysisMode) GatesLateSemantic() bool {
	return m.kind == modeProbe && m.gateLateSemantic
}

func ObserveMode(reason BoundaryReason) (AnalysisMode, error) {
	if reason != ReasonNoRetryCandidate && reason != ReasonPassthroughOnly {
		return AnalysisMode{}, fmt.Errorf("observe mode requires a no-candidate or passthrough-only reason")
	}
	return AnalysisMode{kind: modeObserve, releaseReason: reason}, nil
}

func (m AnalysisMode) validate() error {
	switch m.kind {
	case modeHold, modeProbe:
		if m.releaseReason != "" {
			return fmt.Errorf("analysis mode cannot carry release reason %q", m.releaseReason)
		}
		if m.kind == modeHold && m.gateLateSemantic {
			return fmt.Errorf("hold mode cannot gate late semantic observations")
		}
	case modeObserve:
		if m.releaseReason != ReasonNoRetryCandidate && m.releaseReason != ReasonPassthroughOnly {
			return fmt.Errorf("observe mode has invalid release reason %q", m.releaseReason)
		}
		if m.gateLateSemantic {
			return fmt.Errorf("observe mode cannot gate late semantic observations")
		}
	default:
		return errors.New("analysis mode is required")
	}
	return nil
}

type TransitionCause string

const (
	TransitionExecutorDecision TransitionCause = "executor_decision"
	TransitionSemanticDecision TransitionCause = "semantic_decision"
	TransitionPassthrough      TransitionCause = "passthrough"
)

func (c TransitionCause) validate() error {
	switch c {
	case TransitionExecutorDecision, TransitionSemanticDecision, TransitionPassthrough:
		return nil
	default:
		return fmt.Errorf("unknown response transition cause %q", c)
	}
}

type Termination string

const (
	TerminationCompleted           Termination = "completed"
	TerminationDiscarded           Termination = "discarded"
	TerminationUpstreamReadFailure Termination = "upstream_read_failure"
	TerminationClientWriteFailure  Termination = "client_write_failure"
	TerminationClientCancelled     Termination = "client_cancelled"
	TerminationInternalFailure     Termination = "internal_failure"
)

// ReadTermination preserves the source-side fact needed to distinguish an idle
// watchdog from an upstream read failure without retaining the original error.
type ReadTermination string

const (
	ReadTerminationNone        ReadTermination = ""
	ReadTerminationEOF         ReadTermination = "eof"
	ReadTerminationFailure     ReadTermination = "read_failure"
	ReadTerminationIdleTimeout ReadTermination = "idle_timeout"
)

type ObservationKind uint8

const (
	ObservationIgnore ObservationKind = iota
	ObservationUsage
	ObservationSemanticMatch
	ObservationClientVisible
	ObservationFailOpen
)

// ResponseWriter is the complete client-I/O capability transferred to Start.
// Only the coordinator invokes it; Flush is separated because not every writer
// implements streaming semantics.
type ResponseWriter interface {
	Header() http.Header
	WriteHeader(statusCode int)
	Write([]byte) (int, error)
}

type Driver[T any] interface {
	Read(decoded []byte, emit func(T) bool) (int, error)
	Close() error
}

type DriverFactory[T any] func(io.Reader, allocation.Reserver) (Driver[T], error)

type ObservationOps[T any] struct {
	Inspect       func(T) ObservationKind
	HasUsage      func(T) bool
	CloneUsage    func(T) T
	OverlayUsage  func(*T, T)
	FailureReason func(T) BoundaryReason
	Clone         func(T) T
	Release       func(*T)
}

type TraceEvent struct {
	Name               string
	OperationID        string
	State              ResolutionState
	Reason             BoundaryReason
	UpstreamBytesRead  int64
	ClientBytesWritten int64
	RequestBytes       int
	ProcessBytes       int
}

type TraceSink interface {
	Trace(TraceEvent)
}

type Config[T any] struct {
	ProcessBudget            *ProcessBudget
	Scheduler                Scheduler
	ProbeDuration            time.Duration
	IdleDuration             time.Duration
	RequestMemoryLimit       int
	DecodedBufferBytes       int
	ObservationQueueCapacity int
	CommandQueueCapacity     int
	Observations             ObservationOps[T]
	FailureReason            func(error) BoundaryReason
	Trace                    TraceSink
}

type StartInput[T any] struct {
	OperationID    string
	Mode           AnalysisMode
	StatusCode     int
	Header         http.Header
	Trailer        http.Header
	Body           io.ReadCloser
	Writer         ResponseWriter
	Flush          bool
	InitialFailure BoundaryReason
	NewDriver      DriverFactory[T]
}

type Boundary[T any] struct {
	State          ResolutionState
	Reason         BoundaryReason
	Observation    T
	HasObservation bool
	Forwarding     *ForwardingResponse[T]
}

type SemanticMilestone[T any] struct {
	Matched     bool
	Completed   bool
	State       ResolutionState
	Observation T
}

type Completion[T any] struct {
	State                  ResolutionState
	StatusCode             int
	Header                 http.Header
	Trailer                http.Header
	UpstreamBytesRead      int64
	DecodedBytesAnalyzed   int64
	ClientBodyBytesWritten int64
	PeakRequestBytes       int
	PeakProcessBytes       int
	HeadersCommitted       bool
	BodyClosed             bool
	Termination            Termination
	ReadTermination        ReadTermination
	BoundaryReason         BoundaryReason
	AnalysisFailure        BoundaryReason
	SemanticObservation    T
	HasSemanticObservation bool
	UsageObservation       T
	HasUsageObservation    bool
}

type DiscardReceipt struct {
	Cause                  TransitionCause
	UpstreamBytesRead      int64
	DecodedBytesAnalyzed   int64
	ClientBodyBytesWritten int64
	PeakRequestBytes       int
	PeakProcessBytes       int
	HeadersCommitted       bool
	BodyClosed             bool
	BoundaryReason         BoundaryReason
	AnalysisFailure        BoundaryReason
}

type AlreadyResolved struct {
	State ResolutionState
}

func (e *AlreadyResolved) Error() string {
	if e == nil {
		return "pending response already resolved"
	}
	return fmt.Sprintf("pending response already resolved as state %d", e.State)
}

var (
	ErrAnalysisStopped = errors.New("response analysis stopped")
	ErrInvalidConfig   = errors.New("invalid pending response configuration")
)
