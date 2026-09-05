package upstreamtransport

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/defaults"
)

func TestNewConfiguresIdentityPreservingTransport(t *testing.T) {
	t.Parallel()

	config := Config{
		ConnectTimeout:   23 * time.Millisecond,
		FirstByteTimeout: 47 * time.Millisecond,
	}
	transport := New(config)
	if transport == nil || transport.followClient == nil || transport.rawClient == nil {
		t.Fatal("New returned an uninitialized transport")
	}
	roundTripper, ok := transport.followClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("client transport type = %T, want *http.Transport", transport.followClient.Transport)
	}
	if transport.rawClient.Transport != transport.followClient.Transport {
		t.Fatal("redirect clients do not share one RoundTripper")
	}
	if !roundTripper.DisableCompression {
		t.Error("DisableCompression = false, raw upstream identity would be lost")
	}
	if roundTripper.ResponseHeaderTimeout != config.FirstByteTimeout {
		t.Errorf("ResponseHeaderTimeout = %v, want %v", roundTripper.ResponseHeaderTimeout, config.FirstByteTimeout)
	}
	if roundTripper.MaxIdleConns != defaults.MaxIdleConns {
		t.Errorf("MaxIdleConns = %d, want %d", roundTripper.MaxIdleConns, defaults.MaxIdleConns)
	}
	if roundTripper.MaxIdleConnsPerHost != defaults.MaxIdleConnsPerHost {
		t.Errorf("MaxIdleConnsPerHost = %d, want %d", roundTripper.MaxIdleConnsPerHost, defaults.MaxIdleConnsPerHost)
	}
	if roundTripper.IdleConnTimeout != defaults.IdleConnTimeout {
		t.Errorf("IdleConnTimeout = %v, want %v", roundTripper.IdleConnTimeout, defaults.IdleConnTimeout)
	}
	if roundTripper.TLSHandshakeTimeout != defaults.TLSHandshakeTimeout {
		t.Errorf("TLSHandshakeTimeout = %v, want %v", roundTripper.TLSHandshakeTimeout, defaults.TLSHandshakeTimeout)
	}
	if roundTripper.Proxy == nil || roundTripper.DialContext == nil {
		t.Error("proxy and dial policies must both be configured")
	}
}

func TestCloseIdleConnectionsHandlesEveryInitializationState(t *testing.T) {
	t.Parallel()

	var nilTransport *Transport
	nilTransport.CloseIdleConnections()
	(&Transport{}).CloseIdleConnections()

	closer := &recordingRoundTripper{}
	transport := &Transport{followClient: &http.Client{Transport: closer}}
	transport.CloseIdleConnections()
	if got := closer.closed.Load(); got != 1 {
		t.Fatalf("CloseIdleConnections calls = %d, want 1", got)
	}
}

func TestResponseHeadIsIsolatedAndBodyMovesExactlyOnce(t *testing.T) {
	t.Parallel()

	trailer := http.Header{"X-Checksum": nil}
	body := io.NopCloser(strings.NewReader("payload"))
	response, err := NewResponse(ResponseHead{
		StatusCode:    http.StatusAccepted,
		Protocol:      "HTTP/2.0",
		SourceHeader:  http.Header{"X-Source": {"one"}},
		Header:        http.Header{"X-Client": {"two"}},
		Trailer:       trailer,
		ContentLength: 7,
	}, body)
	if err != nil {
		t.Fatalf("NewResponse: %v", err)
	}
	snapshot := response.Head()
	snapshot.SourceHeader.Set("X-Source", "mutated")
	snapshot.Header.Set("X-Client", "mutated")
	snapshot.Trailer.Set("X-Checksum", "mutated")
	secondSnapshot := response.Head()
	if got := secondSnapshot.SourceHeader.Get("X-Source"); got != "one" {
		t.Errorf("source header leaked mutation: %q", got)
	}
	if got := secondSnapshot.Header.Get("X-Client"); got != "two" {
		t.Errorf("client header leaked mutation: %q", got)
	}
	if got := secondSnapshot.Trailer.Get("X-Checksum"); got != "" {
		t.Errorf("trailer snapshot leaked mutation: %q", got)
	}

	head, movedBody, err := response.Take()
	if err != nil {
		t.Fatalf("Take: %v", err)
	}
	if head.StatusCode != http.StatusAccepted || head.Protocol != "HTTP/2.0" || head.ContentLength != 7 {
		t.Fatalf("moved head = %+v", head)
	}
	trailer.Set("X-Checksum", "complete")
	if got := head.Trailer.Get("X-Checksum"); got != "complete" {
		t.Fatalf("moved trailer = %q, want live value", got)
	}
	data, err := io.ReadAll(movedBody)
	if err != nil {
		t.Fatalf("read moved body: %v", err)
	}
	if string(data) != "payload" {
		t.Fatalf("moved body = %q, want payload", data)
	}
	if _, _, err := response.Take(); !errors.Is(err, ErrBodyTransferred) {
		t.Fatalf("second Take error = %v, want ErrBodyTransferred", err)
	}
	if _, err := response.TakeBody(); !errors.Is(err, ErrBodyTransferred) {
		t.Fatalf("TakeBody after Take error = %v, want ErrBodyTransferred", err)
	}
	if got := response.Head().Trailer; got != nil {
		t.Fatalf("retained trailer after transfer = %#v, want nil", got)
	}
}

