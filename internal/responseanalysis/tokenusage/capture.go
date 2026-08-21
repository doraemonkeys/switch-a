package tokenusage

import "bytes"

// tailBuffer is a ring buffer that retains the last N bytes.
// Used to extract the `usage` field from response tail.
type tailBuffer struct {
	buf  []byte
	size int
	pos  int
	full bool
}

func newTailBuffer(size int) *tailBuffer {
	return &tailBuffer{buf: make([]byte, size), size: size}
}

func (tb *tailBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if n >= tb.size {
		copy(tb.buf, p[n-tb.size:])
		tb.pos = 0
		tb.full = true
		return n, nil
	}
	// Check for wrap-around
	if tb.pos+n >= tb.size {
		tb.full = true
	}
	// Segmented copy
	firstPart := tb.size - tb.pos
	if firstPart >= n {
		copy(tb.buf[tb.pos:], p)
	} else {
		copy(tb.buf[tb.pos:], p[:firstPart])
		copy(tb.buf, p[firstPart:])
	}
	tb.pos = (tb.pos + n) % tb.size
	return n, nil
}

// Bytes returns buffer content in write order.
// Uses make + copy to avoid potential over-allocation from append.
func (tb *tailBuffer) Bytes() []byte {
	if !tb.full {
		return tb.buf[:tb.pos]
	}
	// When pos == 0, buf is already in correct order, return a copy
	if tb.pos == 0 {
		result := make([]byte, tb.size)
		copy(result, tb.buf)
		return result
	}
	result := make([]byte, tb.size)
	n := copy(result, tb.buf[tb.pos:])
	copy(result[n:], tb.buf[:tb.pos])
	return result
}

// CaptureBuffer captures response data before it is parsed into usage fields.
// The transport layer chooses the concrete strategy based on response size.
type CaptureBuffer interface {
	Write(p []byte) (int, error)
	Bytes() []byte
}

// fullCaptureBuffer captures the entire response (for small responses).
type fullCaptureBuffer struct {
	buf *bytes.Buffer
}

func (b *fullCaptureBuffer) Write(p []byte) (int, error) { return b.buf.Write(p) }
func (b *fullCaptureBuffer) Bytes() []byte               { return b.buf.Bytes() }

// NewCaptureBuffer selects the buffer strategy based on Content-Length.
func NewCaptureBuffer(contentLength int64) CaptureBuffer {
	if contentLength == 0 {
		return nil // Empty response, avoid meaningless allocation
	}
	if contentLength > 0 && contentLength <= fullCaptureThreshold {
		return &fullCaptureBuffer{buf: bytes.NewBuffer(make([]byte, 0, contentLength))}
	}
	return newTailBuffer(defaultTokenParseBytes)
}

func newCaptureBuffer(contentLength int64) CaptureBuffer {
	return NewCaptureBuffer(contentLength)
}
