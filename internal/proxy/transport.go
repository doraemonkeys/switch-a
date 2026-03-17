package proxy

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"switch-a/internal/defaults"
)

// sseBufferSize is the buffer size for reading SSE streams.
// The 4096 byte (4KB) value balances memory efficiency with throughput:
// - Small enough to avoid excessive memory allocation per connection
// - Large enough to minimize syscall overhead for typical SSE event sizes
// - Aligned with common OS page sizes for efficient I/O operations
const sseBufferSize = 4096

// Transport handles HTTP request forwarding with SSE support.
type Transport struct {
	client         *http.Client
	connectTimeout time.Duration
	readTimeout    time.Duration
	sseIdleTimeout time.Duration
}

// TransportConfig holds transport configuration.
type TransportConfig struct {
	ConnectTimeout   time.Duration // TCP connection establishment timeout
	FirstByteTimeout time.Duration // Time to wait for first response byte (0 = no timeout)
	ReadTimeout      time.Duration // Idle timeout during data transfer (0 = no timeout)
	SSEIdleTimeout   time.Duration // SSE stream idle timeout (0 = no idle timeout, trust upstream)
}

// NewTransport creates a new transport with the given configuration.
func NewTransport(cfg TransportConfig) *Transport {
	// ResponseHeaderTimeout controls time to receive response headers (first byte) after connection.
	// This is now separate from ReadTimeout (idle timeout during data transfer) to support
	// AI model inference scenarios where:
	// - The model may take 60+ seconds to start responding (needs longer first byte timeout)
	// - But once started, data should flow steadily (shorter idle timeout is appropriate)
	//
	// When FirstByteTimeout is 0, we don't set ResponseHeaderTimeout,
	// allowing long-running requests to complete without being prematurely terminated.
	responseHeaderTimeout := cfg.FirstByteTimeout

	// Create a custom transport with proper timeout handling:
	// - DialContext timeout: controls TCP connection establishment time
	// - ResponseHeaderTimeout: controls time to receive first response byte after connection
	// - Connection pool settings: essential for proxy performance
	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   cfg.ConnectTimeout,
			KeepAlive: defaults.TCPKeepAlive,
		}).DialContext,
		ResponseHeaderTimeout: responseHeaderTimeout,

		// Connection pool configuration - critical for proxy performance.
		// Default MaxIdleConnsPerHost is only 2, which causes excessive connection churn.
		MaxIdleConns:        defaults.MaxIdleConns,
		MaxIdleConnsPerHost: defaults.MaxIdleConnsPerHost,
		IdleConnTimeout:     defaults.IdleConnTimeout,
		TLSHandshakeTimeout: defaults.TLSHandshakeTimeout,

		// Disable compression to avoid decompress/compress overhead in proxy scenarios.
		// The upstream response is passed through as-is to the client.
		DisableCompression: true,
	}

	client := &http.Client{
		Transport: transport,
		// Don't set client timeout here - it would affect SSE streams
		// We handle timeouts at the transport level and in stream reading
	}

	return &Transport{
		client:         client,
		connectTimeout: cfg.ConnectTimeout,
		readTimeout:    cfg.ReadTimeout,
		sseIdleTimeout: cfg.SSEIdleTimeout,
	}
}

// Do executes an HTTP request and returns the response.
// For SSE responses, the caller is responsible for reading the body.
func (t *Transport) Do(req *http.Request) (*http.Response, error) {
	return t.client.Do(req)
}

// CloseIdleConnections closes any idle connections in the underlying transport.
// This should be called when the transport is no longer needed to prevent connection leaks.
func (t *Transport) CloseIdleConnections() {
	t.client.CloseIdleConnections()
}

// UpstreamResponse holds the response from an upstream server.
// The caller must call Close() when done, regardless of whether WriteToClient was called.
type UpstreamResponse struct {
	StatusCode    int
	Header        http.Header
	Body          io.ReadCloser
	ContentLength int64 // -1 if unknown (chunked transfer encoding)
	isSSE         bool
}

// maxDrainBytes limits how much we drain from response body before close.
// This prevents memory exhaustion from large error responses while still
// enabling connection reuse for typical API error responses.
const maxDrainBytes = 64 * 1024 // 64KB

// Close closes the upstream response body.
// For better connection reuse when retrying, use Drain() instead.
func (r *UpstreamResponse) Close() {
	if r.Body != nil {
		_ = r.Body.Close()
	}
}

// Drain reads and discards the response body before closing.
// This enables HTTP connection reuse, which is important for retry scenarios.
// Without draining, Go's http.Client cannot reuse the connection.
func (r *UpstreamResponse) Drain() {
	if r.Body == nil {
		return
	}
	// Drain up to maxDrainBytes to enable connection reuse without risking OOM.
	// LimitReader ensures we don't read more than the limit even for large responses.
	_, _ = io.Copy(io.Discard, io.LimitReader(r.Body, maxDrainBytes))
	_ = r.Body.Close()
}

