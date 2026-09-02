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
	"net/http/httptrace"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/cookie"
	"github.com/doraemonkeys/switch-a/internal/codex/upstreamheaders"
	"github.com/doraemonkeys/switch-a/internal/defaults"
)

var ErrBodyTransferred = errors.New("upstream response body already transferred")

type Config struct {
	ConnectTimeout   time.Duration
	FirstByteTimeout time.Duration
}

// CookiePolicy controls only client Cookie handling at the upstream boundary.
// The gateway handle is always removed independently of this policy.
type CookiePolicy uint8

const (
	PreserveClientCookies CookiePolicy = iota
	ServerManagedCookies
)

// HeaderPolicy keeps rollout gating separate from Cookie policy. Sanitization
// remains the safe zero value for callers that do not participate in the Codex
// feature snapshot.
type HeaderPolicy uint8

const (
	SanitizeProviderHeaders HeaderPolicy = iota
	PreserveClientHeaders
)

type RequestPolicy struct {
	Cookies CookiePolicy
	Headers HeaderPolicy
}

// RedirectPolicy belongs to exchange execution rather than request projection.
// Server-managed Codex cookies need each 3xx response exposed to the attempt
// coordinator, while ordinary HTTP retains net/http's standard redirect rules.
type RedirectPolicy uint8

const (
	FollowRedirects RedirectPolicy = iota
	ExposeRedirects
)

type ExecutionPolicy struct {
	Redirects RedirectPolicy
}

// RequestDisclosure describes whether request-owned identity or continuity
// data may have crossed the upstream transport boundary. Only None is safe for
// selecting a different authority; every other state deliberately preserves the
// current authority because a partial write cannot be disproved.
type RequestDisclosure uint8

const (
	RequestDisclosureUnknown RequestDisclosure = iota
	RequestDisclosureNone
	RequestDisclosurePossible
	RequestDisclosureConfirmed
)

func (d RequestDisclosure) DefinitelyNotDisclosed() bool {
	return d == RequestDisclosureNone
}

func (d RequestDisclosure) String() string {
	switch d {
	case RequestDisclosureNone:
		return "none"
	case RequestDisclosurePossible:
		return "possible"
	case RequestDisclosureConfirmed:
		return "confirmed"
	default:
		return "unknown"
	}
}

type requestDisclosureTracker struct {
	state atomic.Uint32
}

func newRequestDisclosureTracker(initial RequestDisclosure) *requestDisclosureTracker {
	tracker := &requestDisclosureTracker{}
	tracker.state.Store(uint32(initial))
	return tracker
}

func (t *requestDisclosureTracker) trace() *httptrace.ClientTrace {
	markPossible := func() { t.state.Store(uint32(RequestDisclosurePossible)) }
	return &httptrace.ClientTrace{
		// A failed header write may have exposed only a prefix. Marking the first
		// field is intentionally earlier than WroteHeaders/WroteRequest.
		WroteHeaderField: func(string, []string) { markPossible() },
		WroteHeaders:     markPossible,
		WroteRequest:     func(httptrace.WroteRequestInfo) { markPossible() },
	}
}

func (t *requestDisclosureTracker) disclosure(responseReceived bool) RequestDisclosure {
	if responseReceived {
		return RequestDisclosureConfirmed
	}
	return RequestDisclosure(t.state.Load())
}

