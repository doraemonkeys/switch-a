package framing

import (
	"errors"

	"github.com/doraemonkeys/switch-a/internal/responseanalysis/allocation"
)

// JSON emits one atomic candidate only after EOF. The bounded adapter scanner
// then validates exactly one value; keeping syntax outside framing avoids the
// standard library validator's unreservable parser-state allocation.
type JSON struct {
	maxBytes int
	buffer   ownedBuffer
	ended    bool
}

func NewJSON(maxBytes int) *JSON {
	return newJSON(maxBytes, allocation.NoopReserver{})
}

func NewJSONWithReserver(maxBytes int, reserver allocation.Reserver) (*JSON, error) {
	if reserver == nil {
		return nil, &Error{Reason: FailureInternal, Cause: allocation.ErrNilReserver}
	}
	return newJSON(maxBytes, reserver), nil
}

func newJSON(maxBytes int, reserver allocation.Reserver) *JSON {
	return &JSON{
		maxBytes: maxBytes,
		buffer:   newOwnedBuffer(reserver, allocation.ClassFramingBuffer),
	}
}

func (f *JSON) Feed(chunk []byte, eof bool) (Batch, error) {
	if f.ended {
		return Batch{}, &Error{Reason: FailureInternal, Cause: errors.New("JSON framer used after EOF")}
	}
	if f.maxBytes <= 0 {
		return Batch{}, &Error{Reason: FailureInternal, Cause: errors.New("JSON event limit must be positive")}
	}
	if len(chunk) > f.maxBytes-len(f.buffer.bytes()) {
		f.ended = true
		f.buffer.release()
		return Batch{}, &Error{Reason: FailureDecodedEventTooLarge}
	}
	if err := f.buffer.appendBytes(chunk, f.maxBytes); err != nil {
		f.ended = true
		f.buffer.release()
		return Batch{}, &Error{Reason: FailureInternal, Cause: err}
	}
	if !eof {
		return Batch{}, nil
	}
	f.ended = true
	data, grant := f.buffer.transfer()
	data = trimJSONWhitespace(data)
	frame := Frame{Data: data, dataGrant: grant}
	batch := newBatch(f.buffer.reserver)
	if err := batch.append(&frame); err != nil {
		frame.Release()
		batch.Release()
		return Batch{}, &Error{Reason: FailureInternal, Cause: err}
	}
	return batch, nil
}

func (f *JSON) Release() {
	if f == nil {
		return
	}
	f.ended = true
	f.buffer.release()
}

func trimJSONWhitespace(data []byte) []byte {
	start := 0
	for start < len(data) && isJSONWhitespace(data[start]) {
		start++
	}
	end := len(data)
	for end > start && isJSONWhitespace(data[end-1]) {
		end--
	}
	return data[start:end]
}

func isJSONWhitespace(value byte) bool {
	return value == ' ' || value == '\t' || value == '\r' || value == '\n'
}
