package upstreamtransport

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

type observedRequest struct {
	method   string
	body     string
	length   int64
	transfer []string
	trailer  http.Header
}

func readObservedRequest(request *http.Request) (observedRequest, error) {
	body, err := io.ReadAll(request.Body)
	return observedRequest{method: request.Method, body: string(body), length: request.ContentLength, transfer: append([]string(nil), request.TransferEncoding...), trailer: request.Trailer.Clone()}, err
}

func fetchSource(t *testing.T, transport *Transport, url string, source BodySource, policy ExecutionOptions) *Response {
	t.Helper()
	original := httptest.NewRequest(http.MethodPost, "http://gateway.test/input", nil)
	request, err := BuildRequest(t.Context(), http.MethodPost, url, source, original)
	if err != nil {
		t.Fatal(err)
	}
	response, _, err := transport.Fetch(t.Context(), request, policy)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func closeResponse(t *testing.T, response *Response) {
	t.Helper()
	body, err := response.TakeBody()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.Copy(io.Discard, body); err != nil {
		t.Error(err)
	}
	if err = body.Close(); err != nil {
		t.Error(err)
	}
}

func TestSourceFramingOverSocket(t *testing.T) {
	cases := []struct {
		name     string
		framing  BodyFraming
		trailers http.Header
		payload  string
		length   int64
		chunked  bool
	}{
		{name: "known", framing: BodyFraming{ProtocolMajor: 1, ContentLength: 4, HasBody: true, Complete: true}, payload: "wire", length: 4},
		{name: "unknown completed", framing: BodyFraming{ProtocolMajor: 1, ContentLength: -1, HasBody: true, Complete: true}, payload: "wire", length: -1, chunked: true},
		{name: "empty", framing: BodyFraming{ProtocolMajor: 1, ContentLength: 0, Complete: true}, length: 0},
		{name: "declared trailers", framing: BodyFraming{ProtocolMajor: 1, ContentLength: -1, HasBody: true, TrailerKeys: []string{"X-Checksum"}}, payload: "wire", trailers: http.Header{"X-Checksum": {"complete"}}, length: -1, chunked: true},
		{name: "h2 known with trailers", framing: BodyFraming{ProtocolMajor: 2, ContentLength: 4, HasBody: true, Complete: true}, payload: "wire", trailers: http.Header{"X-Late": {"complete"}}, length: -1, chunked: true},
		{name: "h2 receiving late trailers", framing: BodyFraming{ProtocolMajor: 2, ContentLength: 4, HasBody: true}, payload: "wire", trailers: http.Header{"X-Late": {"complete"}}, length: -1, chunked: true},
		{name: "h2 complete without trailers", framing: BodyFraming{ProtocolMajor: 2, ContentLength: 4, HasBody: true, Complete: true}, payload: "wire", length: 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			seen := make(chan observedRequest, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				observation, err := readObservedRequest(r)
				if err != nil {
					t.Error(err)
				}
				seen <- observation
				w.WriteHeader(http.StatusNoContent)
			}))
			defer server.Close()
			source := &memoryBodySource{payload: []byte(tc.payload), framing: tc.framing, trailers: tc.trailers}
			transport := New(Config{})
			defer transport.CloseIdleConnections()
			closeResponse(t, fetchSource(t, transport, server.URL, source, ExecutionOptions{}))
			observation := <-seen
			if observation.body != tc.payload || observation.length != tc.length {
				t.Fatalf("observed=%+v", observation)
			}
			if (len(observation.transfer) > 0) != tc.chunked {
				t.Fatalf("TransferEncoding=%v", observation.transfer)
			}
			for key, values := range tc.trailers {
				if !reflect.DeepEqual(observation.trailer[key], values) {
					t.Errorf("trailer=%v, want %v", observation.trailer, tc.trailers)
				}
			}
		})
	}
}

func TestSourceRedirectOwnsFramingAndReopens(t *testing.T) {
	for _, status := range []int{http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			seen := make(chan observedRequest, 2)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				observation, err := readObservedRequest(r)
				if err != nil {
					t.Error(err)
				}
				seen <- observation
				if r.URL.Path == "/first" {
					w.Header().Set("Location", "/final")
					w.WriteHeader(status)
					return
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			defer server.Close()
			source := &memoryBodySource{payload: []byte("wire"), framing: BodyFraming{ProtocolMajor: 2, ContentLength: 4, HasBody: true, Complete: true}, trailers: http.Header{"X-Late": {"value"}}}
			transport := New(Config{})
			defer transport.CloseIdleConnections()
			closeResponse(t, fetchSource(t, transport, server.URL+"/first", source, ExecutionOptions{}))
			first, final := <-seen, <-seen
			if first.body != "wire" || first.trailer.Get("X-Late") != "value" {
				t.Fatalf("first=%+v", first)
			}
			retain := status == http.StatusTemporaryRedirect || status == http.StatusPermanentRedirect
			if retain {
				if final.method != http.MethodPost || final.body != "wire" || final.length != -1 || final.trailer.Get("X-Late") != "value" || source.opened != 2 {
					t.Fatalf("final=%+v, opened=%d", final, source.opened)
				}
			} else if final.method != http.MethodGet || final.body != "" || final.length != 0 || len(final.transfer) != 0 || len(final.trailer) != 0 || source.opened != 1 {
				t.Fatalf("dropped=%+v, opened=%d", final, source.opened)
			}
		})
	}
}

