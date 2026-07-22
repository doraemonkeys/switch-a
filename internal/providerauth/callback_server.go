package providerauth

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"go.uber.org/zap"
)

const (
	callbackReadHeaderTimeout = 10 * time.Second
	callbackIdleTimeout       = 30 * time.Second
)

// callbackEndpoint is consumed by Service because login-session state, rather
// than process lifetime, determines when the OAuth callback can receive work.
type callbackEndpoint interface {
	Start() error
	Shutdown(ctx context.Context) error
}

type callbackServerRun struct {
	server    *http.Server
	listeners []net.Listener
}

// loopbackCallbackServer is restartable so each group of pending login sessions
// can own the fixed OAuth port only for as long as those sessions need it.
type loopbackCallbackServer struct {
	mu      sync.Mutex
	handler http.Handler
	listen  func() ([]net.Listener, error)
	active  *callbackServerRun
	logger  *zap.Logger
}

type loopbackListenerSpec struct {
	network string
	host    string
}

func loopbackListenerSpecs() []loopbackListenerSpec {
	return []loopbackListenerSpec{
		{network: "tcp4", host: "127.0.0.1"},
		{network: "tcp6", host: "::1"},
	}
}

func loopbackListenerAddr(spec loopbackListenerSpec) string {
	return net.JoinHostPort(spec.host, fmt.Sprintf("%d", loopbackCallbackPort))
}

func newLoopbackCallbackServer(handler http.Handler, logger *zap.Logger) *loopbackCallbackServer {
	if logger == nil {
		logger = zap.NewNop()
	}

	mux := http.NewServeMux()
	mux.Handle(loopbackCallbackPath, handler)
	return &loopbackCallbackServer{
		handler: mux,
		listen:  listenLoopbackListeners,
		logger:  logger,
	}
}

// Start binds the callback endpoint before returning. Serving happens in the
// background so a successful return guarantees that the browser may be opened.
func (s *loopbackCallbackServer) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active != nil {
		return nil
	}

	listeners, err := s.listen()
	if err != nil {
		return err
	}
	run := &callbackServerRun{
		server: &http.Server{
			Handler:           s.handler,
			ReadHeaderTimeout: callbackReadHeaderTimeout,
			IdleTimeout:       callbackIdleTimeout,
		},
		listeners: listeners,
	}
	s.active = run

	addresses := make([]string, 0, len(listeners))
	for _, listener := range listeners {
		addresses = append(addresses, listener.Addr().String())
	}
	s.logger.Info("starting gpt oauth callback server", zap.Strings("addrs", addresses))
	go s.serve(run)
	return nil
}

func (s *loopbackCallbackServer) serve(run *callbackServerRun) {
	var wg sync.WaitGroup
	for _, listener := range run.listeners {
		wg.Go(func() {
			if err := run.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
				s.logger.Error("gpt oauth callback server stopped unexpectedly", zap.Error(err))
				// One advertised localhost stack becoming unavailable makes the
				// run unhealthy; closing its sibling lets the next login restart
				// the endpoint as one coherent generation.
				_ = run.server.Close()
			}
		})
	}
	wg.Wait()

	s.mu.Lock()
	if s.active == run {
		s.active = nil
	}
	s.mu.Unlock()
}

// Shutdown releases the fixed loopback port. A later Start creates a fresh
// http.Server because Go servers cannot be reused after graceful shutdown.
func (s *loopbackCallbackServer) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	run := s.active
	s.mu.Unlock()
	if run == nil {
		return nil
	}

	shutdownErr := run.server.Shutdown(ctx)
	// Serve normally owns listener closure, but Shutdown may win the race before
	// a freshly launched Serve goroutine registers its listener. Closing the
	// bound sockets explicitly guarantees that an immediate next login can bind.
	closeErr := closeLoopbackListeners(run.listeners)
	s.mu.Lock()
	if s.active == run {
		s.active = nil
	}
	s.mu.Unlock()
	s.logger.Info("stopped gpt oauth callback server")
	return errors.Join(shutdownErr, closeErr)
}

func closeLoopbackListeners(listeners []net.Listener) error {
	var errs []error
	for _, listener := range listeners {
		if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func listenLoopbackListeners() ([]net.Listener, error) {
	listeners := make([]net.Listener, 0, len(loopbackListenerSpecs()))
	for _, spec := range loopbackListenerSpecs() {
		listener, err := net.Listen(spec.network, loopbackListenerAddr(spec))
		if err != nil {
			continue
		}
		listeners = append(listeners, listener)
	}
	if len(listeners) == 0 {
		return nil, fmt.Errorf("listen oauth callback on loopback port %d", loopbackCallbackPort)
	}
	return listeners, nil
}
