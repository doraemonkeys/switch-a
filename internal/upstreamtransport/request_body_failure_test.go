package upstreamtransport

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSourcePartialResponseIsNotReplayed(t *testing.T) {
	var received atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/warm" {
			_, _ = io.WriteString(w, "warm")
			return
		}
		_, _ = io.Copy(io.Discard, r.Body)
		received.Add(1)
		connection, buffer, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_, _ = buffer.WriteString("HTTP/1.1 200 OK\r\nX-Incomplete:")
		_ = buffer.Flush()
		_ = connection.Close()
	}))
	defer server.Close()
	transport := New(Config{})
	defer transport.CloseIdleConnections()
	warm, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/warm", nil)
	response, _, err := transport.Fetch(t.Context(), warm, ExecutionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	closeResponse(t, response)
	source := testBodySource([]byte("wire"))
	request, err := BuildRequest(t.Context(), http.MethodGet, server.URL+"/partial", source, httptest.NewRequest(http.MethodGet, "http://gateway.test", nil))
	if err != nil {
		t.Fatal(err)
	}
	response, _, err = transport.Fetch(t.Context(), request, ExecutionOptions{})
	if response != nil {
		closeResponse(t, response)
	}
	if err == nil || source.opened != 1 || received.Load() != 1 {
		t.Fatalf("error=%v, readers=%d, received=%d", err, source.opened, received.Load())
	}
}

type closeObservedReader struct {
	io.ReadCloser
	once   sync.Once
	closed chan struct{}
}

func (r *closeObservedReader) Close() error {
	err := r.ReadCloser.Close()
	r.once.Do(func() { close(r.closed) })
	return err
}

type openingBodySource struct{ reader io.ReadCloser }

func (s openingBodySource) Open() (io.ReadCloser, error) { return s.reader, nil }
func (s openingBodySource) Framing() BodyFraming {
	return BodyFraming{ProtocolMajor: 1, ContentLength: -1, HasBody: true}
}
func (s openingBodySource) Trailers() http.Header { return nil }

func TestSourceHTTP2EarlyRejectionClosesUploadWhileResponseRemainsOpen(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	release := make(chan struct{})
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, "data: early\n\n")
		_ = http.NewResponseController(w).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	transport := New(Config{})
	defer transport.CloseIdleConnections()
	base := transport.followClient.Transport.(*http.Transport)
	base.ForceAttemptHTTP2 = true
	base.TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	closed := make(chan struct{})
	source := openingBodySource{reader: &closeObservedReader{ReadCloser: reader, closed: closed}}
	request, err := BuildRequest(ctx, http.MethodPost, server.URL, source, httptest.NewRequest(http.MethodPost, "http://gateway.test", nil))
	if err != nil {
		t.Fatal(err)
	}
	response, _, err := transport.Fetch(ctx, request, ExecutionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	body, err := response.TakeBody()
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	select {
	case <-closed:
	case <-ctx.Done():
		t.Fatal("upload reader stayed open after H2 rejection")
	}
	prefix := make([]byte, len("data: early\n\n"))
	if _, err = io.ReadFull(body, prefix); err != nil || string(prefix) != "data: early\n\n" {
		t.Fatalf("response=%q, error=%v", prefix, err)
	}
	close(release)
	if _, err = io.Copy(io.Discard, body); err != nil {
		t.Fatal(err)
	}
}
