package errorruleapi

import (
	"bytes"
	"context"
	"errors"
	"io"

	"github.com/doraemonkeys/switch-a/internal/responseanalysis"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis/allocation"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis/framing"
)

// RegistryAnalyzer is the bounded Test Message adapter over A1's public
// registry/decoder/stream surface. Runtime and admin therefore share protocol
// selection, framing, field extraction, and fail-open classification.
type RegistryAnalyzer struct {
	registry responseanalysis.Registry
}

func NewRegistryAnalyzer() RegistryAnalyzer {
	return RegistryAnalyzer{registry: responseanalysis.NewRegistry()}
}

func (a RegistryAnalyzer) Analyze(
	ctx context.Context,
	input MessageAnalysisInput,
	consume func(AnalyzedError) bool,
) MessageAnalysisResult {
	protocol, failure := a.registry.Resolve(
		string(input.APIType), input.ContentType, input.ContentEncoding,
	)
	if failure != "" {
		return MessageAnalysisResult{Failure: failure}
	}
	protocolID := protocol.ID()
	result := MessageAnalysisResult{ProtocolID: &protocolID}
	if consume == nil {
		result.Failure = responseanalysis.FailureAnalysisInternal
		return result
	}

	// The endpoint's encoded and decoded body ceilings make every retained value
	// independently bounded. The no-op grant adapter is therefore intentional:
	// unlike runtime probing, this isolated in-memory tool has no concurrent raw
	// passthrough representation to account against the process probe budget.
	reserver := allocation.NoopReserver{}
	decoder, err := protocol.NewDecoder(bytes.NewReader(input.Body), reserver)
	if err != nil {
		result.Failure = analysisFailure(err)
		return result
	}
	defer func() { _ = decoder.Close() }()
	stream, err := protocol.NewStream(reserver)
	if err != nil {
		result.Failure = analysisFailure(err)
		return result
	}
	defer stream.Release()

	grant, err := reserver.Reserve(allocation.ClassDecodedBuffer, responseanalysis.PumpReadBufferBytes)
	if err != nil || grant == nil {
		result.Failure = analysisFailure(err)
		return result
	}
	defer grant.Release()
	buffer := make([]byte, responseanalysis.PumpReadBufferBytes)

	// The endpoint must stop decoding as soon as its rule evaluator has a
	// decision. Keeping that state in a streaming consumer avoids analyzing later
	// malformed or oversized frames that cannot affect the response.
	consumer := registryAnalysisConsumer{
		result:  &result,
		consume: consume,
	}
	return consumer.read(ctx, decoder, stream, buffer)
}

type registryAnalysisConsumer struct {
	result     *MessageAnalysisResult
	consume    func(AnalyzedError) bool
	frameIndex int
	errorCount int
	stopped    bool
}

func (c *registryAnalysisConsumer) feed(observation responseanalysis.Observation) bool {
	defer observation.Release()
	currentFrame := c.frameIndex
	c.frameIndex++
	switch observation.Class {
	case responseanalysis.EventFailOpen:
		c.result.Failure = observation.AnalysisReason
		c.stopped = true
		return false
	case responseanalysis.EventError:
		if observation.Fields == nil {
			c.result.Failure = responseanalysis.FailureAnalysisInternal
			c.stopped = true
			return false
		}
		if c.errorCount == responseanalysis.MaxTestMessageErrors {
			c.result.Failure = responseanalysis.FailureRequestMemoryExhausted
			c.stopped = true
			return false
		}
		c.errorCount++
		if !c.consume(AnalyzedError{FrameIndex: currentFrame, Fields: *observation.Fields}) {
			c.stopped = true
			return false
		}
	}
	return true
}

func (c *registryAnalysisConsumer) read(
	ctx context.Context,
	decoder io.Reader,
	stream *responseanalysis.Stream,
	buffer []byte,
) MessageAnalysisResult {
	decodedBytes := 0
	for {
		if analysisContextCanceled(ctx) {
			c.result.Failure = responseanalysis.FailureAnalysisInternal
			return *c.result
		}
		remaining := responseanalysis.MaxTestMessageDecodedBodyBytes - decodedBytes
		readCapacity := min(len(buffer), remaining+1)
		n, readErr := decoder.Read(buffer[:readCapacity])
		allowed := min(n, remaining)
		if allowed > 0 {
			decodedBytes += allowed
			stream.Feed(buffer[:allowed], false, c.feed)
			if c.stopped {
				return *c.result
			}
		}
		if n > allowed {
			c.result.Failure = responseanalysis.FailureRequestMemoryExhausted
			return *c.result
		}
		if readErr == nil {
			continue
		}
		if errors.Is(readErr, io.EOF) {
			stream.Feed(nil, true, c.feed)
			return *c.result
		}
		if !c.stopped {
			c.result.Failure = analysisFailure(readErr)
		}
		return *c.result
	}
}

func analysisContextCanceled(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	select {
	case <-ctx.Done():
		return true
	default:
		return false
	}
}

func analysisFailure(err error) responseanalysis.AnalysisFailureReason {
	if reason, ok := allocation.DenialReasonOf(err); ok {
		switch reason {
		case allocation.DenialRequestMemoryExhausted:
			return responseanalysis.FailureRequestMemoryExhausted
		case allocation.DenialProcessMemoryExhausted:
			return responseanalysis.FailureProcessMemoryExhausted
		}
	}
	switch framing.ReasonOf(err) {
	case framing.FailureUnsupportedEncoding:
		return responseanalysis.FailureUnsupportedEncoding
	case framing.FailureContentDecoding:
		return responseanalysis.FailureContentDecoding
	case framing.FailureMalformedFrame:
		return responseanalysis.FailureMalformedFrame
	case framing.FailureDecodedEventTooLarge:
		return responseanalysis.FailureDecodedEventTooLarge
	case framing.FailureSemanticFieldTooLarge:
		return responseanalysis.FailureSemanticFieldTooLarge
	default:
		return responseanalysis.FailureAnalysisInternal
	}
}

var _ MessageAnalyzer = RegistryAnalyzer{}
