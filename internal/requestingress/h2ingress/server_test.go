package h2ingress

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
)

const testTimeout = 5 * time.Second

func startServer(t *testing.T, handler http.Handler, configure func(*http.Server)) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(handler)
	if configure != nil {
		configure(server.Config)
	}
	if err := Configure(server.Config, nil); err != nil {
		t.Fatal(err)
	}
	server.EnableHTTP2 = true
	server.StartTLS()
	t.Cleanup(server.Close)
	return server
}

func await[T any](t *testing.T, channel <-chan T) T {
	t.Helper()
	select {
	case value := <-channel:
		return value
	case <-time.After(testTimeout):
		t.Fatal("timed out")
		var zero T
		return zero
	}
}

func rawClient(t *testing.T, server *httptest.Server) (*tls.Conn, *http2.Framer) {
	t.Helper()
	conn, err := tls.Dial("tcp", server.Listener.Addr().String(), &tls.Config{InsecureSkipVerify: true, NextProtos: []string{http2.NextProtoTLS}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetDeadline(time.Now().Add(testTimeout))
	if _, err := io.WriteString(conn, http2.ClientPreface); err != nil {
		t.Fatal(err)
	}
	writer := http2.NewFramer(conn, nil)
	if err := writer.WriteSettings(); err != nil {
		t.Fatal(err)
	}
	// Reading continuously allows the server's response/control writes to make
	// progress while the test deliberately pauses the request upload.
	go func() {
		reader := http2.NewFramer(nil, conn)
		for {
			if _, err := reader.ReadFrame(); err != nil {
				return
			}
		}
	}()
	return conn, writer
}
func block(fields ...hpack.HeaderField) []byte {
	var buffer bytes.Buffer
	enc := hpack.NewEncoder(&buffer)
	for _, field := range fields {
		_ = enc.WriteField(field)
	}
	return buffer.Bytes()
}
func initialFields(length int) []hpack.HeaderField {
	return []hpack.HeaderField{{Name: ":method", Value: "POST"}, {Name: ":scheme", Value: "https"}, {Name: ":authority", Value: "localhost"}, {Name: ":path", Value: "/"}, {Name: "content-length", Value: strconv.Itoa(length)}}
}

func TestSocketUndeclaredTrailerPreservesHeadAndStreaming(t *testing.T) {
	prefix := make(chan struct{})
	result := make(chan string, 1)
	type key struct{}
	server := startServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 2 || r.ContentLength != 4 || len(r.TransferEncoding) != 0 || len(r.Trailer) != 0 {
			result <- "head changed"
			return
		}
		if r.Context().Value(key{}) != "base" || r.TLS == nil {
			result <- "base context or TLS missing"
			return
		}
		if got := r.Header.Values(associationHeader); len(got) != 2 || got[0] != "client-one" || got[1] != "client-two" {
			result <- "original colliding header changed"
			return
		}
		if r.Header.Get("Cookie") != "client=cookie" || r.Header.Get("Accept-Encoding") != "br" {
			result <- "client headers changed"
			return
		}
		first := make([]byte, 2)
		if _, err := io.ReadFull(r.Body, first); err != nil || string(first) != "wi" {
			result <- "missing streaming prefix"
			return
		}
		close(prefix)
		tail, err := io.ReadAll(r.Body)
		if err != nil || string(tail) != "re" {
			result <- "body failed"
			return
		}
		trailers, ok := Trailers(r)
		if !ok || strings.Join(trailers.Values("X-Late"), ",") != "one,two" {
			result <- "late trailers lost"
			return
		}
		trailers.Set("X-Late", "mutated")
		again, _ := Trailers(r)
		if again.Get("X-Late") != "one" {
			result <- "snapshot alias"
			return
		}
		result <- ""
		w.WriteHeader(http.StatusNoContent)
	}), func(s *http.Server) {
		s.ConnContext = func(ctx context.Context, _ net.Conn) context.Context { return context.WithValue(ctx, key{}, "base") }
	})
	_, writer := rawClient(t, server)
	fields := append(initialFields(4), hpack.HeaderField{Name: associationHeaderLower, Value: "client-one"}, hpack.HeaderField{Name: associationHeaderLower, Value: "client-two"}, hpack.HeaderField{Name: "cookie", Value: "client=cookie"}, hpack.HeaderField{Name: "accept-encoding", Value: "br"})
	initial := block(fields...)
	split := len(initial) / 2
	if err := writer.WriteHeaders(http2.HeadersFrameParam{StreamID: 1, BlockFragment: initial[:split]}); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteContinuation(1, true, initial[split:]); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteData(1, false, []byte("wi")); err != nil {
		t.Fatal(err)
	}
	await(t, prefix)
	if err := writer.WriteData(1, false, []byte("re")); err != nil {
		t.Fatal(err)
	}
	late := block(hpack.HeaderField{Name: "x-late", Value: "one"}, hpack.HeaderField{Name: "x-late", Value: "two"})
	if err := writer.WriteHeaders(http2.HeadersFrameParam{StreamID: 1, EndStream: true, EndHeaders: true, BlockFragment: late}); err != nil {
		t.Fatal(err)
	}
	if failure := await(t, result); failure != "" {
		t.Fatal(failure)
	}
}

