package proxy

import (
	"bytes"
	"compress/gzip"
	"io"
	"strings"
	"sync"
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

	// Detect and decompress gzip (non-SSE responses may be compressed)
	data = maybeDecompressGzip(data)

	i.result = parseTokenUsageWithLogger(data, i.logger)
	if i.result == nil && len(data) > 0 && i.logger != nil {
		i.logger.Debug("token usage parse failed",
			"data_len", len(data),
			"data_preview", truncateForLog(data, 100),
		)
	}
	return i.result, i.complete
}

// maybeDecompressGzip detects and decompresses gzip data.
// Returns original data if not gzip or decompression fails.
// gzip magic number: 0x1f 0x8b
func maybeDecompressGzip(data []byte) []byte {
	if len(data) < 2 || data[0] != 0x1f || data[1] != 0x8b {
		return data // Not gzip, return original
	}
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return data // Decompression failed, return original
	}
	defer reader.Close()
	decompressed, err := io.ReadAll(reader)
	if err != nil {
		return data // Read failed, return original
	}
	return decompressed
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
	mu              sync.Mutex
	wg              sync.WaitGroup
	result          *TokenUsage
	complete        bool
	lastChunk       []byte // Only keep the last data chunk containing usage
	logger          Logger
	contentEncoding string // Content-Encoding header value for decompression
}

// newSSETokenInterceptor creates a new interceptor for SSE streaming responses.
// contentEncoding should be the value of Content-Encoding header (e.g., "gzip", "").
func newSSETokenInterceptor(logger Logger, contentEncoding string) *sseTokenInterceptor {
	return &sseTokenInterceptor{
		logger:          logger,
		contentEncoding: contentEncoding,
	}
}

func (i *sseTokenInterceptor) Wrap(body io.ReadCloser) io.ReadCloser {
	if body == nil {
		i.setComplete(nil)
		return nil
	}

	// Non-gzip: use the original sseReadCloser processing
	if !strings.EqualFold(i.contentEncoding, "gzip") {
		return &sseReadCloser{
			original:    body,
			interceptor: i,
		}
	}

	// TeeReader passthrough: pass original compressed data to client
	// while parsing decompressed data in background goroutine
	pr, pw := io.Pipe()
	tee := io.TeeReader(body, pw)

	// Start background goroutine to decompress and parse SSE events
	i.wg.Add(1)
	go func() {
		defer i.wg.Done()

		gzReader, err := gzip.NewReader(pr)
		if err != nil {
			if i.logger != nil {
				i.logger.Debug("failed to create gzip reader for SSE", "error", err.Error())
			}
			_, _ = io.Copy(io.Discard, pr) // Drain pipe to prevent blocking
			i.setComplete(nil)             // Mark complete even on error
			return
		}
		defer gzReader.Close() // Only close gzReader, pw is closed by gzipPassthroughReadCloser.Close()

		i.parseSSEFromReader(gzReader)
	}()

	if i.logger != nil {
		i.logger.Debug("SSE stream gzip passthrough enabled with TeeReader")
	}

	return &gzipPassthroughReadCloser{
		original: body,
		tee:      tee,
		pw:       pw,
	}
}

// setComplete marks the interceptor as complete with the given result (thread-safe).
func (i *sseTokenInterceptor) setComplete(result *TokenUsage) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.result = result
	i.complete = true
}

func (i *sseTokenInterceptor) Result() (*TokenUsage, bool) {
	i.mu.Lock()
	defer i.mu.Unlock()

	if i.result == nil && len(i.lastChunk) > 0 {
		i.result = parseTokenUsageWithLogger(i.lastChunk, i.logger)
		// Log if parsing failed despite having data
		if i.result == nil && i.logger != nil {
			i.logger.Debug("SSE token usage parse failed",
				"lastChunk_len", len(i.lastChunk),
				"lastChunk_preview", truncateForLog(i.lastChunk, 200),
			)
		}
	} else if i.result == nil && len(i.lastChunk) == 0 && i.complete && i.logger != nil {
		// Log when no usage data was found in the entire SSE stream
		// This could happen if: 1) upstream doesn't return usage, 2) SSE format mismatch
		i.logger.Debug("SSE stream completed but no usage data found",
			"hint", "upstream may not return usage in message_delta, or SSE format mismatch")
	}
	return i.result, i.complete
}

