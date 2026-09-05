// Package h2ingress preserves HTTP/2 trailers which net/http otherwise filters
// to the names declared before the request body.
package h2ingress

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"sync"
	"sync/atomic"

	"go.uber.org/zap"
	"golang.org/x/net/http2"
)

const (
	maxConcurrentStreams   = 250
	maxReadFrameBytes      = 1 << 20
	associationHeader      = "Switch-A-Ingress-Stream"
	associationHeaderLower = "switch-a-ingress-stream"
	associationBudgetBytes = 128
)

var connectionSequence atomic.Uint64

// Configure installs the TLS HTTP/2 boundary before the server starts serving.
// HTTP/1 is unaffected. The HTTP/2 server retains ownership of flow control,
// cancellation, timeouts, keep-alive, and graceful shutdown.
func Configure(server *http.Server, logger *zap.Logger) error {
	if logger == nil {
		logger = zap.NewNop()
	}
	h2 := &http2.Server{MaxConcurrentStreams: maxConcurrentStreams, MaxReadFrameSize: maxReadFrameBytes}
	if err := http2.ConfigureServer(server, h2); err != nil {
		return err
	}
	previousContext, previousState := server.ConnContext, server.ConnState
	var mu sync.Mutex
	// The public ConnContext callback is the only point where the connection's
	// actual base context is available. Keep it in a serving closure, never in
	// protocol/domain state, and discard the closure when the socket closes.
	pending := make(map[net.Conn]func(*http.Server, *tls.Conn, http.Handler))
	server.ConnContext = func(ctx context.Context, conn net.Conn) context.Context {
		if previousContext != nil {
			ctx = previousContext(ctx, conn)
		}
		mu.Lock()
		pending[conn] = func(base *http.Server, tlsConn *tls.Conn, handler http.Handler) {
			serve(ctx, h2, base, tlsConn, handler, logger)
		}
		mu.Unlock()
		return ctx
	}
	server.ConnState = func(conn net.Conn, state http.ConnState) {
		if state == http.StateClosed || state == http.StateHijacked {
			mu.Lock()
			delete(pending, conn)
			mu.Unlock()
		}
		if previousState != nil {
			previousState(conn, state)
		}
	}
	server.TLSNextProto[http2.NextProtoTLS] = func(base *http.Server, conn *tls.Conn, handler http.Handler) {
		mu.Lock()
		run := pending[conn]
		delete(pending, conn)
		mu.Unlock()
		if run == nil {
			logger.Error("HTTP/2 ingress connection context missing")
			_ = conn.Close()
			return
		}
		run(base, conn, handler)
	}
	return nil
}

func serve(ctx context.Context, h2 *http2.Server, server *http.Server, conn *tls.Conn, handler http.Handler, logger *zap.Logger) {
	id := connectionSequence.Add(1)
	observer := newConnection(conn, headerLimit(server), logger.With(zap.Uint64("connection_id", id)))
	// Reserve space only inside the parser-facing configuration for the internal
	// association. The client header block is independently bounded by its
	// original budget, so the marker cannot displace a legitimate client field.
	base := &http.Server{
		MaxHeaderBytes: headerLimit(server) + associationBudgetBytes,
		ReadTimeout:    server.ReadTimeout, ReadHeaderTimeout: server.ReadHeaderTimeout,
		WriteTimeout: server.WriteTimeout, IdleTimeout: server.IdleTimeout,
		ErrorLog:  server.ErrorLog,
		ConnState: func(_ net.Conn, state http.ConnState) { server.ConnState(conn, state) },
	}
	logger.Debug("HTTP/2 ingress connected", zap.Uint64("connection_id", id))
	h2.ServeConn(observer, &http2.ServeConnOpts{Context: ctx, BaseConfig: base, Handler: observer.wrap(handler)})
	logger.Debug("HTTP/2 ingress disconnected", zap.Uint64("connection_id", id))
}

func headerLimit(server *http.Server) int {
	if server.MaxHeaderBytes > 0 {
		return server.MaxHeaderBytes
	}
	return http.DefaultMaxHeaderBytes
}