func TestSocketMultiplexFlowControlAndDeclaredTrailers(t *testing.T) {
	var mu sync.Mutex
	connections := make(map[string]bool)
	server := startServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, exists := r.Header[associationHeader]; exists {
			t.Error("internal header leaked")
		}
		if len(r.Trailer) != 1 {
			t.Error("declarations changed")
		}
		body, err := io.ReadAll(r.Body)
		if err != nil || len(body) != 2*maxReadFrameBytes {
			t.Errorf("body: %d, %v", len(body), err)
		}
		actual, ok := Trailers(r)
		if !ok || actual.Get("X-Final") != "done" {
			t.Error("missing declared trailer")
		}
		mu.Lock()
		connections[r.RemoteAddr] = true
		mu.Unlock()
		_, _ = io.WriteString(w, "ok")
	}), nil)
	client := server.Client()
	const requests = 4
	failures := make(chan error, requests)
	for range requests {
		go func() {
			request, _ := http.NewRequest(http.MethodPost, server.URL, strings.NewReader(strings.Repeat("a", 2*maxReadFrameBytes)))
			request.Trailer = http.Header{"X-Final": {"done"}}
			response, err := client.Do(request)
			if err == nil {
				_, err = io.Copy(io.Discard, response.Body)
				_ = response.Body.Close()
			}
			failures <- err
		}()
	}
	for range requests {
		if err := await(t, failures); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	count := len(connections)
	mu.Unlock()
	if count != 1 {
		t.Fatalf("keepalive/multiplex used %d connections", count)
	}
}

func TestSocketResetAndDisconnectCancelStream(t *testing.T) {
	started := make(chan uint32, 2)
	canceled := make(chan struct{}, 2)
	server := startServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseUint(r.Header.Get("X-Test-Stream"), 10, 32)
		started <- uint32(id)
		<-r.Context().Done()
		canceled <- struct{}{}
	}), nil)
	conn, writer := rawClient(t, server)
	for _, id := range []uint32{1, 3} {
		fields := append(initialFields(4), hpack.HeaderField{Name: "x-test-stream", Value: strconv.FormatUint(uint64(id), 10)})
		if err := writer.WriteHeaders(http2.HeadersFrameParam{StreamID: id, EndHeaders: true, BlockFragment: block(fields...)}); err != nil {
			t.Fatal(err)
		}
		if got := await(t, started); got != id {
			t.Fatal(got)
		}
		if id == 1 {
			if err := writer.WriteRSTStream(id, http2.ErrCodeCancel); err != nil {
				t.Fatal(err)
			}
		} else {
			_ = conn.Close()
		}
		await(t, canceled)
	}
}

func TestConfigurePreservesHTTP1AndCallbacks(t *testing.T) {
	closed := make(chan net.Conn, 1)
	var original net.Conn
	server := startServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 1 {
			t.Error("expected HTTP/1")
		}
		if _, ok := Trailers(r); ok {
			t.Error("HTTP/1 falsely adapted")
		}
		_, _ = io.WriteString(w, "ok")
	}), func(s *http.Server) {
		s.ConnContext = func(ctx context.Context, c net.Conn) context.Context { original = c; return ctx }
		s.ConnState = func(c net.Conn, state http.ConnState) {
			if state == http.StateClosed {
				closed <- c
			}
		}
	})
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, DisableKeepAlives: true}
	defer tr.CloseIdleConnections()
	response, err := (&http.Client{Transport: tr}).Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if got := await(t, closed); got != original {
		t.Error("ConnState connection changed")
	}
}

func TestConfigureRejectsInvalidTLSConfig(t *testing.T) {
	s := &http.Server{TLSConfig: &tls.Config{CipherSuites: []uint16{tls.TLS_RSA_WITH_AES_128_CBC_SHA}}}
	if err := Configure(s, zap.NewNop()); err == nil {
		t.Fatal("expected TLS configuration error")
	}
}
