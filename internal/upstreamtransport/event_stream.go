package upstreamtransport

import (
	"compress/gzip"
	"compress/zlib"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/klauspost/compress/zstd"
)

const (
	// Matching request-body decoding keeps the gateway's representation policy
	// symmetric while rejecting pathological stacks before decoder construction.
	maxContentCodingLayers = 4
	// Streaming responses have no body-size ceiling, so a window ceiling is the
	// only stable way to prevent zstd's 64-GiB default from owning request memory.
	maxStreamingDecoderWindowBytes = 64 * 1024 * 1024
)

var invalidatedRepresentationHeaders = [...]string{
	"Accept-Ranges",
	"Content-Digest",
	"Content-Encoding",
	"Content-Length",
	"Content-Md5",
	"Content-Range",
	"Digest",
	"Etag",
	"Repr-Digest",
	"Trailer",
}

type Failure string

const (
	FailureInvalidEncoding     Failure = "invalid_content_encoding"
	FailureUnsupportedEncoding Failure = "unsupported_content_encoding"
	FailureContentDecoding     Failure = "content_decoding"
)

// DecodeError separates stable failure classification from decoder-specific
// details so retry and observability policy do not depend on error text.
type DecodeError struct {
	Failure Failure
	Coding  string
	Cause   error
}

func (e *DecodeError) Error() string {
	if e == nil {
		return "normalize event-stream response body"
	}
	return fmt.Sprintf("normalize event-stream response body: failure=%s coding=%s: %v", e.Failure, e.Coding, e.Cause)
}

