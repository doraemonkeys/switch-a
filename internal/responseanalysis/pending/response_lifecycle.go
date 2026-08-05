package pending

import "github.com/doraemonkeys/switch-a/internal/responseanalysis/allocation"

const (
	rawPrefixChunkBytes    = 4 * 1024
	maxRawPrefixChunkCount = maxRequestMemoryLimit / rawPrefixChunkBytes

	traceProbeReleased     = "internal_error.probe_released"
	traceResponseFinalized = "internal_error.response_finalized"
)

type prefixChunk struct {
	bytes []byte
	grant allocation.Grant
}

type rawPrefix struct {
	// A fixed slot table makes the raw-prefix metadata independent of upstream
	// read granularity, so one-byte readers cannot create unaccounted growth.
	chunks [maxRawPrefixChunkCount]prefixChunk
	count  int
}

func (c *coordinator[T]) retainRaw(raw []byte) (int, error) {
	retained := 0
	for retained < len(raw) {
		if c.prefix.count > 0 {
			last := &c.prefix.chunks[c.prefix.count-1]
			copied := copy(last.bytes[len(last.bytes):cap(last.bytes)], raw[retained:])
			last.bytes = last.bytes[:len(last.bytes)+copied]
			retained += copied
			if retained == len(raw) {
				return retained, nil
			}
		}
		if c.prefix.count == len(c.prefix.chunks) {
			return retained, &allocation.Denial{
				Reason:            allocation.DenialRequestMemoryExhausted,
				Class:             allocation.ClassRawPrefix,
				RequestedCapacity: 1,
			}
		}
		grant, capacity, err := c.account.reserveUpTo(allocation.ClassRawPrefix, rawPrefixChunkBytes, 1)
		if err != nil {
			return retained, err
		}
		if grant == nil {
			return retained, allocation.ErrNilGrant
		}
		c.prefix.chunks[c.prefix.count] = prefixChunk{
			bytes: make([]byte, 0, capacity),
			grant: grant,
		}
		c.prefix.count++
	}
	return retained, nil
}

func (c *coordinator[T]) commitForwarding(reason BoundaryReason, observation *T, extraRaw []byte) error {
	if c.state != StateProbing {
		return &AlreadyResolved{State: c.state}
	}
	c.stopProbeTimer()
	c.state = StateForwarding
	if reason != "" {
		c.boundaryReason = reason
	}

	writeResponseHead(c.input.Writer, c.input.StatusCode, c.input.Header)
	c.headersCommitted = true
	writeErr := c.flushPrefix()
	if writeErr == nil && len(extraRaw) > 0 {
		writeErr = c.writeRaw(extraRaw)
	}
	if writeErr != nil {
		c.termination = TerminationClientWriteFailure
		c.closeBody()
	}

	boundary := Boundary[T]{State: StateForwarding, Reason: reason, Forwarding: &c.shared.forwarding}
	if observation != nil {
		boundary.Observation = c.config.Observations.Clone(*observation)
		boundary.HasObservation = true
	}
	c.shared.boundary.publish(boundary)
	c.trace(traceProbeReleased, reason)
	return writeErr
}

func (c *coordinator[T]) flushPrefix() error {
	var writeErr error
	for index := 0; index < c.prefix.count; index++ {
		chunk := &c.prefix.chunks[index]
		if writeErr == nil {
			writeErr = c.writeRaw(chunk.bytes)
		}
		chunk.grant.Release()
		chunk.bytes = nil
		chunk.grant = nil
	}
	c.prefix = rawPrefix{}
	return writeErr
}

func (c *coordinator[T]) writeRaw(raw []byte) error {
	if len(raw) == 0 {
		return nil
	}
	written, err := writeFull(c.input.Writer, raw)
	c.clientBytes += int64(written)
	if err == nil {
		flushResponse(c.input.Writer, c.input.Flush)
	}
	return err
}

