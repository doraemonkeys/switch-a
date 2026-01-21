package proxy

import (
	"bytes"
	"io"
)

// ResponseInterceptor intercepts response body to extract token usage.
type ResponseInterceptor interface {
	// Wrap wraps the response Body, returning a new ReadCloser.
	Wrap(body io.ReadCloser) io.ReadCloser
	// Result returns the intercepted result.
	// Must be called after the wrapped body's Read returns io.EOF.
	// complete: whether the body was fully read.
	Result() (usage *TokenUsage, complete bool)
}

// interceptTeeReadCloser wraps ReadCloser, writing to a Writer simultaneously.
// Supports onEOF callback for completion tracking.
type interceptTeeReadCloser struct {
	original io.ReadCloser
	tee      io.Reader
	onEOF    func() // Called when EOF is encountered
}

func (t *interceptTeeReadCloser) Read(p []byte) (int, error) {
	n, err := t.tee.Read(p)
	if err == io.EOF && t.onEOF != nil {
		t.onEOF()
	}
	return n, err
}

func (t *interceptTeeReadCloser) Close() error {
	return t.original.Close()
}

// tokenCaptureInterceptor captures token usage from non-streaming responses.
type tokenCaptureInterceptor struct {
	buf      captureBuffer
	result   *TokenUsage
	complete bool
	logger   Logger
}

// newTokenCaptureInterceptor creates a new interceptor for non-streaming responses.
// contentLength is used to select the optimal capture strategy:
//   - 0: empty response, no buffer allocated
//   - 1 to 32KB: full capture (small response)
//   - >32KB or -1 (chunked): tail buffer (only last 4KB)
func newTokenCaptureInterceptor(contentLength int64, logger Logger) *tokenCaptureInterceptor {
	return &tokenCaptureInterceptor{
		buf:    newCaptureBuffer(contentLength),
		logger: logger,
	}
}

func (i *tokenCaptureInterceptor) Wrap(body io.ReadCloser) io.ReadCloser {
	if body == nil {
		i.complete = true
		return nil
	}
	if i.buf == nil {
		// Content-Length is 0, return original body directly
		i.complete = true
		return body
	}
	return &interceptTeeReadCloser{
		original: body,
		tee:      io.TeeReader(body, i.buf),
		onEOF:    func() { i.complete = true },
	}
}

func (i *tokenCaptureInterceptor) Result() (*TokenUsage, bool) {
	if i.result != nil {
		return i.result, i.complete
	}
	if i.buf == nil {
		return nil, i.complete
	}
	data := i.buf.Bytes()
	i.result = parseTokenUsageWithLogger(data, i.logger)
	if i.result == nil && len(data) > 0 && i.logger != nil {
		i.logger.Debug("token usage parse failed",
			"data_len", len(data),
			"data_preview", truncateForLog(data, 100),
		)
	}
	return i.result, i.complete
}

// ============================================================
// SSE Token Interceptor (Phase 4b)
// ============================================================

// sseTokenInterceptor captures token usage from SSE streaming responses.
// SSE format varies by provider:
// - OpenAI: data: {"choices":[...],"usage":{...}}\n\n
// - Claude: event: message_stop\ndata: {"usage":{...}}\n\n
// Usage is typically in the last valid data chunk before stream ends.
type sseTokenInterceptor struct {
	result    *TokenUsage
	complete  bool
	lastChunk []byte // Only keep the last data chunk containing usage
	logger    Logger
}

// newSSETokenInterceptor creates a new interceptor for SSE streaming responses.
func newSSETokenInterceptor(logger Logger) *sseTokenInterceptor {
	return &sseTokenInterceptor{logger: logger}
}

func (i *sseTokenInterceptor) Wrap(body io.ReadCloser) io.ReadCloser {
	if body == nil {
		i.complete = true
		return nil
	}
	return &sseReadCloser{
		original:    body,
		interceptor: i,
	}
}