func TestResponseTakeBodyMovesCapabilityExactlyOnce(t *testing.T) {
	t.Parallel()

	response, err := NewResponse(ResponseHead{}, io.NopCloser(strings.NewReader("body")))
	if err != nil {
		t.Fatalf("NewResponse: %v", err)
	}
	body, err := response.TakeBody()
	if err != nil {
		t.Fatalf("TakeBody: %v", err)
	}
	if data, err := io.ReadAll(body); err != nil || string(data) != "body" {
		t.Fatalf("read body = %q, %v", data, err)
	}
	if _, err := response.TakeBody(); !errors.Is(err, ErrBodyTransferred) {
		t.Fatalf("second TakeBody error = %v, want ErrBodyTransferred", err)
	}
	if got := response.Head().Trailer; got != nil {
		t.Fatalf("retained trailer after body-only transfer = %#v, want nil", got)
	}
}

func TestResponseConcurrentTransferHasSingleWinner(t *testing.T) {
	t.Parallel()

	response, err := NewResponse(ResponseHead{}, io.NopCloser(strings.NewReader("body")))
	if err != nil {
		t.Fatalf("NewResponse: %v", err)
	}
	var winners atomic.Int32
	var losers atomic.Int32
	var wait sync.WaitGroup
	for range 16 {
		wait.Go(func() {
			body, takeErr := response.TakeBody()
			switch {
			case takeErr == nil:
				winners.Add(1)
				_ = body.Close()
			case errors.Is(takeErr, ErrBodyTransferred):
				losers.Add(1)
			default:
				t.Errorf("TakeBody error = %v", takeErr)
			}
		})
	}
	wait.Wait()
	if winners.Load() != 1 || losers.Load() != 15 {
		t.Fatalf("transfer outcomes: winners=%d losers=%d", winners.Load(), losers.Load())
	}
}

func TestResponseHeadAndTakeSynchronizeTrailerOwnership(t *testing.T) {
	t.Parallel()

	for range 128 {
		response, err := NewResponse(ResponseHead{
			Trailer: http.Header{"X-Late": nil},
		}, io.NopCloser(strings.NewReader("body")))
		if err != nil {
			t.Fatalf("NewResponse: %v", err)
		}
		start := make(chan struct{})
		var wait sync.WaitGroup
		wait.Add(2)
		go func() {
			defer wait.Done()
			<-start
			_ = response.Head()
		}()
		go func() {
			defer wait.Done()
			<-start
			_, body, takeErr := response.Take()
			if takeErr != nil {
				t.Errorf("Take: %v", takeErr)
				return
			}
			_ = body.Close()
		}()
		close(start)
		wait.Wait()
	}
}

func TestResponseNilAndInvalidConstruction(t *testing.T) {
	t.Parallel()

	var response *Response
	if got := response.Head(); !reflect.DeepEqual(got, ResponseHead{}) {
		t.Fatalf("nil Head = %+v", got)
	}
	if _, err := response.TakeBody(); !errors.Is(err, ErrBodyTransferred) {
		t.Fatalf("nil TakeBody error = %v", err)
	}
	if _, _, err := response.Take(); !errors.Is(err, ErrBodyTransferred) {
		t.Fatalf("nil Take error = %v", err)
	}
	if _, err := NewResponse(ResponseHead{}, nil); err == nil {
		t.Fatal("NewResponse accepted nil body")
	}
}