type Transport struct {
	followClient *http.Client
	rawClient    *http.Client
	// Injected RoundTrippers are disclosure-unknown unless they implement their
	// own outer transport contract; only New installs the traced net/http path.
	tracksRequestDisclosure bool
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
	return &Transport{
		followClient:            &http.Client{Transport: roundTripper},
		tracksRequestDisclosure: true,
		rawClient: &http.Client{
			Transport: roundTripper,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

func (t *Transport) CloseIdleConnections() {
	if t == nil {
		return
	}
	if t.followClient != nil {
		t.followClient.CloseIdleConnections()
		return
	}
	if t.rawClient != nil {
		t.rawClient.CloseIdleConnections()
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

func (t *Transport) Fetch(
	ctx context.Context,
	request *http.Request,
	policy ExecutionPolicy,
) (*Response, RequestDisclosure, error) {
	if t == nil || t.followClient == nil || t.rawClient == nil {
		return nil, RequestDisclosureNone, errors.New("upstream transport is not initialized")
	}
	if request == nil {
		return nil, RequestDisclosureNone, errors.New("upstream request is required")
	}
	if ctx == nil {
		return nil, RequestDisclosureNone, errors.New("upstream request context is required")
	}
	client, err := t.clientFor(policy)
	if err != nil {
		return nil, RequestDisclosureNone, err
	}
	initialDisclosure := RequestDisclosureUnknown
	if t.tracksRequestDisclosure {
		initialDisclosure = RequestDisclosureNone
	}
	disclosure := newRequestDisclosureTracker(initialDisclosure)
	traceContext := httptrace.WithClientTrace(ctx, disclosure.trace())
	response, err := client.Do(request.WithContext(traceContext)) //nolint:bodyclose // ownership moves through Response.TakeBody
	if err != nil {
		return nil, disclosure.disclosure(response != nil), err
	}
	sourceHeader := response.Header.Clone()
	clientHeader := downstreamHeader(response)
	wrapped, wrapErr := NewResponse(ResponseHead{
		StatusCode:    response.StatusCode,
		Protocol:      response.Proto,
		SourceHeader:  sourceHeader,
		Header:        clientHeader,
		Trailer:       response.Trailer,
		ContentLength: response.ContentLength,
	}, response.Body)
	return wrapped, disclosure.disclosure(true), wrapErr
}

func (t *Transport) clientFor(policy ExecutionPolicy) (*http.Client, error) {
	switch policy.Redirects {
	case FollowRedirects:
		return t.followClient, nil
	case ExposeRedirects:
		return t.rawClient, nil
	default:
		return nil, errors.New("upstream redirect policy is invalid")
	}
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
	return BuildRequestWithPolicy(ctx, method, upstreamURL, body, original, RequestPolicy{})
}

func BuildRequestWithPolicy(
	ctx context.Context,
	method string,
	upstreamURL string,
	body []byte,
	original *http.Request,
	policy RequestPolicy,
) (*http.Request, error) {
	if original == nil {
		return nil, errors.New("original request is required")
	}
	if err := policy.validate(); err != nil {
		return nil, err
	}
	var source io.Reader
	if body != nil {
		source = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, upstreamURL, source)
	if err != nil {
		return nil, err
	}
	if policy.Headers == PreserveClientHeaders {
		request.Header = upstreamheaders.ForHTTPTransportAttempt(original.Header)
	} else {
		request.Header = upstreamheaders.ForHTTPAttempt(original.Header)
	}
	applyCookiePolicy(request.Header, policy.Cookies)
	if _, present := request.Header["User-Agent"]; !present {
		request.Header["User-Agent"] = nil
	}
	request.Host = request.URL.Host
	return request, nil
}

func (p RequestPolicy) validate() error {
	if p.Cookies != PreserveClientCookies && p.Cookies != ServerManagedCookies {
		return errors.New("upstream cookie policy is invalid")
	}
	if p.Headers != SanitizeProviderHeaders && p.Headers != PreserveClientHeaders {
		return errors.New("upstream header policy is invalid")
	}
	return nil
}
func applyCookiePolicy(header http.Header, policy CookiePolicy) {
	if policy == ServerManagedCookies {
		header.Del("Cookie")
		return
	}
	values := header.Values("Cookie")
	header.Del("Cookie")
	for _, value := range values {
		kept := make([]string, 0, strings.Count(value, ";")+1)
		for pair := range strings.SplitSeq(value, ";") {
			pair = strings.TrimSpace(pair)
			if pair == "" {
				continue
			}
			name, _, _ := strings.Cut(pair, "=")
			if name == providercookie.GatewayHandleName {
				continue
			}
			kept = append(kept, pair)
		}
		if len(kept) > 0 {
			header.Add("Cookie", strings.Join(kept, "; "))
		}
	}
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
		for token := range strings.SplitSeq(value, ",") {
			if token = strings.TrimSpace(token); token != "" {
				tokens = append(tokens, http.CanonicalHeaderKey(token))
			}
		}
	}
	return tokens
}

func cloneHeader(source http.Header) http.Header {
	if source == nil {
		return nil
	}
	return source.Clone()
}
