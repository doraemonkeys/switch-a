package providerauth

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	callbackServerProbeTimeout = 2 * time.Second
	callbackServerPollInterval = 20 * time.Millisecond
)

type ephemeralListenerFactory struct {
	mu        sync.Mutex
	addresses []string
	calls     int
}

func (f *ephemeralListenerFactory) listen() ([]net.Listener, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.calls++
	f.addresses = append(f.addresses, listener.Addr().String())
	f.mu.Unlock()
	return []net.Listener{listener}, nil
}

func (f *ephemeralListenerFactory) snapshot() (int, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls, f.addresses[len(f.addresses)-1]
}

func TestNewLoopbackCallbackServer_UsesSafeDefaults(t *testing.T) {
	server := newLoopbackCallbackServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), nil)

	if server == nil {
		t.Fatal("newLoopbackCallbackServer returned nil")
	}
	if server.logger == nil {
		t.Fatal("logger = nil, want zap nop logger")
	}
	if server.handler == nil {
		t.Fatal("handler = nil, want callback mux")
	}
	if server.listen == nil {
		t.Fatal("listen = nil, want loopback listener factory")
	}
	if activeCallbackServerRun(server) != nil {
		t.Fatal("active run should be nil before the first login")
	}
}

func TestLoopbackCallbackServerShutdown_BeforeStartIsNoop(t *testing.T) {
	server := newLoopbackCallbackServer(http.NotFoundHandler(), nil)

	if err := server.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown returned error before Start: %v", err)
	}
}

func TestListenLoopbackListeners_ReturnsLoopbackListeners(t *testing.T) {
	listeners, err := listenLoopbackListeners()
	if err != nil {
		t.Skipf("loopback callback port unavailable: %v", err)
	}
	defer closeListeners(t, listeners)

	if len(listeners) == 0 {
		t.Fatal("listeners = empty, want at least one loopback listener")
	}
	for _, listener := range listeners {
		network := listener.Addr().Network()
		if network != "tcp" && network != "tcp4" && network != "tcp6" {
			t.Fatalf("listener network = %q, want tcp/tcp4/tcp6", network)
		}
		if listener.Addr().String() == "" {
			t.Fatal("listener address = empty, want bound loopback address")
		}
	}
}

func TestLoopbackCallbackServer_StartIsIdempotentAndRestartable(t *testing.T) {
	var requests atomic.Int32
	server := newLoopbackCallbackServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}), nil)
	listeners := &ephemeralListenerFactory{}
	server.listen = listeners.listen
	t.Cleanup(func() {
		ctx, cleanupCancel := context.WithTimeout(context.Background(), callbackServerProbeTimeout)
		defer cleanupCancel()
		if err := server.Shutdown(ctx); err != nil {
			t.Errorf("cleanup Shutdown returned error: %v", err)
		}
	})

	if err := server.Start(); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	firstRun := activeCallbackServerRun(server)
	if firstRun == nil {
		t.Fatal("active run = nil after Start")
	}
	if firstRun.server.ReadHeaderTimeout != callbackReadHeaderTimeout {
		t.Fatalf("ReadHeaderTimeout = %s, want %s", firstRun.server.ReadHeaderTimeout, callbackReadHeaderTimeout)
	}
	if firstRun.server.IdleTimeout != callbackIdleTimeout {
		t.Fatalf("IdleTimeout = %s, want %s", firstRun.server.IdleTimeout, callbackIdleTimeout)
	}
	if err := server.Start(); err != nil {
		t.Fatalf("second Start returned error: %v", err)
	}
	startCalls, firstAddress := listeners.snapshot()
	if startCalls != 1 {
		t.Fatalf("listener factory calls = %d, want 1 for idempotent Start", startCalls)
	}
	waitForCallbackServer(t, firstAddress)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), callbackServerProbeTimeout)
	if err := server.Shutdown(shutdownCtx); err != nil {
		cancel()
		t.Fatalf("Shutdown returned error: %v", err)
	}
	cancel()
	if activeCallbackServerRun(server) != nil {
		t.Fatal("active run should be nil after Shutdown")
	}

	if err := server.Start(); err != nil {
		t.Fatalf("restart returned error: %v", err)
	}
	if activeCallbackServerRun(server) == firstRun {
		t.Fatal("restart reused a shut down http.Server")
	}
	startCalls, secondAddress := listeners.snapshot()
	if startCalls != 2 {
		t.Fatalf("listener factory calls = %d, want 2 after restart", startCalls)
	}
	waitForCallbackServer(t, secondAddress)
	if requests.Load() != 2 {
		t.Fatalf("callback requests = %d, want one per server generation", requests.Load())
	}

}

func TestLoopbackCallbackServer_StartPropagatesListenerFailure(t *testing.T) {
	listenErr := errors.New("port unavailable")
	server := newLoopbackCallbackServer(http.NotFoundHandler(), nil)
	server.listen = func() ([]net.Listener, error) {
		return nil, listenErr
	}

	if err := server.Start(); !errors.Is(err, listenErr) {
		t.Fatalf("Start error = %v, want %v", err, listenErr)
	}
	if activeCallbackServerRun(server) != nil {
		t.Fatal("active run should remain nil after listener failure")
	}
}

func TestLoopbackCallbackServer_ImmediateShutdownReleasesBoundListener(t *testing.T) {
	probe, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve test address: %v", err)
	}
	address := probe.Addr().String()
	if err := probe.Close(); err != nil {
		t.Fatalf("release test address reservation: %v", err)
	}

	server := newLoopbackCallbackServer(http.NotFoundHandler(), nil)
	server.listen = func() ([]net.Listener, error) {
		listener, listenErr := net.Listen("tcp4", address)
		if listenErr != nil {
			return nil, listenErr
		}
		return []net.Listener{listener}, nil
	}
	for range 20 {
		if err := server.Start(); err != nil {
			t.Fatalf("Start returned error: %v", err)
		}
		if err := server.Shutdown(context.Background()); err != nil {
			t.Fatalf("immediate Shutdown returned error: %v", err)
		}

		rebound, err := net.Listen("tcp4", address)
		if err != nil {
			t.Fatalf("callback listener remained bound after Shutdown: %v", err)
		}
		if err := rebound.Close(); err != nil {
			t.Fatalf("close rebound listener: %v", err)
		}
	}
}

func activeCallbackServerRun(server *loopbackCallbackServer) *callbackServerRun {
	server.mu.Lock()
	defer server.mu.Unlock()
	return server.active
}

func waitForCallbackServer(t *testing.T, address string) {
	t.Helper()

	client := &http.Client{Timeout: callbackServerPollInterval}
	callbackURL := "http://" + address + loopbackCallbackPath
	deadline := time.Now().Add(callbackServerProbeTimeout)
	for time.Now().Before(deadline) {
		response, err := client.Get(callbackURL)
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			if response.StatusCode != http.StatusNoContent {
				t.Fatalf("callback status = %d, want %d", response.StatusCode, http.StatusNoContent)
			}
			return
		}
		time.Sleep(callbackServerPollInterval)
	}

	t.Fatalf("callback server at %s did not become reachable within %s", callbackURL, callbackServerProbeTimeout)
}

func closeListeners(t *testing.T, listeners []net.Listener) {
	t.Helper()

	for _, listener := range listeners {
		if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			t.Fatalf("Close() returned error: %v", err)
		}
	}
}
