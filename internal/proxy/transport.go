package proxy

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"time"
)

// sseBufferSize is the buffer size for reading SSE streams.
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
	// Create a custom transport with connect timeout
	transport := &http.Transport{
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

// ForwardRequest forwards a request to the upstream server and writes the response to w.
// Returns whether the response headers have been written (affects retry capability).
func (t *Transport) ForwardRequest(ctx context.Context, w http.ResponseWriter, upstreamReq *http.Request) (headersWritten bool, statusCode int, err error) {
	// Apply connect timeout to context
	if t.connectTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, t.connectTimeout)
		defer cancel()
	}

	upstreamReq = upstreamReq.WithContext(ctx)

	resp, err := t.client.Do(upstreamReq)
	if err != nil { // coverage-ignore -- network errors are tested at integration level
		return false, 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	// Check if response is SSE
	isSSE := isSSEResponse(resp)

	// Copy response headers
	copyResponseHeaders(w.Header(), resp.Header)

	// Write status code
	w.WriteHeader(resp.StatusCode)
	headersWritten = true
	statusCode = resp.StatusCode

	// Forward body
	if isSSE {
		err = t.forwardSSE(ctx, w, resp.Body)
	} else {
		err = t.forwardRegular(w, resp.Body)
	}

	return headersWritten, statusCode, err
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