func TestBuildRequestCopiesOnlyEndToEndHeaders(t *testing.T) {
	t.Parallel()

	original := httptest.NewRequest(http.MethodPost, "http://client.example/original", nil)
	original.Header = http.Header{
		"Accept":              {"application/json", "text/plain"},
		"authorization":       {"Bearer client-token"},
		"X-API-KEY":           {"client-api-key"},
		"chatgpt-account-id":  {"client-account"},
		"X-Client-Request-Id": {"logical-request-42"},
		"Connection":          {"keep-alive, X-Private", "x-another"},
		"Keep-Alive":          {"timeout=5"},
		"Proxy-Authenticate":  {"challenge"},
		"Proxy-Authorization": {"secret"},
		"Proxy-Connection":    {"keep-alive"},
		"Te":                  {"trailers"},
		"Trailer":             {"X-Late"},
		"Transfer-Encoding":   {"chunked"},
		"Upgrade":             {"websocket"},
		"X-Private":           {"private"},
		"X-Another":           {"another"},
		"X-End-To-End":        {"preserved"},
	}
	payload := []byte("request-body")
	request, err := BuildRequest(t.Context(), http.MethodPut, "https://upstream.example/v1/items?q=1", testBodySource(payload), original)
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	if request.Method != http.MethodPut || request.URL.String() != "https://upstream.example/v1/items?q=1" {
		t.Fatalf("request target = %s %s", request.Method, request.URL)
	}
	if request.Host != "upstream.example" {
		t.Fatalf("Host = %q, want upstream.example", request.Host)
	}
	if request.ContentLength != int64(len(payload)) {
		t.Fatalf("ContentLength = %d, want %d", request.ContentLength, len(payload))
	}
	data, err := io.ReadAll(request.Body)
	if err != nil || !bytes.Equal(data, payload) {
		t.Fatalf("request body = %q, %v", data, err)
	}
	if got := request.Header.Values("Accept"); !reflect.DeepEqual(got, []string{"application/json", "text/plain"}) {
		t.Fatalf("Accept values = %#v", got)
	}
	if got := request.Header.Get("X-End-To-End"); got != "preserved" {
		t.Fatalf("X-End-To-End = %q", got)
	}
	if got := request.Header.Get("X-Client-Request-Id"); got != "logical-request-42" {
		t.Fatalf("X-Client-Request-Id = %q, want logical-request-42", got)
	}
	for _, removed := range []string{
		"Authorization", "X-Api-Key", "ChatGPT-Account-Id",
		"Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization",
		"Proxy-Connection", "Te", "Trailer", "Transfer-Encoding", "Upgrade",
		"X-Private", "X-Another",
	} {
		if values, present := request.Header[removed]; present {
			t.Errorf("hop-by-hop header %s survived: %#v", removed, values)
		}
	}
	if values, present := request.Header["User-Agent"]; !present || values != nil {
		t.Fatalf("absent User-Agent normalization = %#v, present=%v", values, present)
	}
	original.Header.Set("X-End-To-End", "mutated")
	if got := request.Header.Get("X-End-To-End"); got != "preserved" {
		t.Fatalf("request header aliases original: %q", got)
	}
}