// maxSnippetBytes is the default size for response body snippets captured during failover.
const maxSnippetBytes = 512

func normalizeSnippetLimit(maxSnippet int) int {
	if maxSnippet <= 0 {
		return maxSnippetBytes
	}
	return maxSnippet
}

// drainReadCloserWithSnippet captures the leading bytes from an HTTP response body,
// then drains a bounded amount of the remainder before closing. Sharing this helper
// keeps diagnostics consistent between plain HTTP failures and WebSocket handshake
// rejections, both of which surface provider errors as HTTP responses.
func drainReadCloserWithSnippet(body io.ReadCloser, maxSnippet int) string {
	if body == nil {
		return ""
	}
	maxSnippet = normalizeSnippetLimit(maxSnippet)

	snippet := make([]byte, maxSnippet)
	n, _ := io.ReadFull(body, snippet)

	remaining := maxDrainBytes - int64(n)
	if remaining > 0 {
		_, _ = io.Copy(io.Discard, io.LimitReader(body, remaining))
	}
	_ = body.Close()

	return string(snippet[:n])
}

// limitedBuffer captures up to `limit` bytes, discarding excess.
// Used by TeeBody to capture response snippets while forwarding to client.
type limitedBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool // true if any data was discarded due to limit
}

func (lb *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p) // Always return original length to satisfy io.Writer
	remaining := lb.limit - lb.buf.Len()
	if remaining <= 0 {
		lb.truncated = true
		return n, nil // Already full, discard but return success
	}
	if len(p) > remaining {
		lb.truncated = true
		p = p[:remaining]
	}
	lb.buf.Write(p)
	return n, nil
}

// String returns the captured content, with truncation marker if data was discarded.
func (lb *limitedBuffer) String() string {
	if lb.truncated {
		return lb.buf.String() + "...[truncated]"
	}
	return lb.buf.String()
}

// teeReadCloser wraps ReadCloser with TeeReader, preserving Close behavior.
// Critical: Do not use io.NopCloser here; the original Body must be closed
// to prevent connection leaks.
type teeReadCloser struct {
	original io.ReadCloser
	tee      io.Reader
}

func (t *teeReadCloser) Read(p []byte) (int, error) {
	return t.tee.Read(p)
}

func (t *teeReadCloser) Close() error {
	return t.original.Close()
}

// DrainWithSnippet reads the first maxSnippet bytes of the response body for debugging,
// then drains the remaining content and closes. This is used in failover scenarios
// where the response would otherwise be discarded, allowing us to capture error details
// without affecting normal request flow.
//
// Returns the captured snippet as a string. If maxSnippet is 0, uses default size.
func (r *UpstreamResponse) DrainWithSnippet(maxSnippet int) string {
	return drainReadCloserWithSnippet(r.Body, maxSnippet)
}

// TeeBody wraps the response body with a TeeReader that captures the first maxBytes
// into the returned buffer. The original body can still be fully read/forwarded.
// This is used for non-failover 4xx errors where we need to both forward the response
// to the client AND capture a snippet for diagnostics.
//
// If maxBytes is 0, uses default size (maxSnippetBytes).
func (r *UpstreamResponse) TeeBody(maxBytes int) *limitedBuffer {
	if r.Body == nil {
		return nil
	}
	maxBytes = normalizeSnippetLimit(maxBytes)
	lb := &limitedBuffer{limit: maxBytes}
	r.Body = &teeReadCloser{
		original: r.Body,
		tee:      io.TeeReader(r.Body, lb),
	}
	return lb
}

// IsSSE returns true if the response is a Server-Sent Events stream.
func (r *UpstreamResponse) IsSSE() bool {
	return r.isSSE
}

// FetchUpstream sends a request to the upstream server and returns the response
// without writing to the client. This allows the caller to inspect the status code
// and decide whether to retry before committing to the response.
//
// The caller MUST call UpstreamResponse.Close() when done.
func (t *Transport) FetchUpstream(ctx context.Context, upstreamReq *http.Request) (*UpstreamResponse, error) {
	upstreamReq = upstreamReq.WithContext(ctx)

	resp, err := t.client.Do(upstreamReq) //nolint:bodyclose // Body is returned to caller via UpstreamResponse; caller must call Close()
	if err != nil {                       // coverage-ignore -- network errors are tested at integration level
		return nil, err
	}

	return &UpstreamResponse{
		StatusCode:    resp.StatusCode,
		Header:        resp.Header,
		Body:          resp.Body,
		ContentLength: resp.ContentLength, // -1 for chunked/unknown
		isSSE:         isSSEResponse(resp),
	}, nil
}

