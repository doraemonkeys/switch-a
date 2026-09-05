package requestingress

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"
)

type segment struct {
	start      int64
	bytes      []byte
	diskOffset int64
	size       int
}

type Handle struct {
	mu             sync.Mutex
	head           Head
	state          State
	sourceErr      error
	received       int64
	retained       int64
	memory         int64
	disk           int64
	segments       []segment
	storage        Storage
	trailers       http.Header
	input          io.ReadCloser
	inputTrailers  func() http.Header
	options        Options
	changed        chan struct{}
	done           chan struct{}
	pumpDone       bool
	closed         bool
	readers        int
	references     int
	cleaned        bool
	cleanupErr     error
	closeInputOnce sync.Once
	failureOnce    sync.Once
}

// Start freezes client declarations before any read or observer can change the request.
// Pass the server-owned request: a shallow request clone can lose later Trailer map replacement.
func Start(ctx context.Context, request *http.Request, options Options) (*Handle, error) {
	if options.MaxBodyBytes > 0 && request.ContentLength > options.MaxBodyBytes {
		return nil, sourceFailure(FailureLimit, ErrBodyTooLarge)
	}
	if options.MemoryBytes == 0 {
		options.MemoryBytes = DefaultMemoryBytes
	}
	if options.SharedBudget == nil {
		options.SharedBudget = processBudget
	}
	if options.CreateStorage == nil {
		options.CreateStorage = createTemporaryStorage
	}
	inputTrailers := options.TrailerSnapshot
	if inputTrailers == nil {
		inputTrailers = func() http.Header { return request.Trailer.Clone() }
	}
	keys := make([]string, 0, len(request.Trailer))
	for key := range request.Trailer {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	input := request.Body
	if input == nil {
		input = http.NoBody
	}
	h := &Handle{
		head: Head{Protocol: request.Proto, ProtocolMajor: request.ProtoMajor, ProtocolMinor: request.ProtoMinor,
			ContentLength: request.ContentLength, TransferEncoding: append([]string(nil), request.TransferEncoding...),
			TrailerKeys: keys, HasBody: input != http.NoBody},
		state: Receiving, input: input, inputTrailers: inputTrailers, options: options,
		trailers: make(http.Header), changed: make(chan struct{}), done: make(chan struct{}),
	}
	for _, key := range keys {
		h.trailers[key] = nil
	}
	if options.OnHead != nil {
		options.OnHead(h.Head())
	}
	h.emit("started", "", 0)
	go h.pump(ctx)
	return h, nil
}

func (h *Handle) Head() Head {
	// The frozen head is never modified after publication.
	head := h.head
	head.TransferEncoding = append([]string(nil), head.TransferEncoding...)
	head.TrailerKeys = append([]string(nil), head.TrailerKeys...)
	return head
}

func (h *Handle) snapshotLocked() Snapshot {
	return Snapshot{State: h.state, ReceivedBytes: h.received, MemoryBytes: h.memory,
		DiskBytes: h.disk, Err: h.sourceErr, Trailers: h.trailers.Clone(), CleanupErr: h.cleanupErr, FailureKind: failureKind(h.sourceErr)}
}
func (h *Handle) Snapshot() Snapshot    { h.mu.Lock(); defer h.mu.Unlock(); return h.snapshotLocked() }
func (h *Handle) Trailers() http.Header { h.mu.Lock(); defer h.mu.Unlock(); return h.trailers.Clone() }
func (h *Handle) Wait(ctx context.Context) error {
	if err := awaitContext(ctx, h.done); err != nil {
		return err
	}
	return h.Snapshot().Err
}
func (h *Handle) notifyLocked() { close(h.changed); h.changed = make(chan struct{}) }
func (h *Handle) emit(name, attempt string, read int64) {
	if h.options.Trace == nil {
		return
	}
	s := h.Snapshot()
	if name == "cleanup-failed" {
		s.Err = s.CleanupErr
	}
	h.options.Trace(Event{Name: name, OperationID: h.options.OperationID, AttemptID: attempt,
		State: s.State, ReceivedBytes: s.ReceivedBytes, MemoryBytes: s.MemoryBytes, DiskBytes: s.DiskBytes,
		ReaderBytes: read, Err: s.Err})
}

func (h *Handle) stopInput(reason error) {
	h.closeInputOnce.Do(func() {
		if reason != nil && h.options.Interrupt != nil {
			h.options.Interrupt(reason)
		}
		_ = h.input.Close()
	})
}

func (h *Handle) Abort(reason error) {
	if reason == nil {
		reason = ErrAborted
	}
	h.mu.Lock()
	if h.state != Receiving {
		h.mu.Unlock()
		return
	}
	h.state, h.sourceErr = Aborted, reason
	h.notifyLocked()
	h.mu.Unlock()
	h.emit("aborted", "", 0)
	h.stopInput(reason)
}

// Close ends ownership, not each reader's lease. Cleanup follows the last active lease.
func (h *Handle) Close() error {
	h.mu.Lock()
	h.closed = true
	h.notifyLocked()
	h.mu.Unlock()
	h.Abort(ErrClosed)
	<-h.done
	h.mu.Lock()
	err := h.cleanupLocked()
	h.mu.Unlock()
	if err != nil {
		h.emit("cleanup-failed", "", 0)
	}
	return err
}

// Retain keeps the store valid for a non-reader consumer; release is idempotent.
func (h *Handle) Retain() (func(), error) {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil, ErrClosed
	}
	h.references++
	h.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			h.mu.Lock()
			h.references--
			err := h.cleanupLocked()
			h.mu.Unlock()
			if err != nil {
				h.emit("cleanup-failed", "", 0)
			}
		})
	}, nil
}

