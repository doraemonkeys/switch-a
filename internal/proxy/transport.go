package proxy

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
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
	ConnectTimeout time.Duration
	ReadTimeout    time.Duration // 0 = no timeout
	SSEIdleTimeout time.Duration // 0 = no idle timeout (trust upstream)
}

// NewTransport creates a new transport with the given configuration.
func NewTransport(cfg TransportConfig) *Transport {
	// Create a custom transport with proper timeout handling:
	// - DialContext timeout: controls TCP connection establishment time
	// - ResponseHeaderTimeout: controls time to receive response headers after connection
	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout: cfg.ConnectTimeout,
		}).DialContext,
		ResponseHeaderTimeout: cfg.ConnectTimeout,
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
	StatusCode int
	Header     http.Header
	Body       io.ReadCloser
	isSSE      bool
}

// Close closes the upstream response body.
func (r *UpstreamResponse) Close() {
	if r.Body != nil {
		_ = r.Body.Close()
	}
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
		StatusCode: resp.StatusCode,
		Header:     resp.Header,
		Body:       resp.Body,
		isSSE:      isSSEResponse(resp),
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
	return t.forwardRegular(w, resp.Body)
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

// forwardRegular forwards a regular HTTP response body.
func (t *Transport) forwardRegular(w http.ResponseWriter, body io.Reader) error {
	_, err := io.Copy(w, body)
	return err
}

// idleWatchdog monitors SSE streams for idle timeout.
// It closes the body when no data is received within the timeout period,
// which interrupts any blocking Read() call and prevents goroutine leaks.
type idleWatchdog struct {
	closer  io.Closer
	timer   *time.Timer
	timeout time.Duration
	done    chan struct{}
	stopped chan struct{}
}

// newIdleWatchdog creates and starts an idle watchdog for SSE streams.
// The watchdog closes the body if no Reset() is called within the timeout.
// If timeout is 0, returns nil (no watchdog needed).
func newIdleWatchdog(ctx context.Context, closer io.Closer, timeout time.Duration) *idleWatchdog {
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

	go w.run(ctx)
	return w
}

// run is the watchdog goroutine. It exits when:
// - Context is cancelled
// - Timer fires (closes body to interrupt Read)
// - Stop() is called (normal completion)
func (w *idleWatchdog) run(ctx context.Context) {
	defer close(w.stopped)
	defer w.timer.Stop()

	select {
	case <-ctx.Done():
		// Context cancelled - body will be closed elsewhere
		return
	case <-w.done:
		// Normal completion - stop watching
		return
	case <-w.timer.C:
		// Idle timeout - close body to interrupt blocking Read()
		_ = w.closer.Close()
		return
	}
}

// Reset resets the idle timer. Call this after each successful read.
func (w *idleWatchdog) Reset() {
	if w == nil {
		return
	}
	// Stop and drain the timer, then reset
	if !w.timer.Stop() {
		select {
		case <-w.timer.C:
		default:
		}
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

// forwardSSE forwards a Server-Sent Events stream with flushing.
// It uses an idle watchdog to detect silent upstream connections and prevent goroutine leaks.
func (t *Transport) forwardSSE(ctx context.Context, w http.ResponseWriter, body io.ReadCloser) error {
	flusher, ok := w.(http.Flusher)
	if !ok { // coverage-ignore -- standard http.ResponseWriter always implements Flusher
		// Fallback to regular copy if flushing not supported
		return t.forwardRegular(w, body)
	}

	// Start idle watchdog if configured.
	// The watchdog closes body after timeout, interrupting the blocking Read().
	watchdog := newIdleWatchdog(ctx, body, t.sseIdleTimeout)
	defer watchdog.Stop()

	reader := bufio.NewReader(body)
	buf := make([]byte, sseBufferSize)

	for {
		// Note: ctx.Done() check here is ineffective once Read() blocks.
		// The watchdog handles timeout by closing body from another goroutine.
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
			if err == io.EOF {
				return nil
			}
			// Check if this is an idle timeout (body was closed by watchdog)
			if t.sseIdleTimeout > 0 && isClosedError(err) {
				return ErrSSEIdleTimeout
			}
			return err // coverage-ignore -- read errors during SSE are rare
		}
	}
}

// isClosedError checks if the error indicates the connection was closed.
// This happens when the idle watchdog closes the body to interrupt Read().
func isClosedError(err error) bool {
	// Common patterns for closed connection errors
	errStr := err.Error()
	return strings.Contains(errStr, "closed") ||
		strings.Contains(errStr, "EOF") ||
		strings.Contains(errStr, "reset")
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

	// Set Host header to upstream host
	req.Host = req.URL.Host

	return req, nil
}
