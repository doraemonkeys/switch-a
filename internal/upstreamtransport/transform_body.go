package upstreamtransport

import (
	"compress/gzip"
	"compress/zlib"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sync"

	"github.com/klauspost/compress/zstd"
)

// StreamTransform owns protocol conversion; this package owns coding, framing,
// independent transmission readers and cancellation.
type StreamTransform func(context.Context, io.Reader, io.Writer) error
type TransformError func(string, error) error

type transformedSource struct {
	original BodySource
	open     func(io.ReadCloser) (io.ReadCloser, error)
	mu       sync.Mutex
	terminal error
}

func (s *transformedSource) Open() (io.ReadCloser, error) {
	s.mu.Lock()
	terminal := s.terminal
	s.mu.Unlock()
	if terminal != nil {
		return nil, terminal
	}
	original, err := s.original.Open()
	if err != nil {
		return nil, err
	}
	result, err := s.open(original)
	if err != nil {
		_ = original.Close()
	}
	return result, err
}
func (s *transformedSource) Framing() BodyFraming {
	framing := s.original.Framing()
	framing.TrailerKeys = slices.Clone(framing.TrailerKeys)
	if framing.HasBody || framing.ContentLength != 0 {
		framing.ContentLength = -1
	}
	return framing
}
func (s *transformedSource) Trailers() http.Header { return s.original.Trailers() }
func (s *transformedSource) terminalError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.terminal
}
func (s *transformedSource) record(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.terminal == nil {
		s.terminal = err
	}
}

// TransformSource never consumes input during preparation. Every physical open
// derives fresh bytes from the original source, including live-tail reopens.
func TransformSource(ctx context.Context, original BodySource, encoding []string, transform StreamTransform, wrap TransformError) (BodySource, error) {
	if original == nil {
		return nil, nil
	}
	codings, _, err := validatedTransformCodings(encoding)
	if err != nil {
		return nil, wrap("encoding", err)
	}
	framing := original.Framing()
	if !framing.HasBody && framing.ContentLength == 0 && framing.Complete {
		return original, nil
	}
	source := &transformedSource{original: original}
	source.open = func(reader io.ReadCloser) (io.ReadCloser, error) {
		body := newTransformedReader(ctx, reader, codings, transform, func(stage string, err error) error {
			failure := wrap(stage, err)
			source.record(failure)
			return failure
		})
		return body, nil
	}
	return source, nil
}

// TransformReader takes ownership on success. Codec initialization is lazy so
// response timers and visibility gates are installed before upstream reads.
func TransformReader(ctx context.Context, original io.ReadCloser, encoding []string, transform StreamTransform, wrap TransformError) (io.ReadCloser, error) {
	if original == nil {
		return nil, wrap("body", errors.New("original body is required"))
	}
	codings, _, err := validatedTransformCodings(encoding)
	if err != nil {
		return nil, wrap("encoding", err)
	}
	return newTransformedReader(ctx, original, codings, transform, wrap), nil
}
func validatedTransformCodings(values []string) ([]string, string, error) {
	codings, joined, err := parseContentCodings(values)
	if err != nil {
		return nil, joined, err
	}
	if unsupported := firstUnsupportedCoding(codings); unsupported != "" {
		return nil, joined, newDecodeError(FailureUnsupportedEncoding, unsupported, errors.New("content coding has no streaming encoder"))
	}
	return codings, joined, nil
}

type transformedReader struct {
	reader     *io.PipeReader
	source     io.ReadCloser
	sourceOnce sync.Once
	sourceErr  error
	startOnce  sync.Once
	start      func()
	abort      func()
	done       chan struct{}
	closeOnce  sync.Once
}

func newTransformedReader(ctx context.Context, original io.ReadCloser, codings []string, transform StreamTransform, wrap TransformError) *transformedReader {
	input, output := io.Pipe()
	runContext, cancel := context.WithCancel(ctx)
	body := &transformedReader{reader: input, source: original, done: make(chan struct{})}
	body.abort = func() { cancel(); _ = input.Close(); _ = body.closeSource() }
	body.start = func() {
		go func() {
			defer close(body.done)
			defer cancel()
			defer func() { _ = body.closeSource() }()
			stop := context.AfterFunc(runContext, func() { _ = body.closeSource(); _ = output.CloseWithError(runContext.Err()) })
			defer stop()
			source := &transformObservedReader{Reader: original}
			destination := &transformObservedWriter{Writer: output}
			implementationFailure := func(stage string, err error) error {
				originalSnippet, sourceFailure := source.snapshot()
				derivedSnippet, destinationFailure := destination.snapshot()
				if runContext.Err() != nil || errors.Is(err, sourceFailure) || errors.Is(err, destinationFailure) {
					return err
				}
				return wrap(stage, &TransformationError{Stage: stage, OriginalSnippet: originalSnippet, DerivedSnippet: derivedSnippet, Cause: err})
			}
			decoded := newDecodingBody(&sourceCloser{Reader: source, close: body.closeSource}, codings, stringsJoinCodings(codings))
			defer decoded.Close()
			encoder, closeEncoding, err := encodingWriter(destination, codings)
			if err == nil {
				err = transform(runContext, decoded, encoder)
			}
			if err != nil {
				err = implementationFailure("stream", err)
				_ = output.CloseWithError(err)
				if closeEncoding != nil {
					_ = closeEncoding()
				}
				return
			}
			err = closeEncoding()
			if err != nil {
				err = implementationFailure("encoding", err)
			}
			_ = output.CloseWithError(err)
		}()
	}
	return body
}

