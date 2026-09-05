package proxy

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/requestingress"
	"github.com/doraemonkeys/switch-a/internal/requestingress/clientconnection"
	"github.com/doraemonkeys/switch-a/internal/upstreamtransport"
)

func TestPlan10ReviewHandlerAutomaticAbortThenSilentDisconnect(t *testing.T) {
	ingressReady := make(chan *requestingress.Handle, 1)
	aborted := make(chan struct{})
	upstreamCanceled := make(chan struct{})
	continueResponse := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 2 {
			t.Errorf("upstream protocol=%s", r.Proto)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, "data: ready\n\n")
		_ = http.NewResponseController(w).Flush()
		select {
		case <-continueResponse:
			_, _ = io.WriteString(w, "data: continued\n\n")
			_ = http.NewResponseController(w).Flush()
		case <-r.Context().Done():
			close(upstreamCanceled)
			return
		case <-release:
			return
		}
		select {
		case <-r.Context().Done():
			close(upstreamCanceled)
		case <-release:
		}
	}))
	upstream.EnableHTTP2 = true
	upstream.StartTLS()
	defer upstream.Close()
	defer releaseOnce.Do(func() { close(release) })
	removed := make(chan struct{})
	registry := NewActiveRequestRegistryWithHook(func(ActiveRequest, ActiveRequestRemovalReason) { close(removed) })
	handler := ingressIntegrationHandler(t, upstream.URL, registry)
	handler.transportOverride = &Transport{upstream: upstreamtransport.NewWithRoundTripper(upstream.Client().Transport)}
	handler.startIngress = func(ctx context.Context, request *http.Request, options requestingress.Options) (*requestingress.Handle, error) {
		finish := options.OnFinish
		options.OnFinish = func(snapshot requestingress.Snapshot) {
			if finish != nil {
				finish(snapshot)
			}
			if snapshot.State == requestingress.Aborted {
				close(aborted)
			}
		}
		handle, err := requestingress.Start(ctx, request, options)
		if err == nil {
			ingressReady <- handle
		}
		return handle, err
	}
	server := httptest.NewUnstartedServer(handler)
	server.Listener = clientconnection.Listen(server.Listener)
	server.Config.ConnContext = clientconnection.Context
	server.Start()
	defer server.Close()
	conn, err := net.Dial("tcp", server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, err = io.WriteString(conn, "POST /v1/messages HTTP/1.1\r\nHost: localhost\r\nContent-Length: 100000\r\nContent-Type: application/json\r\n\r\n{\"input\":\"")
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	responseReader := bufio.NewReader(response.Body)
	readEvent := func(want string) {
		t.Helper()
		line, err := responseReader.ReadString('\n')
		if err != nil || line != want {
			t.Fatalf("response=%q error=%v want=%q", line, err, want)
		}
		if _, err := responseReader.ReadString('\n'); err != nil {
			t.Fatal(err)
		}
	}
	readEvent("data: ready\n")
	handle := <-ingressReady
	awaitIngressTest(t, aborted)
	if snapshot := handle.Snapshot(); snapshot.State != requestingress.Aborted || snapshot.ReceivedBytes >= 100000 {
		t.Fatalf("source=%+v", snapshot)
	}
	close(continueResponse)
	readEvent("data: continued\n")
	_ = conn.Close()
	awaitIngressTest(t, upstreamCanceled)
	awaitIngressTest(t, removed)
	if len(registry.List()) != 0 {
		t.Fatal("lease remained active after disconnect")
	}
}