func TestBuildRequestDoesNotCarryCredentialsFromPriorAttempt(t *testing.T) {
	t.Parallel()

	client := httptest.NewRequest(http.MethodPost, "http://client.example", nil)
	client.Header.Set("X-Client-Request-Id", "logical-request-42")
	first, err := BuildRequest(t.Context(), http.MethodPost, "https://first.example", nil, client)
	if err != nil {
		t.Fatalf("build first attempt: %v", err)
	}
	first.Header.Set("Authorization", "Bearer first-provider")
	first.Header.Set("X-Api-Key", "first-api-key")
	first.Header.Set("ChatGPT-Account-Id", "first-account")

	second, err := BuildRequest(t.Context(), http.MethodPost, "https://second.example", nil, first)
	if err != nil {
		t.Fatalf("build second attempt: %v", err)
	}
	if got := second.Header.Get("Authorization"); got != "" {
		t.Fatalf("Authorization = %q, want prior credential removed", got)
	}
	if got := second.Header.Get("X-Api-Key"); got != "" {
		t.Fatalf("X-Api-Key = %q, want prior credential removed", got)
	}
	if got := second.Header.Get("ChatGPT-Account-Id"); got != "" {
		t.Fatalf("ChatGPT-Account-Id = %q, want prior account removed", got)
	}
	if got := second.Header.Get("X-Client-Request-Id"); got != "logical-request-42" {
		t.Fatalf("X-Client-Request-Id = %q, want stable logical request ID", got)
	}
}

func TestBuildRequestPreservesExplicitUserAgentAndNilBody(t *testing.T) {
	t.Parallel()

	original := httptest.NewRequest(http.MethodGet, "http://client.example", nil)
	original.Header["User-Agent"] = []string{"client/1", "compat/2"}
	request, err := BuildRequest(t.Context(), http.MethodGet, "http://upstream.example", nil, original)
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	if request.Body != nil {
		t.Fatalf("nil payload created body %T", request.Body)
	}
	if got := request.Header.Values("User-Agent"); !reflect.DeepEqual(got, []string{"client/1", "compat/2"}) {
		t.Fatalf("User-Agent values = %#v", got)
	}
}

func TestBuildRequestRejectsInvalidInputs(t *testing.T) {
	t.Parallel()

	if _, err := BuildRequest(t.Context(), http.MethodGet, "http://upstream.example", nil, nil); err == nil {
		t.Fatal("BuildRequest accepted nil original request")
	}
	original := httptest.NewRequest(http.MethodGet, "http://client.example", nil)
	if _, err := BuildRequest(t.Context(), "BAD\nMETHOD", "http://upstream.example", nil, original); err == nil {
		t.Fatal("BuildRequest accepted invalid method")
	}
	if _, err := BuildRequest(t.Context(), http.MethodGet, "://bad-url", nil, original); err == nil {
		t.Fatal("BuildRequest accepted invalid URL")
	}
}

func TestDownstreamHeaderNormalizesHTTPFraming(t *testing.T) {
	t.Parallel()

	response := &http.Response{
		Header: http.Header{
			"Connection":          {"keep-alive, X-Private", "x-another"},
			"Keep-Alive":          {"timeout=5"},
			"Proxy-Authenticate":  {"challenge"},
			"Proxy-Authorization": {"secret"},
			"Proxy-Connection":    {"keep-alive"},
			"Te":                  {"trailers"},
			"Transfer-Encoding":   {"chunked"},
			"Upgrade":             {"websocket"},
			"X-Private":           {"private"},
			"X-Another":           {"another"},
			"Set-Cookie":          {"one=1", "two=2"},
		},
		Trailer: http.Header{
			"x-zeta":  nil,
			"X-Alpha": nil,
		},
		ContentLength: 128,
	}
	header := downstreamHeader(response)
	if got := header.Get("Content-Length"); got != "128" {
		t.Fatalf("Content-Length = %q, want 128", got)
	}
	if got := header.Values("Set-Cookie"); !reflect.DeepEqual(got, []string{"one=1", "two=2"}) {
		t.Fatalf("Set-Cookie values = %#v", got)
	}
	if got := header.Values("Trailer"); !reflect.DeepEqual(got, []string{"X-Alpha", "X-Zeta"}) {
		t.Fatalf("Trailer declarations = %#v", got)
	}
	for _, removed := range []string{
		"Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization",
		"Proxy-Connection", "Te", "Transfer-Encoding", "Upgrade", "X-Private", "X-Another",
	} {
		if values, present := header[removed]; present {
			t.Errorf("hop-by-hop response header %s survived: %#v", removed, values)
		}
	}
	if got := response.Header.Get("Connection"); got == "" {
		t.Fatal("downstreamHeader mutated source response")
	}
}