type transformObservedReader struct {
	io.Reader
	transformIOObservation
}

func (r *transformObservedReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.record(p[:n], err)
	return n, err
}

type transformObservedWriter struct {
	io.Writer
	transformIOObservation
}

func (w *transformObservedWriter) Write(p []byte) (int, error) {
	n, err := w.Writer.Write(p)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	w.record(p[:n], err)
	return n, err
}

type sourceCloser struct {
	io.Reader
	close func() error
}

func (s *sourceCloser) Close() error { return s.close() }
func stringsJoinCodings(codings []string) string {
	var result string
	for i, coding := range codings {
		if i > 0 {
			result += ","
		}
		result += coding
	}
	return result
}
func (b *transformedReader) closeSource() error {
	b.sourceOnce.Do(func() { b.sourceErr = b.source.Close() })
	return b.sourceErr
}
func (b *transformedReader) Read(target []byte) (int, error) {
	b.startOnce.Do(b.start)
	return b.reader.Read(target)
}
func (b *transformedReader) Close() error {
	b.closeOnce.Do(func() { b.abort(); b.startOnce.Do(func() { close(b.done) }); <-b.done })
	return b.sourceErr
}

type encodingStreamWriter struct {
	io.Writer
	flushers []func() error
}

func (w *encodingStreamWriter) Flush() error {
	for i := range slices.Backward(w.flushers) {
		if err := w.flushers[i](); err != nil {
			return err
		}
	}
	return nil
}
func encodingWriter(destination io.Writer, codings []string) (io.Writer, func() error, error) {
	writer := destination
	var closers []func() error
	var flushers []func() error
	for index := range slices.Backward(codings) {
		switch codings[index] {
		case "identity":
		case "gzip":
			encoder := gzip.NewWriter(writer)
			writer = encoder
			closers = append(closers, encoder.Close)
			flushers = append(flushers, encoder.Flush)
		case "deflate":
			encoder := zlib.NewWriter(writer)
			writer = encoder
			closers = append(closers, encoder.Close)
			flushers = append(flushers, encoder.Flush)
		case "zstd":
			encoder, err := zstd.NewWriter(writer, zstd.WithEncoderConcurrency(1))
			if err != nil {
				return nil, nil, err
			}
			writer = encoder
			closers = append(closers, encoder.Close)
			flushers = append(flushers, encoder.Flush)
		default:
			return nil, nil, fmt.Errorf("unsupported content encoder %q", codings[index])
		}
	}
	return &encodingStreamWriter{Writer: writer, flushers: flushers}, func() error { return closeDecoders(closers) }, nil
}

// SetBodySource changes only transmission source/framing after authentication and
// continuity preparation. It cannot reapply header policies to derived headers.
func SetBodySource(request *http.Request, source BodySource) error {
	if request == nil {
		return errors.New("request is required")
	}
	prepared, err := prepareSourceRequest(request.Context(), request.Method, request.URL.String(), source)
	if err != nil {
		return err
	}
	if request.Body != nil {
		if err = request.Body.Close(); err != nil {
			return err
		}
	}
	request.Body = prepared.Body
	request.GetBody = prepared.GetBody
	request.ContentLength = prepared.ContentLength
	request.TransferEncoding = nil
	request.Trailer = nil
	request.Header.Del("Content-Length")
	request.Header.Del("Transfer-Encoding")
	return nil
}

// DerivedResponseHead retains immutable upstream facts and coding while removing
// validators and length tied to the unmodified representation. Ordinary trailers
// remain live; their ownership still moves with the reader.
func DerivedResponseHead(head ResponseHead) ResponseHead {
	head.Header = head.Header.Clone()
	if !head.AllowsBody() {
		return head
	}
	head.ContentLength = -1
	for _, name := range invalidatedRepresentationHeaders {
		if name != "Content-Encoding" && name != "Trailer" {
			head.Header.Del(name)
		}
	}
	return head
}
