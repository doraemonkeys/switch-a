package proxy

import (
	"context"
	"io"
	"net/http"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/http"
	disguiseresponse "github.com/doraemonkeys/switch-a/internal/proxy/disguise"
)

// firstWriteResponseWriter is the client-visibility boundary for one HTTP
// attempt. It keeps continuity and Cookie state pending until the underlying
// writer makes the response observable, while preserving streaming interfaces.
type firstWriteResponseWriter struct {
	http.ResponseWriter
	prepareVisible      func(http.Header) (*codexhttp.Visibility, error)
	commitVisible       func(*codexhttp.Visibility) error
	onGateFailure       func(error)
	onStreamGateFailure func(error)
	onUncertain         func()
	onCommitError       func(error)
	onFirstWrite        func()
	onCommit            func()
	onWrite             func(int, time.Time)
	onPayload           func([]byte)
	written             bool
	committed           bool
	firstWriteTime      time.Time
	bytesWritten        int64
	writeErr            error
	gatePrepared        bool
	gateErr             error
	visibility          *codexhttp.Visibility
	sseGate             *codexhttp.SSEGate
	sseContext          context.Context
	headerPending       bool
	pendingStatus       int
	responseStream      *disguiseresponse.ResponseStream
	restoreHeader       func(http.Header) (http.Header, error)
	restoreEvent        func([]byte) ([]byte, error)
}

func (w *firstWriteResponseWriter) Write(p []byte) (int, error) {
	if w.responseStream != nil {
		return w.responseStream.Write(p)
	}
	if w != nil && w.sseGate != nil {
		return w.writeSSE(p)
	}
	return w.writePhysical(p, nil)
}

func (w *firstWriteResponseWriter) writePhysical(p []byte, eventVisibility *codexhttp.Visibility) (int, error) {
	originalLength := len(p)
	if w.restoreEvent != nil {
		derived, err := w.restoreEvent(p)
		if err != nil {
			w.writeErr = err
			return 0, err
		}
		p = derived
	}
	if !w.prepareGate() {
		return 0, w.gateErr
	}
	w.writePendingHeader()
	writeTime := time.Now()
	n, err := w.ResponseWriter.Write(p)
	if n > 0 {
		w.commit()
		w.commitVisibility(eventVisibility)
	}
	if err != nil {
		w.writeErr = err
		if n == 0 {
			w.uncertain()
		}
	} else if n != len(p) {
		err = io.ErrShortWrite
		w.writeErr = err
		if n == 0 {
			w.uncertain()
		}
	}
	if n > 0 {
		w.observeWrite(p[:n], writeTime)
	}
	if w.restoreEvent != nil {
		if err == nil {
			return originalLength, nil
		}
		return 0, err
	}
	return n, err
}

func (w *firstWriteResponseWriter) writeSSE(payload []byte) (int, error) {
	bufferedBefore := w.sseGate.BufferedBytes()
	w.sseGate.Append(payload)
	physicalWritten := 0
	for {
		event, ready, err := w.sseGate.PrepareNext(w.responseContext(), false)
		if err != nil {
			w.failSSEGate(err)
			return acceptedCurrentBytes(physicalWritten, bufferedBefore, len(payload)), err
		}
		if !ready {
			return len(payload), nil
		}
		wire := event.ReplayBytes()
		n, writeErr := w.writePhysical(wire, event.Visibility())
		physicalWritten += n
		if n == len(wire) {
			w.sseGate.Consume(len(wire))
		}
		if writeErr != nil {
			return acceptedCurrentBytes(physicalWritten, bufferedBefore, len(payload)), writeErr
		}
	}
}

func acceptedCurrentBytes(physicalWritten, bufferedBefore, currentBytes int) int {
	accepted := physicalWritten - bufferedBefore
	if accepted < 0 {
		return 0
	}
	if accepted > currentBytes {
		return currentBytes
	}
	return accepted
}

func (w *firstWriteResponseWriter) observeWrite(payload []byte, writeTime time.Time) {
	if !w.written {
		w.firstWriteTime = writeTime
		if w.onFirstWrite != nil {
			w.onFirstWrite()
		}
		w.written = true
	}
	w.bytesWritten += int64(len(payload))
	if w.onWrite != nil {
		w.onWrite(len(payload), writeTime)
	}
	if w.onPayload != nil {
		w.onPayload(payload)
	}
}

func (w *firstWriteResponseWriter) WriteHeader(statusCode int) {
	if w != nil && (w.sseGate != nil || w.responseStream != nil) {
		if !w.headerPending && !w.committed {
			w.headerPending = true
			w.pendingStatus = statusCode
		}
		return
	}
	if !w.prepareGate() {
		return
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			w.uncertain()
			panic(recovered)
		}
	}()
	w.ResponseWriter.WriteHeader(statusCode)
	w.commit()
}

func (w *firstWriteResponseWriter) writePendingHeader() {
	if w == nil || !w.headerPending {
		return
	}
	statusCode := w.pendingStatus
	w.headerPending = false
	defer func() {
		if recovered := recover(); recovered != nil {
			w.uncertain()
			panic(recovered)
		}
	}()
	w.ResponseWriter.WriteHeader(statusCode)
	w.commit()
}