func TestDownstreamHeaderRespectsExplicitAndUnknownLengths(t *testing.T) {
	t.Parallel()

	explicit := downstreamHeader(&http.Response{
		Header:        http.Header{"Content-Length": {"9"}},
		ContentLength: 12,
	})
	if got := explicit.Get("Content-Length"); got != "9" {
		t.Fatalf("explicit Content-Length = %q, want 9", got)
	}
	unknown := downstreamHeader(&http.Response{
		Header:        make(http.Header),
		ContentLength: -1,
	})
	if got := unknown.Get("Content-Length"); got != "" {
		t.Fatalf("unknown Content-Length = %q, want empty", got)
	}
}

func TestFetchReturnsNormalizedHeadAndLiveTrailer(t *testing.T) {
	t.Parallel()

	trailer := http.Header{"X-Checksum": nil}
	body := &trailerCompletingBody{
		Reader:  strings.NewReader("event: ready\n\n"),
		trailer: trailer,
	}
	var seenContextValue any
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		seenContextValue = request.Context().Value(contextKey("operation"))
		return &http.Response{
			StatusCode:    http.StatusCreated,
			Proto:         "HTTP/2.0",
			Header:        http.Header{"Content-Type": {" Text/Event-Stream ; charset=utf-8"}, "X-End": {"value"}},
			Trailer:       trailer,
			ContentLength: -1,
			Body:          body,
			Request:       request,
		}, nil
	})}
	transport := &Transport{followClient: client, rawClient: client}

	ctx := context.WithValue(t.Context(), contextKey("operation"), "op-17")
	request, err := http.NewRequest(http.MethodGet, "http://upstream.example/events", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	response, disclosure, err := transport.Fetch(ctx, request, ExecutionOptions{})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if disclosure != RequestDisclosureConfirmed {
		t.Fatalf("request disclosure = %s, want confirmed", disclosure)
	}
	if seenContextValue != "op-17" {
		t.Fatalf("round trip context value = %#v", seenContextValue)
	}
	head, movedBody, err := response.Take()
	if err != nil {
		t.Fatalf("Take: %v", err)
	}
	if head.StatusCode != http.StatusCreated || head.Protocol != "HTTP/2.0" {
		t.Fatalf("response head = %+v", head)
	}
	if got := head.SourceHeader.Get("X-End"); got != "value" {
		t.Fatalf("source header X-End = %q", got)
	}
	if got := head.Header.Get("X-End"); got != "value" {
		t.Fatalf("client header X-End = %q", got)
	}
	data, err := io.ReadAll(movedBody)
	if err != nil || string(data) != "event: ready\n\n" {
		t.Fatalf("read body = %q, %v", data, err)
	}
	if got := head.Trailer.Get("X-Checksum"); got != "sha256:done" {
		t.Fatalf("live trailer = %q, want sha256:done", got)
	}
}

func TestFetchPreservesCompressedWireIdentity(t *testing.T) {
	t.Parallel()

	const payload = "compressed payload"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if got := request.Header.Get("Accept-Encoding"); got != "" {
			t.Errorf("implicit Accept-Encoding = %q, want empty", got)
		}
		writer.Header().Set("Content-Encoding", "gzip")
		gzipWriter := gzip.NewWriter(writer)
		_, _ = io.WriteString(gzipWriter, payload)
		_ = gzipWriter.Close()
	}))
	t.Cleanup(server.Close)

	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	response, disclosure, err := New(Config{ConnectTimeout: time.Second, FirstByteTimeout: time.Second}).Fetch(t.Context(), request, ExecutionOptions{})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if disclosure != RequestDisclosureConfirmed {
		t.Fatalf("request disclosure = %s, want confirmed", disclosure)
	}
	head, body, err := response.Take()
	if err != nil {
		t.Fatalf("Take: %v", err)
	}
	defer body.Close()
	if got := head.SourceHeader.Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	wireBytes, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read compressed body: %v", err)
	}
	if string(wireBytes) == payload {
		t.Fatal("transport transparently decompressed upstream bytes")
	}
	reader, err := gzip.NewReader(bytes.NewReader(wireBytes))
	if err != nil {
		t.Fatalf("open gzip payload: %v", err)
	}
	decoded, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("decode gzip payload: %v", err)
	}
	if string(decoded) != payload {
		t.Fatalf("decoded payload = %q, want %q", decoded, payload)
	}
}

