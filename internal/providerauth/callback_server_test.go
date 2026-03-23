package providerauth

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

const (
	callbackServerProbeTimeout = 2 * time.Second
	callbackServerPollInterval = 20 * time.Millisecond
)

func TestNewCallbackServer_UsesNopLoggerWhenNil(t *testing.T) {
	server := NewCallbackServer(NewService(Config{}), nil)

	if server == nil {
		t.Fatal("NewCallbackServer returned nil")
	}
	if server.logger == nil {
		t.Fatal("logger = nil, want zap nop logger")
	}
	if server.server == nil {
		t.Fatal("server = nil, want http server")
	}
	if server.server.Handler == nil {
		t.Fatal("server.Handler = nil, want callback mux")
	}
	if server.server.ReadHeaderTimeout != callbackReadHeaderTimeout {
		t.Fatalf("ReadHeaderTimeout = %s, want %s", server.server.ReadHeaderTimeout, callbackReadHeaderTimeout)
	}
	if server.server.IdleTimeout != callbackIdleTimeout {
		t.Fatalf("IdleTimeout = %s, want %s", server.server.IdleTimeout, callbackIdleTimeout)
	}
}

func TestCallbackServerShutdown_BeforeStartIsNoop(t *testing.T) {
	server := NewCallbackServer(NewService(Config{}), nil)

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

func TestCallbackServerStartAndShutdown(t *testing.T) {
	if listeners, err := listenLoopbackListeners(); err != nil {
		t.Skipf("loopback callback port unavailable: %v", err)
	} else {
		closeListeners(t, listeners)
	}

	server := NewCallbackServer(NewService(Config{}), nil)
	startErrCh := make(chan error, 1)

	go func() {
		startErrCh <- server.Start()
	}()

	waitForCallbackServer(t)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), callbackServerProbeTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown returned error: %v", err)
	}

	select {
	case err := <-startErrCh:
		if err != nil {
			t.Fatalf("Start returned error after Shutdown: %v", err)
		}
	case <-time.After(callbackServerProbeTimeout):
		t.Fatal("Start did not return after Shutdown")
	}
}

func waitForCallbackServer(t *testing.T) {
	t.Helper()

	client := &http.Client{Timeout: callbackServerPollInterval}
	deadline := time.Now().Add(callbackServerProbeTimeout)
	for time.Now().Before(deadline) {
		response, err := client.Get(LoopbackCallbackAddress())
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			return
		}
		time.Sleep(callbackServerPollInterval)
	}

	t.Fatalf("callback server at %s did not become reachable within %s", LoopbackCallbackAddress(), callbackServerProbeTimeout)
}

func closeListeners(t *testing.T, listeners []net.Listener) {
	t.Helper()

	for _, listener := range listeners {
		if err := listener.Close(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("Close() returned error: %v", err)
		}
	}
}
