package responseanalysis

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/doraemonkeys/switch-a/internal/responseanalysis/framing"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis/pending"
)

type SemanticMatchFunc func(SemanticFields) bool

type AnalyzerOptions struct {
	ProbeDuration    time.Duration
	ProbeMemoryLimit int
	IdleDuration     time.Duration
	Scheduler        Scheduler
	Trace            TraceSink
}

type Analyzer struct {
	registry         Registry
	processBudget    *ProcessMemoryBudget
	probeDuration    time.Duration
	probeMemoryLimit int
	idleDuration     time.Duration
	scheduler        Scheduler
	trace            TraceSink
}

func NewAnalyzer(registry Registry, processBudget *ProcessMemoryBudget, options AnalyzerOptions) (*Analyzer, error) {
	if processBudget == nil {
		return nil, fmt.Errorf("response probe process budget is required")
	}
	probeDuration := options.ProbeDuration
	if probeDuration == 0 {
		probeDuration = DefaultProbeDuration
	}
	if probeDuration < 0 {
		return nil, fmt.Errorf("probe duration must be positive")
	}
	probeMemoryLimit := options.ProbeMemoryLimit
	if probeMemoryLimit == 0 {
		probeMemoryLimit = DefaultProbeMemoryLimit
	}
	if probeMemoryLimit <= 0 || probeMemoryLimit > MaxProbeMemoryLimit {
		return nil, fmt.Errorf("probe memory limit must be between 1 and %d bytes", MaxProbeMemoryLimit)
	}
	if options.IdleDuration < 0 {
		return nil, fmt.Errorf("idle duration cannot be negative")
	}
	scheduler := options.Scheduler
	if scheduler == nil {
		scheduler = RealScheduler{}
	}
	return &Analyzer{
		registry:         registry,
		processBudget:    processBudget,
		probeDuration:    probeDuration,
		probeMemoryLimit: probeMemoryLimit,
		idleDuration:     options.IdleDuration,
		scheduler:        scheduler,
		trace:            options.Trace,
	}, nil
}

type StartInput struct {
	OperationID     string
	Mode            AnalysisMode
	APIType         string
	ContentType     string
	ContentEncoding string
	StatusCode      int
	Header          http.Header
	Trailer         http.Header
	Body            io.ReadCloser
	Writer          ResponseWriter
	IdleDuration    time.Duration
	Match           SemanticMatchFunc
}

func (a *Analyzer) Start(ctx context.Context, input StartInput) *PendingResponse {
	if a == nil {
		return pending.Start(ctx, pending.Config[Observation]{}, pending.StartInput[Observation]{
			Mode:       input.Mode,
			StatusCode: input.StatusCode,
			Header:     input.Header,
			Trailer:    input.Trailer,
			Body:       input.Body,
			Writer:     input.Writer,
		})
	}

	idleDuration := input.IdleDuration
	if idleDuration == 0 {
		idleDuration = a.idleDuration
	}
	config := pending.Config[Observation]{
		ProcessBudget:            a.processBudget,
		Scheduler:                a.scheduler,
		ProbeDuration:            a.probeDuration,
		IdleDuration:             idleDuration,
		RequestMemoryLimit:       a.probeMemoryLimit,
		DecodedBufferBytes:       PumpReadBufferBytes,
		ObservationQueueCapacity: ObservationQueueCapacity,
		CommandQueueCapacity:     PumpCommandQueueCapacity,
		Observations: pending.ObservationOps[Observation]{
			Inspect:       observationInspector(input.Match),
			HasUsage:      runtimeObservationHasUsage,
			CloneUsage:    cloneRuntimeUsageObservation,
			OverlayUsage:  overlayRuntimeUsage,
			FailureReason: observationFailureReason,
			Clone:         cloneRuntimeObservation,
			Release: func(observation *Observation) {
				observation.Release()
			},
		},
		FailureReason: runtimeFailureReason,
		Trace:         a.trace,
	}
	pendingInput := pending.StartInput[Observation]{
		OperationID: input.OperationID,
		Mode:        input.Mode,
		StatusCode:  input.StatusCode,
		Header:      input.Header,
		Trailer:     input.Trailer,
		Body:        input.Body,
		Writer:      input.Writer,
	}
	contentType := input.ContentType
	if contentType == "" {
		contentType = input.Header.Get("Content-Type")
	}
	mediaKind, mediaSupported := parseMediaKind(contentType)
	// Downstream streaming follows the upstream framing even when the executor
	// deliberately holds the response without semantic inspection.
	pendingInput.Flush = mediaSupported && mediaKind == framing.KindSSE

	if input.Mode.Analyzes() {
		contentEncoding := input.ContentEncoding
		if contentEncoding == "" {
			contentEncoding = input.Header.Get("Content-Encoding")
		}
		protocol, failure := a.registry.Resolve(input.APIType, contentType, contentEncoding)
		if failure != "" {
			pendingInput.InitialFailure = pending.BoundaryReason(failure)
		} else {
			pendingInput.NewDriver = func(source io.Reader, reserver pendingReserver) (pending.Driver[Observation], error) {
				return newRuntimeDriver(protocol, source, reserver)
			}
		}
	}
	return pending.Start(ctx, config, pendingInput)
}