// WriteToClient writes the upstream response to the client.
// This should be called after deciding not to retry.
func (t *Transport) WriteToClient(ctx context.Context, w http.ResponseWriter, resp *UpstreamResponse) error {
	// Copy response headers
	copyResponseHeaders(w.Header(), resp.Header)

	// Write status code
	w.WriteHeader(resp.StatusCode)

	// Forward body
	if resp.isSSE {
		return t.forwardSSE(ctx, w, resp.Body)
	}
	return t.forwardRegular(ctx, w, resp.Body)
}

// ErrSSEIdleTimeout is returned when an SSE stream times out due to inactivity.
var ErrSSEIdleTimeout = &sseIdleTimeoutError{}

type sseIdleTimeoutError struct{}

func (e *sseIdleTimeoutError) Error() string {
	return "SSE stream idle timeout: no data received within timeout period"
}

// isSSEResponse checks if the response is Server-Sent Events.
func isSSEResponse(resp *http.Response) bool {
	contentType := resp.Header.Get("Content-Type")
	return strings.Contains(contentType, "text/event-stream")
}

// copyResponseHeaders copies response headers to the client response.
func copyResponseHeaders(dst, src http.Header) {
	for key, values := range src {
		// Skip hop-by-hop headers
		if hopByHopHeaders[key] {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

// ErrReadTimeout is returned when reading the response body times out due to inactivity.
var ErrReadTimeout = &readTimeoutError{}

type readTimeoutError struct{}

func (e *readTimeoutError) Error() string {
	return "upstream read timeout: no data received within timeout period"
}

// regularReadBufferSize is the buffer size for reading regular response bodies.
const regularReadBufferSize = 32 * 1024 // 32KB

// forwardRegular forwards a regular HTTP response body with optional idle timeout.
// Unlike a total timeout, idle timeout only triggers when no data is received
// for the configured duration, allowing large responses to complete normally
// as long as data keeps flowing.
//
// Uses the same watchdog pattern as forwardSSE to prevent goroutine leaks:
// closing the body interrupts blocking Read() calls.
func (t *Transport) forwardRegular(ctx context.Context, w http.ResponseWriter, body io.ReadCloser) error {
	if t.readTimeout <= 0 {
		// No timeout configured, use simple copy
		_, err := io.Copy(w, body)
		return err
	}

	// Handle context cancellation by closing body to unblock Read().
	// This is separate from idle timeout - context cancellation is mandatory.
	ctxDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = body.Close() // Unblock blocking Read()
		case <-ctxDone:
			// Normal exit, body closed elsewhere
		}
	}()
	defer close(ctxDone)

	// Start idle watchdog if configured.
	// The watchdog closes body after timeout, interrupting the blocking Read().
	// Note: Both the context goroutine above and the watchdog may close body,
	// but body.Close() is idempotent so this is safe.
	watchdog := newIdleWatchdog(body, t.readTimeout)
	defer watchdog.Stop()

	buf := make([]byte, regularReadBufferSize)

	for {
		// Note: ctx.Done() check here is ineffective once Read() blocks.
		// The context cancellation goroutine above handles this by closing body.
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		n, err := body.Read(buf)
		if n > 0 {
			// Data received - reset idle watchdog timer
			watchdog.Reset()

			if _, writeErr := w.Write(buf[:n]); writeErr != nil {
				return writeErr
			}
		}

		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil // Normal completion
			}
			// Check if context was cancelled (body closed by context goroutine).
			if ctx.Err() != nil {
				return ctx.Err()
			}
			// Check if this is an idle timeout (body was closed by watchdog).
			if watchdog.TimedOut() {
				return ErrReadTimeout
			}
			// Wrap as upstream read error to distinguish from client write errors
			return NewUpstreamReadError(err)
		}
	}
}

// idleWatchdog monitors SSE streams for idle timeout.
// It closes the body when no data is received within the timeout period,
// which interrupts any blocking Read() call and prevents goroutine leaks.
type idleWatchdog struct {
	closer       io.Closer
	timer        *time.Timer
	timeout      time.Duration
	done         chan struct{}
	stopped      chan struct{}
	timedOut     bool // set to true when watchdog closes connection due to idle timeout
	timedOutLock sync.Mutex
}

// newIdleWatchdog creates and starts an idle watchdog for SSE streams.
// The watchdog closes the body if no Reset() is called within the timeout.
// If timeout is 0, returns nil (no watchdog needed).
func newIdleWatchdog(closer io.Closer, timeout time.Duration) *idleWatchdog {
	if timeout <= 0 {
		return nil
	}

	w := &idleWatchdog{
		closer:  closer,
		timer:   time.NewTimer(timeout),
		timeout: timeout,
		done:    make(chan struct{}),
		stopped: make(chan struct{}),
	}

	go w.run()
	return w
}

