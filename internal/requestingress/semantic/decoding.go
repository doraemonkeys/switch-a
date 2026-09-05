package semantic

import (
	"bufio"
	"compress/gzip"
	"compress/zlib"
	"context"
	"errors"
	"io"
	"strings"

	codexheaders "github.com/doraemonkeys/switch-a/internal/codex/headers"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/klauspost/compress/zstd"
)

const maxContentCodingLayers = 4

var errDecodedLimit = errors.New("decoded body limit exceeded")

type countingReader struct {
	source  io.Reader
	count   int64
	limit   int64
	bounded bool
	failure error
}

func (r *countingReader) Read(p []byte) (int, error) {
	if r.failure != nil {
		return 0, r.failure
	}
	if r.bounded {
		remaining := r.limit - r.count
		if remaining < int64(len(p)) {
			p = p[:remaining+1]
		}
	}
	n, err := r.source.Read(p)
	r.count += int64(n)
	if r.bounded && r.count > r.limit {
		err = errDecodedLimit
	}
	if err != nil && err != io.EOF {
		r.failure = err
	}
	return n, err
}

type contextReader struct {
	source io.Reader
	done   <-chan struct{}
}

func (r contextReader) Read(p []byte) (int, error) {
	select {
	case <-r.done:
		return 0, context.Canceled
	default:
		return r.source.Read(p)
	}
}
func project(ctx context.Context, source io.Reader, options Options) Result {
	codings, reason := parseCodings(options.ContentEncodingValues)
	if reason != "" {
		return unavailable(options, reason, 0)
	}
	if source == nil {
		source = strings.NewReader("")
	}
	wire := bufio.NewReader(contextReader{source: source, done: ctx.Done()})
	_, peekErr := wire.Peek(1)
	if peekErr == io.EOF {
		projection := projection{contract: options.ReasoningContract}
		return projection.finish(false, io.EOF)
	}
	if peekErr != nil {
		return unavailable(options, ReasonContentDecoding, 0)
	}
	encoded := false
	for _, coding := range codings {
		encoded = encoded || coding != "identity"
	}
	if encoded && options.MaxDecodedBytes <= 0 {
		return unavailable(options, ReasonInvalidLimit, 0)
	}
	var reader io.Reader = wire
	var stages []*codingReader
	var closers []func()
	defer func() {
		for i := len(closers) - 1; i >= 0; i-- {
			closers[i]()
		}
	}()
	for i := len(codings) - 1; i >= 0; i-- {
		var err error
		reader, err = openCoding(codings[i], reader, options.MaxDecodedBytes, &closers)
		if err != nil {
			reason = ReasonContentDecoding
			if errors.Is(err, errUnsupportedCoding) {
				reason = ReasonUnsupportedContentEncoding
			}
			return unavailable(options, reason, 0)
		}
		if codings[i] != "identity" {
			stage := &codingReader{source: reader}
			stages = append(stages, stage)
			reader = stage
		}
	}
	counted := &countingReader{source: reader, limit: options.MaxDecodedBytes, bounded: encoded}
	projection := projection{contract: options.ReasoningContract}
	scanner := scanner{reader: bufio.NewReader(counted), rootMember: projection.member}
	jsonErr := scanner.document()
	// JSON validity and coded-representation validity are independent. A late checksum
	// failure invalidates all semantic observations, including fields already scanned.
	_, drainErr := io.Copy(io.Discard, scanner.reader)
	if counted.failure == nil && drainErr == nil {
		drainErr = finishCodings(stages, wire)
	}
	if counted.failure != nil || drainErr != nil {
		failure := counted.failure
		if failure == nil {
			failure = drainErr
		}
		reason = ReasonContentDecoding
		if errors.Is(failure, errDecodedLimit) || errors.Is(failure, zstd.ErrDecoderSizeExceeded) {
			reason = ReasonDecodedBodyTooLarge
		}
		return unavailable(options, reason, counted.count)
	}
	result := projection.finish(true, jsonErr)
	result.DecodedBytes = counted.count
	return result
}
func unavailable(options Options, reason string, count int64) Result {
	reasoning := Fact[model.RequestedReasoningObservation]{State: Unavailable, Reason: reason, Value: reasoningState(model.ReasoningObservationInvalid)}
	if options.ReasoningContract == ReasoningUnsupported {
		reasoning = Fact[model.RequestedReasoningObservation]{State: Known, Value: reasoningState(model.ReasoningObservationUnsupported)}
	}
	return Result{
		Model:        Fact[string]{State: Unavailable, Value: unknownModel, Reason: reason},
		Reasoning:    reasoning,
		Codex:        Fact[codexheaders.ClientEvidence]{State: Unavailable, Reason: reason},
		DecodedBytes: count,
	}
}
func parseCodings(values []string) ([]string, string) {
	joined := strings.TrimSpace(strings.Join(values, ","))
	if joined == "" {
		return nil, ""
	}
	parts := strings.Split(joined, ",")
	if len(parts) > maxContentCodingLayers {
		return nil, ReasonInvalidContentEncoding
	}
	for i := range parts {
		parts[i] = strings.ToLower(strings.TrimSpace(parts[i]))
		if parts[i] == "" {
			return nil, ReasonInvalidContentEncoding
		}
	}
	return parts, ""
}

var errUnsupportedCoding = errors.New("unsupported content coding")

func openCoding(coding string, source io.Reader, limit int64, closers *[]func()) (io.Reader, error) {
	switch coding {
	case "identity":
		return source, nil
	case "gzip":
		reader, err := gzip.NewReader(source)
		if err != nil {
			return nil, err
		}
		*closers = append(*closers, func() { _ = reader.Close() })
		return reader, nil
	case "deflate":
		reader, err := zlib.NewReader(source)
		if err != nil {
			return nil, err
		}
		*closers = append(*closers, func() { _ = reader.Close() })
		return reader, nil
	case "zstd":
		reader, err := zstd.NewReader(source, zstd.WithDecoderConcurrency(1), zstd.WithDecoderLowmem(true), zstd.WithDecoderMaxMemory(max(uint64(limit), uint64(zstd.MinWindowSize))), zstd.WithDecodeBuffersBelow(0))
		if err != nil {
			return nil, err
		}
		*closers = append(*closers, reader.Close)
		return reader, nil
	default:
		return nil, errUnsupportedCoding
	}
}