func (w *firstWriteResponseWriter) prepareGate() bool {
	if w == nil {
		return false
	}
	if w.gatePrepared {
		return w.gateErr == nil
	}
	w.gatePrepared = true
	if w.prepareVisible == nil {
		return true
	}
	visibility, err := w.prepareVisible(w.Header())
	if err == nil {
		w.visibility = visibility
		if w.restoreHeader != nil {
			derived, restoreErr := w.restoreHeader(w.Header())
			if restoreErr != nil {
				w.gateErr = restoreErr
				return false
			}
			for name := range w.Header() {
				delete(w.Header(), name)
			}
			for name, values := range derived {
				w.Header()[name] = append([]string(nil), values...)
			}
		}
		return true
	}
	w.gateErr = err
	if w.onGateFailure != nil {
		w.onGateFailure(err)
	}
	return false
}

func (w *firstWriteResponseWriter) commit() {
	if w == nil || w.committed {
		return
	}
	w.committed = true
	if w.visibility != nil && w.commitVisible != nil {
		if err := w.commitVisible(w.visibility); err != nil && w.onCommitError != nil {
			w.onCommitError(err)
		}
	}
	if w.onCommit != nil {
		w.onCommit()
	}
}

func (w *firstWriteResponseWriter) commitVisibility(visibility *codexhttp.Visibility) {
	if visibility == nil || w.commitVisible == nil {
		return
	}
	if err := w.commitVisible(visibility); err != nil && w.onCommitError != nil {
		w.onCommitError(err)
	}
}

// Flush preserves http.Flusher while withholding incomplete SSE events from
// the client-visible boundary.
func (w *firstWriteResponseWriter) Flush() {
	if w.responseStream != nil {
		return
	}
	if w != nil && w.sseGate != nil && w.sseGate.BufferedBytes() > 0 && !w.committed {
		return
	}
	if !w.prepareGate() {
		return
	}
	w.writePendingHeader()
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		defer func() {
			if recovered := recover(); recovered != nil {
				w.uncertain()
				panic(recovered)
			}
		}()
		f.Flush()
		w.commit()
	}
}

func (w *firstWriteResponseWriter) FlushError() error {
	if w.responseStream != nil {
		return nil
	}
	if w != nil && w.sseGate != nil && w.sseGate.BufferedBytes() > 0 && !w.committed {
		return nil
	}
	if !w.prepareGate() {
		return w.gateErr
	}
	w.writePendingHeader()
	flusher, ok := w.ResponseWriter.(interface{ FlushError() error })
	if !ok {
		w.Flush()
		return nil
	}
	err := flusher.FlushError()
	if err != nil {
		w.uncertain()
		return err
	}
	w.commit()
	return nil
}

func (w *firstWriteResponseWriter) ReadFrom(reader io.Reader) (int64, error) {
	buffer := make([]byte, 32*1024)
	return io.CopyBuffer(struct{ io.Writer }{Writer: w}, reader, buffer)
}

func (w *firstWriteResponseWriter) Unwrap() http.ResponseWriter {
	if w == nil {
		return nil
	}
	return w.ResponseWriter
}

func (w *firstWriteResponseWriter) uncertain() {
	if w != nil && !w.committed && w.onUncertain != nil {
		w.onUncertain()
	}
}

func (w *firstWriteResponseWriter) responseContext() context.Context {
	if w != nil && w.sseContext != nil {
		return w.sseContext
	}
	return context.Background()
}

func (w *firstWriteResponseWriter) failSSEGate(err error) {
	if w == nil || err == nil {
		return
	}
	w.writeErr = err
	if !w.committed {
		w.gateErr = err
		w.headerPending = false
		if w.onGateFailure != nil {
			w.onGateFailure(err)
		}
		return
	}
	if w.onStreamGateFailure != nil {
		w.onStreamGateFailure(err)
	}
}

func (w *firstWriteResponseWriter) Finalize() error {
	if w != nil && w.responseStream != nil {
		if err := w.responseStream.Close(); err != nil {
			w.writeErr = err
			return err
		}
		if w.headerPending {
			if !w.prepareGate() {
				return w.gateErr
			}
			w.writePendingHeader()
		}
		return nil
	}
	if w == nil || w.sseGate == nil {
		return nil
	}
	if w.writeErr != nil {
		return w.writeErr
	}
	if w.gateErr != nil {
		return w.gateErr
	}
	for {
		event, ready, err := w.sseGate.PrepareNext(w.responseContext(), true)
		if err != nil {
			w.failSSEGate(err)
			return err
		}
		if !ready {
			break
		}
		wire := event.ReplayBytes()
		n, writeErr := w.writePhysical(wire, event.Visibility())
		if n == len(wire) {
			w.sseGate.Consume(len(wire))
		}
		if writeErr != nil {
			return writeErr
		}
	}
	if w.headerPending {
		if !w.prepareGate() {
			return w.gateErr
		}
		w.writePendingHeader()
	}
	return nil
}

func (w *firstWriteResponseWriter) DiscardBufferedSSE() {
	if w != nil && w.responseStream != nil {
		w.responseStream.Abort()
	}
	if w != nil && w.sseGate != nil {
		w.sseGate.Discard()
		w.headerPending = false
	}
}