// Wait blocks until background goroutine completes (for gzip passthrough).
// For non-gzip cases, wg.Add(1) is never called, so Wait() returns immediately.
func (i *sseTokenInterceptor) Wait() {
	i.wg.Wait()
}

// parseSSEFromReader reads and parses SSE events from the given reader.
// Used by the background goroutine for gzip passthrough.
func (i *sseTokenInterceptor) parseSSEFromReader(r io.Reader) {
	buf := make([]byte, regularReadBufferSize)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			i.processChunk(buf[:n])
		}
		if err != nil {
			if err != io.EOF && i.logger != nil {
				i.logger.Debug("SSE parse goroutine read error", "error", err.Error())
			}
			break
		}
	}
	// Read result under lock to avoid data race with processChunk
	i.mu.Lock()
	result := i.result
	i.mu.Unlock()
	i.setComplete(result)
}

// processChunk processes a chunk of SSE data.
// This is extracted for reuse between sseReadCloser and parseSSEFromReader.
func (i *sseTokenInterceptor) processChunk(data []byte) {
	i.mu.Lock()
	defer i.mu.Unlock()

	// Handle buffer overflow
	if len(i.lastChunk)+len(data) > maxSSEBuffer {
		excess := len(i.lastChunk) + len(data) - maxSSEBuffer
		if i.logger != nil {
			i.logger.Debug("SSE buffer overflow in processChunk, discarding old data",
				"excess", excess, "bufLen", len(i.lastChunk), "newBytes", len(data))
		}
		if excess < len(i.lastChunk) {
			i.lastChunk = i.lastChunk[excess:]
		} else {
			i.lastChunk = nil
		}
	}

	i.lastChunk = append(i.lastChunk, data...)
	i.extractLastDataLocked()
}

// extractLastDataLocked extracts usage data from the buffer.
// Must be called with mu held.
func (i *sseTokenInterceptor) extractLastDataLocked() {
	for {
		idx, sepLen := findSSEChunkSeparator(i.lastChunk)
		if idx < 0 {
			break
		}
		chunk := i.lastChunk[:idx]
		i.lastChunk = i.lastChunk[idx+sepLen:]

		// Extract data content from SSE chunk
		data := extractSSEDataContent(chunk)
		if data == nil {
			continue
		}

		if containsUsageMarker(data) {
			// Found usage data, save and parse immediately
			savedData := make([]byte, len(data))
			copy(savedData, data)
			if i.logger != nil {
				i.logger.Debug("SSE usage data captured (processChunk)",
					"data_preview", truncateForLog(data, 300),
				)
			}
			i.result = parseTokenUsageWithLogger(savedData, i.logger)
		}
	}
}

// gzipPassthroughReadCloser passes through compressed data to client
// while simultaneously writing to a pipe for background decompression.
type gzipPassthroughReadCloser struct {
	original io.ReadCloser
	tee      io.Reader
	pw       *io.PipeWriter // Closed only in Close()
}

func (g *gzipPassthroughReadCloser) Read(p []byte) (int, error) {
	return g.tee.Read(p)
}

