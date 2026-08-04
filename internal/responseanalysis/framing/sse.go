package framing

import (
	"bytes"
	"errors"

	"github.com/doraemonkeys/switch-a/internal/responseanalysis/allocation"
)

var (
	doneMarker     = []byte("[DONE]")
	eventField     = []byte("event")
	dataField      = []byte("data")
	fieldSeparator = []byte{':'}
)

// SSE retains only the current event. Mutable line/data storage stays charged
// to the framer; dispatch moves the data and event grants into the returned
// Frame without copying payload bytes.
type SSE struct {
	maxBytes   int
	line       ownedBuffer
	data       ownedBuffer
	event      ownedText
	hasData    bool
	eventBytes int
	ended      bool
}

func NewSSE(maxBytes int) *SSE {
	return newSSE(maxBytes, allocation.NoopReserver{})
}

func NewSSEWithReserver(maxBytes int, reserver allocation.Reserver) (*SSE, error) {
	if reserver == nil {
		return nil, &Error{Reason: FailureInternal, Cause: allocation.ErrNilReserver}
	}
	return newSSE(maxBytes, reserver), nil
}

func newSSE(maxBytes int, reserver allocation.Reserver) *SSE {
	return &SSE{
		maxBytes: maxBytes,
		line:     newOwnedBuffer(reserver, allocation.ClassFramingBuffer),
		data:     newOwnedBuffer(reserver, allocation.ClassFramingBuffer),
		event:    newOwnedText(reserver, allocation.ClassFramingBuffer),
	}
}

func (f *SSE) Feed(chunk []byte, eof bool) (Batch, error) {
	if f.ended {
		return Batch{}, &Error{Reason: FailureInternal, Cause: errors.New("SSE framer used after EOF")}
	}
	if f.maxBytes <= 0 {
		return Batch{}, &Error{Reason: FailureInternal, Cause: errors.New("SSE event limit must be positive")}
	}

	batch := newBatch(f.line.reserver)
	for _, current := range chunk {
		if f.eventBytes == f.maxBytes {
			f.ended = true
			f.releaseState()
			return batch, &Error{Reason: FailureDecodedEventTooLarge}
		}
		f.eventBytes++
		if current != '\n' {
			if err := f.line.appendByte(current, f.maxBytes); err != nil {
				f.ended = true
				f.releaseState()
				return batch, &Error{Reason: FailureInternal, Cause: err}
			}
			continue
		}

		frame, ok, err := f.consumeCurrentLine()
		if err != nil {
			f.ended = true
			f.releaseState()
			return batch, &Error{Reason: FailureInternal, Cause: err}
		}
		if ok {
			if err := batch.append(&frame); err != nil {
				frame.Release()
				f.ended = true
				f.releaseState()
				return batch, &Error{Reason: FailureInternal, Cause: err}
			}
		}
	}

	if !eof {
		return batch, nil
	}
	f.ended = true
	if len(f.line.bytes()) > 0 {
		frame, ok, err := f.consumeCurrentLine()
		if err != nil {
			f.releaseState()
			return batch, &Error{Reason: FailureInternal, Cause: err}
		}
		if ok {
			if err := batch.append(&frame); err != nil {
				frame.Release()
				f.releaseState()
				return batch, &Error{Reason: FailureInternal, Cause: err}
			}
		}
	}
	if frame, ok := f.dispatch(); ok {
		if err := batch.append(&frame); err != nil {
			frame.Release()
			f.releaseState()
			return batch, &Error{Reason: FailureInternal, Cause: err}
		}
	}
	f.line.release()
	return batch, nil
}

func (f *SSE) Release() {
	if f == nil {
		return
	}
	f.ended = true
	f.releaseState()
}

func (f *SSE) consumeCurrentLine() (Frame, bool, error) {
	line := f.line.bytes()
	if len(line) > 0 && line[len(line)-1] == '\r' {
		line = line[:len(line)-1]
	}
	return f.consumeLine(line)
}

func (f *SSE) consumeLine(line []byte) (Frame, bool, error) {
	if len(line) == 0 {
		f.line.reset()
		frame, ok := f.dispatch()
		return frame, ok, nil
	}
	if line[0] == ':' {
		f.line.reset()
		return Frame{}, false, nil
	}

	name, value, found := bytes.Cut(line, fieldSeparator)
	if !found {
		value = nil
	}
	if len(value) > 0 && value[0] == ' ' {
		value = value[1:]
	}
	switch {
	case bytes.Equal(name, eventField):
		if err := f.event.replace(value); err != nil {
			return Frame{}, false, err
		}
		f.line.reset()
	case bytes.Equal(name, dataField):
		if err := f.consumeDataLine(line, value); err != nil {
			return Frame{}, false, err
		}
	default:
		f.line.reset()
	}
	return Frame{}, false, nil
}

func (f *SSE) consumeDataLine(line, value []byte) error {
	if !f.hasData {
		// The parsed value is a suffix of line. Moving the line allocation avoids
		// retaining equally large line and data buffers for the common one-line
		// SSE event while preserving reserve-before-allocation semantics.
		valueStart := len(line) - len(value)
		if err := f.line.moveCompactedTo(&f.data, valueStart, len(line)); err != nil {
			return err
		}
		f.hasData = true
		return nil
	}

	existing := f.data.bytes()
	required := len(existing) + 1 + len(value)
	if required <= cap(existing) {
		f.data.data = append(f.data.data, '\n')
		f.data.data = append(f.data.data, value...)
		f.line.release()
		return nil
	}

	if required <= cap(f.line.bytes()) {
		// When the new line owns the larger allocation, build the joined value in
		// that storage before releasing the previous data owner. copy handles the
		// overlap between the raw line value and its compacted destination.
		joined := f.line.data[:required]
		copy(joined[len(existing)+1:], value)
		copy(joined, existing)
		joined[len(existing)] = '\n'
		f.line.data = joined
		f.data.release()
		return f.line.moveCompactedTo(&f.data, 0, required)
	}

	if err := f.data.appendByteAndBytes('\n', value, f.maxBytes); err != nil {
		return err
	}
	f.line.release()
	return nil
}

func (f *SSE) dispatch() (Frame, bool) {
	f.eventBytes = 0
	if !f.hasData {
		f.event.release()
		f.data.release()
		return Frame{}, false
	}

	event, eventGrant := f.event.transfer()
	data, dataGrant := f.data.transfer()
	f.hasData = false
	return Frame{
		Event:      event,
		Data:       data,
		Done:       bytes.Equal(bytes.TrimSpace(data), doneMarker),
		eventGrant: eventGrant,
		dataGrant:  dataGrant,
	}, true
}

func (f *SSE) releaseState() {
	f.line.release()
	f.data.release()
	f.event.release()
	f.hasData = false
	f.eventBytes = 0
}
