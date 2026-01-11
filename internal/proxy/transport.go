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
}

// TransportConfig holds transport configuration.
type TransportConfig struct {
	ConnectTimeout time.Duration
	ReadTimeout    time.Duration // 0 = no timeout
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

// ForwardRequest forwards a request to the upstream server and writes the response to w.
// Returns whether the response headers have been written (affects retry capability).
//
// Deprecated: Use FetchUpstream + WriteToClient for retry-aware forwarding.
// This method is kept for backward compatibility but immediately writes headers.
func (t *Transport) ForwardRequest(ctx context.Context, w http.ResponseWriter, upstreamReq *http.Request) (headersWritten bool, statusCode int, err error) {
	resp, err := t.FetchUpstream(ctx, upstreamReq)
	if err != nil {
		return false, 0, err
	}
	defer resp.Close()

	err = t.WriteToClient(ctx, w, resp)
	return true, resp.StatusCode, err
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

// forwardSSE forwards a Server-Sent Events stream with flushing.
func (t *Transport) forwardSSE(ctx context.Context, w http.ResponseWriter, body io.Reader) error {
	flusher, ok := w.(http.Flusher)
	if !ok { // coverage-ignore -- standard http.ResponseWriter always implements Flusher
		// Fallback to regular copy if flushing not supported
		return t.forwardRegular(w, body)
	}

	reader := bufio.NewReader(body)
	buf := make([]byte, sseBufferSize)

	for {
		select {
		case <-ctx.Done(): // coverage-ignore -- context cancellation tested at integration level
			return ctx.Err()
		default:
		}

		// Set read deadline if configured
		// Note: this only works if body implements net.Conn, which typically it doesn't
		// For SSE, readTimeout of 0 (no timeout) is recommended

		n, err := reader.Read(buf)
		if n > 0 {
			if _, writeErr := w.Write(buf[:n]); writeErr != nil { // coverage-ignore -- write errors occur when client disconnects
				return writeErr
			}
			flusher.Flush()
		}

		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err // coverage-ignore -- read errors during SSE are rare
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

	// Set Host header to upstream host
	req.Host = req.URL.Host

	return req, nil
}