func TestFetchRejectsInvalidStateAndPropagatesRoundTripFailure(t *testing.T) {
	t.Parallel()

	request, err := http.NewRequest(http.MethodGet, "http://upstream.example", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	var nilTransport *Transport
	if _, _, err := nilTransport.Fetch(t.Context(), request, ExecutionOptions{}); err == nil {
		t.Fatal("nil Transport.Fetch succeeded")
	}
	if _, _, err := (&Transport{}).Fetch(t.Context(), request, ExecutionOptions{}); err == nil {
		t.Fatal("uninitialized Transport.Fetch succeeded")
	}

	wantErr := errors.New("dial failed")
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, wantErr
	})}
	transport := &Transport{followClient: client, rawClient: client}
	if _, disclosure, err := transport.Fetch(t.Context(), request, ExecutionOptions{}); !errors.Is(err, wantErr) {
		t.Fatalf("Fetch error = %v, want %v", err, wantErr)
	} else if disclosure != RequestDisclosureUnknown {
		t.Fatalf("custom transport disclosure = %s, want unknown", disclosure)
	}
	if _, _, err := transport.Fetch(t.Context(), nil, ExecutionOptions{}); err == nil {
		t.Fatal("Fetch accepted nil request")
	}
}

func TestFetchProvesConnectionEstablishmentFailuresWereNotDisclosed(t *testing.T) {
	t.Run("TCP connect", func(t *testing.T) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		address := listener.Addr().String()
		if err := listener.Close(); err != nil {
			t.Fatal(err)
		}
		request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://"+address+"/responses", strings.NewReader("state"))
		if err != nil {
			t.Fatal(err)
		}
		_, disclosure, err := New(Config{ConnectTimeout: time.Second, FirstByteTimeout: time.Second}).Fetch(t.Context(), request, ExecutionOptions{})
		if err == nil {
			t.Fatal("connection-refused request unexpectedly succeeded")
		}
		if disclosure != RequestDisclosureNone {
			t.Fatalf("connection-refused disclosure = %s, want none", disclosure)
		}
	})

	t.Run("TLS handshake", func(t *testing.T) {
		plainServer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Error("TLS handshake failure reached the HTTP handler")
		}))
		t.Cleanup(plainServer.Close)
		request, err := http.NewRequestWithContext(
			t.Context(), http.MethodPost,
			strings.Replace(plainServer.URL, "http://", "https://", 1)+"/responses",
			strings.NewReader("state"),
		)
		if err != nil {
			t.Fatal(err)
		}
		_, disclosure, err := New(Config{ConnectTimeout: time.Second, FirstByteTimeout: time.Second}).Fetch(t.Context(), request, ExecutionOptions{})
		if err == nil {
			t.Fatal("TLS handshake request unexpectedly succeeded")
		}
		if disclosure != RequestDisclosureNone {
			t.Fatalf("TLS handshake disclosure = %s, want none", disclosure)
		}
	})
}

func TestHeaderHelpersCoverCaseAndEmptyInputs(t *testing.T) {
	t.Parallel()

	if got := connectionTokens([]string{" keep-alive, X-One ", "", ",x-two,"}); !reflect.DeepEqual(got, []string{"Keep-Alive", "X-One", "X-Two"}) {
		t.Fatalf("connectionTokens = %#v", got)
	}
	if cloneHeader(nil) != nil {
		t.Fatal("cloneHeader(nil) was non-nil")
	}
}

type contextKey string

type roundTripFunc func(*http.Request) (*http.Response, error)

func (roundTrip roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

type recordingRoundTripper struct {
	closed atomic.Int32
}

func (*recordingRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("unexpected round trip")
}

func (r *recordingRoundTripper) CloseIdleConnections() {
	r.closed.Add(1)
}

type trailerCompletingBody struct {
	*strings.Reader
	trailer http.Header
	done    bool
}

func (b *trailerCompletingBody) Read(buffer []byte) (int, error) {
	count, err := b.Reader.Read(buffer)
	if errors.Is(err, io.EOF) && !b.done {
		b.done = true
		b.trailer.Set("X-Checksum", "sha256:done")
	}
	return count, err
}

func (*trailerCompletingBody) Close() error { return nil }
