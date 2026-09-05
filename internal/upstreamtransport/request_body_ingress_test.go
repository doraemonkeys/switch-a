package upstreamtransport

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/requestingress"
	"github.com/doraemonkeys/switch-a/internal/requestingress/h2ingress"
	"go.uber.org/zap"
)

type ingressBodySource struct{ *requestingress.Handle }

func (s ingressBodySource) Framing() BodyFraming {
	head := s.Head()
	return BodyFraming{ProtocolMajor: head.ProtocolMajor, ContentLength: head.ContentLength, HasBody: head.HasBody, TrailerKeys: head.TrailerKeys, Complete: s.Snapshot().State == requestingress.Complete}
}

func TestSourceReplaysPrefixWhileIngressStillReceiving(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var calls atomic.Int32
	reopened := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/warm" {
			_, _ = io.WriteString(w, "warm")
			return
		}
		prefix := make([]byte, len("prefix"))
		if _, err := io.ReadFull(r.Body, prefix); err != nil || string(prefix) != "prefix" {
			t.Errorf("prefix=%q, %v", prefix, err)
			return
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
		close(reopened)
		tail, err := io.ReadAll(r.Body)
		if err != nil || string(tail) != "tail" || r.Trailer.Get("X-Final") != "complete" {
			t.Errorf("tail=%q trailer=%v error=%v", tail, r.Trailer, err)
		}
		_, _ = io.WriteString(w, "complete")
	}))
	defer server.Close()
	transport := New(Config{})
	defer transport.CloseIdleConnections()
	warm, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/warm", nil)
	response, _, err := transport.Fetch(ctx, warm, ExecutionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	closeResponse(t, response)
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	original := httptest.NewRequest(http.MethodGet, "http://gateway.test/input", reader)
	original.Trailer = http.Header{"X-Final": nil}
	ingress, err := requestingress.Start(ctx, original, requestingress.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer ingress.Close()
	request, err := BuildRequest(ctx, http.MethodGet, server.URL+"/live", ingressBodySource{ingress}, original)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		response, _, fetchErr := transport.Fetch(ctx, request, ExecutionOptions{})
		if response != nil {
			body, _ := response.TakeBody()
			if body != nil {
				_, _ = io.Copy(io.Discard, body)
				_ = body.Close()
			}
		}
		done <- fetchErr
	}()
	if _, err = io.WriteString(writer, "prefix"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-reopened:
	case err := <-done:
		t.Fatalf("early fetch failure: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if ingress.Snapshot().State != requestingress.Receiving {
		t.Fatal("retry waited for complete ingress")
	}
	original.Trailer.Set("X-Final", "complete")
	_, _ = io.WriteString(writer, "tail")
	_ = writer.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if calls.Load() != 2 {
		t.Fatalf("calls=%d", calls.Load())
	}
}

func TestSourceHTTP2LateTrailerCrossesToHTTP1WhileReceiving(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	upstreamPrefix := make(chan struct{})
	observed := make(chan observedRequest, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		prefix := make([]byte, 2)
		if _, err := io.ReadFull(r.Body, prefix); err != nil {
			t.Error(err)
			return
		}
		close(upstreamPrefix)
		observation, err := readObservedRequest(r)
		if err != nil {
			t.Error(err)
		}
		observation.body = string(prefix) + observation.body
		observed <- observation
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	transport := New(Config{})
	defer transport.CloseIdleConnections()
	gateway := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 2 || r.ContentLength != 4 || len(r.Trailer) != 0 {
			t.Errorf("inbound head proto=%s length=%d trailer=%v", r.Proto, r.ContentLength, r.Trailer)
		}
		ingress, err := requestingress.Start(r.Context(), r, requestingress.Options{TrailerSnapshot: func() http.Header {
			if trailer, ok := h2ingress.Trailers(r); ok {
				return trailer
			}
			return r.Trailer.Clone()
		}})
		if err != nil {
			t.Error(err)
			return
		}
		defer ingress.Close()
		request, err := BuildRequest(r.Context(), r.Method, upstream.URL, ingressBodySource{ingress}, r)
		if err != nil {
			t.Error(err)
			return
		}
		response, _, err := transport.Fetch(r.Context(), request, ExecutionOptions{})
		if err != nil {
			t.Error(err)
			return
		}
		closeResponse(t, response)
		w.WriteHeader(http.StatusNoContent)
	}))
	if err := h2ingress.Configure(gateway.Config, zap.NewNop()); err != nil {
		t.Fatal(err)
	}
	gateway.EnableHTTP2 = true
	gateway.StartTLS()
	defer gateway.Close()
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, gateway.URL, reader)
	if err != nil {
		t.Fatal(err)
	}
	request.ContentLength = 4
	request.Trailer = make(http.Header)
	// The client adds an undeclared trailer immediately before its body EOF.
	request.Body = &bodyTransmission{ReadCloser: reader, source: &memoryBodySource{trailers: http.Header{"X-Late": {"complete"}}}, trailer: request.Trailer}
	done := make(chan error, 1)
	go func() {
		response, err := gateway.Client().Do(request)
		if response != nil {
			_ = response.Body.Close()
		}
		done <- err
	}()
	if _, err = io.WriteString(writer, "wi"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-upstreamPrefix:
	case err := <-done:
		t.Fatalf("early response: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	_, _ = io.WriteString(writer, "re")
	_ = writer.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	observation := <-observed
	if observation.body != "wire" || observation.length != -1 || observation.trailer.Get("X-Late") != "complete" {
		t.Fatalf("upstream=%+v", observation)
	}
}
