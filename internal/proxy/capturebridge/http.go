package capturebridge

import (
	"io"
	"net/http"

	"github.com/doraemonkeys/switch-a/internal/proxy/capturefailure"
	"github.com/doraemonkeys/switch-a/internal/requestcapture"
)

type HTTPBodyObserver struct {
	body          io.ReadCloser
	recorder      requestcapture.Recorder
	expectedBytes int64
	observedBytes int64
	reachedEOF    bool
	readFailed    bool
}

func (b *HTTPBodyObserver) Read(p []byte) (int, error) {
	n, err := b.body.Read(p)
	if n > 0 {
		b.observedBytes += int64(n)
		b.recorder.ObserveUpstream(p[:n])
	}
	if capturefailure.IsEOF(err) {
		b.reachedEOF = true
	} else if err != nil {
		b.readFailed = true
	}
	return n, err
}

func (b *HTTPBodyObserver) Close() error {
	return b.body.Close()
}

func (b *HTTPBodyObserver) SourceComplete() bool {
	return SourceEndpointComplete(b.observedBytes, b.expectedBytes, b.reachedEOF, b.readFailed)
}

type writerToResponseBody struct {
	*HTTPBodyObserver
	writerTo io.WriterTo
}

func (b *writerToResponseBody) WriteTo(destination io.Writer) (int64, error) {
	written, err := b.writerTo.WriteTo(newUpstreamWriter(destination, b.recorder, b.HTTPBodyObserver))
	if err == nil {
		b.reachedEOF = true
	}
	return written, err
}

type upstreamWriter struct {
	destination io.Writer
	recorder    requestcapture.Recorder
	observer    *HTTPBodyObserver
}

func (w upstreamWriter) Write(payload []byte) (int, error) {
	written, err := w.destination.Write(payload)
	if len(payload) > 0 {
		// WriterTo materialized this entire slice from upstream before the client
		// write. A downstream short write changes client-confirmed bytes, but it must
		// not erase source bytes the gateway already observed.
		w.observer.observedBytes += int64(len(payload))
		w.recorder.ObserveUpstream(payload)
	}
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
	observer *HTTPBodyObserver,
) io.Writer {
	writer := upstreamWriter{destination: destination, recorder: recorder, observer: observer}
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
) (io.ReadCloser, *HTTPBodyObserver) {
	observer := &HTTPBodyObserver{
		body:          body,
		recorder:      recorder,
		expectedBytes: expectedBytes,
	}
	if writerTo, ok := body.(io.WriterTo); ok {
		return &writerToResponseBody{
			HTTPBodyObserver: observer,
			writerTo:         writerTo,
		}, observer
	}
	return observer, observer
}

func SourceEndpointComplete(
	observedBytes int64,
	expectedBytes int64,
	reachedEOF bool,
	readFailed bool,
) bool {
	return !readFailed && (reachedEOF || (expectedBytes >= 0 && observedBytes == expectedBytes))
}