func (e *DecodeError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// EventStreamNormalization keeps the immutable upstream facts alongside the
// one downstream representation consumed by analysis and visibility gates.
type EventStreamNormalization struct {
	Head           ResponseHead
	Body           io.ReadCloser
	SourceEncoding string
	Transformed    bool
	wireBytes      *atomic.Int64
}

// WireBytesRead preserves transport accounting after the downstream body has
// changed representation.
func (n EventStreamNormalization) WireBytesRead() int64 {
	if n.wireBytes == nil {
		return 0
	}
	return n.wireBytes.Load()
}

// NormalizeEventStream converts supported HTTP content codings to identity.
// Decoder construction is intentionally lazy: the response coordinator must
// arm its idle timer before a codec is allowed to read from the upstream body.
// On error the caller retains ownership of body.
func NormalizeEventStream(head ResponseHead, body io.ReadCloser) (EventStreamNormalization, error) {
	if body == nil {
		return EventStreamNormalization{}, newDecodeError(FailureContentDecoding, "", errors.New("response body is required"))
	}
	if !head.AllowsBody() {
		head.Header = head.Header.Clone()
		return EventStreamNormalization{Head: head, Body: body}, nil
	}
	codings, sourceEncoding, err := parseContentCodings(head.Header.Values("Content-Encoding"))
	if err != nil {
		return EventStreamNormalization{}, err
	}
	result := EventStreamNormalization{
		Head: head, Body: body, SourceEncoding: sourceEncoding,
	}
	result.Head.Header = cloneHeader(head.Header)
	if !hasEncodedCoding(codings) {
		return result, nil
	}
	if unsupported := firstUnsupportedCoding(codings); unsupported != "" {
		return EventStreamNormalization{}, newDecodeError(
			FailureUnsupportedEncoding,
			unsupported,
			errors.New("content coding has no streaming event decoder"),
		)
	}

	decodedBody := newDecodingBody(body, codings, sourceEncoding)
	result.Body = decodedBody
	result.wireBytes = &decodedBody.wire.bytes
	result.Head.ContentLength = -1
	result.Head.Trailer = nil
	result.Transformed = true
	for _, name := range invalidatedRepresentationHeaders {
		result.Head.Header.Del(name)
	}
	return result, nil
}

func parseContentCodings(values []string) ([]string, string, error) {
	joined := strings.TrimSpace(strings.Join(values, ","))
	if joined == "" {
		return nil, "", nil
	}
	parts := strings.Split(joined, ",")
	if len(parts) > maxContentCodingLayers {
		return nil, joined, newDecodeError(
			FailureInvalidEncoding,
			joined,
			fmt.Errorf("content encoding has more than %d layers", maxContentCodingLayers),
		)
	}
	codings := make([]string, 0, len(parts))
	for _, part := range parts {
		coding := strings.ToLower(strings.TrimSpace(part))
		if coding == "" {
			return nil, joined, newDecodeError(
				FailureInvalidEncoding,
				joined,
				errors.New("content encoding contains an empty coding"),
			)
		}
		codings = append(codings, coding)
	}
	return codings, strings.Join(codings, ","), nil
}

func hasEncodedCoding(codings []string) bool {
	for _, coding := range codings {
		if coding != "identity" {
			return true
		}
	}
	return false
}

func firstUnsupportedCoding(codings []string) string {
	for _, coding := range codings {
		switch coding {
		case "identity", "gzip", "deflate", "zstd":
		default:
			return coding
		}
	}
	return ""
}

type decodingBody struct {
	source         io.ReadCloser
	wire           byteCountingReader
	codings        []string
	sourceEncoding string

	readMu      sync.Mutex
	reader      io.Reader
	closers     []func() error
	initialized bool
	terminalErr error

	closed    atomic.Bool
	closeOnce sync.Once
	closeErr  error
}

func newDecodingBody(source io.ReadCloser, codings []string, sourceEncoding string) *decodingBody {
	return &decodingBody{
		source: source, wire: byteCountingReader{source: source},
		codings: append([]string(nil), codings...), sourceEncoding: sourceEncoding,
	}
}

func (b *decodingBody) Read(target []byte) (int, error) {
	if b == nil || b.source == nil {
		return 0, newDecodeError(FailureContentDecoding, "", errors.New("decoder is unavailable"))
	}
	if b.closed.Load() {
		return 0, http.ErrBodyReadAfterClose
	}
	b.readMu.Lock()
	defer b.readMu.Unlock()
	if b.closed.Load() {
		return 0, http.ErrBodyReadAfterClose
	}
	if b.terminalErr != nil {
		return 0, b.terminalErr
	}
	if !b.initialized {
		if err := b.initialize(); err != nil {
			b.terminalErr = err
			return 0, err
		}
	}
	n, err := b.reader.Read(target)
	if err == nil || errors.Is(err, io.EOF) {
		return n, err
	}
	b.terminalErr = newDecodeError(FailureContentDecoding, b.sourceEncoding, err)
	return n, b.terminalErr
}

func (b *decodingBody) initialize() error {
	b.initialized = true
	reader := io.Reader(&b.wire)
	for index := range slices.Backward(b.codings) {
		var err error
		reader, err = b.openDecoder(b.codings[index], reader)
		if err != nil {
			_ = closeDecoders(b.closers)
			b.closers = nil
			return err
		}
	}
	b.reader = reader
	return nil
}

type byteCountingReader struct {
	source io.Reader
	bytes  atomic.Int64
}

func (r *byteCountingReader) Read(target []byte) (int, error) {
	n, err := r.source.Read(target)
	r.bytes.Add(int64(n))
	return n, err
}

func (b *decodingBody) openDecoder(coding string, source io.Reader) (io.Reader, error) {
	switch coding {
	case "identity":
		return source, nil
	case "gzip":
		reader, err := gzip.NewReader(source)
		if err != nil {
			return nil, newDecodeError(FailureContentDecoding, coding, err)
		}
		b.closers = append(b.closers, reader.Close)
		return reader, nil
	case "deflate":
		reader, err := zlib.NewReader(source)
		if err != nil {
			return nil, newDecodeError(FailureContentDecoding, coding, err)
		}
		b.closers = append(b.closers, reader.Close)
		return reader, nil
	case "zstd":
		reader, err := zstd.NewReader(
			source,
			zstd.WithDecoderConcurrency(1),
			zstd.WithDecoderLowmem(true),
			zstd.WithDecoderMaxMemory(maxStreamingDecoderWindowBytes),
			zstd.WithDecodeBuffersBelow(0),
		)
		if err != nil {
			return nil, newDecodeError(FailureContentDecoding, coding, err)
		}
		b.closers = append(b.closers, func() error { reader.Close(); return nil })
		return reader, nil
	default:
		return nil, newDecodeError(FailureUnsupportedEncoding, coding, errors.New("content coding is unsupported"))
	}
}

func (b *decodingBody) Close() error {
	if b == nil {
		return nil
	}
	b.closeOnce.Do(func() {
		b.closed.Store(true)
		sourceErr := error(nil)
		if b.source != nil {
			sourceErr = b.source.Close()
		}
		b.readMu.Lock()
		decoderErr := closeDecoders(b.closers)
		b.closers = nil
		b.readMu.Unlock()
		b.closeErr = errors.Join(decoderErr, sourceErr)
	})
	return b.closeErr
}

func closeDecoders(closers []func() error) error {
	var closeErr error
	for index := range slices.Backward(closers) {
		closeErr = errors.Join(closeErr, closers[index]())
	}
	return closeErr
}

func newDecodeError(failure Failure, coding string, cause error) *DecodeError {
	return &DecodeError{Failure: failure, Coding: coding, Cause: cause}
}