func (g *gzipPassthroughReadCloser) Close() error {
	_ = g.pw.Close() // Close pw to signal goroutine to exit
	return g.original.Close()
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
		// Process any remaining data in buffer at EOF
		// The last SSE chunk may not have a trailing \n\n separator
		s.processRemainingBuffer()
		s.interceptor.setComplete(nil) // use thread-safe setter
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

// extractSSEDataContent extracts the data content from an SSE chunk.
// Returns nil if the chunk should be skipped (no data prefix, or [DONE] marker).
// Returns the data bytes if valid data is found.
func extractSSEDataContent(chunk []byte) []byte {
	// Find "data: " line within chunk (supports Claude multi-line format)
	dataIdx := bytes.Index(chunk, sseDataPrefix)
	if dataIdx < 0 {
		return nil // skip - no data prefix
	}

	// Extract content after "data: " until end of line (handle both \n and \r\n)
	lineStart := dataIdx + len(sseDataPrefix)
	lineEnd := bytes.IndexAny(chunk[lineStart:], "\r\n")
	var data []byte
	if lineEnd < 0 {
		data = chunk[lineStart:]
	} else {
		data = chunk[lineStart : lineStart+lineEnd]
	}

	// Skip OpenAI SSE stream end marker [DONE]
	if bytes.Equal(bytes.TrimSpace(data), sseDoneMarker) {
		return nil // skip
	}

	return data
}

// containsUsageMarker checks if data contains any usage marker.
func containsUsageMarker(data []byte) bool {
	for _, marker := range sseUsageMarkers {
		if bytes.Contains(data, marker) {
			return true
		}
	}
	return false
}

// SSE chunk separators - both \n\n and \r\n\r\n are valid per SSE spec
var (
	sseChunkSeparatorLF   = []byte("\n\n")
	sseChunkSeparatorCRLF = []byte("\r\n\r\n")
)

// extractAndSaveUsageData extracts data from an SSE chunk and saves it if it contains usage.
// Returns true if the chunk should be skipped (e.g., [DONE] marker or no data prefix).
func (s *sseReadCloser) extractAndSaveUsageData(chunk []byte, logSource string) bool {
	// Extract data content from SSE chunk
	data := extractSSEDataContent(chunk)
	if data == nil {
		return true // skip
	}

	// Use more precise matching to avoid false positives from response content
	// e.g., "Let me explain the usage of..." should not trigger save
	if containsUsageMarker(data) {
		// Lock when modifying lastChunk for thread-safety and consistency with gzip path
		s.interceptor.mu.Lock()
		s.interceptor.lastChunk = append(s.interceptor.lastChunk[:0], data...)
		if s.interceptor.logger != nil {
			s.interceptor.logger.Debug("SSE usage data captured",
				"source", logSource,
				"data_preview", truncateForLog(data, 300),
			)
		}
		s.interceptor.mu.Unlock()
	}
	return false
}

// findSSEChunkSeparator finds the first SSE chunk separator in data.
// Returns the index and length of the separator, or (-1, 0) if not found.
// Supports both \n\n (Unix) and \r\n\r\n (Windows/HTTP) formats.
func findSSEChunkSeparator(data []byte) (idx int, sepLen int) {
	idxLF := bytes.Index(data, sseChunkSeparatorLF)
	idxCRLF := bytes.Index(data, sseChunkSeparatorCRLF)

	// Neither found
	if idxLF < 0 && idxCRLF < 0 {
		return -1, 0
	}
	// Only LF found
	if idxCRLF < 0 {
		return idxLF, 2
	}
	// Only CRLF found
	if idxLF < 0 {
		return idxCRLF, 4
	}
	// Both found - return the earlier one
	if idxCRLF < idxLF {
		return idxCRLF, 4
	}
	return idxLF, 2
}

// extractLastData extracts "data: {...}" lines and keeps the last one containing usage.
// Note: Claude format may be multi-line: event: message_stop\ndata: {...}\n\n
// We need to find the "data: " line within each chunk, not just check prefix.
func (s *sseReadCloser) extractLastData() {
	// SSE spec allows both \n\n and \r\n\r\n as chunk separators
	// Find the first occurrence of either separator
	processed := false
	for {
		idx, sepLen := findSSEChunkSeparator(s.buf)
		if idx < 0 {
			break
		}
		chunk := s.buf[:idx]
		s.buf = s.buf[idx+sepLen:]
		processed = true

		s.extractAndSaveUsageData(chunk, "stream")
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

// processRemainingBuffer processes any remaining data in the buffer at EOF.
// This handles the case where the last SSE chunk doesn't have a trailing \n\n separator.
func (s *sseReadCloser) processRemainingBuffer() {
	if len(s.buf) == 0 {
		return
	}

	// Treat remaining buffer as a complete chunk
	chunk := s.buf
	s.buf = nil

	s.extractAndSaveUsageData(chunk, "remaining_buffer")
}

func (s *sseReadCloser) Close() error {
	return s.original.Close()
}
