package upstreamtransport

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/http/httptrace"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDisclosureObservationRequiresTransportEvidence(t *testing.T) {
	wantErr := errors.New("custom transport failed")
	for _, tc := range []struct {
		name  string
		write func(*httptrace.ClientTrace)
		want  RequestDisclosure
	}{
		{name: "opaque", want: RequestDisclosureUnknown},
		{name: "partial headers", write: func(trace *httptrace.ClientTrace) { trace.WroteHeaderField("Thread-Id", []string{"new-thread"}) }, want: RequestDisclosurePossible},
		{name: "headers", write: func(trace *httptrace.ClientTrace) { trace.WroteHeaders() }, want: RequestDisclosurePossible},
		{name: "request write failure", write: func(trace *httptrace.ClientTrace) { trace.WroteRequest(httptrace.WroteRequestInfo{Err: wantErr}) }, want: RequestDisclosurePossible},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, observation := ObserveRequestDisclosure(&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				if tc.write != nil {
					tc.write(httptrace.ContextClientTrace(request.Context()))
				}
				return nil, wantErr
			})})
			if observation.Result(false) != RequestDisclosureUnknown {
				t.Fatal("unused client invented a transmission fact")
			}
			request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://upstream.test/responses", nil)
			if err != nil {
				t.Fatal(err)
			}
			response, err := client.Do(request)
			if response != nil {
				response.Body.Close()
			}
			if !errors.Is(err, wantErr) || observation.Result(response != nil) != tc.want {
				t.Fatalf("disclosure=%s err=%v, want %s and original error", observation.Result(response != nil), err, tc.want)
			}
		})
	}
}

func TestDisclosureObservationKeepsEarlierRedirectEvidence(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	refusedURL := "http://" + listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, refusedURL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	client, observation := ObserveRequestDisclosure(server.Client())
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if response != nil {
		response.Body.Close()
	}
	if err == nil || observation.Result(false) != RequestDisclosureConfirmed {
		t.Fatalf("last-hop error erased first-hop response: disclosure=%s err=%v", observation.Result(false), err)
	}
}

func TestDisclosureObservationPreservesClientAndComposesTrace(t *testing.T) {
	var existingTraceCalls atomic.Int32
	var writes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Thread-Id") != "original-thread" || r.Header.Get("Accept-Encoding") != "identity" {
			t.Errorf("request headers changed: %v", r.Header)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	policyErr := errors.New("redirect policy")
	original := &http.Client{
		Transport: server.Client().Transport, Timeout: time.Second, Jar: jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return policyErr },
	}
	client, observation := ObserveRequestDisclosure(original)
	if client == original || client.Jar != jar || client.Timeout != original.Timeout ||
		!errors.Is(client.CheckRedirect(nil, nil), policyErr) || original.Transport != server.Client().Transport {
		t.Fatal("observation changed client policy or original transport")
	}
	ctx := httptrace.WithClientTrace(t.Context(), &httptrace.ClientTrace{
		WroteHeaderField: func(string, []string) { existingTraceCalls.Add(1) },
		WroteRequest:     func(httptrace.WroteRequestInfo) { writes.Add(1) },
	})
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Thread-Id", "original-thread")
	request.Header.Set("Accept-Encoding", "identity")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if observation.Result(false) != RequestDisclosureConfirmed || existingTraceCalls.Load() == 0 || writes.Load() != 1 {
		t.Fatalf("disclosure=%s fields=%d writes=%d", observation.Result(false), existingTraceCalls.Load(), writes.Load())
	}
	client.CloseIdleConnections()
}

func TestDisclosureObservationDefaultClientAndIdleCleanup(t *testing.T) {
	client, observation := ObserveRequestDisclosure(nil)
	if client == http.DefaultClient || observation.Result(false) != RequestDisclosureUnknown {
		t.Fatal("default client was mutated or an unused dial was classified")
	}
	if observation.Result(true) != RequestDisclosureConfirmed {
		t.Fatal("external dialer response was not retained as evidence")
	}
	base := &recordingRoundTripper{}
	observed, _ := ObserveRequestDisclosure(&http.Client{Transport: base})
	observed.CloseIdleConnections()
	if base.closed.Load() != 1 {
		t.Fatal("idle cleanup did not reach original pool")
	}
	observed, _ = ObserveRequestDisclosure(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, io.EOF
	})})
	observed.CloseIdleConnections()
}

func TestDisclosureObservationPartialPhysicalWrite(t *testing.T) {
	const prefixBytes = 8
	clientConn, peer := net.Pipe()
	defer peer.Close()
	received := make(chan string, 1)
	go func() {
		prefix := make([]byte, prefixBytes)
		_, _ = io.ReadFull(peer, prefix)
		received <- string(prefix)
		peer.Close()
	}()
	base := &http.Transport{
		DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
			return &disclosurePartialWriteConn{Conn: clientConn, limit: prefixBytes}, nil
		},
	}
	defer base.CloseIdleConnections()
	client, observation := ObserveRequestDisclosure(&http.Client{Transport: base, Timeout: time.Second})
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://partial.test/responses", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if response != nil {
		response.Body.Close()
	}
	if err == nil || observation.Result(false) != RequestDisclosurePossible {
		t.Fatalf("partial write disclosure=%s err=%v", observation.Result(false), err)
	}
	if prefix := <-received; !strings.HasPrefix(prefix, "GET ") {
		t.Fatalf("no physical request prefix: %q", prefix)
	}
}

type disclosurePartialWriteConn struct {
	net.Conn
	limit int
}

func (c *disclosurePartialWriteConn) Write(payload []byte) (int, error) {
	n, err := c.Conn.Write(payload[:min(c.limit, len(payload))])
	if err != nil {
		return n, err
	}
	return n, io.ErrUnexpectedEOF
}