func (i *sseTokenInterceptor) Result() (*TokenUsage, bool) {
	if i.result == nil && len(i.lastChunk) > 0 {
		i.result = parseTokenUsageWithLogger(i.lastChunk, i.logger)
	}
	return i.result, i.complete
}

// sseReadCloser parses SSE stream and keeps the last data chunk containing usage.
type sseReadCloser struct {
	original    io.ReadCloser
	interceptor *sseTokenInterceptor
	buf         []byte // Unparsed buffer
}

func (s *sseReadCloser) Read(p []byte) (int, error) {
	n, err := s.original.Read(p)
	if n > 0 {
		s.handleBufferOverflow(n)
		s.buf = append(s.buf, p[:n]...)
		s.extractLastData()
	}
	if err == io.EOF {
		s.interceptor.complete = true
	}
	return n, err
}

// handleBufferOverflow discards old data when buffer would exceed maxSSEBuffer.
func (s *sseReadCloser) handleBufferOverflow(newBytes int) {
	if len(s.buf)+newBytes <= maxSSEBuffer {
		return
	}
	// Discard old data, keep the newest
	excess := len(s.buf) + newBytes - maxSSEBuffer
	if s.interceptor.logger != nil {
		s.interceptor.logger.Debug("SSE buffer overflow, discarding old data",
			"excess", excess, "bufLen", len(s.buf), "newBytes", newBytes)
	}
	if excess < len(s.buf) {
		s.buf = s.buf[excess:]
	} else {
		s.buf = nil
	}
}

// sseDataPrefix is the SSE data line prefix.
var sseDataPrefix = []byte("data: ")

// sseUsageMarkers are the JSON keys that indicate usage data.
var sseUsageMarkers = [][]byte{
	[]byte(`"usage":`),
	[]byte(`"usageMetadata":`),
}

// sseDoneMarker is the OpenAI SSE stream end marker.
var sseDoneMarker = []byte("[DONE]")

// extractLastData extracts "data: {...}" lines and keeps the last one containing usage.
// Note: Claude format may be multi-line: event: message_stop\ndata: {...}\n\n
// We need to find the "data: " line within each chunk, not just check prefix.
func (s *sseReadCloser) extractLastData() {
	sseChunkSeparator := []byte("\n\n")
	processed := false
	for {
		idx := bytes.Index(s.buf, sseChunkSeparator)
		if idx < 0 {
			break
		}
		chunk := s.buf[:idx]
		s.buf = s.buf[idx+2:]
		processed = true

		// Find "data: " line within chunk (supports Claude multi-line format)
		dataIdx := bytes.Index(chunk, sseDataPrefix)
		if dataIdx < 0 {
			continue
		}

		// Extract content after "data: " until end of line
		lineStart := dataIdx + len(sseDataPrefix)
		lineEnd := bytes.IndexByte(chunk[lineStart:], '\n')
		var data []byte
		if lineEnd < 0 {
			data = chunk[lineStart:]
		} else {
			data = chunk[lineStart : lineStart+lineEnd]
		}

		// Skip OpenAI SSE stream end marker [DONE]
		if bytes.Equal(bytes.TrimSpace(data), sseDoneMarker) {
			continue
		}

		// Use more precise matching to avoid false positives from response content
		// e.g., "Let me explain the usage of..." should not trigger save
		for _, marker := range sseUsageMarkers {
			if bytes.Contains(data, marker) {
				s.interceptor.lastChunk = append(s.interceptor.lastChunk[:0], data...)
				break
			}
		}
	}

	// Prevent slice operations from keeping underlying array from being GC'd
	// When capacity is too large but actual usage is small, reallocate buffer
	// Added minBufferReallocCapacity threshold to avoid unnecessary reallocation for small buffers
	if processed && len(s.buf) > 0 && cap(s.buf) > maxSSEBuffer/4 && cap(s.buf) > minBufferReallocCapacity {
		newBuf := make([]byte, len(s.buf))
		copy(newBuf, s.buf)
		s.buf = newBuf
	}
}

func (s *sseReadCloser) Close() error {
	return s.original.Close()
}