func (h *Handle) cleanupLocked() error {
	if h.cleaned || !h.closed || !h.pumpDone || h.readers != 0 || h.references != 0 {
		return nil
	}
	h.cleaned = true
	h.options.SharedBudget.release(h.memory)
	h.segments = nil
	h.memory, h.disk = 0, 0
	if h.storage == nil {
		return nil
	}
	h.cleanupErr = errors.Join(h.storage.Close(), h.storage.Remove())
	return h.cleanupErr
}

// Prefix copies only the requested bounded evidence, never the entire body implicitly.
func (h *Handle) Prefix(limit int) []byte {
	if limit <= 0 {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	size := min(int64(limit), h.retained)
	if h.cleaned {
		return nil
	}
	out := make([]byte, size)
	var offset int64
	for _, s := range h.segments {
		n := min(int64(s.size), size-offset)
		if n == 0 {
			break
		}
		if s.bytes != nil {
			copy(out[offset:offset+n], s.bytes[:n])
		} else {
			got, err := h.storage.ReadAt(out[offset:offset+n], s.diskOffset)
			offset += int64(got)
			if err != nil {
				return out[:offset]
			}
			continue
		}
		offset += n
	}
	return out[:offset]
}

func (h *Handle) appendLocked(data []byte) (bool, error) {
	n := int64(len(data))
	var last *segment
	if len(h.segments) > 0 {
		last = &h.segments[len(h.segments)-1]
	}
	// Extend only the unpublished tail, so tiny client reads cannot grow descriptor storage without bound.
	if last != nil && last.bytes != nil && len(data) <= cap(last.bytes)-len(last.bytes) {
		last.bytes = append(last.bytes, data...)
		last.size += len(data)
		h.retained += n
		h.notifyLocked()
		return false, nil
	}
	s, spilled, err := h.storeSegmentLocked(data)
	if err != nil {
		return spilled, err
	}
	if last != nil && last.bytes == nil && s.bytes == nil {
		last.size += s.size
	} else {
		h.segments = append(h.segments, s)
	}
	h.retained += n
	h.notifyLocked()
	return spilled, nil
}

// Segment storage is chosen before publication; existing readers retain their original backing bytes.
func (h *Handle) storeSegmentLocked(data []byte) (segment, bool, error) {
	n := int64(len(data))
	s := segment{start: h.retained, size: len(data)}
	var allocation int64
	if h.storage == nil {
		desired := min(max(n, minimumMemorySegmentBytes), h.options.MemoryBytes-h.memory)
		allocation = h.options.SharedBudget.acquire(n, desired)
	}
	if allocation > 0 {
		// Charge capacity, including unused tail space, before publishing the segment.
		s.bytes = make([]byte, len(data), int(allocation))
		copy(s.bytes, data)
		h.memory += allocation
		return s, false, nil
	}
	spilled := false
	if h.storage == nil {
		storage, err := h.options.CreateStorage()
		if err != nil {
			return s, false, sourceFailure(FailureStorage, fmt.Errorf("create ingress storage: %w", err))
		}
		h.storage = storage
		spilled = true
	}
	s.diskOffset = h.disk
	written, err := h.storage.Write(data)
	if err == nil && written != len(data) {
		err = io.ErrShortWrite
	}
	if err != nil {
		return s, spilled, sourceFailure(FailureStorage, fmt.Errorf("write ingress storage: %w", err))
	}
	h.disk += n
	return s, spilled, nil
}
