package capturebridge

import (
	"io"
	"net/http"
	"sync"

	"github.com/doraemonkeys/switch-a/internal/requestcapture"
	"github.com/doraemonkeys/switch-a/internal/requestcapture/capturefailure"
)

// HTTPBodyFacts is a value-only snapshot of the source-side response stream.
type HTTPBodyFacts struct {
	ObservedBytes int64
	ExpectedBytes int64
	ReachedEOF    bool
	ReadFailed    bool
}

func (f HTTPBodyFacts) SourceComplete() bool {
	return SourceEndpointComplete(f.ObservedBytes, f.ExpectedBytes, f.ReachedEOF, f.ReadFailed)
}

// HTTPBodyObservation deliberately exposes facts rather than body capabilities.
// The pending-response coordinator can retain or copy this handle without creating
// another path that can read or close the upstream response.
type HTTPBodyObservation struct {
	state *httpBodyObservationState
}

func (o HTTPBodyObservation) Facts() HTTPBodyFacts {
	if o.state == nil {
		return HTTPBodyFacts{ExpectedBytes: -1}
	}
	return o.state.facts()
}

func (o HTTPBodyObservation) SourceComplete() bool {
	return o.Facts().SourceComplete()
}

type httpBodyObservationState struct {
	mu            sync.RWMutex
	expectedBytes int64
	observedBytes int64
	reachedEOF    bool
	readFailed    bool
}

func (s *httpBodyObservationState) observeRead(bytesRead int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.observedBytes += int64(bytesRead)
	if capturefailure.IsEOF(err) {
		s.reachedEOF = true
	} else if err != nil {
		s.readFailed = true
	}
}

func (s *httpBodyObservationState) observeWriterToResult(err error, destinationFailed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err == nil {
		s.reachedEOF = true
	} else if !destinationFailed {
		// WriterTo returns source and destination failures through one error slot.
		// A destination failure is tracked by the writer below; every other failure
		// is therefore evidence that the source could not be read to completion.
		s.readFailed = true
	}
}

func (s *httpBodyObservationState) addObservedBytes(bytesRead int) {
	s.mu.Lock()
	s.observedBytes += int64(bytesRead)
	s.mu.Unlock()
}

func (s *httpBodyObservationState) facts() HTTPBodyFacts {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return HTTPBodyFacts{
		ObservedBytes: s.observedBytes,
		ExpectedBytes: s.expectedBytes,
		ReachedEOF:    s.reachedEOF,
		ReadFailed:    s.readFailed,
	}
}

type observedResponseBody struct {
	body        io.ReadCloser
	recorder    requestcapture.Recorder
	observation *httpBodyObservationState
}

func (b *observedResponseBody) Read(payload []byte) (int, error) {
	bytesRead, err := b.body.Read(payload)
	if bytesRead > 0 {
		b.recorder.ObserveUpstream(payload[:bytesRead])
	}
	b.observation.observeRead(bytesRead, err)
	return bytesRead, err
}

func (b *observedResponseBody) Close() error {
	return b.body.Close()
}

type writerToResponseBody struct {
	*observedResponseBody
	writerTo io.WriterTo
}

func (b *writerToResponseBody) WriteTo(destination io.Writer) (int64, error) {
	writeObservation := &destinationWriteObservation{}
	written, err := b.writerTo.WriteTo(newUpstreamWriter(
		destination,
		b.recorder,
		b.observation,
		writeObservation,
	))
	b.observation.observeWriterToResult(err, writeObservation.failed())
	return written, err
}

type destinationWriteObservation struct {
	mu          sync.Mutex
	failedWrite bool
}

func (o *destinationWriteObservation) record(written, offered int, err error) {
	if err == nil && written == offered {
		return
	}
	o.mu.Lock()
	o.failedWrite = true
	o.mu.Unlock()
}

func (o *destinationWriteObservation) failed() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.failedWrite
}

type upstreamWriter struct {
	destination      io.Writer
	recorder         requestcapture.Recorder
	observation      *httpBodyObservationState
	writeObservation *destinationWriteObservation
}

func (w upstreamWriter) Write(payload []byte) (int, error) {
	written, err := w.destination.Write(payload)
	if len(payload) > 0 {
		// WriterTo materialized this entire slice from upstream before the client
		// write. A downstream short write changes client-confirmed bytes, but it must
		// not erase source bytes the gateway already observed.
		w.recorder.ObserveUpstream(payload)
		w.observation.addObservedBytes(len(payload))
	}
	w.writeObservation.record(written, len(payload), err)
	return written, err
}

type flushingUpstreamWriter struct {
	upstreamWriter
	flusher http.Flusher
}

type upstreamResponseWriter struct {
	upstreamWriter
	responseWriter http.ResponseWriter
}

func (w upstreamResponseWriter) Header() http.Header {
	return w.responseWriter.Header()
}

func (w upstreamResponseWriter) WriteHeader(statusCode int) {
	w.responseWriter.WriteHeader(statusCode)
}

type flushingUpstreamResponseWriter struct {
	upstreamResponseWriter
	flusher http.Flusher
}

func (w flushingUpstreamResponseWriter) Flush() {
	w.flusher.Flush()
}

func (w flushingUpstreamWriter) Flush() {
	w.flusher.Flush()
}

func newUpstreamWriter(
	destination io.Writer,
	recorder requestcapture.Recorder,
	observation *httpBodyObservationState,
	writeObservation *destinationWriteObservation,
) io.Writer {
	writer := upstreamWriter{
		destination:      destination,
		recorder:         recorder,
		observation:      observation,
		writeObservation: writeObservation,
	}
	responseWriter, hasResponseWriter := destination.(http.ResponseWriter)
	flusher, hasFlusher := destination.(http.Flusher)
	switch {
	case hasResponseWriter && hasFlusher:
		return flushingUpstreamResponseWriter{
			upstreamResponseWriter: upstreamResponseWriter{upstreamWriter: writer, responseWriter: responseWriter},
			flusher:                flusher,
		}
	case hasResponseWriter:
		return upstreamResponseWriter{upstreamWriter: writer, responseWriter: responseWriter}
	case hasFlusher:
		return flushingUpstreamWriter{upstreamWriter: writer, flusher: flusher}
	default:
		return writer
	}
}

func WrapHTTPResponseBody(
	body io.ReadCloser,
	recorder requestcapture.Recorder,
	expectedBytes int64,
) (io.ReadCloser, HTTPBodyObservation) {
	state := &httpBodyObservationState{expectedBytes: expectedBytes}
	wrapped := &observedResponseBody{
		body:        body,
		recorder:    recorder,
		observation: state,
	}
	observation := HTTPBodyObservation{state: state}
	if writerTo, ok := body.(io.WriterTo); ok {
		return &writerToResponseBody{
			observedResponseBody: wrapped,
			writerTo:             writerTo,
		}, observation
	}
	return wrapped, observation
}

func SourceEndpointComplete(
	observedBytes int64,
	expectedBytes int64,
	reachedEOF bool,
	readFailed bool,
) bool {
	return !readFailed && (reachedEOF || (expectedBytes >= 0 && observedBytes == expectedBytes))
}