func (c *coordinator[T]) releasePrefix() {
	for index := 0; index < c.prefix.count; index++ {
		chunk := &c.prefix.chunks[index]
		chunk.bytes = nil
		if chunk.grant != nil {
			chunk.grant.Release()
			chunk.grant = nil
		}
	}
	c.prefix = rawPrefix{}
}

func (c *coordinator[T]) closeBody() {
	if c.bodyClosed || c.input.Body == nil {
		return
	}
	c.bodyClosed = true
	c.bodyCloseErr = c.input.Body.Close()
}

func (c *coordinator[T]) finalize() {
	if c.finalized {
		return
	}
	c.finalized = true
	c.stopProbeTimer()
	c.stopIdleTimer()
	c.releasePrefix()
	c.closeBody()
	if c.reachedEOF && c.headersCommitted && c.termination == TerminationCompleted {
		writeTrailers(c.input.Writer, c.input.Trailer)
	}

	requestUsed, requestPeak := c.account.snapshot()
	_ = requestUsed
	completion := Completion[T]{
		State:                  c.state,
		StatusCode:             c.input.StatusCode,
		Header:                 cloneHeader(c.input.Header),
		Trailer:                cloneHeader(c.input.Trailer),
		UpstreamBytesRead:      c.upstreamBytes,
		DecodedBytesAnalyzed:   c.decodedBytes,
		ClientBodyBytesWritten: c.clientBytes,
		PeakRequestBytes:       requestPeak,
		PeakProcessBytes:       c.config.ProcessBudget.Peak(),
		HeadersCommitted:       c.headersCommitted,
		BodyClosed:             c.bodyClosed,
		Termination:            c.termination,
		ReadTermination:        c.readTermination,
		BoundaryReason:         c.boundaryReason,
		AnalysisFailure:        c.analysisFailure,
	}
	if c.hasSemantic {
		completion.SemanticObservation = c.config.Observations.Clone(c.semantic)
		completion.HasSemanticObservation = true
		c.config.Observations.Release(&c.semantic)
		c.hasSemantic = false
	}
	if c.hasUsage {
		completion.UsageObservation = c.config.Observations.Clone(c.usageValue)
		completion.HasUsageObservation = true
		c.config.Observations.Release(&c.usageValue)
		c.hasUsage = false
	}
	if c.termination == "" {
		completion.Termination = TerminationInternalFailure
		completion.AnalysisFailure = ReasonAnalysisInternal
	}
	if !completion.HasSemanticObservation {
		c.shared.semantic.publish(SemanticMilestone[T]{Completed: true, State: c.state})
	}
	c.trace(traceResponseFinalized, completion.BoundaryReason)
	c.account.close()
	c.shared.completion.publish(completion)

	if c.discardReply != nil {
		result := discardResult{receipt: DiscardReceipt{
			Cause:                  c.discardCause,
			UpstreamBytesRead:      completion.UpstreamBytesRead,
			DecodedBytesAnalyzed:   completion.DecodedBytesAnalyzed,
			ClientBodyBytesWritten: completion.ClientBodyBytesWritten,
			PeakRequestBytes:       completion.PeakRequestBytes,
			PeakProcessBytes:       completion.PeakProcessBytes,
			HeadersCommitted:       completion.HeadersCommitted,
			BodyClosed:             completion.BodyClosed,
			BoundaryReason:         completion.BoundaryReason,
			AnalysisFailure:        completion.AnalysisFailure,
		}}
		if c.bodyCloseErr != nil {
			result.err = c.bodyCloseErr
		}
		c.discardReply <- result
		c.discardReply = nil
	}
}

func (c *coordinator[T]) trace(name string, reason BoundaryReason) {
	if c.config.Trace == nil {
		return
	}
	requestUsed, _ := c.account.snapshot()
	c.config.Trace.Trace(TraceEvent{
		Name:               name,
		OperationID:        c.input.OperationID,
		State:              c.state,
		Reason:             reason,
		UpstreamBytesRead:  c.upstreamBytes,
		ClientBytesWritten: c.clientBytes,
		RequestBytes:       requestUsed,
		ProcessBytes:       c.config.ProcessBudget.Used(),
	})
}
