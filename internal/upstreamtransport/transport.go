// Package upstreamtransport owns upstream HTTP exchange establishment and
// response-head normalization. Response bodies leave this package exactly once
// and are consumed by responseanalysis, which is the sole runtime reader.
package upstreamtransport

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/doraemonkeys/switch-a/internal/defaults"
)

var ErrBodyTransferred = errors.New("upstream response body already transferred")

type Config struct {
	ConnectTimeout   time.Duration
	FirstByteTimeout time.Duration
}

type Transport struct {
	client *http.Client
}

func New(config Config) *Transport {
	roundTripper := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   config.ConnectTimeout,
			KeepAlive: defaults.TCPKeepAlive,
		}).DialContext,
		ResponseHeaderTimeout: config.FirstByteTimeout,
		MaxIdleConns:          defaults.MaxIdleConns,
		MaxIdleConnsPerHost:   defaults.MaxIdleConnsPerHost,
		IdleConnTimeout:       defaults.IdleConnTimeout,
		TLSHandshakeTimeout:   defaults.TLSHandshakeTimeout,
		// Raw response identity is an executor invariant; implicit decompression
		// would make Content-Encoding and client bytes disagree.
		DisableCompression: true,
	}
	return &Transport{client: &http.Client{Transport: roundTripper}}
}

func (t *Transport) CloseIdleConnections() {
	if t != nil && t.client != nil {
		t.client.CloseIdleConnections()
	}
}

// ResponseHead is a value snapshot. SourceHeader records the exchange as
// received while Header is normalized for the downstream writer.
type ResponseHead struct {
	StatusCode    int
	Protocol      string
	SourceHeader  http.Header
	Header        http.Header
	Trailer       http.Header
	ContentLength int64
	EventStream   bool
}

// Response couples immutable head facts with a move-only body capability.
type Response struct {
	head ResponseHead

	transferMu sync.Mutex
	body       io.ReadCloser
}

func (r *Response) Head() ResponseHead {
	if r == nil {
		return ResponseHead{}
	}
	r.transferMu.Lock()
	defer r.transferMu.Unlock()
	head := r.head
	head.SourceHeader = cloneHeader(head.SourceHeader)
	head.Header = cloneHeader(head.Header)
	head.Trailer = cloneHeader(head.Trailer)
	return head
}

func (r *Response) IsSSE() bool {
	return r != nil && r.head.EventStream
}

// TakeBody transfers the only body capability. A second caller cannot create a
// competing reader or close path.
func (r *Response) TakeBody() (io.ReadCloser, error) {
	if r == nil {
		return nil, ErrBodyTransferred
	}
	r.transferMu.Lock()
	defer r.transferMu.Unlock()
	if r.body == nil {
		return nil, ErrBodyTransferred
	}
	body := r.body
	r.body = nil
	// A caller choosing the body-only API relinquishes trailer observation.
	// Retaining the live map here would race once net/http starts populating it.
	r.head.Trailer = nil
	return body, nil
}

// Take transfers the body together with the trailer map populated by net/http
// during reads. Moving them atomically prevents a caller from snapshotting empty
// declared trailers while another owner consumes the stream.
func (r *Response) Take() (ResponseHead, io.ReadCloser, error) {
	if r == nil {
		return ResponseHead{}, nil, ErrBodyTransferred
	}
	r.transferMu.Lock()
	defer r.transferMu.Unlock()
	if r.body == nil {
		return ResponseHead{}, nil, ErrBodyTransferred
	}
	head := r.head
	head.SourceHeader = cloneHeader(head.SourceHeader)
	head.Header = cloneHeader(head.Header)
	// Trailer remains live until the sole body reader reaches EOF.
	r.head.Trailer = nil
	body := r.body
	r.body = nil
	return head, body, nil
}