func TestSourceStaleConnectionReplayEligibility(t *testing.T) {
	cases := []struct {
		name, method string
		idempotency  bool
		warm         bool
		retry        bool
	}{
		{"reused get", http.MethodGet, false, true, true},
		{"reused post", http.MethodPost, false, true, false},
		{"reused idempotent post", http.MethodPost, true, true, true},
		{"fresh get", http.MethodGet, false, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/warm" {
					_, _ = io.WriteString(w, "warm")
					return
				}
				observation, err := readObservedRequest(r)
				if err != nil {
					t.Error(err)
				}
				if observation.body != "wire" || observation.trailer.Get("X-Value") != "same" {
					t.Errorf("observation=%+v", observation)
				}
				if calls.Add(1) == 1 {
					connection, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					_ = connection.Close()
					return
				}
				_, _ = io.WriteString(w, "complete")
			}))
			defer server.Close()
			transport := New(Config{})
			defer transport.CloseIdleConnections()
			if tc.warm {
				warm, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/warm", nil)
				if err != nil {
					t.Fatal(err)
				}
				response, _, err := transport.Fetch(t.Context(), warm, ExecutionOptions{})
				if err != nil {
					t.Fatal(err)
				}
				closeResponse(t, response)
			}
			source := &memoryBodySource{payload: []byte("wire"), framing: BodyFraming{ProtocolMajor: 1, ContentLength: -1, HasBody: true, Complete: true}, trailers: http.Header{"X-Value": {"same"}}}
			original := httptest.NewRequest(tc.method, "http://gateway.test/input", nil)
			request, err := BuildRequest(t.Context(), tc.method, server.URL+"/fail", source, original)
			if err != nil {
				t.Fatal(err)
			}
			if tc.idempotency {
				request.Header["Idempotency-Key"] = []string{}
			}
			response, disclosure, err := transport.Fetch(t.Context(), request, ExecutionOptions{})
			if tc.retry {
				if err != nil {
					t.Fatal(err)
				}
				closeResponse(t, response)
				if calls.Load() != 2 || source.opened != 2 {
					t.Fatalf("calls=%d, readers=%d", calls.Load(), source.opened)
				}
			} else {
				if err == nil {
					closeResponse(t, response)
					t.Fatal("unsafe replay succeeded")
				}
				if calls.Load() != 1 || source.opened != 1 {
					t.Fatalf("calls=%d, readers=%d", calls.Load(), source.opened)
				}
			}
			if disclosure.DefinitelyNotDisclosed() {
				t.Fatalf("disclosure=%v after request write", disclosure)
			}
		})
	}
}

type pipeBodySource struct {
	reader  *io.PipeReader
	framing BodyFraming
}

func (s *pipeBodySource) Open() (io.ReadCloser, error) { return s.reader, nil }
func (s *pipeBodySource) Framing() BodyFraming         { return s.framing }
func (s *pipeBodySource) Trailers() http.Header        { return nil }

func TestSourceResponseHeaderTimeoutExcludesUpload(t *testing.T) {
	const timeout = 100 * time.Millisecond
	entered := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	}))
	defer server.Close()
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	source := &pipeBodySource{reader: reader, framing: BodyFraming{ProtocolMajor: 1, ContentLength: -1, HasBody: true}}
	transport := New(Config{FirstByteTimeout: timeout})
	defer transport.CloseIdleConnections()
	original := httptest.NewRequest(http.MethodPost, "http://gateway.test", nil)
	request, err := BuildRequest(t.Context(), http.MethodPost, server.URL, source, original)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		response, _, fetchErr := transport.Fetch(t.Context(), request, ExecutionOptions{})
		if response != nil {
			body, _ := response.TakeBody()
			if body != nil {
				_ = body.Close()
			}
		}
		done <- fetchErr
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("upstream headers never arrived")
	}
	select {
	case err := <-done:
		t.Fatalf("upload was charged against response timeout: %v", err)
	case <-time.After(2 * timeout):
	}
	_, _ = io.WriteString(writer, "wire")
	_ = writer.Close()
	select {
	case err := <-done:
		var timeoutError interface{ Timeout() bool }
		if !errors.As(err, &timeoutError) || !timeoutError.Timeout() {
			t.Fatalf("error=%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("missing response header timeout")
	}
}

func TestSourceEarlyResponseDoesNotWaitForUpload(t *testing.T) {
	responseWritten := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := http.NewResponseController(w).EnableFullDuplex(); err != nil {
			t.Error(err)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "early")
		_ = http.NewResponseController(w).Flush()
		close(responseWritten)
		_, _ = io.Copy(io.Discard, r.Body)
	}))
	defer server.Close()
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	source := &pipeBodySource{reader: reader, framing: BodyFraming{ProtocolMajor: 1, ContentLength: -1, HasBody: true}}
	transport := New(Config{})
	defer transport.CloseIdleConnections()
	original := httptest.NewRequest(http.MethodPost, "http://gateway.test", nil)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	request, err := BuildRequest(ctx, http.MethodPost, server.URL, source, original)
	if err != nil {
		t.Fatal(err)
	}
	response, _, err := transport.Fetch(ctx, request, ExecutionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	<-responseWritten
	body, err := response.TakeBody()
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	prefix := make([]byte, len("early"))
	if _, err = io.ReadFull(body, prefix); err != nil || string(prefix) != "early" {
		t.Fatalf("response=%q, %v", prefix, err)
	}
	_, _ = io.WriteString(writer, "remaining upload")
	_ = writer.Close()
	if _, err = io.Copy(io.Discard, body); err != nil {
		t.Fatal(err)
	}
}
