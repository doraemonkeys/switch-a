package upstreamtransport

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestSourceHTTP2DestinationKeepsKnownLengthAndTrailers(t *testing.T) {
	observed := make(chan observedRequest, 1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 2 {
			t.Errorf("protocol=%s", r.Proto)
		}
		observation, err := readObservedRequest(r)
		if err != nil {
			t.Error(err)
		}
		observed <- observation
		w.WriteHeader(http.StatusNoContent)
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	transport := New(Config{})
	defer transport.CloseIdleConnections()
	base := transport.followClient.Transport.(*http.Transport)
	base.ForceAttemptHTTP2 = true
	base.TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	source := &memoryBodySource{payload: []byte("wire"), framing: BodyFraming{ProtocolMajor: 2, ContentLength: 4, HasBody: true, Complete: true}, trailers: http.Header{"X-Late": {"complete"}}}
	closeResponse(t, fetchSource(t, transport, server.URL, source, ExecutionOptions{}))
	observation := <-observed
	if observation.length != 4 || observation.body != "wire" || len(observation.transfer) != 0 || observation.trailer.Get("X-Late") != "complete" {
		t.Fatalf("observed=%+v", observation)
	}
	source.payload = nil
	source.framing.ContentLength = 0
	source.framing.HasBody = false
	closeResponse(t, fetchSource(t, transport, server.URL, source, ExecutionOptions{}))
	observation = <-observed
	if observation.length != -1 || observation.body != "" || observation.trailer.Get("X-Late") != "complete" {
		t.Fatalf("trailer-only observed=%+v", observation)
	}
}

type failedWriteConnection struct {
	net.Conn
	fail *atomic.Bool
}

func (c *failedWriteConnection) Write(p []byte) (int, error) {
	if c.fail.CompareAndSwap(true, false) {
		_ = c.Conn.Close()
		return 0, io.ErrClosedPipe
	}
	return c.Conn.Write(p)
}

func TestSourceUnwrittenStalePostCanReopen(t *testing.T) {
	var fail atomic.Bool
	var received atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/warm" {
			body, err := io.ReadAll(r.Body)
			if err != nil || string(body) != "wire" {
				t.Errorf("body=%q, error=%v", body, err)
			}
			received.Add(1)
		}
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()
	transport := New(Config{})
	defer transport.CloseIdleConnections()
	base := transport.followClient.Transport.(*http.Transport)
	base.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		connection, err := (&net.Dialer{}).DialContext(ctx, network, address)
		if err != nil {
			return nil, err
		}
		return &observedConnection{Conn: &failedWriteConnection{Conn: connection, fail: &fail}}, nil
	}
	warm, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/warm", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, _, err := transport.Fetch(t.Context(), warm, ExecutionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	closeResponse(t, response)
	fail.Store(true)
	source := testBodySource([]byte("wire"))
	closeResponse(t, fetchSource(t, transport, server.URL+"/post", source, ExecutionOptions{}))
	if source.opened != 2 || received.Load() != 1 {
		t.Fatalf("readers=%d, received=%d", source.opened, received.Load())
	}
}

func TestTransmissionTrailerMapsRemainIndependentAfterEOF(t *testing.T) {
	source := &memoryBodySource{payload: []byte("wire"), framing: BodyFraming{ProtocolMajor: 2, ContentLength: 4, HasBody: true, Complete: true}, trailers: http.Header{"X-Value": {"first"}}}
	first, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://example.test", nil)
	owner := &sourceBody{source: source}
	if err := projectBody(first, owner); err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, first.Body)
	_ = first.Body.Close()
	source.trailers.Set("X-Value", "second")
	second := first.Clone(t.Context())
	if err := projectBody(second, owner); err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, second.Body)
	_ = second.Body.Close()
	if first.Trailer.Get("X-Value") != "first" || second.Trailer.Get("X-Value") != "second" {
		t.Fatalf("trailers first=%v second=%v", first.Trailer, second.Trailer)
	}
	for _, request := range []*http.Request{first, second} {
		reader, err := request.GetBody()
		if reader != nil || !errors.Is(err, errTransmissionReopen) {
			t.Fatalf("native reopen factory returned reader=%v error=%v", reader, err)
		}
	}
	// The completed transmission must not mutate its map on a repeated EOF.
	_, _ = first.Body.Read(make([]byte, 1))
	if first.Trailer.Get("X-Value") != "first" {
		t.Fatalf("EOF trailer mutated: %v", first.Trailer)
	}
}

func TestSourceFailureStopsBeforeNetworkAndPreservesCause(t *testing.T) {
	failure := errors.New("source failed")
	source := testBodySource([]byte("wire"))
	source.openErr = failure
	transport := New(Config{})
	defer transport.CloseIdleConnections()
	request, err := BuildRequest(t.Context(), http.MethodPost, "http://127.0.0.1:1", source, httptest.NewRequest(http.MethodPost, "http://gateway.test", nil))
	if err != nil {
		t.Fatal(err)
	}
	_, disclosure, err := transport.Fetch(t.Context(), request, ExecutionOptions{})
	if !errors.Is(err, failure) || !disclosure.DefinitelyNotDisclosed() {
		t.Fatalf("error=%v, disclosure=%v", err, disclosure)
	}
	if _, err = request.Body.Read(make([]byte, 1)); !errors.Is(err, failure) {
		t.Fatalf("direct read error=%v", err)
	}
	_ = request.Body.Close()
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	_, _, err = transport.Fetch(canceled, request, ExecutionOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled error=%v", err)
	}
	if requestBodySource(nil) != nil {
		t.Fatal("nil request had a source")
	}
}
