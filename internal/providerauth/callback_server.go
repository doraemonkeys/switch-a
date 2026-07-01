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

// CallbackServer serves the fixed localhost OAuth callback endpoint required by the
// ChatGPT native-app OAuth client.
type CallbackServer struct {
	server    *http.Server
	listeners []net.Listener
	logger    *zap.Logger
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

// NewCallbackServer creates a loopback callback server that matches the
// advertised localhost redirect URI on both IPv4 and IPv6 loopback stacks.
func NewCallbackServer(service *Service, logger *zap.Logger) *CallbackServer {
	if logger == nil {
		logger = zap.NewNop()
	}

	mux := http.NewServeMux()
	mux.HandleFunc(loopbackCallbackPath, service.handleChatGPTOAuthCallback)

	return &CallbackServer{
		server: &http.Server{
			Handler:           mux,
			ReadHeaderTimeout: callbackReadHeaderTimeout,
			IdleTimeout:       callbackIdleTimeout,
		},
		logger: logger,
	}
}

// Start begins serving callback requests.
func (s *CallbackServer) Start() error {
	listeners, err := listenLoopbackListeners()
	if err != nil {
		return err
	}
	s.listeners = listeners

	addresses := make([]string, 0, len(listeners))
	for _, listener := range listeners {
		addresses = append(addresses, listener.Addr().String())
	}
	s.logger.Info("starting gpt oauth callback server", zap.Strings("addrs", addresses))

	errCh := make(chan error, len(listeners))
	var wg sync.WaitGroup
	for _, listener := range listeners {
		wg.Go(func() {
			if serveErr := s.server.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
				_ = s.server.Close()
				errCh <- serveErr
			}
		})
	}

	wg.Wait()
	close(errCh)
	for serveErr := range errCh {
		if serveErr != nil {
			return serveErr
		}
	}
	return nil
}

// Shutdown gracefully stops the callback server.
func (s *CallbackServer) Shutdown(ctx context.Context) error {
	if len(s.listeners) == 0 {
		return nil
	}
	return s.server.Shutdown(ctx)
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