// run is the watchdog goroutine. It exits when:
// - Timer fires (closes body to interrupt Read)
// - Stop() is called (normal completion)
// Note: Context cancellation is handled by a separate goroutine in forwardSSE.
func (w *idleWatchdog) run() {
	defer close(w.stopped)
	defer w.timer.Stop()

	select {
	case <-w.done:
		// Normal completion - stop watching
		return
	case <-w.timer.C:
		// Idle timeout - set flag and close body to interrupt blocking Read()
		w.timedOutLock.Lock()
		w.timedOut = true
		w.timedOutLock.Unlock()
		_ = w.closer.Close()
		return
	}
}

// Reset resets the idle timer. Call this after each successful read.
// Note: We simply reset the timer without stop-and-drain. If the timer already
// fired, run() has already exited and closed the body, so subsequent reads will
// fail anyway with ErrSSEIdleTimeout — which is the expected idle timeout behavior.
func (w *idleWatchdog) Reset() {
	if w == nil {
		return
	}
	w.timer.Reset(w.timeout)
}

// Stop signals the watchdog to stop and waits for it to exit.
// Call this when the SSE stream completes normally.
func (w *idleWatchdog) Stop() {
	if w == nil {
		return
	}
	close(w.done)
	<-w.stopped // Wait for goroutine to exit
}

// TimedOut returns true if the watchdog closed the connection due to idle timeout.
// This is more reliable than checking error strings, as it uses a flag set by the watchdog.
func (w *idleWatchdog) TimedOut() bool {
	if w == nil {
		return false
	}
	w.timedOutLock.Lock()
	defer w.timedOutLock.Unlock()
	return w.timedOut
}

// forwardSSE forwards a Server-Sent Events stream with flushing.
// It uses an idle watchdog to detect silent upstream connections and prevent goroutine leaks.
func (t *Transport) forwardSSE(ctx context.Context, w http.ResponseWriter, body io.ReadCloser) error {
	flusher, ok := w.(http.Flusher)
	if !ok { // coverage-ignore -- standard http.ResponseWriter always implements Flusher
		// Fallback to regular copy if flushing not supported
		return t.forwardRegular(ctx, w, body)
	}

	// Always handle context cancellation to close body and unblock Read().
	// This is separate from idle timeout - context cancellation is mandatory.
	// Without this, when SSEIdleTimeout=0 (default), Read() would block indefinitely
	// after client disconnects, causing requests to linger in the active registry.
	ctxDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = body.Close() // Unblock blocking Read()
		case <-ctxDone:
			// Normal exit, body closed elsewhere
		}
	}()
	defer close(ctxDone)

	// Start idle watchdog if configured.
	// The watchdog closes body after timeout, interrupting the blocking Read().
	// Note: Both the context goroutine above and the watchdog may close body,
	// but body.Close() is idempotent so this is safe.
	watchdog := newIdleWatchdog(body, t.sseIdleTimeout)
	defer watchdog.Stop()

	reader := bufio.NewReader(body)
	buf := make([]byte, sseBufferSize)

	for {
		// Note: ctx.Done() check here is ineffective once Read() blocks.
		// The context cancellation goroutine above handles this by closing body.
		select {
		case <-ctx.Done(): // coverage-ignore -- context cancellation tested at integration level
			return ctx.Err()
		default:
		}

		n, err := reader.Read(buf)
		if n > 0 {
			// Data received - reset idle watchdog timer
			watchdog.Reset()

			if _, writeErr := w.Write(buf[:n]); writeErr != nil { // coverage-ignore -- write errors occur when client disconnects
				return writeErr
			}
			flusher.Flush()
		}

		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			// Check if context was cancelled (body closed by context goroutine).
			if ctx.Err() != nil {
				return ctx.Err()
			}
			// Check if this is an idle timeout (body was closed by watchdog).
			// Use the watchdog's flag instead of error string matching for reliability.
			// String matching (e.g., checking for "closed", "EOF", "reset") is fragile
			// because real network errors may contain the same patterns and would be
			// incorrectly classified as idle timeouts, affecting circuit breaker stats.
			if watchdog.TimedOut() {
				return ErrSSEIdleTimeout
			}
			// Wrap as upstream read error to distinguish from client write errors
			return NewUpstreamReadError(err) // coverage-ignore -- read errors during SSE are rare
		}
	}
}

// BuildUpstreamRequest creates an HTTP request for the upstream server.
func BuildUpstreamRequest(ctx context.Context, method, upstreamURL string, body []byte, originalReq *http.Request) (*http.Request, error) {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, upstreamURL, bodyReader)
	if err != nil { // coverage-ignore -- only fails with invalid URL/method, which are validated earlier
		return nil, err
	}

	// Copy filtered headers
	CopyHeaders(req.Header, originalReq.Header)
	EnsureExplicitUserAgentHeader(req.Header)

	// Set Host header to upstream host
	req.Host = req.URL.Host

	return req, nil
}
