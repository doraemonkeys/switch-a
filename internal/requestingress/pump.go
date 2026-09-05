package requestingress

import (
	"context"
	"errors"
	"fmt"
	"io"
)

const maxConsecutiveEmptyReads = 100

func (h *Handle) pump(ctx context.Context) {
	cancelWatch := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			h.Abort(ctx.Err())
		case <-cancelWatch:
		}
	}()
	defer close(cancelWatch)
	h.finish(h.receive())
}

func (h *Handle) receiving() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.state == Receiving
}

func (h *Handle) receive() error {
	buffer := make([]byte, SegmentBytes)
	emptyReads := 0
	for h.receiving() {
		n, err := h.input.Read(buffer)
		if n < 0 || n > len(buffer) {
			return sourceFailure(FailureRead, fmt.Errorf("invalid input read count %d", n))
		}
		if n > 0 {
			emptyReads = 0
			if chunkErr := h.acceptChunk(buffer[:n]); chunkErr != nil {
				return chunkErr
			}
		} else if err == nil {
			emptyReads++
			if emptyReads >= maxConsecutiveEmptyReads {
				return sourceFailure(FailureRead, io.ErrNoProgress)
			}
		}
		if err != nil {
			return h.endInput(err)
		}
	}
	return nil
}

func (h *Handle) acceptedBytesLocked(n int) (int, error) {
	allowed := n
	var limitErr error
	if h.options.MaxBodyBytes > 0 && int64(n) > h.options.MaxBodyBytes-h.received {
		allowed = int(max(int64(0), h.options.MaxBodyBytes-h.received))
		limitErr = sourceFailure(FailureLimit, ErrBodyTooLarge)
	}
	if h.head.ContentLength < 0 || !h.head.HasBody {
		return allowed, limitErr
	}
	remaining := int(max(int64(0), h.head.ContentLength-h.received))
	if remaining < allowed {
		return remaining, sourceFailure(FailureLength, ErrLengthMismatch)
	}
	return allowed, limitErr
}

func (h *Handle) acceptChunk(chunk []byte) error {
	h.mu.Lock()
	if h.state != Receiving {
		// An interrupted Read can still return bytes; count them without extending the stopped source.
		h.received += int64(len(chunk))
		err := h.sourceErr
		h.mu.Unlock()
		return err
	}
	allowed, limitErr := h.acceptedBytesLocked(len(chunk))
	h.received += int64(len(chunk))
	var spilled bool
	var storageErr error
	if allowed > 0 {
		spilled, storageErr = h.appendLocked(chunk[:allowed])
	}
	h.mu.Unlock()
	if spilled {
		h.emit("spilled", "", 0)
	}
	if storageErr != nil {
		return storageErr
	}
	if allowed > 0 && h.options.OnChunk != nil {
		h.options.OnChunk(chunk[:allowed])
	}
	return limitErr
}

func (h *Handle) endInput(err error) error {
	if !errors.Is(err, io.EOF) {
		return sourceFailure(FailureRead, fmt.Errorf("read client request: %w", err))
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.state != Receiving {
		return nil
	}
	// The request owner may replace its trailer map while reading, so resolve it only at EOF.
	h.trailers = h.inputTrailers().Clone()
	if h.trailers == nil {
		h.trailers = make(map[string][]string)
	}
	if h.head.ContentLength >= 0 && h.received != h.head.ContentLength {
		return sourceFailure(FailureLength, ErrLengthMismatch)
	}
	return nil
}

func (h *Handle) finish(terminal error) {
	h.mu.Lock()
	if h.state == Receiving {
		if terminal == nil {
			h.state = Complete
		} else {
			h.state, h.sourceErr = Failed, terminal
		}
	}
	state := h.state
	h.notifyLocked()
	h.mu.Unlock()
	// On failure the interrupt runs before Close, because server bodies can serialize Close with Read.
	h.stopInput(terminal)
	h.reportFailure()
	h.mu.Lock()
	snapshot := h.snapshotLocked()
	h.mu.Unlock()
	if h.options.OnFinish != nil {
		h.options.OnFinish(snapshot)
	}
	if state == Complete {
		h.emit("completed", "", 0)
	}
	// Capture finalization is part of the pump lease, so operation cleanup cannot overtake it.
	h.mu.Lock()
	h.pumpDone = true
	cleanupErr := h.cleanupLocked()
	close(h.done)
	h.mu.Unlock()
	if cleanupErr != nil {
		h.emit("cleanup-failed", "", 0)
	}
}