func (t *Transport) Fetch(ctx context.Context, request *http.Request) (*Response, error) {
	if t == nil || t.client == nil {
		return nil, errors.New("upstream transport is not initialized")
	}
	if request == nil {
		return nil, errors.New("upstream request is required")
	}
	if ctx == nil {
		return nil, errors.New("upstream request context is required")
	}
	response, err := t.client.Do(request.WithContext(ctx)) //nolint:bodyclose // ownership moves through Response.TakeBody
	if err != nil {
		return nil, err
	}
	sourceHeader := response.Header.Clone()
	clientHeader := downstreamHeader(response)
	return NewResponse(ResponseHead{
		StatusCode:    response.StatusCode,
		Protocol:      response.Proto,
		SourceHeader:  sourceHeader,
		Header:        clientHeader,
		Trailer:       response.Trailer,
		ContentLength: response.ContentLength,
		EventStream:   isSSE(sourceHeader),
	}, response.Body)
}

// NewResponse establishes the body ownership boundary for custom transports and
// deterministic tests without exposing the body after construction.
func NewResponse(head ResponseHead, body io.ReadCloser) (*Response, error) {
	if body == nil {
		return nil, errors.New("upstream response body is required")
	}
	head.SourceHeader = cloneHeader(head.SourceHeader)
	head.Header = cloneHeader(head.Header)
	return &Response{head: head, body: body}, nil
}

func BuildRequest(
	ctx context.Context,
	method string,
	upstreamURL string,
	body []byte,
	original *http.Request,
) (*http.Request, error) {
	if original == nil {
		return nil, errors.New("original request is required")
	}
	var source io.Reader
	if body != nil {
		source = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, upstreamURL, source)
	if err != nil {
		return nil, err
	}
	copyRequestHeaders(request.Header, original.Header)
	if _, present := request.Header["User-Agent"]; !present {
		request.Header["User-Agent"] = nil
	}
	request.Host = request.URL.Host
	return request, nil
}

var hopByHopResponseHeaders = map[string]struct{}{
	"Connection":          {},
	"Keep-Alive":          {},
	"Proxy-Authenticate":  {},
	"Proxy-Authorization": {},
	"Proxy-Connection":    {},
	"Te":                  {},
	"Transfer-Encoding":   {},
	"Upgrade":             {},
}

func downstreamHeader(response *http.Response) http.Header {
	header := response.Header.Clone()
	for _, nominated := range connectionTokens(header.Values("Connection")) {
		header.Del(nominated)
	}
	for name := range hopByHopResponseHeaders {
		header.Del(name)
	}
	if response.ContentLength >= 0 && header.Get("Content-Length") == "" {
		header.Set("Content-Length", strconv.FormatInt(response.ContentLength, 10))
	}
	if len(response.Trailer) > 0 {
		keys := make([]string, 0, len(response.Trailer))
		for key := range response.Trailer {
			keys = append(keys, http.CanonicalHeaderKey(key))
		}
		sort.Strings(keys)
		header["Trailer"] = keys
	}
	return header
}

func connectionTokens(values []string) []string {
	var tokens []string
	for _, value := range values {
		for _, token := range strings.Split(value, ",") {
			if token = strings.TrimSpace(token); token != "" {
				tokens = append(tokens, http.CanonicalHeaderKey(token))
			}
		}
	}
	return tokens
}

func copyRequestHeaders(target, source http.Header) {
	connectionHeaders := connectionTokens(source.Values("Connection"))
	for key, values := range source {
		canonical := http.CanonicalHeaderKey(key)
		// BuildRequest replays a buffered entity rather than the original transfer,
		// so forwarding its trailer declaration would promise values that cannot arrive.
		if canonical == "Trailer" {
			continue
		}
		if _, excluded := hopByHopResponseHeaders[canonical]; excluded || containsHeader(connectionHeaders, canonical) {
			continue
		}
		for _, value := range values {
			target.Add(canonical, value)
		}
	}
}

func containsHeader(headers []string, target string) bool {
	for _, header := range headers {
		if strings.EqualFold(header, target) {
			return true
		}
	}
	return false
}

func isSSE(header http.Header) bool {
	mediaType := strings.ToLower(strings.TrimSpace(strings.Split(header.Get("Content-Type"), ";")[0]))
	return mediaType == "text/event-stream"
}

func cloneHeader(source http.Header) http.Header {
	if source == nil {
		return nil
	}
	return source.Clone()
}
