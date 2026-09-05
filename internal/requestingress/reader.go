package requestingress

import (
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

type Reader struct {
	handle       *Handle
	attemptID    string
	readMu       sync.Mutex
	offset       int64
	segmentIndex int
	closed       atomic.Bool
	cancel       chan struct{}
	closeOnce    sync.Once
	bytesRead    atomic.Int64
}

func (h *Handle) Open() (io.ReadCloser, error) { return h.OpenReader("") }
func (h *Handle) OpenReader(attemptID string) (*Reader, error) {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil, ErrClosed
	}
	if h.state == Failed || h.state == Aborted {
		err := h.sourceErr
		h.mu.Unlock()
		return nil, err
	}
	h.readers++
	h.mu.Unlock()
	r := &Reader{handle: h, attemptID: attemptID, cancel: make(chan struct{})}
	h.emit("attempt-opened", attemptID, 0)
	return r, nil
}

func (r *Reader) BytesRead() int64 { return r.bytesRead.Load() }
func (r *Reader) Read(p []byte) (int, error) {
	r.readMu.Lock()
	defer r.readMu.Unlock()
	if r.closed.Load() {
		return 0, ErrClosed
	}
	if len(p) == 0 {
		return 0, nil
	}
	for {
		n, changed, err := r.readAvailable(p)
		if changed == nil {
			return n, err
		}
		select {
		case <-changed:
		case <-r.cancel:
			return 0, ErrClosed
		}
	}
}

// A non-nil changed channel means the source is healthy but has no published bytes yet.
func (r *Reader) readAvailable(p []byte) (int, <-chan struct{}, error) {
	h := r.handle
	h.mu.Lock()
	if r.closed.Load() {
		h.mu.Unlock()
		return 0, nil, ErrClosed
	}
	if h.state == Failed || h.state == Aborted {
		err := h.sourceErr
		h.mu.Unlock()
		return 0, nil, err
	}
	if r.offset < h.retained {
		n, err := r.readSegmentLocked(p)
		r.offset += int64(n)
		r.bytesRead.Add(int64(n))
		h.mu.Unlock()
		if err != nil {
			h.reportFailure()
			h.stopInput(err)
		}
		return n, nil, err
	}
	state, changed := h.state, h.changed
	h.mu.Unlock()
	if state == Complete {
		return 0, nil, io.EOF
	}
	return 0, changed, nil
}

func (r *Reader) readSegmentLocked(p []byte) (int, error) {
	h := r.handle
	for r.segmentIndex < len(h.segments) && r.offset >= h.segments[r.segmentIndex].start+int64(h.segments[r.segmentIndex].size) {
		r.segmentIndex++
	}
	s := h.segments[r.segmentIndex]
	within := r.offset - s.start
	count := min(len(p), s.size-int(within))
	if s.bytes != nil {
		return copy(p, s.bytes[within:within+int64(count)]), nil
	}
	n, err := h.storage.ReadAt(p[:count], s.diskOffset+within)
	if n < count && err == nil {
		err = io.ErrUnexpectedEOF
	}
	if err != nil {
		err = sourceFailure(FailureStorage, fmt.Errorf("read ingress storage: %w", err))
		err = r.failStorageLocked(err)
	}
	return n, err
}

func (r *Reader) failStorageLocked(err error) error {
	h := r.handle
	if h.state != Failed && h.state != Aborted {
		h.state, h.sourceErr = Failed, err
		h.notifyLocked()
	}
	return err
}

// Close is a barrier: its return guarantees this reader has no in-flight Read.
func (r *Reader) Close() error {
	r.closeOnce.Do(func() {
		r.closed.Store(true)
		close(r.cancel)
		r.readMu.Lock()
		defer r.readMu.Unlock()
		h := r.handle
		h.mu.Lock()
		h.readers--
		err := h.cleanupLocked()
		h.mu.Unlock()
		if err != nil {
			h.emit("cleanup-failed", r.attemptID, r.BytesRead())
		}
		h.emit("attempt-closed", r.attemptID, r.BytesRead())
	})
	return nil
}
